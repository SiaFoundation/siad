package siadir

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// SiaDirExtension is the name of the metadata file for the sia directory
	SiaDirExtension = ".siadir"

	// DefaultDirHealth is the default health for the directory and the fall
	// back value when there is an error. This is to protect against falsely
	// trying to repair directories that had a read error
	DefaultDirHealth = float64(0)

	// updateDeleteName is the name of a siaDir update that deletes the
	// specified metadata file.
	updateDeleteName = "SiaDirDelete"

	// updateMetadataName is the name of a siaDir update that inserts new
	// information into the metadata file
	updateMetadataName = "SiaDirMetadata"
)

// ApplyUpdates  applies a number of writeaheadlog updates to the corresponding
// SiaDir. This method can apply updates from different SiaDirs and should only
// be run before the SiaDirs are loaded from disk right after the startup of
// siad. Otherwise we might run into concurrency issues.
func ApplyUpdates(updates ...writeaheadlog.Update) error {
	// Apply updates.
	for _, u := range updates {
		err := applyUpdate(modules.ProdDependencies, u)
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// CreateAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func CreateAndApplyTransaction(wal *writeaheadlog.WAL, updates ...writeaheadlog.Update) error {
	// Create the writeaheadlog transaction.
	txn, err := wal.NewTransaction(updates)
	if err != nil {
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Apply the updates.
	if err := ApplyUpdates(updates...); err != nil {
		return errors.AddContext(err, "failed to apply updates")
	}
	// Updates are applied. Let the writeaheadlog know.
	if err := txn.SignalUpdatesApplied(); err != nil {
		return errors.AddContext(err, "failed to signal that updates are applied")
	}
	return nil
}

// IsSiaDirUpdate is a helper method that makes sure that a wal update belongs
// to the SiaDir package.
func IsSiaDirUpdate(update writeaheadlog.Update) bool {
	switch update.Name {
	case updateMetadataName, updateDeleteName:
		return true
	default:
		return false
	}
}

// New creates a new directory in the renter directory and makes sure there is a
// metadata file in the directory and creates one as needed. This method will
// also make sure that all the parent directories are created and have metadata
// files as well and will return the SiaDir containing the information for the
// directory that matches the siaPath provided
func New(siaPath modules.SiaPath, rootDir string, wal *writeaheadlog.WAL) (*SiaDir, error) {
	// Create path to directory and ensure path contains all metadata
	updates, err := createDirMetadataAll(siaPath, rootDir)
	if err != nil {
		return nil, err
	}

	// Create metadata for directory
	md, update, err := createDirMetadata(siaPath, rootDir)
	if err != nil {
		return nil, err
	}

	// Create SiaDir
	sd := &SiaDir{
		metadata: md,
		deps:     modules.ProdDependencies,
		siaPath:  siaPath,
		rootDir:  rootDir,
		wal:      wal,
	}

	return sd, CreateAndApplyTransaction(wal, append(updates, update)...)
}

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(rootDir string, siaPath modules.SiaPath, deps modules.Dependencies, wal *writeaheadlog.WAL) (sd *SiaDir, err error) {
	sd = &SiaDir{
		deps:    deps,
		siaPath: siaPath,
		rootDir: rootDir,
		wal:     wal,
	}
	sd.metadata, err = callLoadSiaDirMetadata(siaPath.SiaDirMetadataSysPath(rootDir), modules.ProdDependencies)
	return sd, err
}

// UpdateMetadata updates the SiaDir metadata on disk
func (sd *SiaDir) UpdateMetadata(metadata Metadata) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.metadata.AggregateHealth = metadata.AggregateHealth
	sd.metadata.AggregateLastHealthCheckTime = metadata.AggregateLastHealthCheckTime
	sd.metadata.AggregateMinRedundancy = metadata.AggregateMinRedundancy
	sd.metadata.AggregateModTime = metadata.AggregateModTime
	sd.metadata.AggregateNumFiles = metadata.AggregateNumFiles
	sd.metadata.AggregateNumStuckChunks = metadata.AggregateNumStuckChunks
	sd.metadata.AggregateNumSubDirs = metadata.AggregateNumSubDirs
	sd.metadata.AggregateSize = metadata.AggregateSize
	sd.metadata.AggregateStuckHealth = metadata.AggregateStuckHealth

	sd.metadata.Health = metadata.Health
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	sd.metadata.MinRedundancy = metadata.MinRedundancy
	sd.metadata.ModTime = metadata.ModTime
	sd.metadata.NumFiles = metadata.NumFiles
	sd.metadata.NumStuckChunks = metadata.NumStuckChunks
	sd.metadata.NumSubDirs = metadata.NumSubDirs
	sd.metadata.Size = metadata.Size
	sd.metadata.StuckHealth = metadata.StuckHealth
	return sd.saveDir()
}

// Delete deletes the SiaDir that belongs to the siaPath
func (sds *SiaDirSet) Delete(siaPath modules.SiaPath) error {
	// Prevent new dirs from being opened.
	sds.mu.Lock()
	defer sds.mu.Unlock()
	// Lock loaded files to prevent persistence from happening and unlock them when
	// we are done deleting the dir.
	var lockedDirs []*siaDirSetEntry
	defer func() {
		for _, entry := range lockedDirs {
			entry.mu.Unlock()
		}
	}()
	for key, entry := range sds.siaDirMap {
		if strings.HasPrefix(key.String(), siaPath.String()) {
			entry.mu.Lock()
			lockedDirs = append(lockedDirs, entry)
		}
	}
	// Grab entry and delete it.
	entry, err := sds.open(siaPath)
	if err != nil && err != ErrUnknownPath {
		return err
	} else if err == ErrUnknownPath {
		return nil // nothing to do
	}
	defer sds.closeEntry(entry)
	if err := entry.callDelete(); err != nil {
		return err
	}
	// Deleting the dir was successful. Delete the open dirs. It's sufficient to do
	// so only in memory to avoid any persistence. They will be removed from the
	// map by the last thread closing the entry.
	for _, entry := range lockedDirs {
		entry.deleted = true
	}
	return nil
}

// DirInfo returns the Directory Information of the siadir
func (sds *SiaDirSet) DirInfo(siaPath modules.SiaPath) (modules.DirectoryInfo, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()
	return sds.readLockDirInfo(siaPath)
}

// DirList returns directories stored in the siadir as well as the DirectoryInfo
// of the siadir
func (sds *SiaDirSet) DirList(siaPath modules.SiaPath) ([]modules.DirectoryInfo, error) {
	sds.mu.Lock()
	defer sds.mu.Unlock()

	// Get DirectoryInfo
	di, err := sds.readLockDirInfo(siaPath)
	if err != nil {
		return nil, err
	}
	dirs := []modules.DirectoryInfo{di}
	var dirsMu sync.Mutex
	loadChan := make(chan string)
	worker := func() {
		for path := range loadChan {
			// Load the dir info.
			var siaPath modules.SiaPath
			if err := siaPath.LoadSysPath(sds.staticRootDir, path); err != nil {
				continue
			}
			var dir modules.DirectoryInfo
			var err error
			dir, err = sds.readLockDirInfo(siaPath)
			if os.IsNotExist(err) || err == ErrUnknownPath {
				continue
			}
			if err != nil {
				continue
			}
			dirsMu.Lock()
			dirs = append(dirs, dir)
			dirsMu.Unlock()
		}
	}
	// spin up some threads
	var wg sync.WaitGroup
	for i := 0; i < dirListRoutines; i++ {
		wg.Add(1)
		go func() {
			worker()
			wg.Done()
		}()
	}
	// Read Directory
	folder := siaPath.SiaDirSysPath(sds.staticRootDir)
	fileInfos, err := ioutil.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	for _, fi := range fileInfos {
		// Check for directories
		if fi.IsDir() {
			loadChan <- filepath.Join(folder, fi.Name())
		}
	}
	close(loadChan)
	wg.Wait()
	return dirs, nil
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
	sd, err := New(siaPath, sds.staticRootDir, sds.wal)
	if err != nil {
		return nil, err
	}
	entry := sds.newSiaDirSetEntry(sd)
	threadUID := randomThreadUID()
	entry.threadMap[threadUID] = newThreadInfo()
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

// Rename renames a SiaDir on disk atomically by locking all the already loaded,
// affected dirs and renaming the root.
// NOTE: This shouldn't be called directly but instead be passed to
// siafileset.RenameDir as an argument.
func (sds *SiaDirSet) Rename(oldPath, newPath modules.SiaPath) error {
	if oldPath.Equals(modules.RootSiaPath()) {
		return errors.New("can't rename root dir")
	}
	if oldPath.Equals(newPath) {
		return nil // nothing to do
	}
	if strings.HasPrefix(newPath.String(), oldPath.String()) {
		return errors.New("can't rename folder into itself")
	}
	// Prevent new dirs from being opened.
	sds.mu.Lock()
	defer sds.mu.Unlock()
	// Lock loaded files to prevent persistence from happening and unlock them when
	// we are done renaming the dir.
	var lockedDirs []*siaDirSetEntry
	defer func() {
		for _, entry := range lockedDirs {
			entry.mu.Unlock()
		}
	}()
	for key, entry := range sds.siaDirMap {
		if strings.HasPrefix(key.String(), oldPath.String()) {
			entry.mu.Lock()
			lockedDirs = append(lockedDirs, entry)
		}
	}
	// Rename the target dir.
	oldPathDisk := oldPath.SiaDirSysPath(sds.staticRootDir)
	newPathDisk := newPath.SiaDirSysPath(sds.staticRootDir)
	err := os.Rename(oldPathDisk, newPathDisk) // TODO: use wal
	if err != nil {
		return errors.AddContext(err, "failed to rename folder")
	}
	// Renaming the target dir was successful. Rename the open dirs.
	for _, entry := range lockedDirs {
		sp, err := entry.siaPath.Rebase(oldPath, newPath)
		if err != nil {
			build.Critical("Rebasing siapaths shouldn't fail", err)
			continue
		}
		// Update the siapath of the entry and the siaDirMap.
		delete(sds.siaDirMap, entry.siaPath)
		entry.siaPath = sp
		sds.siaDirMap[sp] = entry
	}
	return err
}

// createDirMetadata makes sure there is a metadata file in the directory and
// creates one as needed
func createDirMetadata(siaPath modules.SiaPath, rootDir string) (Metadata, writeaheadlog.Update, error) {
	// Check if metadata file exists
	_, err := os.Stat(siaPath.SiaDirMetadataSysPath(rootDir))
	if err == nil || !os.IsNotExist(err) {
		return Metadata{}, writeaheadlog.Update{}, err
	}

	// Initialize metadata, set Health and StuckHealth to DefaultDirHealth so
	// empty directories won't be viewed as being the most in need. Initialize
	// ModTimes.
	md := Metadata{
		AggregateHealth:      DefaultDirHealth,
		AggregateModTime:     time.Now(),
		AggregateStuckHealth: DefaultDirHealth,

		Health:      DefaultDirHealth,
		ModTime:     time.Now(),
		StuckHealth: DefaultDirHealth,
	}
	path := siaPath.SiaDirMetadataSysPath(rootDir)
	update, err := createMetadataUpdate(path, md)
	return md, update, err
}

// createDirMetadataAll creates a path on disk to the provided siaPath and make
// sure that all the parent directories have metadata files.
func createDirMetadataAll(siaPath modules.SiaPath, rootDir string) ([]writeaheadlog.Update, error) {
	// Create path to directory
	if err := os.MkdirAll(siaPath.SiaDirSysPath(rootDir), 0700); err != nil {
		return nil, err
	}

	// Create metadata
	var updates []writeaheadlog.Update
	var err error
	for {
		siaPath, err = siaPath.Dir()
		if err != nil {
			return nil, err
		}
		_, update, err := createDirMetadata(siaPath, rootDir)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(update, writeaheadlog.Update{}) {
			updates = append(updates, update)
		}
		if siaPath.IsRoot() {
			break
		}
	}
	return updates, nil
}

// callLoadSiaDirMetadata loads the directory metadata from disk.
func callLoadSiaDirMetadata(path string, deps modules.Dependencies) (md Metadata, err error) {
	// Open the file.
	file, err := deps.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer file.Close()

	// Read the file
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return Metadata{}, err
	}

	// Parse the json object.
	err = json.Unmarshal(bytes, &md)
	return
}

// callDelete removes the directory from disk and marks it as deleted. Once the
// directory is deleted, attempting to access the directory will return an
// error.
func (sd *SiaDir) callDelete() error {
	update := sd.createDeleteUpdate()
	err := sd.createAndApplyTransaction(update)
	sd.deleted = true
	return err
}

// saveDir saves the whole SiaDir atomically.
func (sd *SiaDir) saveDir() error {
	metadataUpdate, err := sd.saveMetadataUpdate()
	if err != nil {
		return err
	}
	return sd.createAndApplyTransaction(metadataUpdate)
}

// open will return the siaDirSetEntry in memory or load it from disk
func (sds *SiaDirSet) open(siaPath modules.SiaPath) (*SiaDirSetEntry, error) {
	var entry *siaDirSetEntry
	entry, exists := sds.siaDirMap[siaPath]
	if !exists {
		// Try and Load File from disk
		sd, err := LoadSiaDir(sds.staticRootDir, siaPath, modules.ProdDependencies, sds.wal)
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
	entry.threadMap[threadUID] = newThreadInfo()
	return &SiaDirSetEntry{
		siaDirSetEntry: entry,
		threadUID:      threadUID,
	}, nil
}
