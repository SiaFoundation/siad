package dependencies

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// DependencyDisableCloseUploadEntry prevents SiaFileEntries in the upload code
// from being closed.
type DependencyDisableCloseUploadEntry struct {
	modules.ProductionDependencies
}

// Disrupt prevents SiafileEntries in the upload code from being closed.
func (d *DependencyDisableCloseUploadEntry) Disrupt(s string) bool {
	return s == "disableCloseUploadEntry"
}

// DependencyDisableRepairAndHealthLoops prevents the background loops for
// repairs and updating directory metadata from running. This includes
// threadedUploadAndRepair, threadedStuckLoop, and threadedUpdateRenterHealth
type DependencyDisableRepairAndHealthLoops struct {
	modules.ProductionDependencies
}

// Disrupt will prevent the repair and health loops from running
func (d *DependencyDisableRepairAndHealthLoops) Disrupt(s string) bool {
	return s == "DisableRepairAndHealthLoops"
}
