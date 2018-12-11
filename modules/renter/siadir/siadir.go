package siadir

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	// persistVersion defines the Sia version that the persistence was last
	// updated
	persistVersion = "1.4.0"

	// SiaDirExtension is the name of the metadata file for the sia directory
	SiaDirExtension = ".siadir"

	// DefaultDirHealth is the default health for the directory and the fall
	// back value when there is an error. This is to protect against falsely
	// trying to repair directories that had a read error
	DefaultDirHealth = float64(0)
)

var (
	// ErrPathOverload is an error when a siadir already exists at that location
	ErrPathOverload = errors.New("a siadir already exists at that location")
	// ErrUnknownPath is an error when a siadir cannot be found with the given path
	ErrUnknownPath = errors.New("no siadir known with that path")
	// ErrUnknownThread is an error when a siadir is trying to be closed by a
	// thread that is not in the threadMap
	ErrUnknownThread = errors.New("thread should not be calling Close(), does not have control of the siadir")

	siaDirMetadataHeader = persist.Metadata{
		Header:  "Sia Directory Metadata",
		Version: persistVersion,
	}
)

type (
	// SiaDir contains the metadata information about a renter directory
	SiaDir struct {
		staticMetadata siaDirMetadata

		// Utility fields
		deleted bool
		mu      sync.Mutex
		wal     *writeaheadlog.WAL
	}

	// siaDirMetadata is the metadata that is saved to disk as a .siadir file
	siaDirMetadata struct {
		// Health is the health of the most in need file in the directory or any
		// of the sub directories that are not stuck
		Health float64 `json:"health"`
		// StuckHealth is the health of the most in need file in the directory
		// or any of the sub directories, stuck or not stuck
		StuckHealth float64 `json:"stuckhealth"`

		// LastHealthCheckTime is the oldest LastHealthCheckTime of any of the
		// siafiles in the siadir or any of the sub directories
		LastHealthCheckTime time.Time `json:"lasthealthchecktime"`

		// RootDir is the path to the root directory on disk
		RootDir string `json:"rootdir"`

		// SiaPath is the path to the siadir on the sia network
		SiaPath string `json:"siapath"`
	}
)

// New creates a new directory in the renter directory and makes sure there is a
// metadata file in the directory and creates one as needed. This method will
// also make sure that all the parent directories are created and have metadata
// files as well and will return the SiaDir containing the information for the
// directory that matches the siaPath provided
func New(siaPath, rootDir string, wal *writeaheadlog.WAL) (*SiaDir, error) {
	// Create path to direcotry and ensure path contains all metadata
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
		staticMetadata: md,
		wal:            wal,
	}

	return sd, sd.createAndApplyTransaction(append(updates, update)...)
}

// createDirMetadata makes sure there is a metadata file in the directory and
// creates one as needed
func createDirMetadata(siaPath, rootDir string) (siaDirMetadata, writeaheadlog.Update, error) {
	// Check if metadata file exists
	_, err := os.Stat(filepath.Join(rootDir, siaPath, SiaDirExtension))
	if err == nil || !os.IsNotExist(err) {
		return siaDirMetadata{}, writeaheadlog.Update{}, err
	}

	// Initialize metadata, set Health and StuckHealth to DefaultDirHealth so
	// empty directories won't be viewed as being the most in need
	md := siaDirMetadata{
		Health:              DefaultDirHealth,
		StuckHealth:         DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		RootDir:             rootDir,
		SiaPath:             siaPath,
	}
	update, err := createMetadataUpdate(md)
	return md, update, err
}

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(rootDir, siaPath string, wal *writeaheadlog.WAL) (*SiaDir, error) {
	var md siaDirMetadata
	path := filepath.Join(rootDir, siaPath)
	err := persist.LoadJSON(siaDirMetadataHeader, &md, filepath.Join(path, SiaDirExtension))
	return &SiaDir{
		staticMetadata: md,
		wal:            wal,
	}, err
}

// save saves the directory metadata to disk
func (md siaDirMetadata) save() error {
	path := filepath.Join(md.RootDir, md.SiaPath, SiaDirExtension)
	return persist.SaveJSON(siaDirMetadataHeader, md, path)
}

// Delete removes the directory from disk and marks it as deleted. Once the directory is
// deleted, attempting to access the directory will return an error.
func (sd *SiaDir) Delete() error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	update := sd.createDeleteUpdate()
	err := sd.createAndApplyTransaction(update)
	sd.deleted = true
	return err
}

// Deleted returns the deleted field of the siaDir
func (sd *SiaDir) Deleted() bool {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.deleted
}

// Health returns the health metadata of the SiaDir
func (sd *SiaDir) Health() (float64, float64, time.Time) {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.staticMetadata.Health, sd.staticMetadata.StuckHealth, sd.staticMetadata.LastHealthCheckTime
}

// SiaPath returns the SiaPath of the SiaDir
func (sd *SiaDir) SiaPath() string {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.staticMetadata.SiaPath
}

// updateHealth updates the SiaDir metadata on disk with the new Health value
func (sd *SiaDir) updateHealth(health, stuckHealth float64, lastCheck time.Time) error {
	sd.staticMetadata.Health = health
	sd.staticMetadata.StuckHealth = stuckHealth
	sd.staticMetadata.LastHealthCheckTime = lastCheck
	return sd.saveDir()
}

// UpdateHealth is a helper wrapper for calling updateHealth when the SiaDir
// lock is held outside the siadir package
func (sd *SiaDir) UpdateHealth(health, stuckHealth float64, lastCheck time.Time) error {
	return sd.updateHealth(health, stuckHealth, lastCheck)
}
