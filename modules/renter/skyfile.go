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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// SkyfileLayoutSize describes the amount of space within the first sector
	// of a skyfile used to describe the rest of the skyfile.
	SkyfileLayoutSize = 99

	// SkyfileDefaultBaseChunkRedundancy establishes the default redundancy for
	// the base chunk of a skyfile.
	SkyfileDefaultBaseChunkRedundancy = 10

	// SkyfileVersion establishes the current version for creating skyfiles.
	// The skyfile versions are different from the siafile versions.
	SkyfileVersion = 1
)

// skyfileLayout explains the layout information that is used for storing data
// inside of the skyfile. The skyfileLayout always appears as the first bytes
// of the leading chunk.
type skyfileLayout struct {
	version            uint8
	filesize           uint64
	metadataSize       uint64
	fanoutSize         uint64
	fanoutDataPieces   uint8
	fanoutParityPieces uint8
	cipherType         crypto.CipherType
	cipherKey          [64]byte // cipherKey is incompatible with ciphers that need keys larger than 64 bytes
}

// encode will return a []byte that has compactly encoded all of the layout
// data.
func (ll *skyfileLayout) encode() []byte {
	b := make([]byte, SkyfileLayoutSize)
	offset := 0
	b[offset] = ll.version
	offset += 1
	binary.LittleEndian.PutUint64(b[offset:], ll.filesize)
	offset += 8
	binary.LittleEndian.PutUint64(b[offset:], ll.metadataSize)
	offset += 8
	binary.LittleEndian.PutUint64(b[offset:], ll.fanoutSize)
	offset += 8
	b[offset] = ll.fanoutDataPieces
	offset += 1
	b[offset] = ll.fanoutParityPieces
	offset += 1
	copy(b[offset:], ll.cipherType[:])
	offset += len(ll.cipherType)
	copy(b[offset:], ll.cipherKey[:])
	offset += len(ll.cipherKey)

	// Sanity check. If this check fails, encode() does not match the
	// SkyfileLayoutSize.
	if offset != SkyfileLayoutSize {
		build.Critical("layout size does not match the amount of data encoded")
	}
	return b
}

// decode will take a []byte and load the layout from that []byte.
func (ll *skyfileLayout) decode(b []byte) {
	offset := 0
	ll.version = b[offset]
	offset += 1
	ll.filesize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.metadataSize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.fanoutSize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.fanoutDataPieces = b[offset]
	offset += 1
	ll.fanoutParityPieces = b[offset]
	offset += 1
	copy(ll.cipherType[:], b[offset:])
	offset += len(ll.cipherType)
	copy(ll.cipherKey[:], b[offset:])
	offset += len(ll.cipherKey)

	// Sanity check. If this check fails, decode() does not match the
	// SkyfileLayoutSize.
	if offset != SkyfileLayoutSize {
		build.Critical("layout size does not match the amount of data decoded")
	}
}

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
	// also resiliant to host failures.
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

		CipherType: crypto.TypePlain,
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
	return r.managedCreateSkylinkFromFileNode(lup, nil, fileNode, siaPath.Name())
}

// managedCreateSkylinkFromFileNode creates a skylink from a file node.
//
// The name needs to be passed in explicitly because a file node does not track
// its own name, which allows the file to be renamed concurrently without
// causing any race conditions.
func (r *Renter) managedCreateSkylinkFromFileNode(lup modules.SkyfileUploadParameters, metadataBytes []byte, fileNode *filesystem.FileNode, filename string) (modules.Skylink, error) {
	// Check that the encryption key and erasure code is compatible with the
	// skyfile format. This is intentionally done before any heavy computation
	// to catch early errors.
	var ll skyfileLayout
	masterKey := fileNode.MasterKey()
	if len(masterKey.Key()) > len(ll.cipherKey) {
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
		return modules.Skylink{}, errors.New("skylinks currently only support 1-of-N redundancy, other redundancies will be supported in a later version")
	}

	// Create the metadata for this siafile.
	if metadataBytes == nil {
		fm := modules.SkyfileMetadata{
			Filename: filename,
			Mode:     fileNode.Mode(),
		}
		var err error
		metadataBytes, err = skyfileMetadataBytes(fm)
		if err != nil {
			return modules.Skylink{}, errors.AddContext(err, "error retrieving skyfile metadata bytes")
		}
	}

	// Create the fanout for the siafile.
	fanoutBytes, err := skyfileEncodeFanout(fileNode)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to encode the fanout of the siafile")
	}
	headerSize := uint64(SkyfileLayoutSize + len(metadataBytes) + len(fanoutBytes))
	if headerSize > modules.SectorSize {
		return modules.Skylink{}, fmt.Errorf("skyfile does not fit in leading chunk - metadata size plus fanout size must be less than %v bytes, metadata size is %v bytes and fanout size is %v bytes", modules.SectorSize-SkyfileLayoutSize, len(metadataBytes), len(fanoutBytes))
	}

	// Assemble the first chunk of the skyfile.
	ll = skyfileLayout{
		version:            SkyfileVersion,
		filesize:           fileNode.Size(),
		metadataSize:       uint64(len(metadataBytes)),
		fanoutSize:         uint64(len(fanoutBytes)),
		fanoutDataPieces:   uint8(ec.MinPieces()),
		fanoutParityPieces: uint8(ec.NumPieces() - ec.MinPieces()),
		cipherType:         masterKey.Type(),
	}
	copy(ll.cipherKey[:], masterKey.Key())

	// Create the base sector.
	baseSector, fetchSize := skyfileBuildBaseSector(ll.encode(), fanoutBytes, metadataBytes, nil)
	baseSectorReader := bytes.NewReader(baseSector)
	fileUploadParams, err := fileUploadParamsFromLUP(lup)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to build the file upload parameters")
	}

	// Perform the full upload.
	newFileNode, err := r.callUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "skyfile base chunk upload failed")
	}
	defer newFileNode.Close()

	// Create the skylink.
	baseSectorRoot := crypto.MerkleRoot(baseSector)
	skylink, err := modules.NewSkylinkV1(baseSectorRoot, 0, fetchSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to build skylink")
	}

	// Add the skylink to the siafiles.
	err1 := fileNode.AddSkylink(skylink)
	err2 := newFileNode.AddSkylink(skylink)
	err = errors.Compose(err1, err2)
	return skylink, errors.AddContext(err, "unable to add skylink to the sianodes")
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
func uploadSkyfileReadLeadingChunk(lup modules.SkyfileUploadParameters, headerSize uint64) ([]byte, io.Reader, bool, error) {
	// Read data from the reader to fill out the remainder of the first sector.
	fileBytes := make([]byte, modules.SectorSize-headerSize, modules.SectorSize-headerSize+1) // +1 capacity for the peek byte
	size, err := io.ReadFull(lup.Reader, fileBytes)
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
	n, peekErr := io.ReadFull(lup.Reader, peek)
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
	fullReader := io.MultiReader(bytes.NewReader(prependData), lup.Reader)
	return nil, fullReader, true, nil
}

// managedUploadSkyfileLargeFile will accept a fileReader containing all of the
// data to a large siafile and upload it to the Sia network using
// 'callUploadStreamFromReader'. The final skylink is created by calling
// 'CreateSkylinkFromSiafile' on the resulting siafile.
func (r *Renter) managedUploadSkyfileLargeFile(lup modules.SkyfileUploadParameters, metadataBytes []byte, fileReader io.Reader) (modules.Skylink, error) {
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
	siaPath, err := modules.NewSiaPath(lup.SiaPath.String() + "-extended")
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

	// Upload the file using a streamer.
	fileNode, err := r.callUploadStreamFromReader(fup, fileReader, false)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to upload large skyfile")
	}
	defer fileNode.Close()

	// Convert the new siafile we just uploaded into a skyfile using the
	// convert function.
	return r.managedCreateSkylinkFromFileNode(lup, metadataBytes, fileNode, siaPath.Name())
}

// managedUploadBaseSector will take the raw baseSector bytes and upload them,
// returning the resulting merkle root, and the fileNode of the siafile that is
// tracking the base sector.
func (r *Renter) managedUploadBaseSector(lup modules.SkyfileUploadParameters, baseSector []byte, skylink modules.Skylink) error {
	fileUploadParams, err := fileUploadParamsFromLUP(lup)
	if err != nil {
		return errors.AddContext(err, "failed to create siafile upload parameters")
	}

	// Perform the actual upload. This will require turning the base sector into
	// a reader.
	baseSectorReader := bytes.NewReader(baseSector)
	fileNode, err := r.callUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
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
func (r *Renter) managedUploadSkyfileSmallFile(lup modules.SkyfileUploadParameters, metadataBytes []byte, fileBytes []byte) (modules.Skylink, error) {
	ll := skyfileLayout{
		version:      SkyfileVersion,
		filesize:     uint64(len(fileBytes)),
		metadataSize: uint64(len(metadataBytes)),
		// No fanout, no encryption.
	}

	// Create the base sector. This is done as late as possible so that any
	// errors are caught before a large block of memory is allocated.
	baseSector, fetchSize := skyfileBuildBaseSector(ll.encode(), nil, metadataBytes, fileBytes) // 'nil' because there is no fanout

	// Create the skylink.
	baseSectorRoot := crypto.MerkleRoot(baseSector) // Should be identical to the sector roots for each sector in the siafile.
	skylink, err := modules.NewSkylinkV1(baseSectorRoot, 0, fetchSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "failed to build the skylink")
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
func (r *Renter) DownloadSkylink(link modules.Skylink) (modules.SkyfileMetadata, modules.Streamer, error) {
	// Fetch the leading chunk.
	baseSector, offset, err := r.DownloadLeadingChunk(link)
	if err != nil {
		return modules.SkyfileMetadata{}, nil, err
	}

	// Parse out the skyfileLayout.
	var ll skyfileLayout
	ll.decode(baseSector)
	offset += SkyfileLayoutSize

	// Parse out the fanout.
	fanoutBytes := baseSector[offset : offset+ll.fanoutSize]
	offset += ll.fanoutSize

	// Parse out the skyfile metadata.
	var lfm modules.SkyfileMetadata
	metadataSize := uint64(ll.metadataSize)
	err = json.Unmarshal(baseSector[offset:offset+metadataSize], &lfm)
	if err != nil {
		return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "unable to parse link file metadata")
	}
	offset += metadataSize

	// If there is no fanout, all of the data will be contained in the base
	// sector, return a streamer using the data from the base sector.
	if ll.fanoutSize == 0 {
		streamer := streamerFromSlice(baseSector[offset : offset+ll.filesize])
		return lfm, streamer, nil
	}
	if offset+metadataSize+ll.fanoutSize > modules.SectorSize {
		return modules.SkyfileMetadata{}, nil, errors.New("fanout is more than one sector, that is unsupported in this version")
	}

	// There is a fanout, create a fanout streamer and return that.
	fs, err := r.newFanoutStreamer(link, ll, fanoutBytes, modules.SkyfileSubfileMetadata{})
	if err != nil {
		return modules.SkyfileMetadata{}, nil, errors.AddContext(err, "unable to create fanout fetcher")
	}
	return lfm, fs, nil
}

// DownloadSkyfileSubfile will take a skylink and filename, and turn it into the
// metadata and data of a download for the subfile with given filename.
func (r *Renter) DownloadSkyfileSubfile(link modules.Skylink, filename string) (modules.SkyfileSubfileMetadata, modules.Streamer, error) {
	// Fetch the leading chunk.
	baseSector, offset, err := r.DownloadLeadingChunk(link)
	if err != nil {
		return modules.SkyfileSubfileMetadata{}, nil, err
	}

	// Parse out the skyfileLayout.
	var ll skyfileLayout
	ll.decode(baseSector)
	offset += SkyfileLayoutSize

	// Parse out the fanout.
	fanoutBytes := baseSector[offset : offset+ll.fanoutSize]
	offset += ll.fanoutSize

	// Parse out the skyfile metadata.
	var lfm modules.SkyfileMetadata
	metadataSize := uint64(ll.metadataSize)
	err = json.Unmarshal(baseSector[offset:offset+metadataSize], &lfm)
	if err != nil {
		return modules.SkyfileSubfileMetadata{}, nil, errors.AddContext(err, "unable to parse link file metadata")
	}
	offset += metadataSize

	// Find the subfile for given filename
	sfm := lfm.SkyfileSubfileMetadata(filename)
	if sfm.Equals(modules.SkyfileSubfileMetadata{}) {
		return modules.SkyfileSubfileMetadata{}, nil, errors.New("unable to find subfile for given filename")
	}

	// If there is no fanout, all of the data will be contained in the base
	// sector, return a streamer using the data from the base sector.
	if ll.fanoutSize == 0 {
		offset += sfm.Offset
		streamer := streamerFromSlice(baseSector[offset : offset+sfm.Len])
		return sfm, streamer, nil
	}

	if offset+metadataSize+ll.fanoutSize > modules.SectorSize {
		return modules.SkyfileSubfileMetadata{}, nil, errors.New("fanout is more than one sector, that is unsupported in this version")
	}

	// There is a fanout, create a fanout streamer and return that.
	fs, err := r.newFanoutStreamer(link, ll, fanoutBytes, sfm)
	if err != nil {
		return modules.SkyfileSubfileMetadata{}, nil, errors.AddContext(err, "unable to create fanout fetcher")
	}
	return sfm, fs, nil
}

// PinSkylink wil fetch the file associated with the Skylink, and then pin all
// necessary content to maintain that Skylink.
func (r *Renter) PinSkylink(skylink modules.Skylink, lup modules.SkyfileUploadParameters) error {
	// Fetch the leading chunk.
	baseSector, err := r.DownloadByRoot(skylink.MerkleRoot(), 0, modules.SectorSize)
	if err != nil {
		return errors.AddContext(err, "unable to fetch base sector of skylink")
	}
	if uint64(len(baseSector)) != modules.SectorSize {
		return errors.New("download did not fetch enough data, file cannot be re-pinned")
	}
	var offset uint64

	// Parse out the skyfileLayout.
	var ll skyfileLayout
	ll.decode(baseSector)
	offset += SkyfileLayoutSize

	// Sanity check on the version.
	if ll.version != 1 {
		return errors.New("unsupported skyfile version")
	}

	// Parse out the fanout.
	fanoutBytes := baseSector[offset : offset+ll.fanoutSize]
	offset += ll.fanoutSize

	// Re-upload the baseSector.
	err = r.managedUploadBaseSector(lup, baseSector, skylink)
	if err != nil {
		return errors.AddContext(err, "unable to upload base sector")
	}

	// If there is no fanout, nothing more to do, the pin is complete.
	if ll.fanoutSize == 0 {
		return nil
	}

	// Create the erasure coder to use when uploading the file bulk. When going
	// through the 'managedUploadSkyfile' command, a 1-of-N scheme is always used,
	// where the redundancy of the data as a whole matches the proposed
	// redundancy for the base chunk.
	ec, err := siafile.NewRSSubCode(int(ll.fanoutDataPieces), int(ll.fanoutParityPieces), crypto.SegmentSize)
	if err != nil {
		return errors.AddContext(err, "unable to create erasure coder for large file")
	}
	// Create the siapath for the skyfile extra data. This is going to be the
	// same as the skyfile upload siapath, except with a suffix.
	siaPath, err := modules.NewSiaPath(lup.SiaPath.String() + "-extended")
	if err != nil {
		return errors.AddContext(err, "unable to create SiaPath for large skyfile extended data")
	}
	fup := modules.FileUploadParams{
		SiaPath:             siaPath,
		ErasureCode:         ec,
		Force:               lup.Force,
		DisablePartialChunk: true,  // must be set to true - partial chunks change, content addressed files must not change.
		Repair:              false, // indicates whether this is a repair operation

		CipherType: crypto.TypePlain,
	}

	sf := modules.SkyfileSubfileMetadata{}
	streamer, err := r.newFanoutStreamer(skylink, ll, fanoutBytes, sf)
	if err != nil {
		return errors.AddContext(err, "Failed to create fanout streamer for large skyfile pin")
	}
	fileNode, err := r.callUploadStreamFromReader(fup, streamer, false)
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
	// Additional input check - this check is unique to uploading a skyfile
	// from a streamer. The convert siafile function does not need to be passed
	// a reader.
	if lup.Reader == nil {
		return modules.Skylink{}, errors.New("need to provide a stream of upload data")
	}

	// Grab the metadata bytes.
	metadataBytes, err := skyfileMetadataBytes(lup.FileMetadata)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to retrieve skyfile metadata bytes")
	}

	// Read data from the lup reader. If the file data provided fits entirely
	// into the leading chunk, this method will use that data to upload a
	// skyfile directly. If the file data provided does not fit entirely into
	// the leading chunk, a separate method will be called to upload the file
	// separately using upload streaming, and then the siafile conversion
	// function will be used to generate the final skylink.
	headerSize := uint64(SkyfileLayoutSize + len(metadataBytes))
	fileBytes, fileReader, largeFile, err := uploadSkyfileReadLeadingChunk(lup, headerSize)
	if err != nil {
		return modules.Skylink{}, errors.AddContext(err, "unable to retrieve leading chunk file bytes")
	}
	if largeFile {
		return r.managedUploadSkyfileLargeFile(lup, metadataBytes, fileReader)
	}
	return r.managedUploadSkyfileSmallFile(lup, metadataBytes, fileBytes)
}

// DownloadLeadingChunk will download the first sector of the file for given
// skylink.
func (r *Renter) DownloadLeadingChunk(link modules.Skylink) ([]byte, uint64, error) {
	// Pull the offset and fetchSize out of the skylink.
	offset, fetchSize, err := link.OffsetAndFetchSize()
	if err != nil {
		return nil, 0, errors.AddContext(err, "unable to parse skylink")
	}

	// Fetch the leading chunk.
	baseSector, err := r.DownloadByRoot(link.MerkleRoot(), offset, fetchSize)
	if err != nil {
		return nil, 0, errors.AddContext(err, "unable to fetch base sector of skylink")
	}
	if len(baseSector) < SkyfileLayoutSize {
		return nil, 0, errors.New("download did not fetch enough data, layout cannot be decoded")
	}
	return baseSector, offset, nil
}
