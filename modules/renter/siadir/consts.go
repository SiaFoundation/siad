package siadir

import (
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (

	// updateMetadataName is the name of a siaDir update that inserts new
	// information into the metadata file
	updateMetadataName = "SiaDirMetadata"

	// updateDeleteName is the name of a siaDir update that deletes the
	// specified metadata file.
	updateDeleteName = "SiaDirDelete"

	// threadDepth is how deep the ThreadType will track calling files and
	// calling lines
	threadDepth = 3
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
