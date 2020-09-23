package renter

// skyfile.go provides the tools for creating and uploading skyfiles, and then
// receiving the associated skylinks to recover the files. The skyfile is the
// fundamental data structure underpinning Skynet.
//
// The primary trick of the skyfile is that the initial data is stored entirely
// in a single sector which is put on the Sia network using 1-of-N redundancy.
// Every replica has an identical Merkle root, meaning that someone attempting
// to fetch the file only needs the Merkle root and then some way to ask hosts
// on the network whether they have access to the Merkle root.
//
// That single sector then contains all of the other information that is
// necessary to recover the rest of the file. If the file is small enough, the
// entire file will be stored within the single sector. If the file is larger,
// the Merkle roots that are needed to download the remaining data get encoded
// into something called a 'fanout'. While the base chunk is required to use
// 1-of-N redundancy, the fanout chunks can use more sophisticated redundancy.
//
// The 1-of-N redundancy requirement really stems from the fact that Skylinks
// are only 34 bytes of raw data, meaning that there's only enough room in a
// Skylink to encode a single root. The fanout however has much more data to
// work with, meaning there is space to describe much fancier redundancy schemes
// and data fetching patterns.
//
// Skyfiles also contain some metadata which gets encoded as json. The
// intention is to allow uploaders to put any arbitrary metadata fields into
// their file and know that users will be able to see that metadata after
// downloading. A couple of fields such as the mode of the file are supported at
// the base level by Sia.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"gitlab.com/NebulousLabs/Sia/fixtures"
	"gitlab.com/NebulousLabs/Sia/skynet"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem/siafile"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// SkyfileDefaultBaseChunkRedundancy establishes the default redundancy for
	// the base chunk of a skyfile.
	SkyfileDefaultBaseChunkRedundancy = 10
)

var (
	// ErrMetadataTooBig is the error returned when the metadata exceeds a
	// sectorsize.
	ErrMetadataTooBig = errors.New("metadata exceeds sectorsize")

	// ErrRedundancyNotSupported is the error returned when trying to convert a
	// Siafile that was uploaded with redundancy that is not currently supported
	// by Skynet
	ErrRedundancyNotSupported = errors.New("skylinks currently only support 1-of-N redundancy, other redundancies will be supported in a later version")

	// ErrSkylinkBlacklisted is the error returned when a skylink is blacklisted
	ErrSkylinkBlacklisted = errors.New("skylink is blacklisted")

	// ExtendedSuffix is the suffix that is added to a skyfile siapath if it is
	// a large file upload
	ExtendedSuffix = "-extended"
)

// skyfileBuildBaseSector will take all of the elements of the base sector and
// copy them into a freshly created base sector.
func skyfileBuildBaseSector(layoutBytes, fanoutBytes, metadataBytes, fileBytes []byte) ([]byte, uint64) {
	baseSector := make([]byte, modules.SectorSize)
	offset := 0
	copy(baseSector[offset:], layoutBytes)
	offset += len(layoutBytes)
	copy(baseSector[offset:], fanoutBytes)
	offset += len(fanoutBytes)
	copy(baseSector[offset:], metadataBytes)
	offset += len(metadataBytes)
	copy(baseSector[offset:], fileBytes)
	offset += len(fileBytes)
	return baseSector, uint64(offset)
}

// skyfileEstablishDefaults will set any zero values in the lup to be equal to
// the desired defaults.
func skyfileEstablishDefaults(lup *modules.SkyfileUploadParameters) error {
	if lup.BaseChunkRedundancy == 0 {
		lup.BaseChunkRedundancy = SkyfileDefaultBaseChunkRedundancy
	}
	return nil
}

// skyfileMetadataBytes will return the marshalled/encoded bytes for the
// skyfile metadata.
func skyfileMetadataBytes(lm modules.SkyfileMetadata) ([]byte, error) {
	// Compose the metadata into the leading chunk.
	metadataBytes, err := json.Marshal(lm)
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal the link file metadata")
	}
	return metadataBytes, nil
}

// fileUploadParamsFromLUP will derive the FileUploadParams to use when
// uploading the base chunk siafile of a skyfile using the skyfile's upload
// parameters.
func fileUploadParamsFromLUP(lup modules.SkyfileUploadParameters) (modules.FileUploadParams, error) {
	// Create parameters to upload the file with 1-of-N erasure coding and no
	// encryption. This should cause all of the pieces to have the same Merkle
	// root, which is critical to making the file discoverable to viewnodes and
	// also resilient to host failures.
	ec, err := siafile.NewRSSubCode(1, int(lup.BaseChunkRedundancy)-1, crypto.SegmentSize)
	if err != nil {
		return modules.FileUploadParams{}, errors.AddContext(err, "unable to create erasure coder")
	}
	return modules.FileUploadParams{
		SiaPath:             lup.SiaPath,
		ErasureCode:         ec,
		Force:               lup.Force,
		DisablePartialChunk: true,  // must be set to true - partial chunks change, content addressed files must not change.
		Repair:              false, // indicates whether this is a repair operation
	}, nil
}

// streamerFromReader wraps a bytes.Reader to give it a Close() method, which
// allows it to satisfy the modules.Streamer interface.
type streamerFromReader struct {
	*bytes.Reader
}

// Close is a no-op because a bytes.Reader doesn't need to be closed.
func (sfr *streamerFromReader) Close() error {
	return nil
}

// streamerFromSlice returns a modules.Streamer given a slice. This is
// non-trivial because a bytes.Reader does not implement Close.
func streamerFromSlice(b []byte) modules.Streamer {
	reader := bytes.NewReader(b)
	return &streamerFromReader{
		Reader: reader,
	}
}

// CreateSkylinkFromSiafile creates a skyfile from a siafile. This requires
// uploading a new skyfile which contains fanout information pointing to the
// siafile data. The SiaPath provided in 'lup' indicates where the new base
// sector skyfile will be placed, and the siaPath provided as its own input is
// the siaPath of the file that is being used to create the skyfile.
func (r *Renter) CreateSkylinkFromSiafile(lup modules.SkyfileUploadParameters, siaPath modules.SiaPath) (modules.Skylink, error) {
	// Set reasonable default values for any lup fields that are blank.
	err := skyfileEstablishDefaults(&lup)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "skyfile upload parameters are incorrect")
	}

	// Grab the filenode for the provided siapath.
	fileNode, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to open siafile")
	}
	defer fileNode.Close()

	// Override the metadata with the info from the fileNode.
	lup.FileMetadata = modules.SkyfileMetadata{
		Filename: siaPath.Name(),
		Mode:     fileNode.Mode(),
		Length:   fileNode.Size(),
	}
	return r.managedCreateSkylinkFromFileNode(lup, fileNode)
}

// managedCreateSkylinkFromFileNode creates a skylink from a file node.
//
// The name needs to be passed in explicitly because a file node does not track
// its own name, which allows the file to be renamed concurrently without
// causing any race conditions.
func (r *Renter) managedCreateSkylinkFromFileNode(lup modules.SkyfileUploadParameters, fileNode *filesystem.FileNode) (modules.Skylink, error) {
	// First check if any of the skylinks associated with the siafile are
	// blacklisted
	skylinkstrs := fileNode.Metadata().Skylinks
	for _, skylinkstr := range skylinkstrs {
		var skylink modules.Skylink
		err := skylink.LoadString(skylinkstr)
		if err != nil {
			// If there is an error just continue as we shouldn't prevent the
			// conversion due to bad old skylinks
			//
			// Log the error for debugging purposes
			r.log.Printf("WARN: previous skylink for siafile %v could not be loaded from string; potentially corrupt skylink: %v", fileNode.SiaFilePath(), skylinkstr)
			continue
		}
		// Check if skylink is blacklisted
		if r.staticSkynetBlacklist.IsBlacklisted(skylink) {
			// Skylink is blacklisted, return error and try and delete file
			return modules.Skylink{}, errors.Compose(ErrSkylinkBlacklisted, r.DeleteFile(lup.SiaPath))
		}
	}

	// Check that the encryption key and erasure code is compatible with the
	// skyfile format. This is intentionally done before any heavy computation
	// to catch early errors.
	var sl skynet.SkyfileLayout
	masterKey := fileNode.MasterKey()
	if len(masterKey.Key()) > len(sl.KeyData) {
		return modules.Skylink{}, errors.New("cipher key is not supported by the skyfile format")
	}
	ec := fileNode.ErasureCode()
	if ec.Type() != siafile.ECReedSolomonSubShards64 {
		return modules.Skylink{}, errors.New("siafile has unsupported erasure code type")
	}
	// Deny the conversion of siafiles that are not 1 data piece. Not because we
	// cannot download them, but because it is currently inefficient to download
	// them.
	if ec.MinPieces() != 1 {
		return modules.Skylink{}, ErrRedundancyNotSupported
	}

	// Marshal the metadata.
	metadataBytes, err := skyfileMetadataBytes(lup.FileMetadata)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "error retrieving skyfile metadata bytes")
	}

	// Create the fanout for the siafile.
	fanoutBytes, err := skyfileEncodeFanout(fileNode)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to encode the fanout of the siafile")
	}
	headerSize := uint64(skynet.SkyfileLayoutSize + len(metadataBytes) + len(fanoutBytes))
	if headerSize > modules.SectorSize {
		return modules.Skylink{}, fmt.Errorf("skyfile does not fit in leading chunk - metadata size plus fanout size must be less than %v bytes, metadata size is %v bytes and fanout size is %v bytes", modules.SectorSize-skynet.SkyfileLayoutSize, len(metadataBytes), len(fanoutBytes))
	}

	// Assemble the first chunk of the skyfile.
	sl = skynet.SkyfileLayout{
		Version:            skynet.SkyfileVersion,
		Filesize:           fileNode.Size(),
		MetadataSize:       uint64(len(metadataBytes)),
		FanoutSize:         uint64(len(fanoutBytes)),
		FanoutDataPieces:   uint8(ec.MinPieces()),
		FanoutParityPieces: uint8(ec.NumPieces() - ec.MinPieces()),
		CipherType:         masterKey.Type(),
	}
	// If we're uploading in plaintext, we put the key in the baseSector
	if !encryptionEnabled(lup) {
		copy(sl.KeyData[:], masterKey.Key())
	}

	// Create the base sector.
	baseSector, fetchSize := skyfileBuildBaseSector(sl.Encode(), fanoutBytes, metadataBytes, nil)

	// Encrypt the base sector if necessary.
	if encryptionEnabled(lup) {
		err = encryptBaseSectorWithSkykey(baseSector, sl, lup.FileSpecificSkykey)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "Failed to encrypt base sector for upload")
		}
	}

	// Create the skylink.
	baseSectorRoot := crypto.MerkleRoot(baseSector)
	skylink, err := modules.NewSkylinkV1(baseSectorRoot, 0, fetchSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to build skylink")
	}
	if lup.DryRun {
		return skylink, nil
	}

	// Check if the new skylink is blacklisted
	if r.staticSkynetBlacklist.IsBlacklisted(skylink) {
		// Skylink is blacklisted, return error and try and delete file
		return modules.Skylink{}, errors.Compose(ErrSkylinkBlacklisted, r.DeleteFile(lup.SiaPath))
	}

	// Add the skylink to the siafiles.
	err = fileNode.AddSkylink(skylink)
	if err != nil {
		return skylink, errors.AddContext(err, "unable to add skylink to the sianodes")
	}

	// Upload the base sector.
	err = r.managedUploadBaseSector(lup, baseSector, skylink)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "Unable to upload base sector for file node. ")
	}

	return skylink, errors.AddContext(err, "unable to add skylink to the sianodes")
}

// managedCreateFileNodeFromReader takes the file upload parameters and a reader
// and returns a filenode. This method turns the reader into a FileNode without
// effectively uploading the data. It is used to perform a dry-run of a skyfile
// upload.
func (r *Renter) managedCreateFileNodeFromReader(up modules.FileUploadParams, reader io.Reader) (*filesystem.FileNode, error) {
	// Check the upload params first.
	fileNode, err := r.managedInitUploadStream(up)
	if err != nil {
		return nil, err
	}

	// Extract some helper variables
	hpk := types.SiaPublicKey{} // blank host key
	ec := fileNode.ErasureCode()
	psize := fileNode.PieceSize()
	csize := fileNode.ChunkSize()

	var peek []byte
	for chunkIndex := uint64(0); ; chunkIndex++ {
		// Grow the SiaFile to the right size.
		err := fileNode.SiaFile.GrowNumChunks(chunkIndex + 1)
		if err != nil {
			return nil, err
		}

		// Allocate data pieces and fill them with data from r.
		ss := NewStreamShard(reader, peek)
		err = func() error {
			defer ss.Close()

			dataPieces, total, errRead := readDataPieces(ss, ec, psize)
			if errRead != nil {
				return errRead
			}

			dataEncoded, _ := ec.EncodeShards(dataPieces)
			for pieceIndex, dataPieceEnc := range dataEncoded {
				if err := fileNode.SiaFile.AddPiece(hpk, chunkIndex, uint64(pieceIndex), crypto.MerkleRoot(dataPieceEnc)); err != nil {
					return err
				}
			}

			adjustedSize := fileNode.Size() - csize + total
			if err := fileNode.SetFileSize(adjustedSize); err != nil {
				return errors.AddContext(err, "failed to adjust FileSize")
			}
			return nil
		}()
		if err != nil {
			return nil, err
		}

		_, err = ss.Result()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return fileNode, nil
}

// Blacklist returns the merkleroots that are blacklisted
func (r *Renter) Blacklist() ([]crypto.Hash, error) {
	err := r.tg.Add()
	if err != nil {
		return []crypto.Hash{}, err
	}
	defer r.tg.Done()
	return r.staticSkynetBlacklist.Blacklist(), nil
}

// UpdateSkynetBlacklist updates the list of skylinks that are blacklisted
func (r *Renter) UpdateSkynetBlacklist(additions, removals []modules.Skylink) error {
	err := r.tg.Add()
	if err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticSkynetBlacklist.UpdateBlacklist(additions, removals)
}

// Portals returns the list of known skynet portals.
func (r *Renter) Portals() ([]modules.SkynetPortal, error) {
	err := r.tg.Add()
	if err != nil {
		return []modules.SkynetPortal{}, err
	}
	defer r.tg.Done()
	return r.staticSkynetPortals.Portals(), nil
}

// UpdateSkynetPortals updates the list of known Skynet portals that are listed.
func (r *Renter) UpdateSkynetPortals(additions []modules.SkynetPortal, removals []modules.NetAddress) error {
	err := r.tg.Add()
	if err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticSkynetPortals.UpdatePortals(additions, removals)
}

// uploadSkyfileReadLeadingChunk will read the leading chunk of a skyfile. If
// entire file is small enough to fit inside of the leading chunk, the return
// value will be:
//
//   (fileBytes, nil, false, nil)
//
// And if the entire file is too large to fit inside of the leading chunk, the
// return value will be:
//
//   (nil, fileReader, true, nil)
//
// where the fileReader contains all of the data for the file, including the
// data that uploadSkyfileReadLeadingChunk had to read to figure out whether
// the file was too large to fit into the leading chunk.
func uploadSkyfileReadLeadingChunk(r io.Reader, headerSize uint64) ([]byte, io.Reader, bool, error) {
	// Check for underflow.
	if headerSize+1 > modules.SectorSize {
		return nil, nil, false, ErrMetadataTooBig
	}
	// Read data from the reader to fill out the remainder of the first sector.
	fileBytes := make([]byte, modules.SectorSize-headerSize, modules.SectorSize-headerSize+1) // +1 capacity for the peek byte
	size, err := io.ReadFull(r, fileBytes)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	if err != nil {
		return nil, nil, false, errors.AddContext(err, "unable to read the file data")
	}
	// Set fileBytes to the right size.
	fileBytes = fileBytes[:size]

	// See whether there is more data in the reader. If there is no more data in
	// the reader, a small file will be signaled and the data that has been read
	// will be returned.
	peek := make([]byte, 1)
	n, peekErr := io.ReadFull(r, peek)
	if peekErr == io.EOF || peekErr == io.ErrUnexpectedEOF {
		peekErr = nil
	}
	if peekErr != nil {
		return nil, nil, false, errors.AddContext(err, "too much data provided, cannot create skyfile")
	}
	if n == 0 {
		// There is no more data, return the data that was read from the reader
		// and signal a small file.
		return fileBytes, nil, false, nil
	}

	// There is more data. Create a prepend reader using the data we've already
	// read plus the reader that we read from, effectively creating a new reader
	// that is identical to the one that was passed in if no data had been read.
	prependData := append(fileBytes, peek...)
	fullReader := io.MultiReader(bytes.NewReader(prependData), r)
	return nil, fullReader, true, nil
}

// managedUploadSkyfileLargeFile will accept a fileReader containing all of the
// data to a large siafile and upload it to the Sia network using
// 'callUploadStreamFromReader'. The final skylink is created by calling
// 'CreateSkylinkFromSiafile' on the resulting siafile.
func (r *Renter) managedUploadSkyfileLargeFile(lup modules.SkyfileUploadParameters, fileReader io.Reader) (modules.Skylink, error) {
	// Create the erasure coder to use when uploading the file. When going
	// through the 'managedUploadSkyfile' command, a 1-of-N scheme is always
	// used, where the redundancy of the data as a whole matches the proposed
	// redundancy for the base chunk.
	ec, err := siafile.NewRSSubCode(1, int(lup.BaseChunkRedundancy)-1, crypto.SegmentSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to create erasure coder for large file")
	}
	// Create the siapath for the skyfile extra data. This is going to be the
	// same as the skyfile upload siapath, except with a suffix.
	siaPath, err := modules.NewSiaPath(lup.SiaPath.String() + ExtendedSuffix)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to create SiaPath for large skyfile extended data")
	}
	fup := modules.FileUploadParams{
		SiaPath:             siaPath,
		ErasureCode:         ec,
		Force:               lup.Force,
		DisablePartialChunk: true,  // must be set to true - partial chunks change, content addressed files must not change.
		Repair:              false, // indicates whether this is a repair operation

		CipherType: crypto.TypePlain,
	}

	// Check if an encryption key was specified.
	if encryptionEnabled(lup) {
		fanoutSkykey, err := lup.FileSpecificSkykey.DeriveSubkey(fanoutNonceDerivation[:])
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "unable to derive fanout subkey")
		}
		fup.CipherKey, err = fanoutSkykey.CipherKey()
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "unable to get skykey cipherkey")
		}
		fup.CipherType = lup.FileSpecificSkykey.CipherType()
	}

	var fileNode *filesystem.FileNode
	if lup.DryRun {
		// In case of a dry-run we don't want to perform the actual upload,
		// instead we create a filenode that contains all of the data pieces and
		// their merkle roots.
		fileNode, err = r.managedCreateFileNodeFromReader(fup, fileReader)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "unable to upload large skyfile")
		}
	} else {
		// Upload the file using a streamer.
		fileNode, err = r.callUploadStreamFromReader(fup, fileReader)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "unable to upload large skyfile")
		}
	}

	// Defer closing and cleanup of the file in case this was a dry-run
	defer func() {
		err := fileNode.Close()
		if err != nil {
			r.log.Printf("Could not close node, err: %s\n", err.Error())
		}

		if lup.DryRun {
			if err := r.DeleteFile(siaPath); err != nil {
				r.log.Printf("unable to cleanup siafile after performing a dry run of the Skyfile upload, err: %s", err.Error())
			}
		}
	}()

	// Now that we know the filesize, extend the metadata.
	lup.FileMetadata.Length = fileNode.Size()

	// Convert the new siafile we just uploaded into a skyfile using the
	// convert function.
	return r.managedCreateSkylinkFromFileNode(lup, fileNode)
}

// managedUploadBaseSector will take the raw baseSector bytes and upload them,
// returning the resulting merkle root, and the fileNode of the siafile that is
// tracking the base sector.
func (r *Renter) managedUploadBaseSector(lup modules.SkyfileUploadParameters, baseSector []byte, skylink modules.Skylink) error {
	fileUploadParams, err := fileUploadParamsFromLUP(lup)
	if err != nil {
		return errors.AddContext(err, "failed to create siafile upload parameters")
	}
	fileUploadParams.CipherType = crypto.TypePlain // the baseSector should be encrypted by the caller.

	// Perform the actual upload. This will require turning the base sector into
	// a reader.
	baseSectorReader := bytes.NewReader(baseSector)
	fileNode, err := r.callUploadStreamFromReader(fileUploadParams, baseSectorReader)
	if err != nil {
		return errors.AddContext(err, "failed to stream upload small skyfile")
	}
	defer fileNode.Close()

	// Add the skylink to the Siafile.
	err = fileNode.AddSkylink(skylink)
	return errors.AddContext(err, "unable to add skylink to siafile")
}

// managedUploadSkyfileSmallFile uploads a file that fits entirely in the
// leading chunk of a skyfile to the Sia network and returns the skylink that
// can be used to access the file.
func (r *Renter) managedUploadSkyfileSmallFile(lup modules.SkyfileUploadParameters, fileBytes []byte) (modules.Skylink, error) {
	// Complete the metadata by setting the length and marshal it.
	lup.FileMetadata.Length = uint64(len(fileBytes))
	metadataBytes, err := skyfileMetadataBytes(lup.FileMetadata)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to retrieve skyfile metadata bytes")
	}

	sl := skynet.SkyfileLayout{
		Version:      skynet.SkyfileVersion,
		Filesize:     uint64(len(fileBytes)),
		MetadataSize: uint64(len(metadataBytes)),
		// No fanout is set yet.
		// If encryption is set in the upload params, this will be overwritten.
		CipherType: crypto.TypePlain,
	}

	// Create the base sector. This is done as late as possible so that any
	// errors are caught before a large block of memory is allocated.
	baseSector, fetchSize := skyfileBuildBaseSector(sl.Encode(), nil, metadataBytes, fileBytes) // 'nil' because there is no fanout

	if encryptionEnabled(lup) {
		err := encryptBaseSectorWithSkykey(baseSector, sl, lup.FileSpecificSkykey)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "Failed to encrypt base sector for upload")
		}
	}

	// Create the skylink.
	baseSectorRoot := crypto.MerkleRoot(baseSector) // Should be identical to the sector roots for each sector in the siafile.
	skylink, err := modules.NewSkylinkV1(baseSectorRoot, 0, fetchSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "failed to build the skylink")
	}

	// If this is a dry-run, we do not need to upload the base sector
	if lup.DryRun {
		return skylink, nil
	}

	// Upload the base sector.
	err = r.managedUploadBaseSector(lup, baseSector, skylink)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "failed to upload base sector")
	}
	return skylink, nil
}

// DownloadSkylink will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) DownloadSkylink(link modules.Skylink, timeout time.Duration) (modules.SkyfileMetadata, modules.Streamer, error) {
	if err := r.tg.Add(); err != nil {
		return modules.SkyfileMetadata{}, nil, err
	}
	defer r.tg.Done()
	return r.managedDownloadSkylink(link, timeout)
}

// DownloadSkylinkRaw will take a link and turn it into the data of a download
// without any decoding of the metadata, fanout, or decryption.
func (r *Renter) DownloadSkylinkRaw(link modules.Skylink, timeout time.Duration) (modules.Streamer, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	baseSector, _, err := r.managedDownloadBaseSector(link, timeout)
	return streamerFromSlice(baseSector), err
}

// managedDownloadSkylink will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) managedDownloadSkylink(link modules.Skylink, timeout time.Duration) (modules.SkyfileMetadata, modules.Streamer, error) {
	if r.deps.Disrupt("resolveSkylinkToFixture") {
		sf, err := fixtures.LoadSkylinkFixture(link)
		if err != nil {
			return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "failed to fetch fixture")
		}
		return sf.Metadata, streamerFromSlice(sf.Content), nil
	}

	baseSector, streamer, err := r.managedDownloadBaseSector(link, timeout)
	if err != nil {
		return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "unable to perform raw download of the skyfile")
	}
	if streamer != nil {
		return streamer.Metadata(), streamer, nil
	}

	// Check if the base sector is encrypted, and attempt to decrypt it.
	// This will fail if we don't have the decryption key.
	var fileSpecificSkykey skykey.Skykey
	if isEncryptedBaseSector(baseSector) {
		fileSpecificSkykey, err = r.decryptBaseSector(baseSector)
		if err != nil {
			return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "Unable to decrypt skyfile base sector")
		}
	}

	// Parse out the metadata of the skyfile.
	layout, fanoutBytes, metadata, baseSectorPayload, err := skynet.ParseSkyfileMetadata(baseSector)
	if err != nil {
		return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "error parsing skyfile metadata")
	}

	// If there is no fanout, all of the data will be contained in the base
	// sector, return a streamer using the data from the base sector.
	if layout.FanoutSize == 0 {
		streamer := streamerFromSlice(baseSectorPayload)
		return metadata, streamer, nil
	}

	// There is a fanout, create a fanout streamer and return that.
	fs, err := r.newFanoutStreamer(link, layout, metadata, fanoutBytes, timeout, fileSpecificSkykey)
	if err != nil {
		return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "unable to create fanout fetcher")
	}
	return metadata, fs, nil
}

// managedDownloadBaseSector will download the baseSector for the skylink or
// return the active stream
func (r *Renter) managedDownloadBaseSector(link modules.Skylink, timeout time.Duration) ([]byte, *stream, error) {
	// Check if link is blacklisted
	if r.staticSkynetBlacklist.IsBlacklisted(link) {
		return nil, nil, ErrSkylinkBlacklisted
	}

	// Check if this skylink is already in the stream buffer set. If so, we can
	// skip the lookup procedure and use any data that other threads have
	// cached.
	id := link.DataSourceID()
	streamer, exists := r.staticStreamBufferSet.callNewStreamFromID(id, 0)
	if exists {
		return nil, streamer, nil
	}

	// Pull the offset and fetchSize out of the skylink.
	offset, fetchSize, err := link.OffsetAndFetchSize()
	if err != nil {
		return nil, nil, errors.AddContext(err, "unable to parse skylink")
	}

	// Fetch the leading chunk.
	baseSector, err := r.DownloadByRoot(link.MerkleRoot(), offset, fetchSize, timeout)
	if err != nil {
		return nil, nil, errors.AddContext(err, "unable to fetch base sector of skylink")
	}
	if len(baseSector) < skynet.SkyfileLayoutSize {
		return nil, nil, errors.New("download did not fetch enough data, layout cannot be decoded")
	}

	// Return the baseSector
	return baseSector, nil, nil
}

// PinSkylink wil fetch the file associated with the Skylink, and then pin all
// necessary content to maintain that Skylink.
func (r *Renter) PinSkylink(skylink modules.Skylink, lup modules.SkyfileUploadParameters, timeout time.Duration) error {
	// Check if link is blacklisted
	if r.staticSkynetBlacklist.IsBlacklisted(skylink) {
		return ErrSkylinkBlacklisted
	}

	// Set sane defaults for unspecified values.
	skyfileEstablishDefaults(&lup)

	// Fetch the leading chunk.
	baseSector, err := r.DownloadByRoot(skylink.MerkleRoot(), 0, modules.SectorSize, timeout)
	if err != nil {
		return errors.AddContext(err, "unable to fetch base sector of skylink")
	}
	if uint64(len(baseSector)) != modules.SectorSize {
		return errors.New("download did not fetch enough data, file cannot be re-pinned")
	}

	// Check if the base sector is encrypted, and attempt to decrypt it.
	var fileSpecificSkykey skykey.Skykey
	encrypted := isEncryptedBaseSector(baseSector)
	if encrypted {
		fileSpecificSkykey, err = r.decryptBaseSector(baseSector)
		if err != nil {
			return errors.AddContext(err, "Unable to decrypt skyfile base sector")
		}
	}

	// Parse out the metadata of the skyfile.
	layout, fanoutBytes, metadata, _, err := skynet.ParseSkyfileMetadata(baseSector)
	if err != nil {
		return errors.AddContext(err, "error parsing skyfile metadata")
	}

	// Start setting up the FUP.
	fup := modules.FileUploadParams{
		Force:               lup.Force,
		DisablePartialChunk: true,  // must be set to true - partial chunks change, content addressed files must not change.
		Repair:              false, // indicates whether this is a repair operation
		CipherType:          crypto.TypePlain,
	}

	// Re-encrypt the baseSector for upload and add the fanout key to the fup.
	if encrypted {
		err = encryptBaseSectorWithSkykey(baseSector, layout, fileSpecificSkykey)
		if err != nil {
			return errors.AddContext(err, "Error re-encrypting base sector")
		}

		// Derive the fanout key and add to the fup.
		fanoutSkykey, err := fileSpecificSkykey.DeriveSubkey(fanoutNonceDerivation[:])
		if err != nil {
			return errors.AddContext(err, "Error deriving fanout skykey")
		}
		fup.CipherKey, err = fanoutSkykey.CipherKey()
		if err != nil {
			return errors.AddContext(err, "Error getting fanout CipherKey")
		}
		fup.CipherType = fanoutSkykey.CipherType()

		// These fields aren't used yet, but we'll set them anyway to mimic behavior in
		// upload/download code for consistency.
		lup.SkykeyName = fileSpecificSkykey.Name
		lup.FileSpecificSkykey = fileSpecificSkykey
	}

	// Re-upload the baseSector.
	err = r.managedUploadBaseSector(lup, baseSector, skylink)
	if err != nil {
		return errors.AddContext(err, "unable to upload base sector")
	}

	// If there is no fanout, nothing more to do, the pin is complete.
	if layout.FanoutSize == 0 {
		return nil
	}
	// Create the erasure coder to use when uploading the file bulk.
	fup.ErasureCode, err = siafile.NewRSSubCode(int(layout.FanoutDataPieces), int(layout.FanoutParityPieces), crypto.SegmentSize)
	if err != nil {
		return errors.AddContext(err, "unable to create erasure coder for large file")
	}
	// Create the siapath for the skyfile extra data. This is going to be the
	// same as the skyfile upload siapath, except with a suffix.
	fup.SiaPath, err = modules.NewSiaPath(lup.SiaPath.String() + "-extended")
	if err != nil {
		return errors.AddContext(err, "unable to create SiaPath for large skyfile extended data")
	}

	// Create the fanout streamer that will download the file.
	streamer, err := r.newFanoutStreamer(skylink, layout, metadata, fanoutBytes, timeout, fileSpecificSkykey)
	if err != nil {
		return errors.AddContext(err, "Failed to create fanout streamer for large skyfile pin")
	}

	// Upload directly from the fanout download streamer.
	fileNode, err := r.callUploadStreamFromReader(fup, streamer)
	if err != nil {
		return errors.AddContext(err, "unable to upload large skyfile")
	}
	err = fileNode.AddSkylink(skylink)
	if err != nil {
		return errors.AddContext(err, "unable to upload skyfile fanout")
	}
	return nil
}

// UploadSkyfile will upload the provided data with the provided metadata,
// returning a skylink which can be used by any viewnode to recover the full
// original file and metadata. The skylink will be unique to the combination of
// both the file data and metadata.
func (r *Renter) UploadSkyfile(lup modules.SkyfileUploadParameters) (modules.Skylink, error) {
	// Set reasonable default values for any lup fields that are blank.
	err := skyfileEstablishDefaults(&lup)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "skyfile upload parameters are incorrect")
	}

	// If a skykey name or ID was specified, generate a file-specific key for this
	// upload.
	if encryptionEnabled(lup) && lup.SkykeyName != "" {
		key, err := r.SkykeyByName(lup.SkykeyName)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "UploadSkyfile unable to get skykey")
		}
		lup.FileSpecificSkykey, err = key.GenerateFileSpecificSubkey()
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "UploadSkyfile unable to generate subkey")
		}
	} else if encryptionEnabled(lup) {
		key, err := r.SkykeyByID(lup.SkykeyID)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "UploadSkyfile unable to get skykey")
		}

		lup.FileSpecificSkykey, err = key.GenerateFileSpecificSubkey()
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "UploadSkyfile unable to generate subkey")
		}
	}

	// Additional input check - this check is unique to uploading a skyfile
	// from a streamer. The convert siafile function does not need to be passed
	// a reader.
	if lup.Reader == nil {
		return modules.Skylink{}, errors.New("need to provide a stream of upload data")
	}

	// Create a dummy metadata and marshal it for computing the size of the
	// header. By using the maximum length, we make sure that we reserve enough
	// space.
	dummyMD := lup.FileMetadata
	dummyMD.Length = math.MaxUint64
	dummyMDBytes, err := skyfileMetadataBytes(dummyMD)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to retrieve skyfile metadata bytes")
	}

	// Read data from the lup reader. If the file data provided fits entirely
	// into the leading chunk, this method will use that data to upload a
	// skyfile directly. If the file data provided does not fit entirely into
	// the leading chunk, a separate method will be called to upload the file
	// separately using upload streaming, and then the siafile conversion
	// function will be used to generate the final skylink.
	headerSize := uint64(skynet.SkyfileLayoutSize + len(dummyMDBytes))
	fileBytes, fileReader, largeFile, err := uploadSkyfileReadLeadingChunk(lup.Reader, headerSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to retrieve leading chunk file bytes")
	}

	var skylink modules.Skylink
	if largeFile {
		skylink, err = r.managedUploadSkyfileLargeFile(lup, fileReader)
	} else {
		skylink, err = r.managedUploadSkyfileSmallFile(lup, fileBytes)
	}
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to upload skyfile")
	}
	if lup.DryRun {
		return skylink, nil
	}

	// Check if skylink is blacklisted
	if !r.staticSkynetBlacklist.IsBlacklisted(skylink) {
		return skylink, nil
	}

	// Skylink is blacklisted, try and delete the file and return an error
	deleteErr := r.DeleteFile(lup.SiaPath)
	if largeFile {
		extendedSiaPath, err := modules.NewSiaPath(lup.SiaPath.String() + ExtendedSuffix)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "unable to create extended SiaPath for large skyfile deletion")
		}
		deleteErr = errors.Compose(deleteErr, r.DeleteFile(extendedSiaPath))
	}
	return modules.Skylink{}, errors.Compose(ErrSkylinkBlacklisted, deleteErr)
}
