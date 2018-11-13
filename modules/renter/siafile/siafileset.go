package siafile

import (
	"os"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// The SiaFileSet structure helps track the number of threads using a siafile
// and will enable features like caching and lazy loading to improve start up
// times and reduce memory usage. SiaFile methods such as New, Delete, Rename,
// etc should be called through the SiaFileSet to maintain atomic transactions.
//
// TODO - implement caching
//
// TODO - implement lazy loading and enable removing SiaFileEntries from the map

type (
	// SiaFileSet is a helper struct responsible for managing the renter's
	// siafiles in memory
	SiaFileSet struct {
		mu         sync.Mutex
		SiaFileMap map[string]*SiaFile
	}
)

// NewSiaFileSet initializes and returns a SiaFileSet
func NewSiaFileSet() *SiaFileSet {
	return &SiaFileSet{
		SiaFileMap: make(map[string]*SiaFile),
	}
}

// All returns all the siafiles in the SiaFileSet, this will also increment all
// the threadCounts
//
// Note: This is currently only needed for the Files endpoint. This is an
// expensive call so it should be avoided unless absolutely necessary
func (sfs *SiaFileSet) All() []*SiaFile {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	var siaFiles []*SiaFile
	for _, sf := range sfs.SiaFileMap {
		sf.mu.Lock()
		sf.threadCount++
		sf.mu.Unlock()
		siaFiles = append(siaFiles, sf)
	}
	return siaFiles
}

// delete removes the entry from the map, decrements the threadCount, and deletes
// the siafile
func (sfs *SiaFileSet) delete(key string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, exists := sfs.SiaFileMap[key]
	if !exists {
		return ErrUnknownPath
	}
	sf.threadCount--
	if sf.threadCount < 0 {
		build.Critical("siafile threadCount less than zero")
	}
	if sf.threadCount == 0 {
		delete(sfs.SiaFileMap, key)
	}
	return nil
}

// LoadSiaFile loads a SiaFile from disk, adds it to the SiaFileSet, increments
// the threadCount, and returns the SiaFile. Since this method returns the siafile,
// wherever LoadSiaFile is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the threadCount never
// being decremented after load
func (sfs *SiaFileSet) LoadSiaFile(siapath, path string, wal *writeaheadlog.WAL) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, ok := sfs.SiaFileMap[siapath]
	if ok {
		return sf, nil
	}
	sf, err := LoadSiaFile(path, wal)
	if err != nil {
		return nil, err
	}
	sf.mu.Lock()
	sf.threadCount++
	sf.mu.Unlock()
	sfs.SiaFileMap[sf.SiaPath()] = sf
	return sf, nil
}

// NewSiaFile create a new SiaFile, adds it to the SiaFileSet, increments the
// threadCount, and returns the SiaFile. Since this method returns the siafile,
// wherever NewSiaFile is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the threadCount never
// being decremented after creating the SiaFile
func (sfs *SiaFileSet) NewSiaFile(siaFilePath, siaPath, source string, wal *writeaheadlog.WAL, erasureCode modules.ErasureCoder, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, err := New(siaFilePath, siaPath, source, wal, erasureCode, masterKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	sf.mu.Lock()
	sf.SiaFileSet = sfs
	sf.threadCount++
	sf.mu.Unlock()
	sfs.SiaFileMap[sf.SiaPath()] = sf
	return sf, nil
}

// Open returns the siafile from the SiaFileSet for the corresponding key and
// increments the threadCount
//
// TODO - when files are removed from memory this method should be updated to
// either return the siafile in memory or load from disk
func (sfs *SiaFileSet) Open(key string) (*SiaFile, bool) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, exists := sfs.SiaFileMap[key]
	if !exists {
		return nil, exists
	}
	sf.mu.Lock()
	sf.threadCount++
	sf.mu.Unlock()
	return sf, exists
}

// Remove deletes the entry from the map
func (sfs *SiaFileSet) Remove(key string) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	delete(sfs.SiaFileMap, key)
}

// rename changes the key corresponding to the siafile in the SiaFileSet
func (sfs *SiaFileSet) rename(oldKey, newKey string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, exists := sfs.SiaFileMap[oldKey]
	if !exists {
		return ErrUnknownPath
	}
	_, exists = sfs.SiaFileMap[newKey]
	if exists {
		return ErrPathOverload
	}
	sfs.SiaFileMap[newKey] = sf
	delete(sfs.SiaFileMap, oldKey)
	return nil
}

// Close returns the siafile to the SiaFileSet and decrementing the threadCount.
// If the threadCount is 0 then it will remove the file from memory
//
// NOTE: The siafile should have been acquired by Get()
func (sfs *SiaFileSet) Close(sf *SiaFile) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, exists := sfs.SiaFileMap[sf.SiaPath()]
	if !exists {
		build.Critical("siafile doesn't exist")
		return
	}
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.threadCount--
	if sf.threadCount < 0 {
		build.Critical("siafile threadCount less than zero")
		return
	}
	if sf.threadCount == 0 {
		// TODO - when ready to remove files from memory, remove files that are
		// no longer being used by any of the active threads
		//
		// delete(sfs.SiaFileMap, sf.SiaPath())
	}
}
