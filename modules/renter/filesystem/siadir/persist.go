package siadir

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// SiaDirExtension is the name of the metadata file for the sia directory
	SiaDirExtension = ".siadir"

	// DefaultDirHealth is the default health for the directory and the fall
	// back value when there is an error. This is to protect against falsely
	// trying to repair directories that had a read error
	DefaultDirHealth = float64(0)

	// DefaultDirRedundancy is the default redundancy for the directory and the
	// fall back value when there is an error. This is to protect against
	// falsely trying to repair directories that had a read error
	DefaultDirRedundancy = float64(-1)

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
func New(path, rootPath string, mode os.FileMode, wal *writeaheadlog.WAL) (*SiaDir, error) {
	// Create path to directory and ensure path contains all metadata
	updates, err := createDirMetadataAll(path, rootPath, mode)
	if err != nil {
		return nil, err
	}

	// Create metadata for directory
	md, update, err := createDirMetadata(path, mode)
	if err != nil {
		return nil, err
	}

	// Create SiaDir
	sd := &SiaDir{
		metadata: md,
		deps:     modules.ProdDependencies,
		path:     path,
		wal:      wal,
	}

	return sd, CreateAndApplyTransaction(wal, append(updates, update)...)
}

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(path string, deps modules.Dependencies, wal *writeaheadlog.WAL) (sd *SiaDir, err error) {
	sd = &SiaDir{
		deps: deps,
		path: path,
		wal:  wal,
	}
	sd.metadata, err = callLoadSiaDirMetadata(filepath.Join(path, modules.SiaDirExtension), modules.ProdDependencies)
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

// createDirMetadata makes sure there is a metadata file in the directory and
// creates one as needed
func createDirMetadata(path string, mode os.FileMode) (Metadata, writeaheadlog.Update, error) {
	// Check if metadata file exists
	mdPath := filepath.Join(path, modules.SiaDirExtension)
	_, err := os.Stat(mdPath)
	if err == nil {
		return Metadata{}, writeaheadlog.Update{}, os.ErrExist
	} else if !os.IsNotExist(err) {
		return Metadata{}, writeaheadlog.Update{}, err
	}

	// Initialize metadata, set Health and StuckHealth to DefaultDirHealth so
	// empty directories won't be viewed as being the most in need. Initialize
	// ModTimes.
	md := Metadata{
		AggregateHealth:        DefaultDirHealth,
		AggregateMinRedundancy: DefaultDirRedundancy,
		AggregateModTime:       time.Now(),
		AggregateStuckHealth:   DefaultDirHealth,

		Health:        DefaultDirHealth,
		MinRedundancy: DefaultDirRedundancy,
		Mode:          mode,
		ModTime:       time.Now(),
		StuckHealth:   DefaultDirHealth,
	}
	update, err := createMetadataUpdate(mdPath, md)
	return md, update, err
}

// createDirMetadataAll creates a path on disk to the provided siaPath and make
// sure that all the parent directories have metadata files.
func createDirMetadataAll(dirPath, rootPath string, mode os.FileMode) ([]writeaheadlog.Update, error) {
	// Create path to directory
	if err := os.MkdirAll(dirPath, 0700); err != nil {
		return nil, err
	}
	if dirPath == rootPath {
		return []writeaheadlog.Update{}, nil
	}

	// Create metadata
	var updates []writeaheadlog.Update
	var err error
	for {
		dirPath = filepath.Dir(dirPath)
		if err != nil {
			return nil, err
		}
		if dirPath == string(filepath.Separator) {
			dirPath = rootPath
		}
		_, update, err := createDirMetadata(dirPath, mode)
		if err != nil && !os.IsExist(err) {
			return nil, err
		}
		if !os.IsExist(err) {
			updates = append(updates, update)
		}
		if dirPath == rootPath {
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

	// CompatV1420 check if filemode is set. If not use the default. It's fine
	// not to persist it right away since it will either be persisted anyway or
	// we just set the values again the next time we load it and hope that it
	// gets persisted then.
	if md.Version == "" && md.Mode == 0 {
		md.Mode = modules.DefaultDirPerm
		md.Version = "1.0"
	}
	return
}

// Rename renames the SiaDir to targetPath.
func (sd *SiaDir) rename(targetPath string) error {
	err := os.Rename(sd.path, targetPath)
	if err != nil {
		return err
	}
	sd.path = targetPath
	return nil
}

// Delete removes the directory from disk and marks it as deleted. Once the
// directory is deleted, attempting to access the directory will return an
// error.
func (sd *SiaDir) Delete() error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
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

// Rename renames the SiaDir to targetPath.
func (sd *SiaDir) Rename(targetPath string) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.rename(targetPath)
}

// SetPath sets the path field of the dir.
func (sd *SiaDir) SetPath(targetPath string) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.path = targetPath
}
