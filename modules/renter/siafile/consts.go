package siafile

import (
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// pageSize is the size of a physical page on disk.
	pageSize = 4096

	// defaultReservedMDPages is the number of pages we reserve for the
	// metadata when we create a new siaFile. Should the metadata ever grow
	// larger than that, new pages are added on demand.
	defaultReservedMDPages = 1

	// siaFileUpdateName is the name of a siaFile update.
	siaFileUpdateName = "SiaFile"
)

var (
	// fileRoot is the subfolder of the renter dir in which the SiaFiles are
	// stored. They are stored within the same directory structure they are
	// uploaded with to Sia.
	fileRoot = filepath.Join(modules.RenterDir, "files")
)
