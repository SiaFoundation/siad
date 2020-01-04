package renter

// linkfile.go provides the tools for creating and uploading linkfiles, and then
// receiving the associated links to recover the files.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

// Profiling singleton
var start time.Time

const (
	// LinkfileLayoutSize describes the amount of space within the first sector
	// of a linkfile used to describe the rest of the linkfile.
	LinkfileLayoutSize = 256

	// LinkfileDefaultBaseChunkRedundancy establishes the default redundancy for
	// the base chunk of a linkfile.
	LinkfileDefaultBaseChunkRedundancy = 10

	// LinkfileDefaultIntraSectorDataPieces defines the default number of data
	// pieces that are used for the intra-sector erasure coding.
	LinkfileDefaultIntraSectorDataPieces = 1

	// LinkfileDefaultIntraSectorParityPieces defines the default number of
	// parity pieces that are used for the intra-sector erasure coding.
	LinkfileDefaultIntraSectorParityPieces = 0

	// LinkfileVersion establishes the current version for creating linkfiles.
	// The sialinks will share the same version.
	LinkfileVersion = 1
)

// linkfileLayout explains the layout information that is used for storing data
// inside of the linkfile. The linkfileLayout always appears as the first bytes
// of the leading chunk, and no intra-sector erasure coding is every applied to
// these bytes.
//
// The linkfile layout contains all of the information necessary to reconstruct
// the sialink:
//
// Version:      matches ll.version
// MerkleRoot:   the merkle root of the leading sector - must be determined some other way
// HeaderSize:   LinkfileLayoutSize + ll.metadataSize + ll.fanoutHeaderSize
// FileSize:     ll.filesize
// DataPieces:   ll.intraSectorDataPieces
// ParityPieces: ll.intraSectorParityPieces
type linkfileLayout struct {
	version                 uint8
	filesize                uint64
	metadataSize            uint32
	intraSectorDataPieces   uint8
	intraSectorParityPieces uint8
	fanoutHeaderSize        uint32
	fanoutExtensionSize     uint64
	fanoutDataPieces        uint8
	fanoutParityPieces      uint8
	cipherType              crypto.CipherType
	cipherKey               [64]byte  // cipherKey is incompatible with ciphers that need keys larger than 64 bytes
	reserved                [155]byte // in case more fields are needed for future extensions
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
	binary.LittleEndian.PutUint32(b[offset:], ll.metadataSize)
	offset += 4
	b[offset] = ll.intraSectorDataPieces
	offset += 1
	b[offset] = ll.intraSectorParityPieces
	offset += 1
	binary.LittleEndian.PutUint32(b[offset:], ll.fanoutHeaderSize)
	offset += 4
	binary.LittleEndian.PutUint64(b[offset:], ll.fanoutExtensionSize)
	offset += 8
	b[offset] = ll.fanoutDataPieces
	offset += 1
	b[offset] = ll.fanoutParityPieces
	offset += 1
	copy(b[offset:], ll.cipherType[:])
	offset += len(ll.cipherType)
	copy(b[offset:], ll.cipherKey[:])
	offset += len(ll.cipherKey)
	return b
}

// decode will take a []byte and load the layout from that []byte.
func (ll *linkfileLayout) decode(b []byte) {
	offset := 0
	ll.version = b[offset]
	offset += 1
	ll.filesize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.metadataSize = binary.LittleEndian.Uint32(b[offset:])
	offset += 4
	ll.intraSectorDataPieces = b[offset]
	offset += 1
	ll.intraSectorParityPieces = b[offset]
	offset += 1
	ll.fanoutHeaderSize = binary.LittleEndian.Uint32(b[offset:])
	offset += 4
	ll.fanoutExtensionSize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.fanoutDataPieces = b[offset]
	offset += 1
	ll.fanoutParityPieces = b[offset]
	offset += 1
	copy(ll.cipherType[:], b[offset:])
	offset += len(ll.cipherType)
	copy(ll.cipherKey[:], b[offset:])
	offset += len(ll.cipherKey)
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

// linkfileBuildSialink will build a sialink given all of the inputs to the
// sialink.
func linkfileBuildSialink(version uint8, merkleRoot crypto.Hash, lup modules.LinkfileUploadParameters, fetchSize uint64) modules.Sialink {
	var ld modules.LinkData
	ld.SetVersion(version)
	ld.SetMerkleRoot(merkleRoot)
	ld.SetDataPieces(lup.IntraSectorDataPieces)
	ld.SetParityPieces(lup.IntraSectorParityPieces)
	ld.SetFetchSize(uint64(fetchSize))
	return ld.Sialink()
}

// linkfileEstablishDefaults will set any zero values in the lup to be equal to
// the desired defaults.
func linkfileEstablishDefaults(lup *modules.LinkfileUploadParameters) error {
	if lup.BaseChunkRedundancy == 0 {
		lup.BaseChunkRedundancy = LinkfileDefaultBaseChunkRedundancy
	}
	if lup.IntraSectorDataPieces == 0 {
		lup.IntraSectorDataPieces = LinkfileDefaultIntraSectorDataPieces
	}
	if lup.IntraSectorParityPieces == 0 {
		lup.IntraSectorParityPieces = LinkfileDefaultIntraSectorParityPieces
	}
	if lup.FileMetadata.Mode == 0 {
		lup.FileMetadata.Mode = modules.DefaultFilePerm
	}
	if lup.FileMetadata.CreateTime == 0 {
		lup.FileMetadata.CreateTime = time.Now().Unix()
	}

	// Input checks - ensure the settings are compatible.
	if lup.IntraSectorDataPieces != 1 {
		return errors.New("intra-sector erasure coding not yet supported, intra sector data pieces must be set to 1")
	}
	if lup.IntraSectorParityPieces != 0 {
		return errors.New("intra-sector erasure coding not yet supported, intra sector parity pieces must be set to 0")
	}
	return nil
}

// linkfileMetadataBytes will return the marshalled/encoded bytes for the
// linkfile metadata.
func linkfileMetadataBytes(fm modules.LinkfileMetadata) ([]byte, error) {
	// Compose the metadata into the leading sector.
	metadataBytes, err := json.Marshal(fm)
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal the link file metadata")
	}
	maxMetaSize := modules.SectorSize - LinkfileLayoutSize
	if uint64(len(metadataBytes)) > maxMetaSize {
		return nil, fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(metadataBytes), maxMetaSize)
	}
	return metadataBytes, nil
}

// linkfileUploadParams will derive the siafile upload parameters for the base
// chunk siafile of the linkfile from the LinkfileUploadParameters.
func linkfileUploadParams(lup modules.LinkfileUploadParameters) (modules.FileUploadParams, error) {
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
	// Grab the filenode for the provided siapath.
	fileNode, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return "", errors.AddContext(err, "unable to open siafile")
	}
	defer fileNode.Close()
	return r.createSialinkFromFileNode(lup, fileNode, siaPath)
}

// createSialinkFromFileNode creates a sialink from a file node.
//
// TODO: The siaPath needs to be passed in because at this time I do not believe
// there is any way to extract the siaPath from the fileNode.
func (r *Renter) createSialinkFromFileNode(lup modules.LinkfileUploadParameters, fileNode *filesystem.FileNode, siaPath modules.SiaPath) (modules.Sialink, error) {
	// Set reasonable default values for any lup fields that are blank.
	err := linkfileEstablishDefaults(&lup)
	if err != nil {
		return "", errors.AddContext(err, "linkfile upload parameters are incorrect")
	}

	// Check that the encryption key and erasure code is compatible with the
	// linkfile format. This is intentionally done before any heavy computation
	// to catch early errors.
	var ll linkfileLayout
	masterKey := fileNode.MasterKey()
	if len(masterKey.Key()) > len(ll.cipherKey) {
		return "", errors.AddContext(err, "cipher key is not supported by the linkfile format")
	}
	ec := fileNode.ErasureCode()
	if ec.Type() != siafile.ECReedSolomonSubShards64 {
		return "", errors.AddContext(err, "siafile has unsupported erasure code type")
	}

	// Create the metadata for this siafile and compute the resulting header
	// size.
	//
	// TODO: 'lup' has an fm in it, need to figure out if those values should
	// obliterate these ones or not.
	fm := modules.LinkfileMetadata{
		Name:       siaPath.Name(),
		Mode:       fileNode.Mode(),
		CreateTime: fileNode.CreateTime().Unix(),
	}
	metadataBytes, err := linkfileMetadataBytes(fm)
	if err != nil {
		return "", errors.AddContext(err, "error retrieving linkfile metadata bytes")
	}

	// Implementation gap - if the file is small enough to fit entirely within
	// one sector, return an error. A full fanout is not necessary, a simple
	// linkfile should be created instead.
	noFanoutHeaderSize := uint64(LinkfileLayoutSize + len(metadataBytes))
	if noFanoutHeaderSize+fileNode.Size() <= modules.SectorSize {
		return "", errors.New("cannot convert siafiles that are small enough to fit inside a fanout")
	}

	// Create the fanout for the siafile.
	fanoutBytes, err := linkfileEncodeFanout(fileNode)
	if err != nil {
		return "", errors.AddContext(err, "unable to encode the fanout of the siafile")
	}
	headerSize := noFanoutHeaderSize + uint64(len(fanoutBytes))
	if headerSize+uint64(len(fanoutBytes)) > modules.SectorSize {
		return "", errors.New("siafile is too large to be turned into a sialink")
	}

	// Assemble the first chunk of the linkfile.
	ll = linkfileLayout{
		version:                 1,
		filesize:                fileNode.Size(),
		metadataSize:            uint32(len(metadataBytes)),
		intraSectorDataPieces:   LinkfileDefaultIntraSectorDataPieces,
		intraSectorParityPieces: LinkfileDefaultIntraSectorParityPieces,
		fanoutHeaderSize:        uint32(len(fanoutBytes)),
		fanoutDataPieces:        uint8(ec.MinPieces()),
		fanoutParityPieces:      uint8(ec.NumPieces() - ec.MinPieces()),
		cipherType:              masterKey.Type(),
	}
	copy(ll.cipherKey[:], masterKey.Key())

	// Create the base sector.
	baseSector, fetchSize := linkfileBuildBaseSector(ll.encode(), metadataBytes, fanoutBytes, nil)
	baseSectorReader := bytes.NewReader(baseSector)
	fileUploadParams, err := linkfileUploadParams(lup)
	if err != nil {
		return "", errors.AddContext(err, "unable to build the file upload parameters")
	}

	// Perform the full upload.
	newFileNode, err := r.managedUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
	if err != nil {
		return "", errors.AddContext(err, "linkfile base chunk upload failed")
	}
	defer newFileNode.Close()

	// Create the sialink.
	baseSectorRoot := crypto.MerkleRoot(baseSector)
	sialink := linkfileBuildSialink(LinkfileVersion, baseSectorRoot, lup, fetchSize)

	// Add the sialink to the siafiles.
	err1 := fileNode.AddSialink(sialink)
	err2 := newFileNode.AddSialink(sialink)
	err = errors.Compose(err1, err2)
	return sialink, errors.AddContext(err, "unable to add sialink to the sianodes")
}

// DownloadSialink will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) DownloadSialink(link modules.Sialink) (modules.LinkfileMetadata, modules.Streamer, error) {
	start = time.Now()
	// Parse the provided link into a usable structure for fetching downloads.
	var ld modules.LinkData
	err := ld.LoadSialink(link)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to parse link for download")
	}

	// Check that the link follows the restrictions of the current software
	// capabilities.
	if ld.Version() < 1 || ld.Version() < LinkfileVersion {
		return modules.LinkfileMetadata{}, nil, errors.New("link version is not recognized")
	}
	if ld.DataPieces() != 1 || ld.ParityPieces() != 0 {
		return modules.LinkfileMetadata{}, nil, errors.New("intra-root erasure coding not supported")
	}
	// NOTE: Once intra-sector erasure coding is in play, this check has to
	// change to account for the fact that a lot of the bytes are redundant.
	fetchSize := ld.FetchSize()
	if fetchSize > modules.SectorSize {
		fetchSize = modules.SectorSize
	}

	// Fetch the leading sector.
	baseSector, err := r.DownloadByRoot(ld.MerkleRoot(), 0, fetchSize)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to fetch base sector of sialink")
	}
	if len(baseSector) < LinkfileLayoutSize {
		return modules.LinkfileMetadata{}, nil, errors.New("download did not fetch enough data, layout cannot be decoded")
	}

	// Parse out the linkfileLayout.
	offset := uint64(0)
	var ll linkfileLayout
	ll.decode(baseSector)
	offset += LinkfileLayoutSize

	// Check that there is no fanout extension, as that is not currently
	// supported.
	if ll.fanoutExtensionSize != 0 {
		return modules.LinkfileMetadata{}, nil, errors.New("downloading large siafiles is not supported in this version of siad")
	}

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
	if ll.fanoutHeaderSize == 0 {
		streamer := streamerFromSlice(baseSector[offset : offset+ll.filesize])
		return lfm, streamer, nil
	}

	// There is a fanout, create a fanout streamer and return that.
	fanoutBytes := baseSector[offset : offset+uint64(ll.fanoutHeaderSize)]
	fs, err := r.newFanoutStreamer(ll, fanoutBytes)
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
	//
	// NOTE: When intra-sector erasure coding is added to improve download
	// speeds, the fileData buffer size will need to be adjusted.
	fileBytes := make([]byte, modules.SectorSize-headerSize)
	size, err := io.ReadFull(lup.Reader, fileBytes)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	if err != nil {
		return nil, nil, false, errors.AddContext(err, "unable to read the file data")
	}
	// Set fileBytes to the right size.
	fileBytes = fileBytes[:size]

	// Attempt to read more data from the reader. If reading more data is
	// successful, there is too much data to create a linkfile, an error must be
	// returned.
	peek := make([]byte, 1)
	n, peekErr := io.ReadFull(lup.Reader, peek)
	if peekErr == io.EOF || peekErr == io.ErrUnexpectedEOF {
		peekErr = nil
	}
	if peekErr != nil {
		return nil, nil, false, errors.AddContext(err, "too much data provided, cannot create linkfile")
	}
	if n == 0 {
		return fileBytes, nil, false, nil
	}
	prependData := append(fileBytes, peek...)
	prependReader := newPrependReader(prependData, lup.Reader)
	return nil, prependReader, true, nil
}

// uploadLinkfileLargeFile will accept a fileReader containing all of the data
// to a large siafile and upload it to the Sia network using
// 'managedUploadStreamFromReader'. The final sialink is created by calling
// 'CreateSialinkFromSiafile' on the resulting siafile.
func (r *Renter) uploadLinkfileLargeFile(lup modules.LinkfileUploadParameters, metadataBytes []byte, fileReader io.Reader) (modules.Sialink, error) {
	// Create the erasure coder to use when uploading the file bulk. When going
	// through the 'uploadLinkfile' command, a 1-of-N scheme is always used,
	// where the redundancy of the data as a whole matches the propsed
	// redundancy for the base chunk.
	ec, err := siafile.NewRSSubCode(1, int(lup.BaseChunkRedundancy)-1, crypto.SegmentSize)
	if err != nil {
		return "", errors.AddContext(err, "unable to create erasure coder for large file")
	}
	// Create the siapath for the linkfile extra data. This is going to be the
	// same as the linkfile upload siapath, except with a suffix.
	siaPath, err := modules.NewSiaPath(lup.SiaPath.String()+"-extended")
	if err != nil {
		return "", errors.AddContext(err, "unable to create SiaPath for large linkfile extended data")
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
		return "", errors.AddContext(err, "unable to upload large linkfile")
	}

	// Convert the new siafile we just uploaded into a linkfile using the
	// convert function.
	return r.createSialinkFromFileNode(lup, fileNode, siaPath)
}

// uploadLinkfileSmallFile uploads a file that fits entirely in the leading
// chunk of a linkfile to the Sia network and returns the sialink that can be
// used to access the file.
func (r *Renter) uploadLinkfileSmallFile(lup modules.LinkfileUploadParameters, metadataBytes []byte, fileBytes []byte) (modules.Sialink, error) {
	ll := linkfileLayout{
		version:                 LinkfileVersion,
		filesize:                uint64(len(fileBytes)),
		metadataSize:            uint32(len(metadataBytes)),
		intraSectorDataPieces:   lup.IntraSectorDataPieces,
		intraSectorParityPieces: lup.IntraSectorParityPieces,
	}

	// Create the base sector. This is done as late as possible so that any
	// errors are caught before a large block of memory is allocated.
	baseSector, fetchSize := linkfileBuildBaseSector(ll.encode(), metadataBytes, nil, fileBytes)

	// Perform the actual upload. This will require turning the base sector into
	// a reader.
	fileUploadParams, err := linkfileUploadParams(lup)
	if err != nil {
		return "", errors.AddContext(err, "failed to create siafile upload parameters")
	}
	baseSectorReader := bytes.NewReader(baseSector)
	fileNode, err := r.managedUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
	if err != nil {
		return "", errors.AddContext(err, "failed to small linkfile")
	}
	defer fileNode.Close()

	// Create the sialink.
	baseSectorRoot := crypto.MerkleRoot(baseSector) // Should be identical to the sector roots for each sector in the siafile.
	sialink := linkfileBuildSialink(LinkfileVersion, baseSectorRoot, lup, fetchSize)
	// Add the sialink to the Siafile.
	err = fileNode.AddSialink(sialink)
	return sialink, errors.AddContext(err, "unable to add sialink to siafile")
}

// UploadLinkfile will upload the provided data with the provided name and
// metadata, returning a sialink which can be used by any viewnode to recover
// the full original file and metadata.
//
// UploadLinkfile accepts a data stream directly. This method of generating a
// linkfile is limited to files where the data + metadata fully fits within a
// single sector. Larger files will need to be uploaded as siafiles first, and
// then converted using a convert function (as of writing this comment, no
// convert function exists).
func (r *Renter) UploadLinkfile(lup modules.LinkfileUploadParameters) (modules.Sialink, error) {
	// Set reasonable default values for any lup fields that are blank.
	err := linkfileEstablishDefaults(&lup)
	if err != nil {
		return "", errors.AddContext(err, "linkfile upload parameters are incorrect")
	}
	// Additional input check - this check is unique to uploading a linkfile
	// from a streamer. The convert siafile function does not need to be passed
	// a reader.
	if lup.Reader == nil {
		return "", errors.New("need to provide a stream of upload data")
	}

	// Fetch the bytes for the metadata and the data.
	metadataBytes, err := linkfileMetadataBytes(lup.FileMetadata)
	if err != nil {
		return "", errors.AddContext(err, "unable to retrieve linkfile metadata bytes")
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
		return "", errors.AddContext(err, "unable to retreive leading chunk file bytes")
	}
	if largeFile {
		return r.uploadLinkfileLargeFile(lup, metadataBytes, fileReader)
	}
	return r.uploadLinkfileSmallFile(lup, metadataBytes, fileBytes)
}
