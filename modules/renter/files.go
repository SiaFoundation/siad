package renter

import (
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrEmptyFilename is an error when filename is empty
	ErrEmptyFilename = errors.New("filename must be a nonempty string")
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
//
// TODO: The data is not cleared from any contracts where the host is not
// immediately online.
func (r *Renter) DeleteFile(nickname string) error {
	entry, err := r.staticFileSet.Open(nickname, r.filesDir, siafile.SiaFileDeleteThread)
	if err != nil {
		return err
	}
	defer entry.Close(siafile.SiaFileDeleteThread)
	// TODO: delete the sectors of the file as well.
	if err := r.staticFileSet.Delete(entry); err != nil {
		return err
	}
	// Save renter
	lockID := r.mu.Lock()
	defer r.mu.Unlock(lockID)
	return r.saveSync()
}

// FileList returns all of the files that the renter has.
func (r *Renter) FileList() []modules.FileInfo {
	// Get all the renter files
	files, err := r.staticFileSet.All(r.filesDir, siafile.SiaFileAPIThread)
	if err != nil {
		return []modules.FileInfo{}
	}

	// Save host keys in map. We can't do that under the same lock since we
	// need to call a public method on the file.
	pks := make(map[string]types.SiaPublicKey)
	for _, file := range files {
		for _, pk := range file.HostPublicKeys() {
			pks[string(pk.Key)] = pk
		}
	}

	// Build 2 maps that map every pubkey to its offline and goodForRenew
	// status.
	goodForRenew := make(map[string]bool)
	offline := make(map[string]bool)
	contracts := make(map[string]modules.RenterContract)
	for _, pk := range pks {
		contract, ok := r.hostContractor.ContractByPublicKey(pk)
		if !ok {
			continue
		}
		goodForRenew[string(pk.Key)] = ok && contract.Utility.GoodForRenew
		offline[string(pk.Key)] = r.hostContractor.IsOffline(pk)
		contracts[string(pk.Key)] = contract
	}

	// Build the list of FileInfos.
	fileList := []modules.FileInfo{}
	for _, f := range files {
		localPath := f.LocalPath()
		_, err := os.Stat(localPath)
		onDisk := !os.IsNotExist(err)
		redundancy := f.Redundancy(offline, goodForRenew)
		fileList = append(fileList, modules.FileInfo{
			AccessTime:     f.AccessTime(),
			Available:      f.Available(offline),
			ChangeTime:     f.ChangeTime(),
			CipherType:     f.MasterKey().Type().String(),
			CreateTime:     f.CreateTime(),
			Expiration:     f.Expiration(contracts),
			Filesize:       f.Size(),
			LocalPath:      localPath,
			ModTime:        f.ModTime(),
			OnDisk:         onDisk,
			Recoverable:    onDisk || redundancy >= 1,
			Redundancy:     redundancy,
			Renewing:       true,
			SiaPath:        f.SiaPath(),
			UploadedBytes:  f.UploadedBytes(),
			UploadProgress: f.UploadProgress(),
		})
	}
	return fileList
}

// File returns file from siaPath queried by user.
// Update based on FileList
func (r *Renter) File(siaPath string) (modules.FileInfo, error) {
	// Get the file and its contracts
	entry, err := r.staticFileSet.Open(siaPath, r.filesDir, siafile.SiaFileAPIThread)
	if err != nil {
		return modules.FileInfo{}, err
	}
	defer entry.Close(siafile.SiaFileAPIThread)
	file := entry.SiaFile()
	pks := file.HostPublicKeys()

	// Build 2 maps that map every contract id to its offline and goodForRenew
	// status.
	goodForRenew := make(map[string]bool)
	offline := make(map[string]bool)
	contracts := make(map[string]modules.RenterContract)
	for _, pk := range pks {
		contract, ok := r.hostContractor.ContractByPublicKey(pk)
		if !ok {
			continue
		}
		goodForRenew[string(pk.Key)] = ok && contract.Utility.GoodForRenew
		offline[string(pk.Key)] = r.hostContractor.IsOffline(pk)
		contracts[string(pk.Key)] = contract
	}

	// Build the FileInfo
	renewing := true
	localPath := file.LocalPath()
	_, err = os.Stat(localPath)
	onDisk := !os.IsNotExist(err)
	redundancy := file.Redundancy(offline, goodForRenew)
	fileInfo := modules.FileInfo{
		AccessTime:     file.AccessTime(),
		Available:      file.Available(offline),
		ChangeTime:     file.ChangeTime(),
		CipherType:     file.MasterKey().Type().String(),
		CreateTime:     file.CreateTime(),
		Expiration:     file.Expiration(contracts),
		Filesize:       file.Size(),
		LocalPath:      localPath,
		ModTime:        file.ModTime(),
		OnDisk:         onDisk,
		Recoverable:    onDisk || redundancy >= 1,
		Redundancy:     redundancy,
		Renewing:       renewing,
		SiaPath:        file.SiaPath(),
		UploadedBytes:  file.UploadedBytes(),
		UploadProgress: file.UploadProgress(),
	}

	return fileInfo, nil
}

// RenameFile takes an existing file and changes the nickname. The original
// file must exist, and there must not be any file that already has the
// replacement nickname.
func (r *Renter) RenameFile(currentName, newName string) error {
	err := validateSiapath(newName)
	if err != nil {
		return err
	}
	entry, err := r.staticFileSet.Open(currentName, r.filesDir, siafile.SiaFileRenameThread)
	if err != nil {
		return err
	}
	defer entry.Close(siafile.SiaFileRenameThread)
	return r.staticFileSet.Rename(entry, newName, filepath.Join(r.filesDir, newName+siafile.ShareExtension))
}

// fileToSiaFile converts a legacy file to a SiaFile. Fields that can't be
// populated using the legacy file remain blank.
func (r *Renter) fileToSiaFile(f *file, repairPath string) (*siafile.SiaFile, error) {
	fileData := siafile.FileData{
		Name:        f.name,
		FileSize:    f.size,
		MasterKey:   f.masterKey,
		ErasureCode: f.erasureCode,
		RepairPath:  repairPath,
		PieceSize:   f.pieceSize,
		Mode:        os.FileMode(f.mode),
		Deleted:     f.deleted,
		UID:         f.staticUID,
	}
	chunks := make([]siafile.FileChunk, f.numChunks())
	for i := 0; i < len(chunks); i++ {
		chunks[i].Pieces = make([][]siafile.Piece, f.erasureCode.NumPieces())
	}
	for _, contract := range f.contracts {
		pk := r.hostContractor.ResolveIDToPubKey(contract.ID)
		for _, piece := range contract.Pieces {
			chunks[piece.Chunk].Pieces[piece.Piece] = append(chunks[piece.Chunk].Pieces[piece.Piece], siafile.Piece{
				HostPubKey: pk,
				MerkleRoot: piece.MerkleRoot,
			})
		}
	}
	fileData.Chunks = chunks
	return siafile.NewFromFileData(fileData, r.staticFileSet)
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
