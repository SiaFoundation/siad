package siadir

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
)

type (
	// SiaDir contains the metadata information about a renter directory
	SiaDir struct {
		metadata Metadata

		// path is the path of the SiaDir folder.
		path string

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

// Deleted returns the deleted field of the siaDir
func (sd *SiaDir) Deleted() bool {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.deleted
}

// Metadata returns the metadata of the SiaDir
func (sd *SiaDir) Metadata() Metadata {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.metadata
}

// Path returns the path of the SiaDir on disk.
func (sd *SiaDir) Path() string {
	sd.mu.Lock()
	defer sd.mu.Unlock()
	return sd.path
}
