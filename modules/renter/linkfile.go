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
	LinkfileLayoutSize = 20
)

var (
	// LinkfileSiaFolder is the folder where all of the linkfiles are stored.
	LinkfileSiaFolder = modules.NewGlobalSiaPath("/var/linkfiles")
)

// linkfileLayout explains the layout information that is used for storing data
// inside of the linkfile. The linkfileLayout always appears right at the front
// of the linkfile.
type linkfileLayout struct {
	filesize           uint64
	metadataSize       uint32
	initialFanoutSize  uint32
	fanoutDataPieces   uint16
	fanoutParityPieces uint16
}

// encode will return a []byte that has compactly encoded all of the layout
// data.
func (ll *linkfileLayout) encode() []byte {
	offset := 0
	b := make([]byte, LinkfileLayoutSize)
	binary.LittleEndian.PutUint64(b[offset:], ll.filesize)
	offset += 8
	binary.LittleEndian.PutUint32(b[offset:], ll.metadataSize)
	offset += 4
	binary.LittleEndian.PutUint32(b[offset:], ll.initialFanoutSize)
	offset += 4
	binary.LittleEndian.PutUint16(b[offset:], ll.fanoutDataPieces)
	offset += 2
	binary.LittleEndian.PutUint16(b[offset:], ll.fanoutParityPieces)
	offset += 2
	return b
}

// decode will take a []byte and load the layout from that []byte.
func (ll *linkfileLayout) decode(b []byte) {
	offset := 0
	ll.filesize = binary.LittleEndian.Uint64(b[offset:])
	offset += 8
	ll.metadataSize = binary.LittleEndian.Uint32(b[offset:])
	offset += 4
	ll.initialFanoutSize = binary.LittleEndian.Uint32(b[offset:])
	offset += 4
	ll.fanoutDataPieces = binary.LittleEndian.Uint16(b[offset:])
	offset += 2
	ll.fanoutParityPieces = binary.LittleEndian.Uint16(b[offset:])
	offset += 2
}

// DownloadSialink will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) DownloadSialink(link string) (modules.LinkfileMetadata, []byte, error) {
	// Parse the provided link into a usable structure for fetching downloads.
	var ld LinkData
	err := ld.LoadString(link)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to parse link for download")
	}

	// Check that the link follows the restrictions of the current software
	// capabilities.
	//
	// TODO: Some of these restrictions will be lifted as the full linkfile spec
	// is completed.
	if ld.Version != 1 {
		return modules.LinkfileMetadata{}, nil, errors.New("link is not version 1")
	}
	if LinkfileLayoutSize+ld.PayloadSize > modules.SectorSize {
		return modules.LinkfileMetadata{}, nil, errors.New("size of file suggests a fanout was used - this version does not support fanouts")
	}
	if ld.DataPieces != 1 {
		return modules.LinkfileMetadata{}, nil, errors.New("data pieces must be set to 1 on a link")
	}
	if ld.ParityPieces != 1 {
		return modules.LinkfileMetadata{}, nil, errors.New("parity pieces must be set to 1 on a link")
	}

	// Fetch the actual file.
	baseSector, err := r.DownloadByRoot(ld.MerkleRoot, 0, LinkfileLayoutSize+ld.PayloadSize)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "link based download has failed")
	}

	// Parse out the linkfileLayout.
	offset := uint32(0)
	var ll linkfileLayout
	ll.decode(baseSector)
	offset += LinkfileLayoutSize

	// Parse out the linkfile metadata.
	var lfm modules.LinkfileMetadata
	err = json.Unmarshal(baseSector[offset:offset+ll.metadataSize], &lfm)
	if err != nil {
		return modules.LinkfileMetadata{}, nil, errors.AddContext(err, "unable to parse link file metadata")
	}
	offset += ll.metadataSize

	// TODO: Parse out the fanout.

	// TODO: If there is intra-sector sharding, that code needs to be applied
	// here.

	return lfm, baseSector[offset : LinkfileLayoutSize+ld.PayloadSize], nil
}

// UploadLinkfile will upload the provided data with the provided name and
// metadata, returning a sialink which can be used by any viewnode to recover
// the full original file and metadata.
//
// TODO: Params we need:
//		+ Initial sector erasure coding settings
//		+ Initial fanout size
//		+ Fanout redundancy settings
//		+ A 'force' parameter to overwrite any existing linkfiles
func (r *Renter) UploadLinkfile(lfm modules.LinkfileMetadata, fileDataReader io.Reader) (string, error) {
	// Compose the metadata into the leading sector.
	mlfm, err := json.Marshal(lfm)
	if err != nil {
		return "", errors.AddContext(err, "unable to marshal the link file metadata")
	}
	if len(mlfm) > math.MaxUint32 {
		return "", fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(mlfm), math.MaxUint32)
	}

	// TODO: Compute fanout information based on the supplied redundancy. When
	// we start doing a full fanout actually the entire first chunk is going to
	// need to be kept in memory (and all subsequent fanout chunks) while the
	// rest of the file uploads. The overhead here isn't too bad though, at
	// worst each 4 KiB of memory points to 4 MiB of streamed uploaded data. At
	// best it's more like each 4 KiB of memory points to 100 MiB of streamed
	// uploaded data; for reasonably sized files and reasonable parallelism, the
	// viewnode will not run out of memory.

	// Compute the layout bytes for the sector.
	ll := linkfileLayout{
		metadataSize:       uint32(len(mlfm)),
		initialFanoutSize:  0, // TODO: Will be updated when fanout is implemented
		fanoutDataPieces:   0, // TODO: Will be updated when fanout is implemented
		fanoutParityPieces: 0, // TODO: Will be updated when fanout is implemented
	}
	llData := ll.encode()
	headerSize := uint64(uint32(len(llData)) + ll.metadataSize + ll.initialFanoutSize)

	// TODO: Create the fanout data.
	fanoutData := make([]byte, ll.initialFanoutSize) // empty slice for now, so that the full math can be used.

	// Read data from the reader to fill out the remainder of the first sector.
	//
	// TODO: Going to need to adjust the size of the readBuf based on the type
	// and amount of intra-sector erasure coding being performed. Can't read
	// directly into erasure coding shards though because we don't know how much
	// data total is being read yet.
	fileData := make([]byte, modules.SectorSize-headerSize)
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
	fullPath, err := LinkfileSiaFolder.Join(lfm.Name)
	if err != nil {
		return "", errors.AddContext(err, "unable to create a linkfile with the given name")
	}
	// TODO: allow the caller to decide what sort of replication should be used
	// on this first chunk.
	ec, err := siafile.NewRSSubCode(1, 10, 64)
	if err != nil {
		return "", errors.AddContext(err, "unable to create erasure coder")
	}
	up := modules.FileUploadParams{
		SiaPath:             fullPath,
		ErasureCode:         ec,
		Force:               false,
		DisablePartialChunk: true,
		Repair:              false, // indicates whether this is a repair operation

		CipherType: crypto.TypePlain,
	}

	// Perform the actual upload. This will requring turning the buffer we
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

	// Create the link data and return the resulting sialink.
	ld := LinkData{
		Version:      1,
		MerkleRoot:   mr,
		PayloadSize:  uint64(offset - LinkfileLayoutSize),
		DataPieces:   1,
		ParityPieces: 1,
	}
	return ld.String(), nil
}
