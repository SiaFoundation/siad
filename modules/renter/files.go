package renter

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
)

// A file is a single file that has been uploaded to the network. Files are
// split into equal-length chunks, which are then erasure-coded into pieces.
// Each piece is separately encrypted, using a key derived from the file's
// master key. The pieces are uploaded to hosts in groups, such that one file
// contract covers many pieces.
type file struct {
	name        string
	size        uint64 // Static - can be accessed without lock.
	contracts   map[types.FileContractID]fileContract
	masterKey   [crypto.EntropySize]byte // Static - can be accessed without lock.
	erasureCode modules.ErasureCoder     // Static - can be accessed without lock.
	pieceSize   uint64                   // Static - can be accessed without lock.
	mode        uint32                   // actually an os.FileMode
	deleted     bool                     // indicates if the file has been deleted.

	staticUID string // A UID assigned to the file when it gets created.

	mu sync.RWMutex
}

// A fileContract is a contract covering an arbitrary number of file pieces.
// Chunk/Piece metadata is used to split the raw contract data appropriately.
type fileContract struct {
	ID     types.FileContractID
	IP     modules.NetAddress
	Pieces []pieceData

	WindowStart types.BlockHeight
}

// pieceData contains the metadata necessary to request a piece from a
// fetcher.
//
// TODO: Add an 'Unavailable' flag that can be set if the host loses the piece.
// Some TODOs exist in 'repair.go' related to this field.
type pieceData struct {
	Chunk      uint64      // which chunk the piece belongs to
	Piece      uint64      // the index of the piece in the chunk
	MerkleRoot crypto.Hash // the Merkle root of the piece
}

// DeleteFile removes a file entry from the renter and deletes its data from
// the hosts it is stored on.
func (r *Renter) DeleteFile(siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticFileSet.Delete(siaPath)
}

// FileList returns all of the files that the renter has.
func (r *Renter) FileList() ([]modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return []modules.FileInfo{}, err
	}
	defer r.tg.Done()
	offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
	fileList := []modules.FileInfo{}
	err := filepath.Walk(r.staticFilesDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder. We simply ignore missing files.
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}

		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != modules.SiaFileExtension {
			return nil
		}

		// Load the Siafile.
		str := strings.TrimSuffix(strings.TrimPrefix(path, r.staticFilesDir), modules.SiaFileExtension)
		siaPath, err := modules.NewSiaPath(str)
		if err != nil {
			return err
		}
		file, err := r.fileInfo(siaPath, offlineMap, goodForRenewMap, contractsMap)
		if os.IsNotExist(err) || err == siafile.ErrUnknownPath {
			return nil
		}
		if err != nil {
			return err
		}
		fileList = append(fileList, file)
		return nil
	})

	return fileList, err
}

// File returns file from siaPath queried by user.
// Update based on FileList
func (r *Renter) File(siaPath modules.SiaPath) (modules.FileInfo, error) {
	if err := r.tg.Add(); err != nil {
		return modules.FileInfo{}, err
	}
	defer r.tg.Done()
	offline, goodForRenew, contracts := r.managedContractUtilityMaps()
	return r.fileInfo(siaPath, offline, goodForRenew, contracts)
}

// RenameFile takes an existing file and changes the nickname. The original
// file must exist, and there must not be any file that already has the
// replacement nickname.
func (r *Renter) RenameFile(currentName, newName modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.staticFileSet.Rename(currentName, newName)
}

// fileInfo returns information on a siafile. As a performance optimization, the
// fileInfo takes the maps returned by renter.managedContractUtilityMaps as
// many files at once.
func (r *Renter) fileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	// Get the file and its contracts
	entry, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return modules.FileInfo{}, err
	}
	defer entry.Close()

	// Build the FileInfo
	var onDisk bool
	localPath := entry.LocalPath()
	if localPath != "" {
		_, err = os.Stat(localPath)
		onDisk = err == nil
	}
	redundancy := entry.Redundancy(offline, goodForRenew)
	health, stuckHealth, numStuckChunks := entry.Health(offline, goodForRenew)
	fileInfo := modules.FileInfo{
		AccessTime:       entry.AccessTime(),
		Available:        redundancy >= 1,
		ChangeTime:       entry.ChangeTime(),
		CipherType:       entry.MasterKey().Type().String(),
		CreateTime:       entry.CreateTime(),
		Expiration:       entry.Expiration(contracts),
		Filesize:         entry.Size(),
		Health:           health,
		LocalPath:        localPath,
		MaxHealth:        math.Max(health, stuckHealth),
		MaxHealthPercent: entry.HealthPercentage(math.Max(health, stuckHealth)),
		ModTime:          entry.ModTime(),
		NumStuckChunks:   numStuckChunks,
		OnDisk:           onDisk,
		Recoverable:      onDisk || redundancy >= 1,
		Redundancy:       redundancy,
		Renewing:         true,
		SiaPath:          r.staticFileSet.SiaPath(entry).String(),
		Stuck:            numStuckChunks > 0,
		StuckHealth:      stuckHealth,
		UploadedBytes:    entry.UploadedBytes(),
		UploadProgress:   entry.UploadProgress(),
	}

	return fileInfo, nil
}

// fileToSiaFile converts a legacy file to a SiaFile. Fields that can't be
// populated using the legacy file remain blank.
func (r *Renter) fileToSiaFile(f *file, repairPath string, oldContracts []modules.RenterContract) (*siafile.SiaFileSetEntry, error) {
	// Create a mapping of contract ids to host keys.
	contracts := r.hostContractor.Contracts()
	idToPk := make(map[types.FileContractID]types.SiaPublicKey)
	for _, c := range contracts {
		idToPk[c.ID] = c.HostPublicKey
	}
	// Add old contracts to the mapping too.
	for _, c := range oldContracts {
		idToPk[c.ID] = c.HostPublicKey
	}

	fileData := siafile.FileData{
		Name:        f.name,
		FileSize:    f.size,
		MasterKey:   f.masterKey,
		ErasureCode: f.erasureCode,
		RepairPath:  repairPath,
		PieceSize:   f.pieceSize,
		Mode:        os.FileMode(f.mode),
		Deleted:     f.deleted,
		UID:         siafile.SiafileUID(f.staticUID),
	}
	chunks := make([]siafile.FileChunk, f.numChunks())
	for i := 0; i < len(chunks); i++ {
		chunks[i].Pieces = make([][]siafile.Piece, f.erasureCode.NumPieces())
	}
	for _, contract := range f.contracts {
		pk, exists := idToPk[contract.ID]
		if !exists {
			r.log.Printf("Couldn't find pubKey for contract %v with WindowStart %v",
				contract.ID, contract.WindowStart)
			continue
		}

		for _, piece := range contract.Pieces {
			// Make sure we don't add the same piece on the same host multiple
			// times.
			duplicate := false
			for _, p := range chunks[piece.Chunk].Pieces[piece.Piece] {
				if p.HostPubKey.String() == pk.String() {
					duplicate = true
					break
				}
			}
			if duplicate {
				continue
			}
			chunks[piece.Chunk].Pieces[piece.Piece] = append(chunks[piece.Chunk].Pieces[piece.Piece], siafile.Piece{
				HostPubKey: pk,
				MerkleRoot: piece.MerkleRoot,
			})
		}
	}
	fileData.Chunks = chunks
	return r.staticFileSet.NewFromLegacyData(fileData)
}

// numChunks returns the number of chunks that f was split into.
func (f *file) numChunks() uint64 {
	// empty files still need at least one chunk
	if f.size == 0 {
		return 1
	}
	n := f.size / f.staticChunkSize()
	// last chunk will be padded, unless chunkSize divides file evenly.
	if f.size%f.staticChunkSize() != 0 {
		n++
	}
	return n
}

// staticChunkSize returns the size of one chunk.
func (f *file) staticChunkSize() uint64 {
	return f.pieceSize * uint64(f.erasureCode.MinPieces())
}
