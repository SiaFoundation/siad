package siafile

import (
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	// pageSize is the size of a physical page on disk.
	pageSize = 4096

	// defaultReservedMDPages is the number of pages we reserve for the
	// metadata when we create a new siaFile. Should the metadata ever grow
	// larger than that, new pages are added on demand.
	defaultReservedMDPages = 1

	// updateInsertName is the name of a siaFile update that inserts data at a specific index.
	updateInsertName = "Insert"
)

// IsSiaFileUpdate is a helper method that makes sure that a wal update belongs
// to the SiaFile package.
func IsSiaFileUpdate(update writeaheadlog.Update) bool {
	switch update.Name {
	case updateInsertName:
		return true
	default:
		return false
	}
}
