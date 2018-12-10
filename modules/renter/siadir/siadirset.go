package siadir

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

type (
	// SiaDirSet handles the thread management for the SiaDirs on disk and in memory
	SiaDirSet struct {
		rootDir   string
		siaDirMap map[string]*siaDirSetEntry

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
		siaDirMap: make(map[string]*siaDirSetEntry),
		wal:       wal,
	}
}

// close removes the thread from the threadMap. If the length of threadMap count
// is 0 then it will remove the siaDirSetEntry from the SiaDirSet map, which
// will remove it from memory
func (entry *SiaDirSetEntry) close() error {
	if _, ok := entry.threadMap[entry.threadUID]; !ok {
		return ErrUnknownThread
	}
	delete(entry.threadMap, entry.threadUID)
	if len(entry.threadMap) == 0 {
		delete(entry.siaDirSet.siaDirMap, entry.SiaPath())
	}
	return nil
}

// exists checks to see if a SiaDir with the provided siaPath already exists in
// the renter
func (sds *SiaDirSet) exists(siaPath string) (bool, error) {
	// Make sure there are no leading slashes
	siaPath = strings.TrimPrefix(siaPath, "/")
	// Check for SiaDir in Memory
	_, exists := sds.siaDirMap[siaPath]
	if exists {
		return exists, nil
	}
	// Check for SiaDir on disk
	_, err := os.Stat(filepath.Join(sds.rootDir, siaPath+"/"+SiaDirExtension))
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
func (sds *SiaDirSet) open(siaPath string) (*SiaDirSetEntry, error) {
	// Make sure there are no leading slashes
	siaPath = strings.TrimPrefix(siaPath, "/")
	var entry *siaDirSetEntry
	entry, exists := sds.siaDirMap[siaPath]
	if !exists {
		// Try and Load File from disk
		sd, err := LoadSiaDir(sds.rootDir, siaPath, sds.wal)
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

// Close removes the thread from the threadMap. If the length of threadMap count
// is 0 then it will remove the SiaDirSetEntry from the SiaDirSet map, which
// will remove it from memory
func (entry *SiaDirSetEntry) Close() error {
	entry.siaDirSet.mu.Lock()
	defer entry.siaDirSet.mu.Unlock()
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	return entry.close()
}

// Delete deletes the SiaDir that belongs to the siaPath
func (sds *SiaDirSet) Delete(siaPath string) error {
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
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	defer entry.close()
	// Delete SiaDir
	if err := entry.Delete(); err != nil {
		return err
	}
	return nil
}

// Exists checks to see if a file with the provided siaPath already exists in
// the renter
func (sds *SiaDirSet) Exists(siaPath string) (bool, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	return sds.exists(siaPath)
}

// NewSiaDir creates a new SiaDir and returns a SiaDirSetEntry
func (sds *SiaDirSet) NewSiaDir(siaPath string) (*SiaDirSetEntry, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	siaPath = strings.TrimPrefix(siaPath, "/")
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

// Open returns the siafile from the SiaDirSet for the corresponding key and
// adds the thread to the entry's threadMap. If the siafile is not in memory it
// will load it from disk
func (sds *SiaDirSet) Open(siaPath string) (*SiaDirSetEntry, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	return sds.open(siaPath)
}

// UpdateHealth will update the health of the SiaDirSetEntry in memory and on disk
func (sds *SiaDirSet) UpdateHealth(siaPath string, health, stuckHealth float64, lastCheck time.Time) error {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	siaPath = strings.TrimPrefix(siaPath, "/")
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
	defer entry.close()
	return entry.SiaDir.UpdateHealth(health, stuckHealth, lastCheck)
}
