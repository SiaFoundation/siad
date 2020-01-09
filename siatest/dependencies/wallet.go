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

// DependencyDisableAsyncUnlock will prevent the wallet to catch up to consensus
// after unlocking it.
type DependencyDisableAsyncUnlock struct {
	modules.ProductionDependencies
}

// Disrupt will prevent the wallet to catch up to consensus after unlocking it.
func (d *DependencyDisableAsyncUnlock) Disrupt(s string) bool {
	return s == "DisableAsyncUnlock"
}
