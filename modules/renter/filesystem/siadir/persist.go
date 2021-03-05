package siadir

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
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
)

var (
	// ErrDeleted is the error returned if the siadir is deleted
	ErrDeleted = errors.New("siadir is deleted")

	// errInvalidChecksum is the error returned if the siadir checksum is invalid
	errInvalidChecksum = errors.New("invalid checksum")
)

// New creates a new directory in the renter directory and makes sure there is a
// metadata file in the directory and creates one as needed. This method will
// also make sure that all the parent directories are created and have metadata
// files as well and will return the SiaDir containing the information for the
// directory that matches the siaPath provided
//
// NOTE: the fullPath is expected to include the rootPath. The rootPath is used
// to determine when to stop recursively creating siadir metadata.
func New(fullPath, rootPath string, mode os.FileMode) (*SiaDir, error) {
	// Create path to directory and ensure path contains all metadata
	deps := modules.ProdDependencies
	err := createDirMetadataAll(fullPath, rootPath, mode, deps)
	if err != nil {
		return nil, errors.AddContext(err, "unable to create metadatas for parent directories")
	}

	// Create metadata for directory
	md, err := createDirMetadata(fullPath, mode)
	if err != nil {
		return nil, errors.AddContext(err, "unable to create metadata for directory")
	}

	// Create SiaDir
	sd := &SiaDir{
		metadata: md,
		deps:     deps,
		path:     fullPath,
	}

	return sd, sd.saveDir()
}

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(path string, deps modules.Dependencies) (sd *SiaDir, err error) {
	sd = &SiaDir{
		deps: deps,
		path: path,
	}
	sd.metadata, err = callLoadSiaDirMetadata(filepath.Join(path, modules.SiaDirExtension), modules.ProdDependencies)
	return sd, err
}

// Delete removes the directory from disk and marks it as deleted. Once the
// directory is deleted, attempting to access the directory will return an
// error.
func (sd *SiaDir) Delete() error {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	// Check if the SiaDir is already deleted
	if sd.deleted {
		return nil
	}

	// Create and apply the delete update
	err := os.RemoveAll(sd.path)
	if err != nil {
		return errors.AddContext(err, "unable to delete siadir")
	}
	sd.deleted = true
	return nil
}

// Rename renames the SiaDir to targetPath.
func (sd *SiaDir) Rename(targetPath string) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()

	// Check if Deleted
	if sd.deleted {
		return errors.AddContext(ErrDeleted, "cannot rename a deleted SiaDir")
	}
	return sd.rename(targetPath)
}

// SetPath sets the path field of the dir.
func (sd *SiaDir) SetPath(targetPath string) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	// Check if Deleted
	if sd.deleted {
		return errors.AddContext(ErrDeleted, "cannot set the path of a deleted SiaDir")
	}
	sd.path = targetPath
	return nil
}

// UpdateBubbledMetadata updates the SiaDir Metadata that is bubbled and saves
// the changes to disk. For fields that are not bubbled, this method sets them
// to the current values in the SiaDir metadata
func (sd *SiaDir) UpdateBubbledMetadata(metadata Metadata) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	metadata.Mode = sd.metadata.Mode
	metadata.Version = sd.metadata.Version
	return sd.updateMetadata(metadata)
}

// UpdateLastHealthCheckTime updates the SiaDir LastHealthCheckTime and
// AggregateLastHealthCheckTime and saves the changes to disk
func (sd *SiaDir) UpdateLastHealthCheckTime(aggregateLastHealthCheckTime, lastHealthCheckTime time.Time) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	md := sd.metadata
	md.AggregateLastHealthCheckTime = aggregateLastHealthCheckTime
	md.LastHealthCheckTime = lastHealthCheckTime
	return sd.updateMetadata(md)
}

// UpdateMetadata updates the SiaDir metadata on disk
func (sd *SiaDir) UpdateMetadata(metadata Metadata) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.updateMetadata(metadata)
}

// callLoadSiaDirMetadata loads the directory metadata from disk.
func callLoadSiaDirMetadata(path string, deps modules.Dependencies) (md Metadata, err error) {
	// Open the file.
	file, err := deps.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer func() {
		err = errors.Compose(err, file.Close())
	}()

	// Read the file
	fileBytes, err := ioutil.ReadAll(file)
	if err != nil {
		return Metadata{}, errors.AddContext(err, "unable to read bytes from file")
	}

	// Verify there is enough data for a checksum
	if len(fileBytes) < crypto.HashSize {
		return Metadata{}, errors.New("file does not contain enough bytes for a checksum")
	}

	// Verify checksum
	checksum := fileBytes[:crypto.HashSize]
	mdBytes := fileBytes[crypto.HashSize:]
	fileChecksum := crypto.HashBytes(mdBytes)
	if !bytes.Equal(checksum, fileChecksum[:]) {
		return Metadata{}, errors.New("checksum invalid")
	}

	// Parse the json object.
	err = json.Unmarshal(mdBytes, &md)

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

// createDirMetadata makes sure there is a metadata file in the directory and
// creates one as needed
func createDirMetadata(path string, mode os.FileMode) (Metadata, error) {
	// Check if metadata file exists
	mdPath := filepath.Join(path, modules.SiaDirExtension)
	_, err := os.Stat(mdPath)
	if err == nil {
		return Metadata{}, os.ErrExist
	} else if !os.IsNotExist(err) {
		return Metadata{}, err
	}

	// Initialize metadata, set Health and StuckHealth to DefaultDirHealth so
	// empty directories won't be viewed as being the most in need. Initialize
	// ModTimes.
	now := time.Now()
	md := Metadata{
		AggregateHealth:        DefaultDirHealth,
		AggregateMinRedundancy: DefaultDirRedundancy,
		AggregateModTime:       now,
		AggregateRemoteHealth:  DefaultDirHealth,
		AggregateStuckHealth:   DefaultDirHealth,

		Health:        DefaultDirHealth,
		MinRedundancy: DefaultDirRedundancy,
		Mode:          mode,
		ModTime:       now,
		RemoteHealth:  DefaultDirHealth,
		StuckHealth:   DefaultDirHealth,
	}
	return md, nil
}

// createDirMetadataAll creates a path on disk to the provided siaPath and make
// sure that all the parent directories have metadata files.
func createDirMetadataAll(dirPath, rootPath string, mode os.FileMode, deps modules.Dependencies) error {
	// Create path to directory
	if err := os.MkdirAll(dirPath, modules.DefaultDirPerm); err != nil {
		return err
	}
	if dirPath == rootPath {
		return nil
	}

	// Create metadata
	for {
		dirPath = filepath.Dir(dirPath)
		if dirPath == string(filepath.Separator) || dirPath == "." {
			dirPath = rootPath
		}
		md, err := createDirMetadata(dirPath, mode)
		if err != nil && !os.IsExist(err) {
			return errors.AddContext(err, "unable to create metadata")
		}
		// Save metadata
		err = saveDir(dirPath, md, deps)
		if err != nil {
			return errors.AddContext(err, "unable to saveDir")
		}
		if dirPath == rootPath {
			break
		}
	}
	return nil
}

// Rename renames the SiaDir to targetPath.
func (sd *SiaDir) rename(targetPath string) error {
	// TODO: os.Rename is not ACID
	err := os.Rename(sd.path, targetPath)
	if err != nil {
		return err
	}
	sd.path = targetPath
	return nil
}

// saveDir saves the SiaDir's metadata to disk.
func (sd *SiaDir) saveDir() (err error) {
	// Check if Deleted
	if sd.deleted {
		return errors.AddContext(ErrDeleted, "cannot save a deleted SiaDir")
	}
	return saveDir(sd.path, sd.metadata, sd.deps)
}

// updateMetadata updates the SiaDir metadata on disk
func (sd *SiaDir) updateMetadata(metadata Metadata) error {
	// Check if the directory is deleted
	if sd.deleted {
		return errors.AddContext(ErrDeleted, "cannot update the metadata for a deleted directory")
	}

	// Update metadata
	sd.metadata.AggregateHealth = metadata.AggregateHealth
	sd.metadata.AggregateLastHealthCheckTime = metadata.AggregateLastHealthCheckTime
	sd.metadata.AggregateMinRedundancy = metadata.AggregateMinRedundancy
	sd.metadata.AggregateModTime = metadata.AggregateModTime
	sd.metadata.AggregateNumFiles = metadata.AggregateNumFiles
	sd.metadata.AggregateNumStuckChunks = metadata.AggregateNumStuckChunks
	sd.metadata.AggregateNumSubDirs = metadata.AggregateNumSubDirs
	sd.metadata.AggregateRemoteHealth = metadata.AggregateRemoteHealth
	sd.metadata.AggregateRepairSize = metadata.AggregateRepairSize
	sd.metadata.AggregateSize = metadata.AggregateSize
	sd.metadata.AggregateStuckHealth = metadata.AggregateStuckHealth
	sd.metadata.AggregateStuckSize = metadata.AggregateStuckSize

	sd.metadata.AggregateSkynetFiles = metadata.AggregateSkynetFiles
	sd.metadata.AggregateSkynetSize = metadata.AggregateSkynetSize

	sd.metadata.Health = metadata.Health
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	sd.metadata.MinRedundancy = metadata.MinRedundancy
	sd.metadata.ModTime = metadata.ModTime
	sd.metadata.Mode = metadata.Mode
	sd.metadata.NumFiles = metadata.NumFiles
	sd.metadata.NumStuckChunks = metadata.NumStuckChunks
	sd.metadata.NumSubDirs = metadata.NumSubDirs
	sd.metadata.RemoteHealth = metadata.RemoteHealth
	sd.metadata.RepairSize = metadata.RepairSize
	sd.metadata.Size = metadata.Size
	sd.metadata.StuckHealth = metadata.StuckHealth
	sd.metadata.StuckSize = metadata.StuckSize

	sd.metadata.SkynetFiles = metadata.SkynetFiles
	sd.metadata.SkynetSize = metadata.SkynetSize

	sd.metadata.Version = metadata.Version

	// Testing check to ensure new fields aren't missed
	if build.Release == "testing" && !reflect.DeepEqual(sd.metadata, metadata) {
		str := fmt.Sprintf(`Input metadata not equal to set metadata
		metadata
		%v
		sd.metadata
		%v`, metadata, sd.metadata)
		build.Critical(str)
	}

	// Sanity check that siadir is on disk
	_, err := os.Stat(sd.path)
	if os.IsNotExist(err) {
		build.Critical("UpdateMetadata called on a SiaDir that does not exist on disk")
		err = os.MkdirAll(filepath.Dir(sd.path), modules.DefaultDirPerm)
		if err != nil {
			return errors.AddContext(err, "unable to create missing siadir directory on disk")
		}
	}

	return sd.saveDir()
}

// saveDir saves the metadata to disk at the provided path.
func saveDir(path string, md Metadata, deps modules.Dependencies) error {
	// Open .siadir file
	f, err := deps.OpenFile(filepath.Join(path, SiaDirExtension), os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open file")
	}
	defer func() {
		errors.Compose(err, f.Close())
	}()
	// Marshal metadata
	data, err := json.Marshal(md)
	if err != nil {
		return errors.AddContext(err, "unable to marshal metadata")
	}

	// Generate checksum
	checksum := crypto.HashBytes(data)

	// Write the checksum to the file
	_, err = f.WriteAt(checksum[:], 0)
	if err != nil {
		return errors.AddContext(err, "unable to write checksum")
	}

	// Write the metadata to disk
	_, err = f.WriteAt(data, int64(len(checksum)))
	if err != nil {
		return errors.AddContext(err, "unable to write data to disk")
	}
	return f.Sync()
}
