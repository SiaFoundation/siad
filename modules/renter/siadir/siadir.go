package siadir

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
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
)

type (
	// SiaDir contains the metadata information about a renter directory
	SiaDir struct {
		staticMetadata siaDirMetadata

		// Utility fields
		deleted bool
		deps    modules.Dependencies
		mu      sync.Mutex
		wal     *writeaheadlog.WAL
	}

	// SiaDirHealth is an exported struct containing the health information of
	// the SiaDir
	SiaDirHealth struct {
		Health              float64
		StuckHealth         float64
		LastHealthCheckTime time.Time
		NumStuckChunks      uint64
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

		// NumStuckChunks is the sum of all the Stuck Chunks of any of the
		// siafiles in the siadir or any of the sub directories
		NumStuckChunks uint64 `json:"numstuckchunks"`

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
		deps:           modules.ProdDependencies,
		wal:            wal,
	}

	return sd, managedCreateAndApplyTransaction(wal, append(updates, update)...)
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
		Health:      DefaultDirHealth,
		StuckHealth: DefaultDirHealth,
		RootDir:     rootDir,
		SiaPath:     siaPath,
	}
	update, err := createMetadataUpdate(md)
	return md, update, err
}

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(rootDir, siaPath string, deps modules.Dependencies, wal *writeaheadlog.WAL) (*SiaDir, error) {
	path := filepath.Join(rootDir, siaPath)
	sd := &SiaDir{
		deps: deps,
		wal:  wal,
	}
	// Open the file.
	file, err := sd.deps.Open(filepath.Join(path, SiaDirExtension))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read the file
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, err
	}

	// Parse the json object.
	err = json.Unmarshal(bytes, &sd.staticMetadata)

	return sd, err
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
func (sd *SiaDir) Health() SiaDirHealth {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return SiaDirHealth{
		Health:              sd.staticMetadata.Health,
		StuckHealth:         sd.staticMetadata.StuckHealth,
		LastHealthCheckTime: sd.staticMetadata.LastHealthCheckTime,
		NumStuckChunks:      sd.staticMetadata.NumStuckChunks,
	}
}

// SiaPath returns the SiaPath of the SiaDir
func (sd *SiaDir) SiaPath() string {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.staticMetadata.SiaPath
}

// updateHealth updates the SiaDir metadata on disk with the new Health value
func (sd *SiaDir) updateHealth(health SiaDirHealth) error {
	sd.staticMetadata.Health = health.Health
	sd.staticMetadata.StuckHealth = health.StuckHealth
	sd.staticMetadata.LastHealthCheckTime = health.LastHealthCheckTime
	sd.staticMetadata.NumStuckChunks = health.NumStuckChunks
	return sd.saveDir()
}

// UpdateHealth is a helper wrapper for calling updateHealth
func (sd *SiaDir) UpdateHealth(health SiaDirHealth) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.updateHealth(health)
}
