package siadir

import (
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	// updateDeleteName is the name of a siaDir update that deletes the
	// specified metadata file.
	updateDeleteName = "SiaDirDelete"

	// updateMetadataName is the name of a siaDir update that inserts new
	// information into the metadata file
	updateMetadataName = "SiaDirMetadata"

	// threadDepth is how deep the ThreadType will track calling files and
	// calling lines
	threadDepth = 3

	// dirListRoutines is the number of goroutines used in DirList to load siadir
	// metadata from disk
	dirListRoutines = 20
)

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
