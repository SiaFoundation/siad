package renter

// linkfile.go provides the tools for creating and uploading linkfiles, and then
// receiving the associated links to recover the files.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// LinkfileLayoutSize describes the amount of space within the first sector
	// of a linkfile used to describe the rest of the linkfile.
	LinkfileLayoutSize = 99

	// LinkfileDefaultBaseChunkRedundancy establishes the default redundancy for
	// the base chunk of a linkfile.
	LinkfileDefaultBaseChunkRedundancy = 10

	// LinkfileVersion establishes the current version for creating linkfiles.
	// The linkfile versions are different from the siafile versions.
	LinkfileVersion = 1
)

// linkfileLayout explains the layout information that is used for storing data
// inside of the linkfile. The linkfileLayout always appears as the first bytes
// of the leading chunk.
type linkfileLayout struct {
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
func (ll *linkfileLayout) encode() []byte {
	b := make([]byte, LinkfileLayoutSize)
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
	// LinkfileLayoutSize.
	if offset != LinkfileLayoutSize {
		panic("layout size does not match the amount of data encoded")
	}
	return b
}

// decode will take a []byte and load the layout from that []byte.
func (ll *linkfileLayout) decode(b []byte) {
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
	// LinkfileLayoutSize.
	if offset != LinkfileLayoutSize {
		panic("layout size does not match the amount of data decdoed")
	}
}

// linkfileBuildBaseSector will take all of the elements of the base sector and
// copy them into a freshly created base sector.
func linkfileBuildBaseSector(layoutBytes, metadataBytes, fanoutBytes, fileBytes []byte) ([]byte, uint64) {
	baseSector := make([]byte, modules.SectorSize)
	offset := 0
	copy(baseSector[offset:], layoutBytes)
	offset += len(layoutBytes)
	copy(baseSector[offset:], metadataBytes)
	offset += len(metadataBytes)
	copy(baseSector[offset:], fanoutBytes)
	offset += len(fanoutBytes)
	copy(baseSector[offset:], fileBytes)
	offset += len(fileBytes)
	return baseSector, uint64(offset)
}

// linkfileEstablishDefaults will set any zero values in the lup to be equal to
// the desired defaults.
func linkfileEstablishDefaults(lup *modules.LinkfileUploadParameters) error {
	if lup.BaseChunkRedundancy == 0 {
		lup.BaseChunkRedundancy = LinkfileDefaultBaseChunkRedundancy
	}
	return nil
}

// linkfileMetadataBytes will return the marshalled/encoded bytes for the
// linkfile metadata.
func linkfileMetadataBytes(lm modules.LinkfileMetadata) ([]byte, error) {
	// Compose the metadata into the leading sector.
	metadataBytes, err := json.Marshal(lm)
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal the link file metadata")
	}
	maxMetaSize := modules.SectorSize - LinkfileLayoutSize
	if uint64(len(metadataBytes)) > maxMetaSize {
		return nil, fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(metadataBytes), maxMetaSize)
	}
	return metadataBytes, nil
}

// fileUploadParamsFromLUP will dervie the FileUploadParams to use when
// uploading the base chunk siafile of a linkfile using the linkfile's upload
// parameters.
func fileUploadParamsFromLUP(lup modules.LinkfileUploadParameters) (modules.FileUploadParams, error) {
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

// streamerFromReader wraps a bytes.Reader to give it a Close() method.
type streamerFromReader struct {
	*bytes.Reader
}

// Close is a no-op because a bytes.Reaader doesn't need to be closed.
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

// CreateSialinkFromSiafile creates a linkfile from a siafile. This requires
// uploading a new linkfile which contains fanout information pointing to the
// siafile data. The SiaPath provided in 'lup' indicates where the new base
// sector linkfile will be placed, and the siaPath provided as its own input is
// the siaPath of the file that is being used to create the linkfile.
func (r *Renter) CreateSialinkFromSiafile(lup modules.LinkfileUploadParameters, siaPath modules.SiaPath) (modules.Sialink, error) {
	// Set reasonable default values for any lup fields that are blank.
	err := linkfileEstablishDefaults(&lup)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "linkfile upload parameters are incorrect")
	}

	// Grab the filenode for the provided siapath.
	fileNode, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to open siafile")
	}
	defer fileNode.Close()
	return r.managedCreateSialinkFromFileNode(lup, fileNode, siaPath.Name())
}

// managedCreateSialinkFromFileNode creates a sialink from a file node.
//
// The name needs to be passed in explicitly because a file node does not track
// its own name, which allows the file to be renamed concurrently without
// causing any race conditions.
func (r *Renter) managedCreateSialinkFromFileNode(lup modules.LinkfileUploadParameters, fileNode *filesystem.FileNode, filename string) (modules.Sialink, error) {
	// Check that the encryption key and erasure code is compatible with the
	// linkfile format. This is intentionally done before any heavy computation
	// to catch early errors.
	var ll linkfileLayout
	masterKey := fileNode.MasterKey()
	if len(masterKey.Key()) > len(ll.cipherKey) {
		return modules.Sialink{}, errors.New("cipher key is not supported by the linkfile format")
	}
	ec := fileNode.ErasureCode()
	if ec.Type() != siafile.ECReedSolomonSubShards64 {
		return modules.Sialink{}, errors.New("siafile has unsupported erasure code type")
	}
	// Deny the conversion of siafiles that are not 1 data piece. Not because we
	// cannot download them, but because it is currently inefficient to download
	// them.
	if ec.MinPieces() != 1 {
		return modules.Sialink{}, errors.New("sialinks currently only support 1-of-N redundancy, other redundancies will be supported in a later version")
	}

	// Create the metadata for this siafile.
	fm := modules.LinkfileMetadata{
		Filename: filename,
		Mode:     fileNode.Mode(),
	}
	metadataBytes, err := linkfileMetadataBytes(fm)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "error retrieving linkfile metadata bytes")
	}

	// Create the fanout for the siafile.
	fanoutBytes, err := linkfileEncodeFanout(fileNode)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to encode the fanout of the siafile")
	}
	headerSize := uint64(LinkfileLayoutSize + len(metadataBytes) + len(fanoutBytes))
	if headerSize > modules.SectorSize {
		return modules.Sialink{}, errors.New("siafile is too large to be turned into a sialink - large files will be supported in a later version")
	}

	// Assemble the first chunk of the linkfile.
	ll = linkfileLayout{
		version:            1,
		filesize:           fileNode.Size(),
		metadataSize:       uint64(len(metadataBytes)),
		fanoutSize:         uint64(len(fanoutBytes)),
		fanoutDataPieces:   uint8(ec.MinPieces()),
		fanoutParityPieces: uint8(ec.NumPieces() - ec.MinPieces()),
		cipherType:         masterKey.Type(),
	}
	copy(ll.cipherKey[:], masterKey.Key())

	// Create the base sector.
	baseSector, fetchSize := linkfileBuildBaseSector(ll.encode(), metadataBytes, fanoutBytes, nil)
	baseSectorReader := bytes.NewReader(baseSector)
	fileUploadParams, err := fileUploadParamsFromLUP(lup)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to build the file upload parameters")
	}

	// Perform the full upload.
	newFileNode, err := r.managedUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "linkfile base chunk upload failed")
	}
	defer newFileNode.Close()

	// Create the sialink.
	baseSectorRoot := crypto.MerkleRoot(baseSector)
	sialink, err := modules.NewSialinkV1(baseSectorRoot, 0, fetchSize)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to build sialink")
	}

	// Add the sialink to the siafiles.
	err1 := fileNode.AddSialink(sialink)
	err2 := newFileNode.AddSialink(sialink)
	err = errors.Compose(err1, err2)
	return sialink, errors.AddContext(err, "unable to add sialink to the sianodes")
}

// DownloadSialink will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) DownloadSialink(link modules.Sialink) (modules.LinkfileMetadata, modules.Streamer, error) {
	// Pull the offset and fetchSize out of the linkfile.
	offset, fetchSize, err := link.OffsetAndFetchSize()
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to parse sialink")
	}

	// Fetch the leading sector.
	baseSector, err := r.DownloadByRoot(link.MerkleRoot(), offset, fetchSize)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to fetch base sector of sialink")
	}
	if len(baseSector) < LinkfileLayoutSize {
		return modules.LinkfileMetadata{}, nil, errors.New("download did not fetch enough data, layout cannot be decoded")
	}

	// Parse out the linkfileLayout.
	var ll linkfileLayout
	ll.decode(baseSector)
	offset += LinkfileLayoutSize

	// Parse out the linkfile metadata.
	var lfm modules.LinkfileMetadata
	metadataSize := uint64(ll.metadataSize)
	err = json.Unmarshal(baseSector[offset:offset+metadataSize], &lfm)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to parse link file metadata")
	}
	offset += metadataSize

	// If there is no fanout, all of the data will be contained in the base
	// sector, return a streamer using the data from the base sector.
	if ll.fanoutSize == 0 {
		streamer := streamerFromSlice(baseSector[offset : offset+ll.filesize])
		return lfm, streamer, nil
	}

	// TODO: Support fanout extensions. This is only necessary for very large
	// files, a 1-of-N file can get over 400 GB with no fanout extension, a
	// 10-of-30 file can get over 100 GB with no fanout extension.
	//
	// The general strategy for this would be to parse the fanout extension
	// using the same erasure coding scheme as the rest of the file, treating
	// any fanout in this file as a pointer to the extended fanout. That
	// extended fanout would then point to the true file.
	if offset+ll.fanoutSize > modules.SectorSize {
		return modules.LinkfileMetadata{}, nil, errors.New("fanout is more than one sector, that is unsupported in this version")
	}

	// There is a fanout, create a fanout streamer and return that.
	fanoutBytes := baseSector[offset : offset+uint64(ll.fanoutSize)]
	fs, err := r.newFanoutStreamer(link, ll, fanoutBytes)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to create fanout fetcher")
	}
	return lfm, fs, nil
}

// prependReader is an io.Reader which
type prependReader struct {
	prependData []byte
	io.Reader
}

// Read will read data from the prependReader.
func (pr *prependReader) Read(b []byte) (int, error) {
	n := copy(b, pr.prependData)
	if n != 0 {
		pr.prependData = pr.prependData[n:]
		return n, nil
	}
	return pr.Reader.Read(b)
}

// newPrependReader accepts a slice and a reader, and returns a reader that will
// read out the prepended data before transitioning to using the standard
// reader.
func newPrependReader(prepend []byte, reader io.Reader) io.Reader {
	return &prependReader{
		prependData: prepend,
		Reader:      reader,
	}
}

// uploadLinkfileReadLeadingChunk will read the leading chunk of a linkfile. If
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
// data that uploadLinkfileReadLeadingChunk had to read to figure out whether
// the file was too large to fit into the leading chunk.
func uploadLinkfileReadLeadingChunk(lup modules.LinkfileUploadParameters, headerSize uint64) ([]byte, io.Reader, bool, error) {
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
		return nil, nil, false, errors.AddContext(err, "too much data provided, cannot create linkfile")
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
	prependReader := newPrependReader(prependData, lup.Reader)
	return nil, prependReader, true, nil
}

// managedUploadLinkfileLargeFile will accept a fileReader containing all of the data
// to a large siafile and upload it to the Sia network using
// 'managedUploadStreamFromReader'. The final sialink is created by calling
// 'CreateSialinkFromSiafile' on the resulting siafile.
func (r *Renter) managedUploadLinkfileLargeFile(lup modules.LinkfileUploadParameters, metadataBytes []byte, fileReader io.Reader) (modules.Sialink, error) {
	// Create the erasure coder to use when uploading the file bulk. When going
	// through the 'managedUploadLinkfile' command, a 1-of-N scheme is always used,
	// where the redundancy of the data as a whole matches the propsed
	// redundancy for the base chunk.
	ec, err := siafile.NewRSSubCode(1, int(lup.BaseChunkRedundancy)-1, crypto.SegmentSize)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to create erasure coder for large file")
	}
	// Create the siapath for the linkfile extra data. This is going to be the
	// same as the linkfile upload siapath, except with a suffix.
	siaPath, err := modules.NewSiaPath(lup.SiaPath.String() + "-extended")
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to create SiaPath for large linkfile extended data")
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
	fileNode, err := r.managedUploadStreamFromReader(fup, fileReader, false)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to upload large linkfile")
	}

	// Convert the new siafile we just uploaded into a linkfile using the
	// convert function.
	return r.managedCreateSialinkFromFileNode(lup, fileNode, siaPath.Name())
}

// managedUploadLinkfileSmallFile uploads a file that fits entirely in the
// leading chunk of a linkfile to the Sia network and returns the sialink that
// can be used to access the file.
func (r *Renter) managedUploadLinkfileSmallFile(lup modules.LinkfileUploadParameters, metadataBytes []byte, fileBytes []byte) (modules.Sialink, error) {
	ll := linkfileLayout{
		version:      LinkfileVersion,
		filesize:     uint64(len(fileBytes)),
		metadataSize: uint64(len(metadataBytes)),
		// No fanout, no encryption.
	}

	// Create the base sector. This is done as late as possible so that any
	// errors are caught before a large block of memory is allocated.
	baseSector, fetchSize := linkfileBuildBaseSector(ll.encode(), metadataBytes, nil, fileBytes) // 'nil' because there is no fanout

	// Perform the actual upload. This will require turning the base sector into
	// a reader.
	fileUploadParams, err := fileUploadParamsFromLUP(lup)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "failed to create siafile upload parameters")
	}
	baseSectorReader := bytes.NewReader(baseSector)
	fileNode, err := r.managedUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "failed to small linkfile")
	}
	defer fileNode.Close()

	// Create the sialink.
	baseSectorRoot := crypto.MerkleRoot(baseSector) // Should be identical to the sector roots for each sector in the siafile.
	sialink, err := modules.NewSialinkV1(baseSectorRoot, 0, fetchSize)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "failed to build sialink")
	}

	// Add the sialink to the Siafile. The sialink is returned even if there is
	// an error, because the sialink itself is available on the Sia network now,
	// even if the file metadata couldn't be properly updated.
	err = fileNode.AddSialink(sialink)
	return sialink, errors.AddContext(err, "unable to add sialink to siafile")
}

// UploadLinkfile will upload the provided data with the provided metadata,
// returning a sialink which can be used by any viewnode to recover the full
// original file and metadata. The sialink will be unique to the combination of
// both the file data and metadata.
func (r *Renter) UploadLinkfile(lup modules.LinkfileUploadParameters) (modules.Sialink, error) {
	// Set reasonable default values for any lup fields that are blank.
	err := linkfileEstablishDefaults(&lup)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "linkfile upload parameters are incorrect")
	}
	// Additional input check - this check is unique to uploading a linkfile
	// from a streamer. The convert siafile function does not need to be passed
	// a reader.
	if lup.Reader == nil {
		return modules.Sialink{}, errors.New("need to provide a stream of upload data")
	}

	// Grab the metadata bytes.
	metadataBytes, err := linkfileMetadataBytes(lup.FileMetadata)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to retrieve linkfile metadata bytes")
	}

	// Read data from the lup reader. If the file data provided fits entirely
	// into the leading chunk, this method will use that data to upload a
	// linkfile directly. If the file data provided does not fit entirely into
	// the leading chunk, a separate method will be called to upload the file
	// separately using upload streaming, and then the siafile conversion
	// function will be used to generate the final sialink.
	headerSize := uint64(LinkfileLayoutSize + len(metadataBytes))
	fileBytes, fileReader, largeFile, err := uploadLinkfileReadLeadingChunk(lup, headerSize)
	if err != nil {
		return modules.Sialink{}, errors.AddContext(err, "unable to retrieve leading chunk file bytes")
	}
	if largeFile {
		return r.managedUploadLinkfileLargeFile(lup, metadataBytes, fileReader)
	}
	return r.managedUploadLinkfileSmallFile(lup, metadataBytes, fileBytes)
}
