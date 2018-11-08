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
		SiaFileMap map[string]*SiaFileSetEntry
	}
	// SiaFileSetEntry is responsible for managing a siafile in memory
	SiaFileSetEntry struct {
		counter int
		siaFile *SiaFile
	}
)

// NewSiaFileSet initializes and returns a SiaFileSet
func NewSiaFileSet() *SiaFileSet {
	return &SiaFileSet{
		SiaFileMap: make(map[string]*SiaFileSetEntry),
	}
}

// All returns all the siafiles in the SiaFileSet, this will also increment all
// the counters
//
// TODO - this method should be updated when siafiles are removed from memory.
// Where All() is called from should be looked at to determine if the calling
// method(s) need all siafiles from disk or just the current siafiles in memory
func (sfs *SiaFileSet) All() []*SiaFile {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	var siaFiles []*SiaFile
	for _, entry := range sfs.SiaFileMap {
		entry.counter++
		siaFiles = append(siaFiles, entry.siaFile)
	}
	return siaFiles
}

// Delete removes the entry from the map, decrements the counter, and deletes
// the siafile
func (sfs *SiaFileSet) Delete(key string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	entry, exists := sfs.SiaFileMap[key]
	if !exists {
		return ErrUnknownPath
	}
	err := entry.siaFile.Delete()
	if err != nil {
		return err
	}
	entry.counter--
	if entry.counter < 0 {
		build.Critical("siaFileEntry counter less than zero")
	}
	if entry.counter == 0 {
		delete(sfs.SiaFileMap, key)
	}
	return nil
}

// Get returns the siafile from the SiaFileSet for the corresponding key and
// increments the counter
func (sfs *SiaFileSet) Get(key string) (*SiaFile, bool) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	entry, exists := sfs.SiaFileMap[key]
	if !exists {
		return nil, exists
	}
	entry.counter++
	return entry.siaFile, exists
}

// LoadSiaFile loads a SiaFile from disk, adds it to the SiaFileSet, increments
// the counter, and returns the SiaFile. Since this method returns the siafile,
// wherever LoadSiaFile is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the counter never
// being decremented after load
//
// TODO - Currently this method is only called on load. Once lazy loading is
// enabled and files are being removed from memory when not in use, a check
// should be added before calling this method to confirm that the file is not
// already in memory
func (sfs *SiaFileSet) LoadSiaFile(path string, wal *writeaheadlog.WAL) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, err := LoadSiaFile(path, wal)
	if err != nil {
		return nil, err
	}
	sfs.SiaFileMap[sf.SiaPath()] = &SiaFileSetEntry{
		counter: 1,
		siaFile: sf,
	}
	return sf, nil
}

// NewFromFileData creates a new SiaFile from a FileData object that was
// previously created from a legacy file. Since this method returns the siafile,
// wherever NewFromFileData is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the counter never
// being decremented after creating the SiaFile
func (sfs *SiaFileSet) NewFromFileData(fd FileData) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, err := NewFromFileData(fd)
	if err != nil {
		return nil, err
	}
	sfs.SiaFileMap[sf.SiaPath()] = &SiaFileSetEntry{
		counter: 1,
		siaFile: sf,
	}
	return sf, nil
}

// NewSiaFile create a new SiaFile, adds it to the SiaFileSet, increments the
// counter, and returns the SiaFile. Since this method returns the siafile,
// wherever NewSiaFile is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the counter never
// being decremented after creating the SiaFile
func (sfs *SiaFileSet) NewSiaFile(siaFilePath, siaPath, source string, wal *writeaheadlog.WAL, erasureCode modules.ErasureCoder, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, err := New(siaFilePath, siaPath, source, wal, erasureCode, masterKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	sfs.SiaFileMap[sf.SiaPath()] = &SiaFileSetEntry{
		counter: 1,
		siaFile: sf,
	}
	return sf, nil
}

// Remove deletes the entry from the map
func (sfs *SiaFileSet) Remove(key string) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	delete(sfs.SiaFileMap, key)
}

// Rename changes the key corresponding to the siafile in the SiaFileSet and
// then calls Rename on the siafile
func (sfs *SiaFileSet) Rename(oldKey, newKey, newPath string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	entry, exists := sfs.SiaFileMap[oldKey]
	if !exists {
		return ErrUnknownPath
	}
	_, exists = sfs.SiaFileMap[newKey]
	if exists {
		return ErrPathOverload
	}
	err := entry.siaFile.Rename(newKey, newPath)
	if err != nil {
		return err
	}
	sfs.SiaFileMap[newKey] = entry
	delete(sfs.SiaFileMap, oldKey)
	return nil
}

// Return returns the siafile to the SiaFileSet by decrementing the counter. The
// siafile should have been acquired by Get()
func (sfs *SiaFileSet) Return(sf *SiaFile) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	entry, exists := sfs.SiaFileMap[sf.SiaPath()]
	if !exists {
		build.Critical("siaFileEntry doesn't exist")
		return
	}
	entry.counter--
	if entry.counter < 0 {
		build.Critical("siaFileEntry counter less than zero")
		return
	}
	if entry.counter == 0 {
		// TODO - when ready to remove files from memory, remove files that are
		// no longer being used by any of the active threads
		//
		// delete(sfs.SiaFileMap, sf.SiaPath())
	}
}
