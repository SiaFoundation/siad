package siafile

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// The SiaFileSet structure helps track the number of threads using a siafile
// and will enable features like caching and lazy loading to improve start up
// times and reduce memory usage. SiaFile methods such as New, Delete, Rename,
// etc should be called through the SiaFileSet to maintain atomic transactions.
type (
	// SiaFileSet is a helper struct responsible for managing the renter's
	// siafiles in memory
	SiaFileSet struct {
		siaFileDir string
		siaFileMap map[string]*siaFileSetEntry

		// utilities
		mu  sync.Mutex
		wal *writeaheadlog.WAL
	}

	// siaFileSetEntry contains information about the threads accessing the
	// SiaFile and references to the SiaFile and the SiaFileSet
	siaFileSetEntry struct {
		*SiaFile
		siaFileSet *SiaFileSet

		threadMap   map[uint64]threadInfo
		threadMapMu sync.Mutex
	}

	// SiaFileSetEntry is the exported struct that is returned to the thread
	// accessing the SiaFile and the Entry
	SiaFileSetEntry struct {
		*siaFileSetEntry
		threadUID uint64
	}

	// threadInfo contains useful information about the thread accessing the
	// SiaFileSetEntry
	threadInfo struct {
		callingFiles []string
		callingLines []int
		lockTime     time.Time
	}
)

// NewSiaFileSet initializes and returns a SiaFileSet
func NewSiaFileSet(filesDir string, wal *writeaheadlog.WAL) *SiaFileSet {
	return &SiaFileSet{
		siaFileDir: filesDir,
		siaFileMap: make(map[string]*siaFileSetEntry),
		wal:        wal,
	}
}

// newThreadType created a threadInfo entry for the threadMap
func newThreadType() threadInfo {
	tt := threadInfo{
		callingFiles: make([]string, threadDepth+1),
		callingLines: make([]int, threadDepth+1),
		lockTime:     time.Now(),
	}
	for i := 0; i <= threadDepth; i++ {
		_, tt.callingFiles[i], tt.callingLines[i], _ = runtime.Caller(2 + i)
	}
	return tt
}

// randomThreadUID returns a random uint64 to be used as the thread UID in the
// threadMap of the SiaFileSetEntry
func randomThreadUID() uint64 {
	return fastrand.Uint64n(math.MaxUint64)
}

// close removes the thread from the threadMap. If the length of threadMap count
// is 0 then it will remove the siaFileSetEntry from the SiaFileSet map, which
// will remove it from memory
func (entry *SiaFileSetEntry) close() error {
	if _, ok := entry.threadMap[entry.threadUID]; !ok {
		return ErrUnknownThread
	}
	delete(entry.threadMap, entry.threadUID)
	if len(entry.threadMap) == 0 {
		delete(entry.siaFileSet.siaFileMap, entry.SiaPath())
	}
	return nil
}

// CopyEntry copies the SiaFileSetEntry n times and returns an array of
// SiaFileSetEntrys
func (entry *SiaFileSetEntry) CopyEntry(n int) []*SiaFileSetEntry {
	var entrys []*SiaFileSetEntry
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	for i := 0; i < n; i++ {
		threadUID := randomThreadUID()
		entrys = append(entrys, &SiaFileSetEntry{
			siaFileSetEntry: entry.siaFileSetEntry,
			threadUID:       threadUID,
		})
		entry.threadMap[threadUID] = newThreadType()
	}
	return entrys
}

// Close removes the thread from the threadMap. If the length of threadMap count
// is 0 then it will remove the SiaFileSetEntry from the SiaFileSet map, which
// will remove it from memory
func (entry *SiaFileSetEntry) Close() error {
	entry.siaFileSet.mu.Lock()
	defer entry.siaFileSet.mu.Unlock()
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	return entry.close()
}

// exists checks to see if a file with the provided siaPath already exists in
// the renter
func (sfs *SiaFileSet) exists(siaPath string) bool {
	// Make sure there are no leading slashes
	siaPath = strings.TrimPrefix(siaPath, "/")
	// Check for file in Memory
	_, exists := sfs.siaFileMap[siaPath]
	if exists {
		return exists
	}
	// Check for file on disk
	_, err := os.Stat(filepath.Join(sfs.siaFileDir, siaPath+ShareExtension))
	return !os.IsNotExist(err)
}

// newSiaFileSetEntry initializes and returns a siaFileSetEntry
func (sfs *SiaFileSet) newSiaFileSetEntry(sf *SiaFile) *siaFileSetEntry {
	threads := make(map[uint64]threadInfo)
	return &siaFileSetEntry{
		SiaFile:    sf,
		siaFileSet: sfs,
		threadMap:  threads,
	}
}

// open will return the siaFileSetEntry in memory or load it from disk
func (sfs *SiaFileSet) open(siaPath string) (*SiaFileSetEntry, error) {
	// Make sure there are no leading slashes
	siaPath = strings.TrimPrefix(siaPath, "/")
	var entry *siaFileSetEntry
	entry, exists := sfs.siaFileMap[siaPath]
	if !exists {
		// Try and Load File from disk
		sf, err := LoadSiaFile(filepath.Join(sfs.siaFileDir, siaPath+ShareExtension), sfs.wal)
		if err != nil {
			return nil, err
		}
		entry = sfs.newSiaFileSetEntry(sf)
		sfs.siaFileMap[siaPath] = entry
	}
	if entry.Deleted() {
		return nil, ErrUnknownPath
	}
	threadUID := randomThreadUID()
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	entry.threadMap[threadUID] = newThreadType()
	return &SiaFileSetEntry{
		siaFileSetEntry: entry,
		threadUID:       threadUID,
	}, nil
}

// All returns all the siafiles in the renter by either returning them from the
// SiaFileSet of reading them from disk. The SiaFileSetEntry is closed after the
// SiaFile is added to the slice. This way the files are removed from memory by
// GC as soon as the calling method returns
//
// Note: This is currently only needed for the Files endpoint. This is an
// expensive call so it should be avoided unless absolutely necessary
//
// TODO - Delete once !3378 is merged
func (sfs *SiaFileSet) All() ([]*SiaFileSetEntry, error) {
	var entrys []*SiaFileSetEntry
	sfs.mu.Lock()
	dir := sfs.siaFileDir
	sfs.mu.Unlock()
	// Recursively load all files found in renter directory. Errors
	// are not considered fatal and are ignored.
	return entrys, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			return nil
		}

		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != ShareExtension {
			return nil
		}

		// Load the Siafile.
		siaPath := strings.TrimSuffix(strings.TrimPrefix(path, dir), ShareExtension)
		entry, err := sfs.Open(siaPath)
		if err != nil {
			return nil
		}
		entrys = append(entrys, entry)
		return nil
	})
}

// Delete deletes the SiaFileSetEntry's SiaFile
func (sfs *SiaFileSet) Delete(siaPath string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Check if SiaFile exists
	exists := sfs.exists(siaPath)
	if !exists {
		return ErrUnknownPath
	}
	// Grab entry
	entry, err := sfs.open(siaPath)
	if err != nil {
		return err
	}
	// Defer close entry
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	defer entry.close()
	// Delete SiaFile
	if err := entry.Delete(); err != nil {
		return err
	}
	// Delete entry from set.
	delete(sfs.siaFileMap, entry.SiaPath())
	return nil
}

// Exists checks to see if a file with the provided siaPath already exists in
// the renter
func (sfs *SiaFileSet) Exists(siaPath string) bool {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.exists(siaPath)
}

// NewFromFileData creates a new SiaFile from a FileData object that was
// previously created from a legacy file.
func (sfs *SiaFileSet) NewFromFileData(fd FileData) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Make sure there are no leading slashes
	fd.Name = strings.TrimPrefix(fd.Name, "/")
	// legacy masterKeys are always twofish keys
	mk, err := crypto.NewSiaKey(crypto.TypeTwofish, fd.MasterKey[:])
	if err != nil {
		return nil, errors.AddContext(err, "failed to restore master key")
	}
	currentTime := time.Now()
	ecType, ecParams := marshalErasureCoder(fd.ErasureCode)
	file := &SiaFile{
		staticMetadata: metadata{
			AccessTime:              currentTime,
			ChunkOffset:             defaultReservedMDPages * pageSize,
			ChangeTime:              currentTime,
			CreateTime:              currentTime,
			StaticFileSize:          int64(fd.FileSize),
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
			SiaPath:                 fd.Name,
		},
		deleted:        fd.Deleted,
		deps:           modules.ProdDependencies,
		siaFilePath:    fd.RepairPath,
		staticUniqueID: fd.UID,
		wal:            sfs.wal,
	}
	file.staticChunks = make([]chunk, len(fd.Chunks))
	for i := range file.staticChunks {
		file.staticChunks[i].Pieces = make([][]piece, file.staticMetadata.staticErasureCode.NumPieces())
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
				file.staticChunks[chunkIndex].Pieces[pieceIndex] = append(file.staticChunks[chunkIndex].Pieces[pieceIndex], piece{
					HostTableOffset: tableOffset,
					MerkleRoot:      p.MerkleRoot,
				})
			}
		}
	}
	entry := sfs.newSiaFileSetEntry(file)
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadType()
	sfs.siaFileMap[fd.Name] = entry
	return &SiaFileSetEntry{
		siaFileSetEntry: entry,
		threadUID:       threadUID,
	}, file.saveFile()
}

// NewSiaFile create a new SiaFile, adds it to the SiaFileSet, adds the thread
// to the threadMap, and returns the SiaFileSetEntry. Since this method returns
// the SiaFileSetEntry, wherever NewSiaFile is called there should be a Close
// called on the SiaFileSetEntry to avoid the file being stuck in memory due the
// thread never being removed from the threadMap
func (sfs *SiaFileSet) NewSiaFile(up modules.FileUploadParams, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	siaPath := strings.TrimPrefix(up.SiaPath, "/")
	// Check is SiaFile already exists
	exists := sfs.exists(siaPath)
	if exists && !up.Force {
		return nil, ErrPathOverload
	}
	// Make sure there are no leading slashes
	siaFilePath := filepath.Join(sfs.siaFileDir, siaPath+ShareExtension)
	sf, err := New(siaFilePath, siaPath, up.Source, sfs.wal, up.ErasureCode, masterKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	entry := sfs.newSiaFileSetEntry(sf)
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadType()
	sfs.siaFileMap[siaPath] = entry
	return &SiaFileSetEntry{
		siaFileSetEntry: entry,
		threadUID:       threadUID,
	}, nil
}

// Open returns the siafile from the SiaFileSet for the corresponding key and
// adds the thread to the entry's threadMap. If the siafile is not in memory it
// will load it from disk
func (sfs *SiaFileSet) Open(siaPath string) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.open(siaPath)
}

// Rename renames the SiaFile of the SiaFileSetEntry, it does not create a new
// entry for the new name nor does it delete the entry with with old name
func (sfs *SiaFileSet) Rename(siaPath, newSiaPath string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Make sure there are no leading slashes
	siaPath = strings.TrimPrefix(siaPath, "/")
	newSiaPath = strings.TrimPrefix(newSiaPath, "/")
	// Check if SiaFile Exists
	exists := sfs.exists(siaPath)
	if !exists {
		return ErrUnknownPath
	}
	// Check for Conflict
	exists = sfs.exists(newSiaPath)
	if exists {
		return ErrPathOverload
	}
	// Grab entry
	entry, err := sfs.open(siaPath)
	if err != nil {
		return err
	}
	// Defer close entry
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	defer entry.close()
	// Update SiaFileSet map
	sfs.siaFileMap[newSiaPath] = entry.siaFileSetEntry
	delete(sfs.siaFileMap, siaPath)
	// Rename SiaFile
	return entry.Rename(newSiaPath, filepath.Join(sfs.siaFileDir, newSiaPath+ShareExtension))
}
