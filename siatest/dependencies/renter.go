package dependencies

import (
	"sync"

	"go.sia.tech/siad/modules"
)

// DependencyTimeoutProjectDownloadByRoot immediately times out projects that
// try to download a sector by its root.
type DependencyTimeoutProjectDownloadByRoot struct {
	modules.ProductionDependencies
}

// Disrupt forces an immediate timeout for DownloadByRoot projects.
func (d *DependencyTimeoutProjectDownloadByRoot) Disrupt(s string) bool {
	return s == "timeoutProjectDownloadByRoot"
}

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

// DependencyDisableRepairAndHealthLoopsMulti prevents the background loops for
// repairs and updating directory metadata from running in multiple places. This
// includes threadedUploadAndRepair, threadedStuckLoop, and
// threadedUpdateRenterHealth
type DependencyDisableRepairAndHealthLoopsMulti struct {
	modules.ProductionDependencies
}

// Disrupt will prevent the repair and health loops from running
func (d *DependencyDisableRepairAndHealthLoopsMulti) Disrupt(s string) bool {
	return s == "DisableRepairAndHealthLoops" || s == "DisableLHCTCorrection"
}

// DependencyAddUnrepairableChunks will have the repair loop always add chunks
// to the upload heap even if they are unrepairable
type DependencyAddUnrepairableChunks struct {
	modules.ProductionDependencies
}

// Disrupt will prevent the repair and health loops from running
func (d *DependencyAddUnrepairableChunks) Disrupt(s string) bool {
	return s == "DisableRepairAndHealthLoops" || s == "AddUnrepairableChunks"
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

// DependencyDisableUploadGougingCheck ignores the upload gouging check
type DependencyDisableUploadGougingCheck struct {
	modules.ProductionDependencies
}

// Disrupt will prevent the uploads to fail due to upload gouging
func (d *DependencyDisableUploadGougingCheck) Disrupt(s string) bool {
	return s == "DisableUploadGouging"
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

// DependencySkipPrepareNextChunk skips the managedPrepareNextChunk step when
// calling managedPushChunkForRepair.
type DependencySkipPrepareNextChunk struct {
	modules.ProductionDependencies
}

// Disrupt forces an immediate timeout for DownloadByRoot projects.
func (d *DependencySkipPrepareNextChunk) Disrupt(s string) bool {
	return s == "skipPrepareNextChunk"
}

// DependencyDontUpdateStuckStatusOnCleanup will not set the chunk's stuck
// status when cleaning up the upload chunk.
type DependencyDontUpdateStuckStatusOnCleanup struct {
	modules.ProductionDependencies
}

// Disrupt will ignore a failed repair.
func (d *DependencyDontUpdateStuckStatusOnCleanup) Disrupt(s string) bool {
	return s == "DontUpdateChunkStatus"
}

// DependencyToggleDisableDeleteBlockedFiles can toggle the renter's ability to
// delete blocked files in the health loop.
type DependencyToggleDisableDeleteBlockedFiles struct {
	mu             sync.Mutex
	deleteDisabled bool
	modules.ProductionDependencies
}

// DisableDeleteBlockedFiles will toggle the renter's ability to delete blocked
// files.
func (d *DependencyToggleDisableDeleteBlockedFiles) DisableDeleteBlockedFiles(disable bool) {
	d.mu.Lock()
	d.deleteDisabled = disable
	d.mu.Unlock()
}

// Disrupt will prevent the renter from deleting blocked files when delete is
// disabled.
func (d *DependencyToggleDisableDeleteBlockedFiles) Disrupt(s string) bool {
	d.mu.Lock()
	disabled := d.deleteDisabled
	d.mu.Unlock()

	return disabled && (s == "DisableDeleteBlockedFiles")
}
