package renter

// linkfile.go provides the tools for creating and uploading linkfiles, and then
// receiving the associated links to recover the files.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// LinkfileLayoutSize describes the amount of space within the first sector
	// of a linkfile used to describe the rest of the linkfile.
	LinkfileLayoutSize = 14

	// LinkfileDefaultSectorDataPieces establishes the default number of data
	// pieces that are used when creating the base sector for a linkfile.
	LinkfileDefaultSectorDataPieces = 1

	// LinkfileDefaultSectorParityPieces establishes the default number of
	// parity pieces that are used when creating the base sector for a linkfile.
	LinkfileDefaultSectorParityPieces = 9
)

// linkfileLayout explains the layout information that is used for storing data
// inside of the linkfile. The linkfileLayout always appears right at the front
// of the linkfile.
type linkfileLayout struct {
	filesize           uint64
	metadataSize       uint32
	fanoutDataPieces   uint8
	fanoutParityPieces uint8
}

// encode will return a []byte that has compactly encoded all of the layout
// data.
func (ll *linkfileLayout) encode() []byte {
	b := make([]byte, LinkfileLayoutSize)
	offset := 0
	binary.LittleEndian.PutUint64(b[offset:], ll.filesize)
	offset += 8
	binary.LittleEndian.PutUint32(b[offset:], ll.metadataSize)
	offset += 4
	b[offset] = ll.fanoutDataPieces
	offset += 1
	b[offset] = ll.fanoutParityPieces
	offset += 1
	return b
}

// decode will take a []byte and load the layout from that []byte.
func (ll *linkfileLayout) decode(b []byte) {
	offset := 0
	ll.filesize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.metadataSize = binary.LittleEndian.Uint32(b[offset:])
	offset += 4
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
	if ld.Version != 1 {
		return modules.LinkfileMetadata{}, nil, errors.New("link is not version 1")
	}
	if headerSize+ld.FileSize > modules.SectorSize {
		return modules.LinkfileMetadata{}, nil, errors.New("size of file suggests a fanout was used - this version does not support fanouts")
	}
	if ld.DataPieces != 1 {
		return modules.LinkfileMetadata{}, nil, errors.New("data pieces must be set to 1 on a link")
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

// UploadLinkfile will upload the provided data with the provided name and
// metadata, returning a sialink which can be used by any viewnode to recover
// the full original file and metadata.
func (r *Renter) UploadLinkfile(lfm modules.LinkfileMetadata, siaPath modules.SiaPath, overwriteExistingFile bool, fileDataReader io.Reader) (modules.Sialink, error) {
	// Input checks.
	if fileDataReader == nil {
		return "", errors.New("need to provide a stream of upload data")
	}
	if lfm.BaseSectorDataPieces == 0 {
		lfm.BaseSectorDataPieces = LinkfileDefaultSectorDataPieces
	}
	if lfm.BaseSectorParityPieces == 0 {
		lfm.BaseSectorParityPieces = LinkfileDefaultSectorParityPieces
	}
	if lfm.BaseSectorDataPieces != 1 {
		return "", errors.New("intra-sector erasure coding not yet supported")
	}
	if lfm.FanoutDataPieces != 0 {
		return "", errors.New("fanout not yet supported")
	}
	if lfm.FanoutParityPieces != 0 {
		return "", errors.New("fanout not yet supported")
	}

	// Compose the metadata into the leading sector.
	mlfm, err := json.Marshal(lfm)
	if err != nil {
		return "", errors.AddContext(err, "unable to marshal the link file metadata")
	}
	maxMetaSize := math.MaxUint16 / 2
	if len(mlfm) > maxMetaSize {
		return "", fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(mlfm), maxMetaSize)
	}

	// Compute the layout bytes for the sector.
	ll := linkfileLayout{
		metadataSize:       uint32(len(mlfm)),
		fanoutDataPieces:   0, // TODO: Will be updated when fanout is implemented
		fanoutParityPieces: 0, // TODO: Will be updated when fanout is implemented
	}
	llData := ll.encode()
	headerSize := uint32(len(llData)) + ll.metadataSize

	// TODO: Create the fanout data. The size of the fanout data is going to
	// have to be computed using some external function, it's going to be based
	// on the erasure coding parameters so that four full fanout pointers can
	// always fit.
	fanoutData := make([]byte, 0) // empty slice for now, so that the full math can be used.

	// Read data from the reader to fill out the remainder of the first sector.
	//
	// TODO: Going to need to adjust the size of the readBuf based on the type
	// and amount of intra-sector erasure coding being performed. Can't read
	// directly into erasure coding shards though because we don't know how much
	// data total is being read yet.
	fileData := make([]byte, modules.SectorSize-uint64(headerSize))
	size, err := io.ReadFull(fileDataReader, fileData)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	if err != nil {
		return "", errors.AddContext(err, "unable to read the file data")
	}

	// TODO: Read all of the remaining data and build the fanout structures.

	// TODO: Compute the filesize and payload size.

	// TODO: Perform the intra-sector erasure coding.

	// Create the base sector. This is done as late as possible so that any
	// errors are caught before a large block of memory is allocated.
	baseSector := make([]byte, modules.SectorSize)

	// Copy the layout and the metadata into the base sector.
	offset := 0
	copy(baseSector[offset:], llData)
	offset += len(llData)
	copy(baseSector[offset:], mlfm)
	offset += len(mlfm)
	copy(baseSector[offset:], fanoutData)
	offset += len(fanoutData)
	// TODO: When there is erasure coding, this will have to loop over the EC
	// shards.
	copy(baseSector[offset:], fileData)
	offset += size

	// Create parameters to upload the file with 1-of-N erasure coding and no
	// encryption. This should cause all of the pieces to have the same Merkle
	// root, which is critical to making the file discoverable to viewnodes and
	// also resiliant to host failures.
	ec, err := siafile.NewRSSubCode(int(lfm.BaseSectorDataPieces), int(lfm.BaseSectorParityPieces), crypto.SegmentSize)
	if err != nil {
		return "", errors.AddContext(err, "unable to create erasure coder")
	}
	up := modules.FileUploadParams{
		SiaPath:             siaPath,
		ErasureCode:         ec,
		Force:               overwriteExistingFile,
		DisablePartialChunk: true,  // must be set to true - partial chunks change, content addressed files must not change.
		Repair:              false, // indicates whether this is a repair operation

		CipherType: crypto.TypePlain,
	}

	// Perform the actual upload. This will require turning the buffer we
	// grabbed earlier back into a reader.
	baseSectorReader := bytes.NewReader(baseSector)
	fileNode, err := r.managedUploadStreamFromReader(up, baseSectorReader, false)
	if err != nil {
		return "", errors.AddContext(err, "failed to upload the file")
	}
	defer fileNode.Close()

	// Block until the file is available from the Sia network.
	//
	// TODO: May be able to find a better way to block than polling.
	start := time.Now()
	for time.Since(start) > 5*time.Minute && fileNode.Metadata().Redundancy < 1 {
		// This adds as much as one second of artificial latency to the upload
		// time, but the upload time is not something that we are optimizing for
		// at this time.
		time.Sleep(time.Second)
	}

	// The Merkle root should be the exact data that was uploaded due to the
	// erasure coding and encryption settings.
	mr := crypto.MerkleRoot(baseSector)

	// TODO: Write a test to ensure that adding a bunch of linkfiles to the
	// siafile metadata is okay, make sure that if the metadata size rolls over,
	// the siafile updates are handled correctly.

	// Create the sialink.
	ld := LinkData{
		Version:      1,
		MerkleRoot:   mr,
		HeaderSize:   headerSize,
		FileSize:     uint64(size),
		DataPieces:   lfm.BaseSectorDataPieces,
		ParityPieces: lfm.BaseSectorParityPieces,
	}
	sialink := ld.Sialink()

	// Add the sialink toe the Siafile.
	err = fileNode.AddSialink(sialink)
	if err != nil {
		return sialink, errors.AddContext(err, "unable to add sialink to siafile")
	}

	return sialink, nil
}
