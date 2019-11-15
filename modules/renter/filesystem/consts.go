package filesystem

import "gitlab.com/NebulousLabs/writeaheadlog"

const (
	// updateDeleteDirName is the name of a filesystem update that deletes the
	// specified directory metadata file.
	updateDeleteDirName = "SiaDirDelete"

	// updateDirMetadataName is the name of a siaDir update that inserts new
	// information into the siadir metadata file
	updateDirMetadataName = "SiaDirMetadata"

	// threadDepth is how deep the ThreadType will track calling files and
	// calling lines
	threadDepth = 3
)

// IsFileSystemUpdate is a helper method that makes sure that a wal update
// belongs to the filesystem package.
func IsFileSystemUpdate(update writeaheadlog.Update) bool {
	switch update.Name {
	case updateDirMetadataName, updateDeleteDirName:
		return true
	default:
		return false
	}
}
