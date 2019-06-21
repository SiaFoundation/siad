package siafile

import (
	"os"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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
		UID         SiafileUID
		Chunks      []FileChunk
	}
	// FileChunk is a helper struct that contains data about a chunk.
	FileChunk struct {
		Pieces [][]Piece
	}
)

// NewFromLegacyData creates a new SiaFile from data that was previously loaded
// from a legacy file.
func (sfs *SiaFileSet) NewFromLegacyData(fd FileData) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()

	// Legacy master keys are always twofish keys.
	mk, err := crypto.NewSiaKey(crypto.TypeTwofish, fd.MasterKey[:])
	if err != nil {
		return nil, errors.AddContext(err, "failed to restore master key")
	}
	currentTime := time.Now()
	ecType, ecParams := marshalErasureCoder(fd.ErasureCode)
	siaPath, err := modules.NewSiaPath(fd.Name)
	if err != nil {
		return &SiaFileSetEntry{}, err
	}
	zeroHealth := float64(1 + fd.ErasureCode.MinPieces()/(fd.ErasureCode.NumPieces()-fd.ErasureCode.MinPieces()))
	file := &SiaFile{
		staticMetadata: Metadata{
			AccessTime:              currentTime,
			ChunkOffset:             defaultReservedMDPages * pageSize,
			ChangeTime:              currentTime,
			CreateTime:              currentTime,
			CachedHealth:            zeroHealth,
			CachedStuckHealth:       0,
			CachedRedundancy:        0,
			CachedUploadProgress:    0,
			FileSize:                int64(fd.FileSize),
			LocalPath:               fd.RepairPath,
			StaticMasterKey:         mk.Key(),
			StaticMasterKeyType:     mk.Type(),
			Mode:                    fd.Mode,
			ModTime:                 currentTime,
			staticErasureCode:       fd.ErasureCode,
			StaticErasureCodeType:   ecType,
			StaticErasureCodeParams: ecParams,
			StaticPagesPerChunk:     numChunkPagesRequired(fd.ErasureCode.NumPieces()),
			StaticPieceSize:         fd.PieceSize,
			UniqueID:                SiafileUID(fd.UID),
		},
		siaFilePath: siaPath.SiaFileSysPath(sfs.staticSiaFileDir),
		deps:        modules.ProdDependencies,
		deleted:     fd.Deleted,
		wal:         sfs.wal,
	}
	file.chunks = make([]chunk, len(fd.Chunks))
	for i := range file.chunks {
		file.chunks[i].Pieces = make([][]piece, file.staticMetadata.staticErasureCode.NumPieces())
	}
	// Update cached fields for 0-Byte files.
	if file.staticMetadata.FileSize == 0 {
		file.staticMetadata.CachedHealth = 0
		file.staticMetadata.CachedStuckHealth = 0
		file.staticMetadata.CachedRedundancy = float64(fd.ErasureCode.NumPieces()) / float64(fd.ErasureCode.MinPieces())
		file.staticMetadata.CachedUploadProgress = 100
	}

	// Populate the pubKeyTable of the file and add the pieces.
	pubKeyMap := make(map[string]uint32)
	for chunkIndex, chunk := range fd.Chunks {
		for pieceIndex, pieceSet := range chunk.Pieces {
			for _, p := range pieceSet {
				// Check if we already added that public key.
				tableOffset, exists := pubKeyMap[string(p.HostPubKey.Key)]
				if !exists {
					tableOffset = uint32(len(file.pubKeyTable))
					pubKeyMap[string(p.HostPubKey.Key)] = tableOffset
					file.pubKeyTable = append(file.pubKeyTable, HostPublicKey{
						PublicKey: p.HostPubKey,
						Used:      true,
					})
				}
				// Add the piece to the SiaFile.
				file.chunks[chunkIndex].Pieces[pieceIndex] = append(file.chunks[chunkIndex].Pieces[pieceIndex], piece{
					HostTableOffset: tableOffset,
					MerkleRoot:      p.MerkleRoot,
				})
			}
		}
	}
	entry, err := sfs.newSiaFileSetEntry(file)
	if err != nil {
		return nil, err
	}
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadInfo()
	sfse := &SiaFileSetEntry{
		siaFileSetEntry: entry,
		threadUID:       threadUID,
	}

	// Update the cached fields for progress and uploaded bytes.
	_, _ = file.UploadProgressAndBytes()

	return sfse, errors.AddContext(file.saveFile(), "unable to save file")
}
