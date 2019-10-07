package siadir

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
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
		metadata Metadata

		// staticPath is the path of the SiaDir folder.
		staticPath string

		// Utility fields
		deleted bool
		deps    modules.Dependencies
		mu      sync.Mutex
		wal     *writeaheadlog.WAL
	}

	// Metadata is the metadata that is saved to disk as a .siadir file
	Metadata struct {
		// For each field in the metadata there is an aggregate value and a
		// siadir specific value. If a field has the aggregate prefix it means
		// that the value takes into account all the siafiles and siadirs in the
		// sub tree. The definition of aggregate and siadir specific values is
		// otherwise the same.
		//
		// Health is the health of the most in need siafile that is not stuck
		//
		// LastHealthCheckTime is the oldest LastHealthCheckTime of any of the
		// siafiles in the siadir and is the last time the health was calculated
		// by the health loop
		//
		// MinRedundancy is the minimum redundancy of any of the siafiles in the
		// siadir
		//
		// ModTime is the last time any of the siafiles in the siadir was
		// updated
		//
		// NumFiles is the total number of siafiles in a siadir
		//
		// NumStuckChunks is the sum of all the Stuck Chunks of any of the
		// siafiles in the siadir
		//
		// NumSubDirs is the number of sub-siadirs in a siadir
		//
		// Size is the total amount of data stored in the siafiles of the siadir
		//
		// StuckHealth is the health of the most in need siafile in the siadir,
		// stuck or not stuck

		// The following fields are aggregate values of the siadir. These values are
		// the totals of the siadir and any sub siadirs, or are calculated based on
		// all the values in the subtree
		AggregateHealth              float64   `json:"aggregatehealth"`
		AggregateLastHealthCheckTime time.Time `json:"aggregatelasthealthchecktime"`
		AggregateMinRedundancy       float64   `json:"aggregateminredundancy"`
		AggregateModTime             time.Time `json:"aggregatemodtime"`
		AggregateNumFiles            uint64    `json:"aggregatenumfiles"`
		AggregateNumStuckChunks      uint64    `json:"aggregatenumstuckchunks"`
		AggregateNumSubDirs          uint64    `json:"aggregatenumsubdirs"`
		AggregateSize                uint64    `json:"aggregatesize"`
		AggregateStuckHealth         float64   `json:"aggregatestuckhealth"`

		// The following fields are information specific to the siadir that is not
		// an aggregate of the entire sub directory tree
		Health              float64   `json:"health"`
		LastHealthCheckTime time.Time `json:"lasthealthchecktime"`
		MinRedundancy       float64   `json:"minredundancy"`
		ModTime             time.Time `json:"modtime"`
		NumFiles            uint64    `json:"numfiles"`
		NumStuckChunks      uint64    `json:"numstuckchunks"`
		NumSubDirs          uint64    `json:"numsubdirs"`
		Size                uint64    `json:"size"`
		StuckHealth         float64   `json:"stuckhealth"`
	}
)

// DirReader is a helper type that allows reading a raw .siadir from disk while
// keeping the file in memory locked.
type DirReader struct {
	f  *os.File
	sd *SiaDir
}

// Close closes the underlying file.
func (sdr *DirReader) Close() error {
	sdr.sd.mu.Unlock()
	return sdr.f.Close()
}

// Read calls Read on the underlying file.
func (sdr *DirReader) Read(b []byte) (int, error) {
	return sdr.f.Read(b)
}

// Stat returns the FileInfo of the underlying file.
func (sdr *DirReader) Stat() (os.FileInfo, error) {
	return sdr.f.Stat()
}

// New creates a new directory in the renter directory and makes sure there is a
// metadata file in the directory and creates one as needed. This method will
// also make sure that all the parent directories are created and have metadata
// files as well and will return the SiaDir containing the information for the
// directory that matches the siaPath provided
func New(path, rootPath string, wal *writeaheadlog.WAL) (*SiaDir, error) {
	// Create path to directory and ensure path contains all metadata
	updates, err := createDirMetadataAll(path, rootPath)
	if err != nil {
		return nil, err
	}

	// Create metadata for directory
	md, update, err := createDirMetadata(path)
	if err != nil {
		return nil, err
	}

	// Create SiaDir
	sd := &SiaDir{
		metadata:   md,
		deps:       modules.ProdDependencies,
		staticPath: path,
		wal:        wal,
	}

	return sd, managedCreateAndApplyTransaction(wal, append(updates, update)...)
}

// createDirMetadata makes sure there is a metadata file in the directory and
// creates one as needed
func createDirMetadata(path string) (Metadata, writeaheadlog.Update, error) {
	// Check if metadata file exists
	_, err := os.Stat(filepath.Join(path, modules.SiaDirExtension))
	if !os.IsNotExist(err) {
		return Metadata{}, writeaheadlog.Update{}, os.ErrExist
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
	path = filepath.Join(path, modules.SiaDirExtension)
	update, err := createMetadataUpdate(path, md)
	return md, update, err
}

// loadSiaDirMetadata loads the directory metadata from disk.
func loadSiaDirMetadata(path string, deps modules.Dependencies) (md Metadata, err error) {
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

// LoadSiaDir loads the directory metadata from disk
func LoadSiaDir(path string, deps modules.Dependencies, wal *writeaheadlog.WAL) (sd *SiaDir, err error) {
	sd = &SiaDir{
		deps:       deps,
		staticPath: path,
		wal:        wal,
	}
	sd.metadata, err = loadSiaDirMetadata(filepath.Join(path, modules.SiaDirExtension), modules.ProdDependencies)
	return sd, err
}

// delete removes the directory from disk and marks it as deleted. Once the directory is
// deleted, attempting to access the directory will return an error.
func (sd *SiaDir) delete() error {
	update := sd.createDeleteUpdate()
	err := sd.createAndApplyTransaction(update)
	sd.deleted = true
	return err
}

// Delete removes the directory from disk and marks it as deleted. Once the directory is
// deleted, attempting to access the directory will return an error.
func (sd *SiaDir) Delete() error {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.delete()
}

// Deleted returns the deleted field of the siaDir
func (sd *SiaDir) Deleted() bool {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.deleted
}

// DirReader creates a io.ReadCloser that can be used to read the raw SiaDir
// from disk.
func (sd *SiaDir) DirReader() (*DirReader, error) {
	sd.mu.Lock()
	if sd.deleted {
		sd.mu.Unlock()
		return nil, errors.New("can't copy deleted SiaDir")
	}
	// Open file.
	path := filepath.Join(sd.staticPath, modules.SiaDirExtension)
	f, err := os.Open(path)
	if err != nil {
		sd.mu.Unlock()
		return nil, err
	}
	return &DirReader{
		sd: sd,
		f:  f,
	}, nil
}

// Metadata returns the metadata of the SiaDir
func (sd *SiaDir) Metadata() Metadata {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.metadata
}

// Path returns the SiaPath of the SiaDir
func (sd *SiaDir) Path() string {
	return sd.staticPath
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
