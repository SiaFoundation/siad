package siadir

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/persist"
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
		mu             sync.Mutex
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

		// SiaPath is the path to the siadir on the sia network
		SiaPath string `json:"siapath"`
	}
)

// New creates a new directory in the renter directory and makes sure there is a
// metadata file in the directory and creates one as needed. This method will
// also make sure that all the parent directories are created and have metadata
// files as well and will return the SiaDir containing the information for the
// directory that matches the siaPath provided
func New(siaPath, rootDir string) (*SiaDir, error) {
	// Create direcotry
	siaDirFilePath := filepath.Join(rootDir, siaPath)
	if err := os.MkdirAll(siaDirFilePath, 0700); err != nil {
		return nil, err
	}

	// Create metadata for directory
	md, err := createDirMetadata(siaPath, siaDirFilePath)
	if err != nil {
		return nil, err
	}

	// Make sure all parent directories have metadata files
	siaPath, path := pathDirs(siaPath, siaDirFilePath)
	for path != filepath.Dir(rootDir) {
		if _, err := createDirMetadata(siaPath, path); err != nil {
			return nil, err
		}
		siaPath, path = pathDirs(siaPath, path)
	}
	return &SiaDir{
		staticMetadata: md,
	}, nil
}

// createDirMetadata makes sure there is a metadata file in the directory and
// creates one as needed
func createDirMetadata(siaPath, fullPath string) (siaDirMetadata, error) {
	// Check if metadata file exists
	_, err := os.Stat(filepath.Join(fullPath, SiaDirExtension))
	if err == nil || !os.IsNotExist(err) {
		return siaDirMetadata{}, err
	}

	// Initialize metadata, set Health and StuckHealth to DefaultDirHealth so
	// empty directories won't be viewed as being the most in need
	md := siaDirMetadata{
		Health:              DefaultDirHealth,
		StuckHealth:         DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		SiaPath:             siaPath,
	}
	return md, md.save(fullPath)
}

// pathDirs returns the directories for the input paths
func pathDirs(siaPath, path string) (string, string) {
	siaPath = filepath.Dir(siaPath)
	if siaPath == "." {
		siaPath = ""
	}
	path = filepath.Dir(path)
	return siaPath, path
}

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(rootDir, siaPath string) (*SiaDir, error) {
	var md siaDirMetadata
	path := filepath.Join(rootDir, siaPath)
	err := persist.LoadJSON(siaDirMetadataHeader, &md, filepath.Join(path, SiaDirExtension))
	return &SiaDir{
		staticMetadata: md,
	}, err
}

// save saves the directory metadata to disk
func (md siaDirMetadata) save(path string) error {
	return persist.SaveJSON(siaDirMetadataHeader, md, filepath.Join(path, SiaDirExtension))
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

// UpdateHealth updates the SiaDir metadata on disk with the new metadata health
// values
func (sd *SiaDir) UpdateHealth(health, stuckHealth float64, lastCheck time.Time, path string) error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	sd.staticMetadata.Health = health
	sd.staticMetadata.StuckHealth = stuckHealth
	sd.staticMetadata.LastHealthCheckTime = lastCheck
	return sd.staticMetadata.save(path)
}
