package siafile

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/karrick/godirwalk"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
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
		staticSiaFileDir string
		siaFileMap       map[SiafileUID]*siaFileSetEntry
		siapathToUID     map[modules.SiaPath]SiafileUID

		// utilities
		mu  sync.Mutex
		wal *writeaheadlog.WAL
	}

	// siaFileSetEntry contains information about the threads accessing the
	// SiaFile and references to the SiaFile and the SiaFileSet
	siaFileSetEntry struct {
		*SiaFile
		staticSiaFileSet *SiaFileSet

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
		staticSiaFileDir: filesDir,
		siaFileMap:       make(map[SiafileUID]*siaFileSetEntry),
		siapathToUID:     make(map[modules.SiaPath]SiafileUID),
		wal:              wal,
	}
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
// threadMap of the SiaFileSetEntry
func randomThreadUID() uint64 {
	return fastrand.Uint64n(math.MaxUint64)
}

// CopyEntry returns a copy of the SiaFileSetEntry
func (entry *SiaFileSetEntry) CopyEntry() (*SiaFileSetEntry, error) {
	// Grab siafile set lock
	entry.staticSiaFileSet.mu.Lock()
	defer entry.staticSiaFileSet.mu.Unlock()
	// Check if entry is deleted, we don't want to make a copy of a deleted
	// entry
	if entry.Deleted() {
		return nil, errors.New("can't make copy of deleted siafile entry")
	}
	// Sanity Check that the entry is currently in the SiaFileSet
	_, exists := entry.staticSiaFileSet.siaFileMap[entry.UID()]
	if !exists {
		return nil, errors.New("siafile entry not found in siafileset")
	}
	// Create copy
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadInfo()
	copy := &SiaFileSetEntry{
		siaFileSetEntry: entry.siaFileSetEntry,
		threadUID:       threadUID,
	}
	// sanity check that copy isn't nil
	if copy == nil {
		build.Critical("nil copy is about to be returned from CopyEntry")
		return nil, errors.New("unable to create copy")
	}
	return copy, nil
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
	entry.staticSiaFileSet.mu.Lock()
	entry.staticSiaFileSet.closeEntry(entry)
	entry.staticSiaFileSet.mu.Unlock()
	return nil
}

// FileSet returns the SiaFileSet of the entry.
func (entry *SiaFileSetEntry) FileSet() *SiaFileSet {
	return entry.staticSiaFileSet
}

// SiaPath returns the siapath of a siafile.
func (sfs *SiaFileSet) SiaPath(entry *SiaFileSetEntry) modules.SiaPath {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.siaPath(entry.siaFileSetEntry)
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

	if _, exists := entry.threadMap[entry.threadUID]; !exists {
		build.Critical("threaduid doesn't exist in threadMap: ", entry.SiaFilePath(), len(entry.threadMap))
	}
	delete(entry.threadMap, entry.threadUID)

	// The entry that exists in the siafile set may not be the same as the entry
	// that is being closed, this can happen if there was a rename or a delete
	// and then a new/different file was uploaded with the same siapath.
	//
	// If they are not the same entry, there is nothing more to do.
	currentEntry := sfs.siaFileMap[entry.UID()]
	if currentEntry != entry.siaFileSetEntry {
		return
	}

	// If there are no more threads that have the current entry open, delete this
	// entry from the set cache and save the file to make sure all changes are
	// persisted.
	if len(currentEntry.threadMap) == 0 {
		delete(sfs.siaFileMap, entry.UID())
		delete(sfs.siapathToUID, sfs.siaPath(entry.siaFileSetEntry))
	}
}

// createAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func (sfs *SiaFileSet) createAndApplyTransaction(updates ...writeaheadlog.Update) error {
	if len(updates) == 0 {
		return nil
	}
	// Create the writeaheadlog transaction.
	txn, err := sfs.wal.NewTransaction(updates)
	if err != nil {
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Apply the updates.
	if err := applyUpdates(modules.ProdDependencies, updates...); err != nil {
		return errors.AddContext(err, "failed to apply updates")
	}
	// Updates are applied. Let the writeaheadlog know.
	if err := txn.SignalUpdatesApplied(); err != nil {
		return errors.AddContext(err, "failed to signal that updates are applied")
	}
	return nil
}

// delete deletes the SiaFileSetEntry's SiaFile
func (sfs *SiaFileSet) delete(siaPath modules.SiaPath) error {
	// Fetch the corresponding siafile and call delete.
	//
	// NOTE: since we are just accessing the entry directly from the map while
	// holding the SiaFileSet lock we are not creating a new threadUID and
	// therefore do not need to call close on this entry
	entry, _, exists := sfs.siaPathToEntryAndUID(siaPath)
	if !exists {
		// Check if the file exists on disk
		siaFilePath := siaPath.SiaFileSysPath(sfs.staticSiaFileDir)
		_, err := os.Stat(siaFilePath)
		if os.IsNotExist(err) {
			return ErrUnknownPath
		}
		// If the entry does not exist then we want to just remove the entry from disk
		// without loading it from disk to avoid errors due to corrupt siafiles
		update := createDeleteUpdate(siaFilePath)
		return sfs.createAndApplyTransaction(update)
	}

	// Delete SiaFile
	err := entry.Delete()
	if err != nil {
		return err
	}
	// Remove the siafile from the set maps so that other threads can't find
	// it.
	delete(sfs.siaFileMap, entry.UID())
	delete(sfs.siapathToUID, sfs.siaPath(entry))
	return nil
}

// siaPath is a convenience wrapper around FromSysPath. Since the files are
// loaded from disk, the siapaths should always be correct. It's argument is
// also an entry instead of the path and dir.
func (sfs *SiaFileSet) siaPath(entry *siaFileSetEntry) (sp modules.SiaPath) {
	if err := sp.FromSysPath(entry.SiaFilePath(), sfs.staticSiaFileDir); err != nil {
		build.Critical("Siapath of entry is corrupted. This shouldn't happen within the SiaFileSet", err)
	}
	return
}

// siaPathToEntryAndUID translates a siaPath to a siaFileSetEntry and
// SiafileUID while also sanity checking siapathToUID and siaFileMap for
// consistency.
func (sfs *SiaFileSet) siaPathToEntryAndUID(siaPath modules.SiaPath) (*siaFileSetEntry, SiafileUID, bool) {
	uid, exists := sfs.siapathToUID[siaPath]
	if !exists {
		return nil, "", false
	}
	entry, exists2 := sfs.siaFileMap[uid]
	if !exists2 {
		build.Critical("siapathToUID and siaFileMap are inconsistent")
		delete(sfs.siapathToUID, siaPath)
		return nil, "", false
	}
	return entry, uid, exists
}

// exists checks to see if a file with the provided siaPath already exists in
// the renter
func (sfs *SiaFileSet) exists(siaPath modules.SiaPath) bool {
	// Check for file in Memory
	_, UID, exists1 := sfs.siaPathToEntryAndUID(siaPath)
	_, exists2 := sfs.siaFileMap[UID]
	// Sanity check that the maps are consistent
	if exists1 != exists2 {
		build.Critical("SiaFileSet in memory maps are inconsistent", exists1, exists2)
	}
	// Since maps are consistent, only need to check one
	if exists1 {
		return true
	}
	// Check for file on disk
	_, err := os.Stat(siaPath.SiaFileSysPath(sfs.staticSiaFileDir))
	return !os.IsNotExist(err)
}

// readLockFileInfo returns information on a siafile. As a performance
// optimization, the fileInfo takes the maps returned by
// renter.managedContractUtilityMaps for many files at once.
func (sfs *SiaFileSet) readLockCachedFileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	// Get the file's metadata and its contracts
	md, err := sfs.readLockMetadata(siaPath)
	if err != nil {
		return modules.FileInfo{}, err
	}

	// Build the FileInfo
	var onDisk bool
	localPath := md.LocalPath
	if localPath != "" {
		_, err = os.Stat(localPath)
		onDisk = err == nil
	}
	fileInfo := modules.FileInfo{
		AccessTime:       md.AccessTime,
		Available:        md.CachedRedundancy >= 1,
		ChangeTime:       md.ChangeTime,
		CipherType:       md.StaticMasterKeyType.String(),
		CreateTime:       md.CreateTime,
		Expiration:       md.CachedExpiration,
		Filesize:         uint64(md.FileSize),
		Health:           md.CachedHealth,
		LocalPath:        localPath,
		MaxHealth:        math.Max(md.CachedHealth, md.CachedStuckHealth),
		MaxHealthPercent: healthPercentage(md.CachedHealth, md.CachedStuckHealth, md.staticErasureCode),
		ModTime:          md.ModTime,
		NumStuckChunks:   md.NumStuckChunks,
		OnDisk:           onDisk,
		Recoverable:      onDisk || md.CachedRedundancy >= 1,
		Redundancy:       md.CachedRedundancy,
		Renewing:         true,
		SiaPath:          siaPath,
		Stuck:            md.NumStuckChunks > 0,
		StuckHealth:      md.CachedStuckHealth,
		UploadedBytes:    md.CachedUploadedBytes,
		UploadProgress:   md.CachedUploadProgress,
	}
	return fileInfo, nil
}

// newSiaFileSetEntry initializes and returns a siaFileSetEntry
func (sfs *SiaFileSet) newSiaFileSetEntry(sf *SiaFile) (*siaFileSetEntry, error) {
	threads := make(map[uint64]threadInfo)
	entry := &siaFileSetEntry{
		SiaFile:          sf,
		staticSiaFileSet: sfs,
		threadMap:        threads,
	}
	// Sanity check that the UID is in fact unique.
	if _, exists := sfs.siaFileMap[entry.UID()]; exists {
		err := errors.New("siafile was already loaded")
		build.Critical(err)
		return nil, err
	}
	// Sanity check that there isn't a naming conflict
	if _, exists := sfs.siapathToUID[sfs.siaPath(entry)]; exists {
		err := errors.New("siapath already in map")
		build.Critical(err)
		return nil, err
	}
	// Add entry to siaFileMap and siapathToUID map.
	sfs.siaFileMap[entry.UID()] = entry
	sfs.siapathToUID[sfs.siaPath(entry)] = entry.UID()
	return entry, nil
}

// open will return the siaFileSetEntry in memory or load it from disk
func (sfs *SiaFileSet) open(siaPath modules.SiaPath) (*SiaFileSetEntry, error) {
	var entry *siaFileSetEntry
	var exists bool
	entry, _, exists = sfs.siaPathToEntryAndUID(siaPath)
	if !exists {
		// Try and Load File from disk
		sf, err := LoadSiaFile(siaPath.SiaFileSysPath(sfs.staticSiaFileDir), sfs.wal)
		if os.IsNotExist(err) {
			return nil, ErrUnknownPath
		}
		if err != nil {
			return nil, err
		}
		// Check for duplicate uid.
		if conflictingEntry, exists := sfs.siaFileMap[sf.UID()]; exists {
			err := fmt.Errorf("%v and %v share the same UID", sfs.siaPath(conflictingEntry), siaPath)
			build.Critical(err)
			return nil, err
		}
		entry, err = sfs.newSiaFileSetEntry(sf)
		if err != nil {
			return nil, err
		}
	}
	if entry.Deleted() {
		return nil, ErrUnknownPath
	}
	threadUID := randomThreadUID()
	entry.threadMapMu.Lock()
	defer entry.threadMapMu.Unlock()
	entry.threadMap[threadUID] = newThreadInfo()
	return &SiaFileSetEntry{
		siaFileSetEntry: entry,
		threadUID:       threadUID,
	}, nil
}

// readLockMetadata returns the metadata of the SiaFile at siaPath. NOTE: The
// 'readLock' prefix in this case is used to indicate that it's safe to call
// this method with other 'readLock' methods without locking since is doesn't
// write to any fields. This guarantee can be made by locking sfs.mu and then
// spawning multiple threads which call 'readLock' methods in parallel.
func (sfs *SiaFileSet) readLockMetadata(siaPath modules.SiaPath) (Metadata, error) {
	var entry *siaFileSetEntry
	entry, _, exists := sfs.siaPathToEntryAndUID(siaPath)
	if exists {
		// Get metadata from entry.
		return entry.Metadata(), nil
	}
	// Try and Load Metadata from disk
	md, err := LoadSiaFileMetadata(siaPath.SiaFileSysPath(sfs.staticSiaFileDir))
	if os.IsNotExist(err) {
		return Metadata{}, ErrUnknownPath
	}
	if err != nil {
		return Metadata{}, err
	}
	return md, nil
}

// AddExistingSiaFile adds an existing SiaFile to the set and stores it on disk.
// If the exact same file already exists, this is a no-op. If a file already
// exists with a different UID, the UID will be updated and a unique path will
// be chosen. If no file exists, the UID will be updated but the path remains
// the same.
func (sfs *SiaFileSet) AddExistingSiaFile(sf *SiaFile) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.addExistingSiaFile(sf, 0)
}

// addExistingSiaFile adds an existing SiaFile to the set and stores it on disk.
// If the exact same file already exists, this is a no-op. If a file already
// exists with a different UID, the UID will be updated and a unique path will
// be chosen. If no file exists, the UID will be updated but the path remains
// the same.
func (sfs *SiaFileSet) addExistingSiaFile(sf *SiaFile, suffix uint) error {
	// Check if a file with that path exists already.
	var siaPath modules.SiaPath
	err := siaPath.LoadSysPath(sfs.staticSiaFileDir, sf.SiaFilePath())
	if err != nil {
		return err
	}
	var oldFile *SiaFileSetEntry
	if suffix == 0 {
		oldFile, err = sfs.open(siaPath)
	} else {
		oldFile, err = sfs.open(siaPath.AddSuffix(suffix))
	}
	exists := err == nil
	if exists {
		defer sfs.closeEntry(oldFile)
	}
	if err != nil && err != ErrUnknownPath {
		return err
	}
	// If it doesn't exist, update the UID, the path if necessary and save the
	// file.
	if !exists {
		sf.UpdateUniqueID()
		if suffix > 0 {
			siaFilePath := siaPath.AddSuffix(suffix).SiaFileSysPath(sfs.staticSiaFileDir)
			sf.SetSiaFilePath(siaFilePath)
		}
		return sf.Save()
	}
	// If it exists and the UID matches too, skip the file.
	if sf.UID() == oldFile.UID() {
		return nil
	}
	// If it exists and the UIDs don't match, increment the suffix and try again.
	return sfs.addExistingSiaFile(sf, suffix+1)
}

// Delete deletes the SiaFileSetEntry's SiaFile
func (sfs *SiaFileSet) Delete(siaPath modules.SiaPath) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.delete(siaPath)
}

// Exists checks to see if a file with the provided siaPath already exists in
// the renter
func (sfs *SiaFileSet) Exists(siaPath modules.SiaPath) bool {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.exists(siaPath)
}

// FileInfo returns information on a siafile. As a performance optimization, the
// fileInfo takes the maps returned by renter.managedContractUtilityMaps for
// many files at once.
func (sfs *SiaFileSet) FileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	entry, err := sfs.Open(siaPath)
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
	health, stuckHealth, numStuckChunks := entry.Health(offline, goodForRenew)
	redundancy := entry.Redundancy(offline, goodForRenew)
	uploadProgress, uploadedBytes := entry.UploadProgressAndBytes()
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
		MaxHealthPercent: healthPercentage(health, stuckHealth, entry.ErasureCode()),
		ModTime:          entry.ModTime(),
		NumStuckChunks:   numStuckChunks,
		OnDisk:           onDisk,
		Recoverable:      onDisk || redundancy >= 1,
		Redundancy:       redundancy,
		Renewing:         true,
		SiaPath:          siaPath,
		Stuck:            numStuckChunks > 0,
		StuckHealth:      stuckHealth,
		UploadedBytes:    uploadedBytes,
		UploadProgress:   uploadProgress,
	}
	return fileInfo, nil
}

// CachedFileInfo returns a modules.FileInfo for a given file like FileInfo but
// instead of computing redundancy, health etc. it uses cached values.
func (sfs *SiaFileSet) CachedFileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.readLockCachedFileInfo(siaPath, offline, goodForRenew, contracts)
}

// FileList returns all of the files that the renter has in the folder specified
// by siaPath. If cached is true, this method will used cached values for
// health, redundancy etc.
func (sfs *SiaFileSet) FileList(siaPath modules.SiaPath, recursive, cached bool, offlineMap map[string]bool, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) ([]modules.FileInfo, error) {
	// Guarantee that no other thread is writing to sfs. This is only necessary
	// when 'cached' is true since it allows us to call 'readLockCachedFileInfo'.
	// Otherwise we need to hold the lock for every call to 'fileInfo' anyway.
	if cached {
		sfs.mu.Lock()
		defer sfs.mu.Unlock()
	}
	// Declare a worker method to spawn workers which keep loading files from disk
	// until the loadChan is closed.
	fileList := []modules.FileInfo{}
	var fileListMu sync.Mutex
	loadChan := make(chan string)
	worker := func() {
		for path := range loadChan {
			// Load the Siafile.
			var siaPath modules.SiaPath
			if err := siaPath.LoadSysPath(sfs.staticSiaFileDir, path); err != nil {
				continue
			}
			var file modules.FileInfo
			var err error
			if cached {
				file, err = sfs.readLockCachedFileInfo(siaPath, offlineMap, goodForRenewMap, contractsMap)
			} else {
				// It is ok to call an Exported method here because we only
				// acquire the siaFileSet lock if we are requesting the cached
				// values
				file, err = sfs.FileInfo(siaPath, offlineMap, goodForRenewMap, contractsMap)
			}
			if os.IsNotExist(err) || err == ErrUnknownPath {
				continue
			}
			if err != nil {
				continue
			}
			fileListMu.Lock()
			fileList = append(fileList, file)
			fileListMu.Unlock()
		}
	}
	// spin up some threads
	var wg sync.WaitGroup
	for i := 0; i < fileListRoutines; i++ {
		wg.Add(1)
		go func() {
			worker()
			wg.Done()
		}()
	}
	// Walk over the whole tree if recursive is specified.
	folder := siaPath.SiaDirSysPath(sfs.staticSiaFileDir)
	if recursive {
		err := godirwalk.Walk(folder, &godirwalk.Options{
			Unsorted: true,
			Callback: func(path string, info *godirwalk.Dirent) error {
				// Skip folders and non-sia files.
				if info.IsDir() || filepath.Ext(path) != modules.SiaFileExtension {
					return nil
				}
				loadChan <- path
				return nil
			},
		})
		if err != nil {
			return nil, err
		}
	} else {
		fis, err := ioutil.ReadDir(folder)
		if err != nil {
			return nil, err
		}
		for _, info := range fis {
			if info.IsDir() || filepath.Ext(info.Name()) != modules.SiaFileExtension {
				continue
			}
			loadChan <- filepath.Join(folder, info.Name())
		}
	}
	close(loadChan)
	wg.Wait()
	return fileList, nil
}

// NewSiaFile create a new SiaFile, adds it to the SiaFileSet, adds the thread
// to the threadMap, and returns the SiaFileSetEntry. Since this method returns
// the SiaFileSetEntry, wherever NewSiaFile is called there should be a Close
// called on the SiaFileSetEntry to avoid the file being stuck in memory due the
// thread never being removed from the threadMap
func (sfs *SiaFileSet) NewSiaFile(up modules.FileUploadParams, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Check is SiaFile already exists
	exists := sfs.exists(up.SiaPath)
	if exists && !up.Force {
		return nil, ErrPathOverload
	}
	// Make sure there are no leading slashes
	siaFilePath := up.SiaPath.SiaFileSysPath(sfs.staticSiaFileDir)
	sf, err := New(up.SiaPath, siaFilePath, up.Source, sfs.wal, up.ErasureCode, masterKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	entry, err := sfs.newSiaFileSetEntry(sf)
	if err != nil {
		return nil, err
	}
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadInfo()
	return &SiaFileSetEntry{
		siaFileSetEntry: entry,
		threadUID:       threadUID,
	}, nil
}

// Open returns the siafile from the SiaFileSet for the corresponding key and
// adds the thread to the entry's threadMap. If the siafile is not in memory it
// will load it from disk
func (sfs *SiaFileSet) Open(siaPath modules.SiaPath) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.open(siaPath)
}

// Rename will move a siafile from one path to a new path. Existing entries that
// are already open at the old path will continue to be valid.
func (sfs *SiaFileSet) Rename(siaPath, newSiaPath modules.SiaPath) error {
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
	sfs.siapathToUID[newSiaPath] = entry.UID()
	delete(sfs.siapathToUID, siaPath)

	// Update the siafile to have a new name.
	return entry.Rename(newSiaPath, newSiaPath.SiaFileSysPath(sfs.staticSiaFileDir))
}

// healthPercentage returns the health in a more human understandable format out
// of 100%
func healthPercentage(h, sh float64, ec modules.ErasureCoder) float64 {
	health := math.Max(h, sh)
	dataPieces := ec.MinPieces()
	parityPieces := ec.NumPieces() - dataPieces
	worstHealth := 1 + float64(dataPieces)/float64(parityPieces)
	return 100 * ((worstHealth - health) / worstHealth)
}

// DeleteDir deletes a siadir and all the siadirs and siafiles within it
// recursively.
func (sfs *SiaFileSet) DeleteDir(siaPath modules.SiaPath, deleteDir siadir.DeleteDirFunc) error {
	// Prevent new files from being opened.
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Lock all files within the dir.
	var lockedFiles []*siaFileSetEntry
	defer func() {
		for _, entry := range lockedFiles {
			entry.mu.Unlock()
		}
	}()
	for sp := range sfs.siapathToUID {
		entry, _, exists := sfs.siaPathToEntryAndUID(sp)
		if !exists {
			continue
		}
		if strings.HasPrefix(sp.String(), siaPath.String()) {
			entry.mu.Lock()
			lockedFiles = append(lockedFiles, entry)
		}
	}
	// Delete the dir using the provided delete function.
	if err := deleteDir(siaPath); err != nil {
		return errors.AddContext(err, "failed to delete dir")
	}
	// Delete was successful. Delete the siafiles in memory before they are being
	// unlocked again.
	for _, entry := range lockedFiles {
		// Get siaPath.
		var sp modules.SiaPath
		if err := sp.LoadSysPath(sfs.staticSiaFileDir, entry.siaFilePath); err != nil {
			build.Critical("Getting the siaPath shouldn't fail")
			continue
		}
		// Mark the file as deleted. It will be closed automatically by the last
		// thread calling 'Close' on it.
		entry.deleted = true
	}
	return nil
}

// RenameDir renames a siadir and all the siadirs and siafiles within it
// recursively.
func (sfs *SiaFileSet) RenameDir(oldPath, newPath modules.SiaPath, rename siadir.RenameDirFunc) error {
	if oldPath.Equals(modules.RootSiaPath()) {
		return errors.New("can't rename root dir")
	}
	if oldPath.Equals(newPath) {
		return nil // nothing to do
	}
	if strings.HasPrefix(newPath.String(), oldPath.String()) {
		return errors.New("can't rename folder into itself")
	}
	// Prevent new files from being opened.
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	var lockedFiles []*siaFileSetEntry
	defer func() {
		for _, entry := range lockedFiles {
			entry.mu.Unlock()
		}
	}()
	// Lock all files within the old dir.
	for siaPath := range sfs.siapathToUID {
		entry, _, exists := sfs.siaPathToEntryAndUID(siaPath)
		if !exists {
			continue
		}
		if strings.HasPrefix(siaPath.String(), oldPath.String()) {
			entry.mu.Lock()
			lockedFiles = append(lockedFiles, entry)
		}
	}
	// Rename the dir using the provided rename function.
	if err := rename(oldPath, newPath); err != nil {
		return errors.AddContext(err, "failed to rename dir")
	}
	// Rename was successful. Rename the siafiles in memory before they are being
	// unlocked again.
	for _, entry := range lockedFiles {
		// Get old SiaPath.
		var oldSiaPath modules.SiaPath
		if err := oldSiaPath.LoadSysPath(sfs.staticSiaFileDir, entry.siaFilePath); err != nil {
			build.Critical("Getting the siaPath shouldn't fail")
			continue
		}
		// Rebase path to new folder.
		sp, err := oldSiaPath.Rebase(oldPath, newPath)
		if err != nil {
			build.Critical("Rebasing siapaths shouldn't fail")
			continue
		}
		// Update the siafilepath of the entry and the siafileToUIDMap.
		delete(sfs.siapathToUID, oldSiaPath)
		sfs.siapathToUID[sp] = entry.staticMetadata.UniqueID
		entry.siaFilePath = sp.SiaFileSysPath(sfs.staticSiaFileDir)
	}
	return nil
}
