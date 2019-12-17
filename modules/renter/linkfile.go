package renter

// linkfile.go provides the tools for creating and uploading linkfiles, and then
// receiving the associated links to recover the files.

// TODO: To test this stuff properly we may need to increase the sector size
// during testing >.>

// TODO: We should probably enable the metadata to be extended beyond 4096
// bytes. At that point, the metadata could just bleed into the fully encoded
// data where the non-flat erasure coding is happening, and the linkfile
// retreiver just knows to handle it correctly.
//
// I have some ideas for how to do this now, the spec will end up changing.

import (
	"bytes"
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
	// LinkfileMetadataMaxSize is the amount of space in a linkfile that is
	// allocated for file metadata.
	LinkFileMetadataMaxSize = 512

	// LinkFileFanoutSize is the amount of space in a linkfile that each sector
	// allocates to fanout sectors.
	LinkFileFanoutSize = 512

	// FileStartOffset establishes where in the linkfile data that the actual
	// underlying file data begins.
	FileStartOffset = LinkFileMetadataMaxSize + LinkFileFanoutSize

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

// DownloadLinkFile will take a link and turn it into the metadata and data of a
// download.
func (r *Renter) DownloadLinkFile(link string) (modules.LinkFileMetadata, []byte, error) {
	// Parse the provided link into a usable structure for fetching downloads.
	var ld LinkData
	err := ld.LoadString(link)
	if err != nil {
		return modules.LinkFileMetadata{}, nil, errors.AddContext(err, "unable to parse link for download")
	}

	// Check that the link follows the restrictions of the current software
	// capabilities.
	if ld.Version != 1 {
		return modules.LinkFileMetadata{}, nil, errors.New("link is not version 1")
	}
	if ld.Filesize > modules.SectorSize - FileStartOffset {
		return modules.LinkFileMetadata{}, nil, errors.New("links with fanouts not supported")
	}
	if ld.DataPieces != 1 {
		return modules.LinkFileMetadata{}, nil, errors.New("data pieces must be set to 1 on a link")
	}
	if ld.ParityPieces != 1 {
		return modules.LinkFileMetadata{}, nil, errors.New("parity pieces must be set to 1 on a link")
	}

	// Fetch the actual file.
	linkFileData, err := r.DownloadByRoot(ld.MerkleRoot, 0, ld.Filesize + FileStartOffset)
	if err != nil {
		return modules.LinkFileMetadata{}, nil, errors.AddContext(err, "link based download has failed")
	}

	// Parse out the link file metadata. Need to use a json.NewDecoder because
	// the length of the metadata is unknown, simply calling json.Unmarshal will
	// result in an error when it hits the padding.
	var lfm modules.LinkFileMetadata
	bufDat := make([]byte, LinkFileMetadataMaxSize)
	copy(bufDat, linkFileData)
	buf := bytes.NewBuffer(bufDat)
	err = json.NewDecoder(buf).Decode(&lfm)
	if err != nil {
		return modules.LinkFileMetadata{}, nil, errors.AddContext(err, "unable to parse link file metadata")
	}

	// Return everything.
	return lfm, linkFileData[FileStartOffset:FileStartOffset+ld.Filesize], nil
}

// UploadLinkFile will upload the provided data with the provided name and stats
func (r *Renter) UploadLinkFile(lfm modules.LinkFileMetadata, fileData io.Reader) (string, error) {
	// Compose the metadata into the leading sector.
	mlfm, err := json.Marshal(lfm)
	if err != nil {
		return "", errors.AddContext(err, "unable to marshal the link file metadata")
	}
	if len(mlfm) > LinkFileMetadataMaxSize {
		return "", fmt.Errorf("encoded metadata size of %v exceeds the maximum of %v", len(mlfm), LinkFileMetadataMaxSize)
	}

	// Read all of the file data from the reader.
	readBuf := make([]byte, modules.SectorSize)
	size, err := io.ReadFull(fileData, readBuf)
	maxSize := modules.SectorSize - FileStartOffset
	if uint64(size) > maxSize {
		return "", fmt.Errorf("maximum size for a linkfile at the current siad version is %v", maxSize)
	}
	if size == 0 {
		// TODO: We may not need this check, who is to say that empty files are
		// bad and can't be shared.
		return "", errors.New("refusing to upload an empty file")
	}

	// Assemble the raw data of the linkfile.
	linkFileData := make([]byte, size+FileStartOffset)
	copy(linkFileData, mlfm)
	copy(linkFileData[FileStartOffset:], readBuf[:size])

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
		SiaPath:             fullPath,
		ErasureCode:         ec,
		Force:               false,
		DisablePartialChunk: true,
		Repair:              false, // indicates whether this is a repair operation

		CipherType: crypto.TypePlain,
	}

	// Perform the actual upload. This will requring turning the buffer we
	// grabbed earlier back into a reader.
	fileDataReader := bytes.NewReader(linkFileData)
	// TODO: This should probably return a filehandle or something, so that the
	// caller can open the file without worrying about any sort of race
	// conditions.
	err = r.UploadStreamFromReader(up, fileDataReader)
	if err != nil {
		return "", errors.AddContext(err, "failed to upload the file")
	}

	// TODO: Is this blocking? Do we need to block? - looks like the answer is
	// that the UploadStreamFromReader call is not blocking, therefore this
	// needs to block until the upload is complete. Which cannot be done
	// correctly without getting the file node that's being used for uploading
	// because wayward renames could cause issues.
	time.Sleep(time.Second * 40)

	// Open the newly uploaded file and get the merkle roots of all the pieces.
	// They should match eachother.
	//
	// TODO: should really get the snapshot from the call to upload rather than
	// have to open it separately.
	fileNode, err := r.staticFileSystem.OpenSiaFile(fullPath)
	if err != nil {
		return "", errors.AddContext(err, "unable to open the sia file after uploading")
	}
	defer fileNode.Close()
	snap, err := fileNode.Snapshot(fullPath)
	if err != nil {
		return "", errors.AddContext(err, "unable to retrieve a snapshot after uploading")
	}

	// Get all of the pieces of the first chunk and check that they have the
	// same merkle root. The merkle root will be used to create the link.
	//
	// TODO: We assume that there is at least one complete piece here, need to
	// rewrite that assumption out to avoid a crash.
	pieces := snap.Pieces(0)
	mr := pieces[0][0].MerkleRoot
	for _, pieceSet := range pieces {
		for _, piece := range pieceSet {
			if piece.MerkleRoot != mr {
				return "", fmt.Errorf("pieces in the linkfile have different merkle roots: %v :: %v", piece.MerkleRoot, mr)
			}
		}
	}

	// Create the link data.
	ld := LinkData{
		Version:      1,
		MerkleRoot:   mr,
		Filesize:     uint64(size),
		DataPieces:   1,
		ParityPieces: 1,
	}
	// Convert the link data to a string and return.
	sialink := ld.String()
	return sialink, nil
}
