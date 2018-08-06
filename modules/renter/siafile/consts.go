package siafile

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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
	updateInsertName = "SiaFile-Insert"

	// updateDeleteName is the name of a siaFile update that deletes the
	// specified file.
	updateDeleteName = "SiaFile-Delete"
)

var (
	// ecReedSolomon is the marshaled type of the reed solomon coder.
	ecReedSolomon = [4]byte{0, 0, 0, 1}

	// Erasure-coded piece size
	pieceSize = modules.SectorSize - crypto.TwofishOverhead
)

// IsSiaFileUpdate is a helper method that makes sure that a wal update belongs
// to the SiaFile package.
func IsSiaFileUpdate(update writeaheadlog.Update) bool {
	switch update.Name {
	case updateInsertName:
		return true
	case updateDeleteName:
		return true
	default:
		return false
	}
}
