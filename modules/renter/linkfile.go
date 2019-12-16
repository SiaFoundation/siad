package renter

// linkfile.go provides the tools for creating and uploading linkfiles, and then
// receiving the associated links to recover the files.

// TODO: To test this stuff properly we may need to increase the sector size
// during testing >.>

// TODO: Need to kill the magic constants.

// TODO: We should probably enable the metadata to be extended beyond 4096
// bytes. At that point, the metadata could just bleed into the fully encoded
// data where the non-flat erasure coding is happening, and the linkfile
// retreiver just knows to handle it correctly.
//
// I have some ideas for how to do this now, the spec will end up changing.

import (
	"bytes"
	"fmt"
	"encoding/json"
	"io"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// LinkfileMetadataMaxSize is the amount of space in a linkfile that is
	// allocated for file metadata.
	LinkFileMetadataMaxSize = 4096

	// LinkFileFanoutSize is the amount of space in a linkfile that each sector
	// allocates to fanout sectors.
	LinkFileFanoutSize = 8192

	// LinkFileSiaFolder is the folder where all of the linkfiles are stored.
	//
	// TODO: Move this to /var/linkfiles or some equivalent name. I'm not sure
	// that 'linkfiles' is the right base folder name (though I do think /var is
	// the right place) because... well maybe it is the right name I forgot the
	// reason.
	//
	// TODO: Would be great to have this be a proper SiaPath instead of just a
	// string.
	LinkFileSiaFolder = "/home/user/linkfiles"
)

// LinkFileMetadata is all of the metadata that gets placed into the first 4096
// bytes of the linkfile, and is used to set the metadata of the file when
// writing back to disk. The data is json-encoded when it is placed into the
// leading bytes of the linkfile, meaning that this struct can be extended
// without breaking compatibility.
type LinkFileMetadata struct {
	Name string `json:"name"`
	Mode string `json:"mode"`

	// TODO: More fields.
}

// UploadLinkFile will upload the provided data with the provided name and stats 
func (r *Renter) UploadLinkFile(lfm LinkFileMetadata, fileData io.Reader) (string, error) {
	// Compose the metadata into the leading sector.
	mlfm, err := json.Marshal(lfm)
	if err != nil {
		return "", errors.AddContext(err, "unable to marshal the link file metadata")
	}
	if len(mlfm) > LinkFileMetadataMaxSize {
		return "", fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(mlfm), LinkFileMetadataMaxSize)
	}

	// Helper vars.
	dataOffset := uint64(LinkFileMetadataMaxSize + LinkFileFanoutSize)

	// Read all of the file data from the reader.
	readBuf := make([]byte, modules.SectorSize)
	size, err := io.ReadFull(fileData, readBuf)
	maxSize := modules.SectorSize - dataOffset
	if uint64(size) > maxSize {
		return "", fmt.Errorf("maximum size for a linkfile at the current siad version is %v", maxSize)
	}
	if size == 0 {
		// TODO: We may not need this check, who is to say that empty files are
		// bad and can't be shared.
		return "", errors.New("refusing to upload an empty file")
	}

	// Assemble the raw data of the linkfile.
	linkFileData := make([]byte, LinkFileMetadataMaxSize + LinkFileFanoutSize + size)
	copy(linkFileData, mlfm)
	copy(linkFileData[dataOffset:], readBuf[:size])

	// Create parameters to upload the file with 1-of-N erasure coding and no
	// encryption. This should cause all of the pieces to have the same Merkle
	// root, which is critical to making the file discoverable to viewnodes and
	// also resiliant to host failures.
	spBase, err := modules.NewSiaPath(LinkFileSiaFolder)
	if err != nil {
		return "", errors.AddContext(err, "unable to create a siapath from the base")
	}
	fullPath, err := spBase.Join(lfm.Name)
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
		SiaPath: fullPath,
		ErasureCode: ec,
		Force: false,
		DisablePartialChunk: true,
		Repair: true,

		CipherType: crypto.TypePlain,
	}

	// Perform the actual upload. This will requring turning the buffer we
	// grabbed earlier back into a reader.
	fileDataReader := bytes.NewReader(linkFileData)
	err = r.UploadStreamFromReader(up, fileDataReader)
	if err != nil {
		return "", errors.AddContext(err, "failed to upload the file")
	}

	// TODO: Verify the merkle root.

	// Create the link data.
	ld := LinkData{
		Version: 1,
		// TODO: MerkleRoot: mr,
		Filesize: uint64(size),
		DataPieces: 1,
		ParityPieces: 1,
	}
	// Convert the link data to a string and return.
	sialink := ld.String()
	return sialink, nil
}
