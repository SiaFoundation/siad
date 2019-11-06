package dependencies

import (
	"sync"

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

// DependencyToggleWatchdogBroadcast can toggle the watchdog's ability to
// broadcast transactions.
type DependencyToggleWatchdogBroadcast struct {
	mu                 sync.Mutex
	broadcastsDisabled bool
	modules.ProductionDependencies
}

func (d *DependencyToggleWatchdogBroadcast) DisableWatchdogBroadcast(disable bool) {
	d.mu.Lock()
	d.broadcastsDisabled = disable
	d.mu.Unlock()
}

// Disrupt will prevent watchdog from rebroadcasting transactions when
// broadcasting is disabled.
func (d *DependencyToggleWatchdogBroadcast) Disrupt(s string) bool {
	d.mu.Lock()
	disabled := d.broadcastsDisabled
	d.mu.Unlock()

	return disabled && (s == "DisableWatchdogBroadcast")
}
