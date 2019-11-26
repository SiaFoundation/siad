package dependencies

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// HostRejectAllSessionLocks is a dependency injection for the host that will
// cause the host to reject all contracts as though they do not exist.
type HostRejectAllSessionLocks struct {
	modules.ProductionDependencies
}

// Disrupt will interpret a signal from the host and tell the host to pretend it
// has no record of the contract.
func (d *HostRejectAllSessionLocks) Disrupt(s string) bool {
	return s == "loopLockNoRecordOfThatContract"
}

// NewDependencyHostDiskTrouble creates a new dependency that disrupts storage
// folder operations due to disk trouble
func NewDependencyHostDiskTrouble() *DependencyInterruptOnceOnKeyword {
	return newDependencyInterruptOnceOnKeyword("diskTrouble")
}
