package renter

// linkfile.go provides the tools for creating and uploading linkfiles, and then
// receiving the associated links to recover the files.

// Brief description of how the fanout will work: The fanout describes the set
// of sector roots that can be fetched for each chunk in a larger linkfile. The
// erasure coding settings of the fanout explain how many sector roots are
// needed per chunk. The fanout sector roots are listed in-order, and the total
// number of chunks multiplied by the number of sectors per chunk multiplied by
// the number of bytes per sector root give you the total fanout size - from the
// fanout size and fanout erasure coding settings the total number of chunks can
// be determined.
//
// If the fanout description doesn't fit entirely within the first chunk, a
// second chunk can be created to house the rest of the fanout. The decision may
// also be made to put the entirety of the fanout into its own siafile, meaning
// a linkfile could end up being 3 siafiles total - one siafile for the base
// chunk, one siafile for the fanout description, and one siafile for the actual
// file data.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// LinkfileLayoutSize describes the amount of space within the first sector
	// of a linkfile used to describe the rest of the linkfile.
	LinkfileLayoutSize = 64

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
	reserved                [35]byte
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
}

// DownloadSialink will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) DownloadSialink(link modules.Sialink) (modules.LinkfileMetadata, []byte, error) {
	// Parse the provided link into a usable structure for fetching downloads.
	var ld LinkData
	err := ld.LoadSialink(link)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to parse link for download")
	}
	headerSize := uint64(ld.HeaderSize)

	// Check that the link follows the restrictions of the current software
	// capabilities.
	if ld.Version < 1 || ld.Version < LinkfileVersion {
		return modules.LinkfileMetadata{}, nil, errors.New("link version is not recognized")
	}
	if headerSize+ld.FileSize > modules.SectorSize {
		return modules.LinkfileMetadata{}, nil, errors.New("size of file suggests a fanout was used - this version does not support fanouts")
	}
	if ld.DataPieces != 1 || ld.ParityPieces != 0 {
		return modules.LinkfileMetadata{}, nil, errors.New("inra-root erasure coding not supported")
	}

	// Fetch the actual file.
	baseSector, err := r.DownloadByRoot(ld.MerkleRoot, 0, headerSize+ld.FileSize)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "link based download has failed")
	}

	// Parse out the linkfileLayout.
	offset := uint64(0)
	var ll linkfileLayout
	ll.decode(baseSector)
	offset += LinkfileLayoutSize

	// Check that there are no fanout settings, currently the download sialink
	// function does not support fanout.
	if ll.fanoutHeaderSize != 0 || ll.fanoutExtensionSize != 0 {
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

	return lfm, baseSector[offset : headerSize+ld.FileSize], nil
}

// uploadLinkfileEstablishDefaults will set any zero values in the lup to be
// equal to the desired defaults.
func uploadLinkfileEstablishDefaults(lup *modules.LinkfileUploadParameters) {
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
}

// uploadLinkfileMetadataBytes will return the marshalled/encoded bytes for the
// linkfile metadata.
func uploadLinkfileMetadataBytes(lup modules.LinkfileUploadParameters) ([]byte, error) {
	// Compose the metadata into the leading sector.
	metadataBytes, err := json.Marshal(lup.FileMetadata)
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal the link file metadata")
	}
	maxMetaSize := modules.SectorSize - LinkfileLayoutSize
	if uint64(len(metadataBytes)) > maxMetaSize {
		return nil, fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(metadataBytes), maxMetaSize)
	}
	return metadataBytes, nil
}

// uploadLinkfileFileBytes will return the file data bytes of the file being
// uploaded.
func uploadLinkfileFileBytes(lup modules.LinkfileUploadParameters, headerSize uint32) ([]byte, error) {
	// Read data from the reader to fill out the remainder of the first sector.
	//
	// NOTE: When intra-sector erasure coding is added to improve download
	// speeds, the fileData buffer size will need to be adjusted.
	fileBytes := make([]byte, modules.SectorSize-uint64(headerSize))
	size, err := io.ReadFull(lup.Reader, fileBytes)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	if err != nil {
		return nil, errors.AddContext(err, "unable to read the file data")
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
		return nil, errors.AddContext(err, "too mcuh data provided, cannot create linkfile")
	}
	if n != 0 {
		return nil, errors.New("too much data provided, cannot create linkfile")
	}
	return fileBytes, nil
}

// uploadLinkfileUploadParams will derive the siafile upload parameters for the
// base chunk siafile of the linkfile from the LinkfileUploadParameters.
func uploadLinkfileUploadParams(lup modules.LinkfileUploadParameters) (modules.FileUploadParams, error) {
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
	uploadLinkfileEstablishDefaults(&lup)

	// Input checks - ensure the settings are compatible.
	if lup.Reader == nil {
		return "", errors.New("need to provide a stream of upload data")
	}
	if lup.IntraSectorDataPieces != 1 {
		return "", errors.New("intra-sector erasure coding not yet supported, intra sector data pieces must be set to 1")
	}
	if lup.IntraSectorParityPieces != 0 {
		return "", errors.New("intra-sector erasure coding not yet supported, intra sector parity pieces must be set to 0")
	}

	// Fetch the bytes for the metadata and the data.
	metadataBytes, err := uploadLinkfileMetadataBytes(lup)
	if err != nil {
		return "", errors.AddContext(err, "error retrieving linkfile metadata bytes")
	}
	headerSize := uint32(LinkfileLayoutSize + len(metadataBytes))
	fileBytes, err := uploadLinkfileFileBytes(lup, headerSize)
	if err != nil {
		return "", errors.AddContext(err, "error retrieving linkfile file bytes")
	}
	ll := linkfileLayout{
		version:                 LinkfileVersion,
		filesize:                uint64(len(fileBytes)),
		metadataSize:            uint32(len(metadataBytes)),
		intraSectorDataPieces:   lup.IntraSectorDataPieces,
		intraSectorParityPieces: lup.IntraSectorParityPieces,
	}
	layoutBytes := ll.encode()

	// Create the base sector. This is done as late as possible so that any
	// errors are caught before a large block of memory is allocated.
	baseSector := make([]byte, modules.SectorSize)
	offset := 0
	copy(baseSector[offset:], layoutBytes)
	offset += len(layoutBytes)
	copy(baseSector[offset:], metadataBytes)
	offset += len(metadataBytes)
	copy(baseSector[offset:], fileBytes)
	offset += len(fileBytes)

	// Perform the actual upload. This will require turning the base sector into
	// a reader.
	fileUploadParams, err := uploadLinkfileUploadParams(lup)
	if err != nil {
		return "", errors.AddContext(err, "failed to create siafile upload parameters")
	}
	baseSectorReader := bytes.NewReader(baseSector)
	fileNode, err := r.managedUploadStreamFromReader(fileUploadParams, baseSectorReader, false)
	if err != nil {
		return "", errors.AddContext(err, "failed to upload the file")
	}
	defer fileNode.Close()

	// Create the sialink.
	mr := crypto.MerkleRoot(baseSector) // Should be identical to the sector roots for each sector in the siafile.
	ld := LinkData{
		Version:      1,
		MerkleRoot:   mr,
		HeaderSize:   headerSize,
		FileSize:     uint64(len(fileBytes)),
		DataPieces:   lup.IntraSectorDataPieces,
		ParityPieces: lup.IntraSectorParityPieces,
	}
	sialink := ld.Sialink()
	// Add the sialink to the Siafile.
	err = fileNode.AddSialink(sialink)
	if err != nil {
		return sialink, errors.AddContext(err, "unable to add sialink to siafile")
	}
	return sialink, nil
}
