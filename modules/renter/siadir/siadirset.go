package siadir

import (
	"math"
	"os"
	"runtime"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// threadDepth is how deep the threadInfo will track calling files and
	// calling lines
	threadDepth = 3

	// dirListRoutines is the number of goroutines used in DirList to load siadir
	// metadata from disk
	dirListRoutines = 20
)

var (
	// ErrPathOverload is an error when a siadir already exists at that location
	ErrPathOverload = errors.New("a siadir already exists at that location")
	// ErrUnknownPath is an error when a siadir cannot be found with the given path
	ErrUnknownPath = errors.New("no siadir known with that path")
)

type (
	// SiaDirSet handles the thread management for the SiaDirs on disk and in memory
	SiaDirSet struct {
		staticRootDir string
		siaDirMap     map[modules.SiaPath]*siaDirSetEntry

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

	// A RenameDirFunc is a function that can be used to rename a SiaDir. It's
	// passed to the SiaFileSet to rename the direcory after already loaded
	// SiaFiles are locked. A RenameDirFunc is assumed to lock the SiaDirSet and
	// can therefore not be called from a locked SiaDirSet.
	RenameDirFunc func(oldPath, newPath modules.SiaPath) error

	// A DeleteDirFunc is a function that can be used to delete a SiaDir. It's
	// passed to the SiaFileSet to delete the direcory after already loaded
	// SiaFiles are locked. A DeleteDirFunc is assumed to lock the SiaDirSet and
	// can therefore not be called from a locked SiaDirSet.
	DeleteDirFunc func(siaPath modules.SiaPath) error
)

// HealthPercentage returns the health in a more human understandable format out
// of 100%
//
// The percentage is out of 1.25, this is to account for the RepairThreshold of
// 0.25 and assumes that the worst health is 1.5. Since we do not repair until
// the health is worse than the RepairThreshold, a health of 0 - 0.25 is full
// health. Likewise, a health that is greater than 1.25 is essentially 0 health.
func HealthPercentage(health float64) float64 {
	healthPercent := 100 * (1.25 - health)
	if healthPercent > 100 {
		healthPercent = 100
	}
	if healthPercent < 0 {
		healthPercent = 0
	}
	return healthPercent
}

// NewSiaDirSet initializes and returns a SiaDirSet
func NewSiaDirSet(rootDir string, wal *writeaheadlog.WAL) *SiaDirSet {
	return &SiaDirSet{
		staticRootDir: rootDir,
		siaDirMap:     make(map[modules.SiaPath]*siaDirSetEntry),
		wal:           wal,
	}
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
	rootSiaDir := modules.RootSiaPath()
	exists, err := sds.exists(rootSiaDir)
	if exists {
		return nil
	}
	if !os.IsNotExist(err) && err != nil {
		return err
	}
	_, err = New(rootSiaDir, sds.staticRootDir, sds.wal)
	return err
}

// newThreadInfo created a threadInfo entry for the threadMap
func newThreadInfo() threadInfo {
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
	currentEntry := sds.siaDirMap[entry.siaPath]
	if currentEntry != entry.siaDirSetEntry {
		return
	}

	// If there are no more threads that have the current entry open, delete
	// this entry from the set cache.
	if len(currentEntry.threadMap) == 0 {
		delete(sds.siaDirMap, entry.siaPath)
	}
}

// readLockMetadata returns the metadata of the SiaDir at siaPath. NOTE: The
// 'readLock' prefix in this case is used to indicate that it's safe to call
// this method with other 'readLock' methods without locking since is doesn't
// write to any fields. This guarantee can be made by locking sfs.mu and then
// spawning multiple threads which call 'readLock' methods in parallel.
func (sds *SiaDirSet) readLockMetadata(siaPath modules.SiaPath) (Metadata, error) {
	var entry *siaDirSetEntry
	entry, exists := sds.siaDirMap[siaPath]
	if exists {
		// Get metadata from entry.
		return entry.Metadata(), nil
	}
	// Load metadat from disk.
	return callLoadSiaDirMetadata(siaPath.SiaDirMetadataSysPath(sds.staticRootDir), modules.ProdDependencies)
}

// readLockDirInfo returns the Directory Information of the siadir. NOTE: The 'readLock'
// prefix in this case is used to indicate that it's safe to call this method
// with other 'readLock' methods without locking since is doesn't write to any
// fields. This guarantee can be made by locking sfs.mu and then spawning
// multiple threads which call 'readLock' methods in parallel.
func (sds *SiaDirSet) readLockDirInfo(siaPath modules.SiaPath) (modules.DirectoryInfo, error) {
	// Grab the siadir metadata
	metadata, err := sds.readLockMetadata(siaPath)
	if err != nil {
		return modules.DirectoryInfo{}, err
	}
	aggregateMaxHealth := math.Max(metadata.AggregateHealth, metadata.AggregateStuckHealth)
	maxHealth := math.Max(metadata.Health, metadata.StuckHealth)
	return modules.DirectoryInfo{
		// Aggregate Fields
		AggregateHealth:              metadata.AggregateHealth,
		AggregateLastHealthCheckTime: metadata.AggregateLastHealthCheckTime,
		AggregateMaxHealth:           aggregateMaxHealth,
		AggregateMaxHealthPercentage: HealthPercentage(aggregateMaxHealth),
		AggregateMinRedundancy:       metadata.AggregateMinRedundancy,
		AggregateMostRecentModTime:   metadata.AggregateModTime,
		AggregateNumFiles:            metadata.AggregateNumFiles,
		AggregateNumStuckChunks:      metadata.AggregateNumStuckChunks,
		AggregateNumSubDirs:          metadata.AggregateNumSubDirs,
		AggregateSize:                metadata.AggregateSize,
		AggregateStuckHealth:         metadata.AggregateStuckHealth,

		// SiaDir Fields
		Health:              metadata.Health,
		LastHealthCheckTime: metadata.LastHealthCheckTime,
		MaxHealth:           maxHealth,
		MaxHealthPercentage: HealthPercentage(maxHealth),
		MinRedundancy:       metadata.MinRedundancy,
		MostRecentModTime:   metadata.ModTime,
		NumFiles:            metadata.NumFiles,
		NumStuckChunks:      metadata.NumStuckChunks,
		NumSubDirs:          metadata.NumSubDirs,
		SiaPath:             siaPath,
		DirSize:             metadata.Size,
		StuckHealth:         metadata.StuckHealth,
	}, nil
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
	_, err := os.Stat(siaPath.SiaDirMetadataSysPath(sds.staticRootDir))
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
