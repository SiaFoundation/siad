package siadir

import (
	"math"
	"os"
	"runtime"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

type (
	// SiaDirSet handles the thread management for the SiaDirs on disk and in memory
	SiaDirSet struct {
		rootDir   string
		siaDirMap map[modules.SiaPath]*siaDirSetEntry

		// utilities
		mu  sync.Mutex
		wal *writeaheadlog.WAL
	}

	// siaDirSetEntry contains information about the threads accessing the
	// SiaDir and references to the SiaDir and the SiaDirSet
	siaDirSetEntry struct {
		*SiaDir
		siaDirSet *SiaDirSet

		threadMap   map[uint64]threadInfo
		threadMapMu sync.Mutex
	}

	// SiaDirSetEntry is the exported struct that is returned to the thread
	// accessing the SiaDir and the Entry
	SiaDirSetEntry struct {
		*siaDirSetEntry
		threadUID uint64
	}

	// threadInfo contains useful information about the thread accessing the
	// SiaDirSetEntry
	threadInfo struct {
		callingFiles []string
		callingLines []int
		lockTime     time.Time
	}
)

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
// threadMap of the SiaDirSetEntry
func randomThreadUID() uint64 {
	return fastrand.Uint64n(math.MaxUint64)
}

// NewSiaDirSet initializes and returns a SiaDirSet
func NewSiaDirSet(rootDir string, wal *writeaheadlog.WAL) *SiaDirSet {
	return &SiaDirSet{
		rootDir:   rootDir,
		siaDirMap: make(map[modules.SiaPath]*siaDirSetEntry),
		wal:       wal,
	}
}

// exists checks to see if a SiaDir with the provided siaPath already exists in
// the renter
func (sds *SiaDirSet) exists(siaPath modules.SiaPath) (bool, error) {
	// Check for SiaDir in Memory
	_, exists := sds.siaDirMap[siaPath]
	if exists {
		return exists, nil
	}
	// Check for SiaDir on disk
	_, err := os.Stat(siaPath.SiaDirMetadataSysPath(sds.rootDir))
	if err == nil {
		return true, nil
	}
	return false, err
}

// newSiaDirSetEntry initializes and returns a siaDirSetEntry
func (sds *SiaDirSet) newSiaDirSetEntry(sd *SiaDir) *siaDirSetEntry {
	threads := make(map[uint64]threadInfo)
	return &siaDirSetEntry{
		SiaDir:    sd,
		siaDirSet: sds,
		threadMap: threads,
	}
}

// open will return the siaDirSetEntry in memory or load it from disk
func (sds *SiaDirSet) open(siaPath modules.SiaPath) (*SiaDirSetEntry, error) {
	var entry *siaDirSetEntry
	entry, exists := sds.siaDirMap[siaPath]
	if !exists {
		// Try and Load File from disk
		sd, err := LoadSiaDir(sds.rootDir, siaPath, modules.ProdDependencies, sds.wal)
		if os.IsNotExist(err) {
			return nil, ErrUnknownPath
		}
		if err != nil {
			return nil, err
		}
		entry = sds.newSiaDirSetEntry(sd)
		sds.siaDirMap[siaPath] = entry
	}
	threadUID := randomThreadUID()
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	entry.threadMap[threadUID] = newThreadType()
	return &SiaDirSetEntry{
		siaDirSetEntry: entry,
		threadUID:      threadUID,
	}, nil
}

// Close will close the set entry, removing the entry from memory if there are
// no other entries using the siadir.
//
// Note that 'Close' grabs a lock on the SiaDirSet, do not call this function
// while holding a lock on the SiaDirSet - standard concurrency conventions
// though dictate that you should not be calling exported / capitalized
// functions while holding a lock anyway, but this function is particularly
// sensitive to that.
func (entry *SiaDirSetEntry) Close() error {
	entry.siaDirSet.mu.Lock()
	defer entry.siaDirSet.mu.Unlock()
	entry.siaDirSet.closeEntry(entry)
	return nil
}

// closeEntry will close an entry in the SiaDirSet, removing the siadir from the
// cache if no other entries are open for that siadir.
//
// Note that this function needs to be called while holding a lock on the
// SiaDirSet, per standard concurrency conventions. This function also goes and
// grabs a lock on the entry that it is being passed, which means that the lock
// cannot be held while calling 'closeEntry'.
//
// The memory model we have has the SiaDirSet as the superior object, so per
// convention methods on the SiaDirSet should not be getting held while entry
// locks are being held, but this function is particularly dependent on that
// convention.
func (sds *SiaDirSet) closeEntry(entry *SiaDirSetEntry) {
	// Lock the thread map mu and remove the threadUID from the entry.
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	delete(entry.threadMap, entry.threadUID)

	// The entry that exists in the siadir set may not be the same as the entry
	// that is being closed, this can happen if there was a rename or a delete
	// and then a new/different file was uploaded with the same siapath.
	//
	// If they are not the same entry, there is nothing more to do.
	currentEntry := sds.siaDirMap[entry.metadata.SiaPath]
	if currentEntry != entry.siaDirSetEntry {
		return
	}

	// If there are no more threads that have the current entry open, delete
	// this entry from the set cache.
	if len(currentEntry.threadMap) == 0 {
		delete(sds.siaDirMap, entry.metadata.SiaPath)
	}
}

// Delete deletes the SiaDir that belongs to the siaPath
func (sds *SiaDirSet) Delete(siaPath modules.SiaPath) error {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	// Check if SiaDir exists
	exists, err := sds.exists(siaPath)
	if !exists && os.IsNotExist(err) {
		return ErrUnknownPath
	}
	if err != nil {
		return err
	}
	// Grab entry
	entry, err := sds.open(siaPath)
	if err != nil {
		return err
	}
	// Defer close entry
	defer sds.closeEntry(entry)
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	// Delete SiaDir
	if err := entry.Delete(); err != nil {
		return err
	}
	return nil
}

// Exists checks to see if a file with the provided siaPath already exists in
// the renter
func (sds *SiaDirSet) Exists(siaPath modules.SiaPath) (bool, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	return sds.exists(siaPath)
}

// InitRootDir initializes the root directory SiaDir on disk. The root directory
// is not added in memory or returned.
func (sds *SiaDirSet) InitRootDir() error {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	// Check is SiaDir already exists
	rootSiaDir := modules.RootDirSiaPath()
	exists, err := sds.exists(rootSiaDir)
	if exists {
		return nil
	}
	if !os.IsNotExist(err) && err != nil {
		return err
	}
	_, err = New(rootSiaDir, sds.rootDir, sds.wal)
	return err
}

// NewSiaDir creates a new SiaDir and returns a SiaDirSetEntry
func (sds *SiaDirSet) NewSiaDir(siaPath modules.SiaPath) (*SiaDirSetEntry, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	// Check is SiaDir already exists
	exists, err := sds.exists(siaPath)
	if exists {
		return nil, ErrPathOverload
	}
	if !os.IsNotExist(err) && err != nil {
		return nil, err
	}
	sd, err := New(siaPath, sds.rootDir, sds.wal)
	if err != nil {
		return nil, err
	}
	entry := sds.newSiaDirSetEntry(sd)
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadType()
	sds.siaDirMap[siaPath] = entry
	return &SiaDirSetEntry{
		siaDirSetEntry: entry,
		threadUID:      threadUID,
	}, nil
}

// Open returns the siadir from the SiaDirSet for the corresponding key and
// adds the thread to the entry's threadMap. If the siadir is not in memory it
// will load it from disk
func (sds *SiaDirSet) Open(siaPath modules.SiaPath) (*SiaDirSetEntry, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	return sds.open(siaPath)
}

// UpdateMetadata will update the metadata of the SiaDir in memory and on disk
func (sds *SiaDirSet) UpdateMetadata(siaPath modules.SiaPath, metadata Metadata) error {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	exists, err := sds.exists(siaPath)
	if !exists && os.IsNotExist(err) {
		return ErrUnknownPath
	}
	if err != nil {
		return err
	}
	entry, err := sds.open(siaPath)
	if err != nil {
		return err
	}
	defer sds.closeEntry(entry)
	return entry.UpdateMetadata(metadata)
}
