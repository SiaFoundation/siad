package dependencies

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// DependencyUnsyncedConsensus makes the consensus set appear unsynced
type DependencyUnsyncedConsensus struct {
	modules.ProductionDependencies
}

// Disrupt will prevent the consensus set from appearing synced
func (d *DependencyUnsyncedConsensus) Disrupt(s string) bool {
	return s == "UnsyncedConsensus"
}
