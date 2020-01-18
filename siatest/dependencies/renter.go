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

// DependencyFailUploadStreamFromReader prevents SiaFileEntries in the upload code
// from being closed.
type DependencyFailUploadStreamFromReader struct {
	modules.ProductionDependencies
}

// Disrupt prevents SiafileEntries in the upload code from being closed.
func (d *DependencyFailUploadStreamFromReader) Disrupt(s string) bool {
	return s == "failUploadStreamFromReader"
}

// DependencyToggleWatchdogBroadcast can toggle the watchdog's ability to
// broadcast transactions.
type DependencyToggleWatchdogBroadcast struct {
	mu                 sync.Mutex
	broadcastsDisabled bool
	modules.ProductionDependencies
}

// DisableWatchdogBroadcast will prevent the watchdog from broadcating
// transactions.
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

// DependencyHighMinHostScore returns high minimum-allowed host score for GFR to
// help simulate churn related to low scoring hosts.
type DependencyHighMinHostScore struct {
	mu                  sync.Mutex
	forcingHighMinScore bool
	modules.ProductionDependencies
}

// Disrupt causes a high min-score for GFR to be returned.
func (d *DependencyHighMinHostScore) Disrupt(s string) bool {
	d.mu.Lock()
	forcingHighMinScore := d.forcingHighMinScore
	d.mu.Unlock()
	return forcingHighMinScore && s == "HighMinHostScore"
}

// ForceHighMinHostScore causes the dependency disrupt to activate.
func (d *DependencyHighMinHostScore) ForceHighMinHostScore(force bool) {
	d.mu.Lock()
	d.forcingHighMinScore = force
	d.mu.Unlock()
}
