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

// Close will close the set entry, removing the entry from memory if there are
// no other entries using the siafile.
//
// Note that 'Close' grabs a lock on the SiaFileSet, do not call this function
// while holding a lock on the SiafileSet - standard concurrency conventions
// though dictate that you should not be calling exported / capitalized
// functions while holding a lock anyway, but this function is particularly
// sensitive to that.
func (entry *SiaFileSetEntry) Close() error {
	entry.siaFileSet.mu.Lock()
	entry.siaFileSet.closeEntry(entry)
	entry.siaFileSet.mu.Unlock()
	return nil
}

// closeEntry will close an entry in the SiaFileSet, removing the siafile from
// the cache if no other entries are open for that siafile.
//
// Note that this function needs to be called while holding a lock on the
// SiaFileSet, per standard concurrency conventions. This function also goes and
// grabs a lock on the entry that it is being passed, which means that the lock
// cannot be held while calling 'closeEntry'.
//
// The memory model we have has the SiaFileSet as the superior object, so per
// convention methods on the SiaFileSet should not be getting held while entry
// locks are being held, but this function is particularly dependent on that
// convention.
func (sfs *SiaFileSet) closeEntry(entry *SiaFileSetEntry) {
	// Lock the thread map mu and remove the threadUID from the entry.
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	delete(entry.threadMap, entry.threadUID)

	// The entry that exists in the siafile set may not be the same as the entry
	// that is being closed, this can happen if there was a rename or a delete
	// and then a new/different file was uploaded with the same siapath.
	//
	// If they are not the same entry, there is nothing more to do.
	currentEntry := sfs.siaFileMap[entry.staticMetadata.SiaPath]
	if currentEntry != entry.siaFileSetEntry {
		return
	}

	// If there are no more threads that have the current entry open, delete
	// this entry from the set cache.
	if len(currentEntry.threadMap) == 0 {
		delete(sfs.siaFileMap, entry.staticMetadata.SiaPath)
	}
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

// Delete deletes the SiaFileSetEntry's SiaFile
func (sfs *SiaFileSet) Delete(siaPath string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()

	// Fetch the corresponding siafile and call delete.
	entry, err := sfs.open(siaPath)
	if err != nil {
		return err
	}
	err = entry.Delete()
	if err != nil {
		return err
	}

	// Remove the siafile from the set map so that other threads can't find it.
	delete(sfs.siaFileMap, entry.staticMetadata.SiaPath)
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
		siaFilePath:    filepath.Join(sfs.siaFileDir, fd.Name+ShareExtension),
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

// Rename will move a siafile from one path to a new path. Existing entries that
// are already open at the old path will continue to be valid.
func (sfs *SiaFileSet) Rename(siaPath, newSiaPath string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()

	// Check for a conflict in the destination path.
	exists := sfs.exists(newSiaPath)
	if exists {
		return ErrPathOverload
	}
	// Grab the existing entry.
	entry, err := sfs.open(siaPath)
	if err != nil {
		return err
	}
	// This whole function is wrapped in a set lock, which means per convention
	// we cannot call an exported function (Close) on the entry. We will have to
	// call closeEntry on the set instead.
	defer sfs.closeEntry(entry)

	// Update SiaFileSet map to hold the entry in the new siapath.
	sfs.siaFileMap[newSiaPath] = entry.siaFileSetEntry
	delete(sfs.siaFileMap, siaPath)

	// Update the siafile to have a new name.
	return entry.Rename(newSiaPath, filepath.Join(sfs.siaFileDir, newSiaPath+ShareExtension))
}
