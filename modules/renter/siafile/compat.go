package siafile

import (
	"os"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// FileData is a helper struct that contains all the relevant information
	// of a file. It simplifies passing the necessary data between modules and
	// keeps the interface clean.
	FileData struct {
		Name        string
		FileSize    uint64
		MasterKey   [crypto.EntropySize]byte
		ErasureCode modules.ErasureCoder
		RepairPath  string
		PieceSize   uint64
		Mode        os.FileMode
		Deleted     bool
		UID         string
		Chunks      []FileChunk
	}
	// FileChunk is a helper struct that contains data about a chunk.
	FileChunk struct {
		Pieces [][]Piece
	}
)

// NewFromFileData creates a new SiaFile from a FileData object that was
// previously created from a legacy file.
func NewFromFileData(fd FileData) (*SiaFile, error) {
	// legacy masterKeys are always twofish keys
	mk, err := crypto.NewSiaKey(crypto.TypeTwofish, fd.MasterKey[:])
	if err != nil {
		return nil, errors.AddContext(err, "failed to restore master key")
	}
	currentTime := time.Now()
	file := &SiaFile{
		staticMetadata: metadata{
			AccessTime:          currentTime,
			ChunkOffset:         defaultReservedMDPages * pageSize,
			ChangeTime:          currentTime,
			CreateTime:          currentTime,
			StaticFileSize:      int64(fd.FileSize),
			LocalPath:           fd.RepairPath,
			StaticMasterKey:     mk.Key(),
			StaticMasterKeyType: mk.Type(),
			Mode:                fd.Mode,
			ModTime:             currentTime,
			StaticPieceSize:     fd.PieceSize,
			SiaPath:             fd.Name,
		},
		deleted:   fd.Deleted,
		staticUID: fd.UID,
	}
	file.staticChunks = make([]chunk, len(fd.Chunks))
	for i := range file.staticChunks {
		ecType, ecParams := marshalErasureCoder(fd.ErasureCode)
		file.staticChunks[i].staticErasureCode = fd.ErasureCode
		file.staticChunks[i].StaticErasureCodeType = ecType
		file.staticChunks[i].StaticErasureCodeParams = ecParams
		file.staticChunks[i].Pieces = make([][]Piece, file.staticChunks[i].staticErasureCode.NumPieces())
	}

	// Populate the pubKeyTable of the file and add the pieces.
	pubKeyMap := make(map[string]int)
	for chunkIndex, chunk := range fd.Chunks {
		for pieceIndex, pieceSet := range chunk.Pieces {
			for _, piece := range pieceSet {
				// Check if we already added that public key.
				if _, exists := pubKeyMap[string(piece.HostPubKey.Key)]; !exists {
					pubKeyMap[string(piece.HostPubKey.Key)] = len(file.pubKeyTable)
					file.pubKeyTable = append(file.pubKeyTable, piece.HostPubKey)
				}
				// Add the piece to the SiaFile.
				file.staticChunks[chunkIndex].Pieces[pieceIndex] = append(file.staticChunks[chunkIndex].Pieces[pieceIndex], Piece{
					HostPubKey: piece.HostPubKey,
					MerkleRoot: piece.MerkleRoot,
				})
			}
		}
	}
	return file, nil
}
