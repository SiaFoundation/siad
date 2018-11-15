package siafile

import (
	"os"
	"path/filepath"
	"strings"
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

// All returns all the siafiles in the renter by either returning them from the
// SiaFileSet of reading them from disk, this will also add the threadType to
// all the SiaFile threadMaps and increment the thread type counters.
//
// Note: This is currently only needed for the Files endpoint. This is an
// expensive call so it should be avoided unless absolutely necessary
func (sfs *SiaFileSet) All(dir string, threadType ThreadType, wal *writeaheadlog.WAL) ([]*SiaFile, error) {
	var siaFiles []*SiaFile
	// Recursively load all files found in renter directory. Errors
	// are not considered fatal and are ignored.
	return siaFiles, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
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
		sf, err := sfs.Open(siaPath, dir, SiaFileLoadThread, wal)
		if err != nil {
			return nil
		}
		siaFiles = append(siaFiles, sf)
		return nil
	})
}

// delete removes the SiaFile from the map
func (sfs *SiaFileSet) delete(key string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	_, exists := sfs.SiaFileMap[key]
	if !exists {
		return ErrUnknownPath
	}
	delete(sfs.SiaFileMap, key)
	return nil
}

// NewSiaFile create a new SiaFile, adds it to the SiaFileSet, increments the
// threadCount, and returns the SiaFile. Since this method returns the siafile,
// wherever NewSiaFile is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the threadCount never
// being decremented after creating the SiaFile
func (sfs *SiaFileSet) NewSiaFile(siaFilePath, siaPath, source string, wal *writeaheadlog.WAL, erasureCode modules.ErasureCoder, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode, threadType ThreadType) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, err := New(siaFilePath, siaPath, source, wal, erasureCode, masterKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	sf.mu.Lock()
	if _, ok := sf.threadMap[threadType]; !ok {
		sf.threadMap[threadType] = 0
	}
	sf.threadMap[threadType]++
	sf.SiaFileSet = sfs
	sf.mu.Unlock()
	sfs.SiaFileMap[sf.SiaPath()] = sf
	return sf, nil
}

// Open returns the siafile from the SiaFileSet for the corresponding key and
// increments the threadCount. If the siafile is not in memory it will load it
// from disk
func (sfs *SiaFileSet) Open(siaPath, filesDir string, threadType ThreadType, wal *writeaheadlog.WAL) (*SiaFile, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Check for file in Memory
	var sf *SiaFile
	var err error
	sf, exists := sfs.SiaFileMap[siaPath]
	if !exists {
		// Try and Load File from disk
		sf, err = LoadSiaFile(filepath.Join(filesDir, siaPath+ShareExtension), wal)
		if err != nil {
			return nil, err
		}
		sfs.SiaFileMap[sf.SiaPath()] = sf
		sf.mu.Lock()
		sf.SiaFileSet = sfs
		sf.mu.Unlock()
	}
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if _, ok := sf.threadMap[threadType]; !ok {
		sf.threadMap[threadType] = 0
	}
	sf.threadMap[threadType]++
	return sf, nil
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

// Close returns the siafile to the SiaFileSet and decrementing the threadType
// count. If the threadType count is 0 then it will remove the file from memory
func (sfs *SiaFileSet) Close(sf *SiaFile, threadType ThreadType) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, exists := sfs.SiaFileMap[sf.SiaPath()]
	if !exists {
		build.Critical("siafile doesn't exist")
		return ErrUnknownPath
	}
	siaPath := sf.SiaPath()
	sf.mu.Lock()
	defer sf.mu.Unlock()
	if _, ok := sf.threadMap[threadType]; !ok {
		return ErrUnknownThread
	}
	sf.threadMap[threadType]--
	if sf.threadMap[threadType] < 0 {
		build.Critical("threadType count should never be less than zero")
	}
	if sf.threadMap[threadType] > 0 {
		return nil
	}
	if sf.threadMap[threadType] == 0 {
		delete(sf.threadMap, threadType)
	}
	if len(sf.threadMap) == 0 {
		delete(sfs.SiaFileMap, siaPath)
	}
	return nil
}
