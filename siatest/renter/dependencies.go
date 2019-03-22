package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// dependencyBlockScan blocks the scan progress of the hostdb until Scan is
// called on the dependency.
type dependencyBlockScan struct {
	modules.ProductionDependencies
	closed bool
	c      chan struct{}
}

// dependencyDisableCloseUploadEntry prevents SiaFileEntries in the upload code
// from being closed.
type dependencyDisableCloseUploadEntry struct {
	modules.ProductionDependencies
}

// dependencyDisableContractRecovery prents recoverable contracts from being
// recovered in threadedContractMaintenance.
type dependencyDisableContractRecovery struct {
	modules.ProductionDependencies
}

// dependencyDisableRecoveryStatusReset prevents the fields scanInProgress and
// atomicRecoveryScanHeight from being reset after the scan is done.
type dependencyDisableRecoveryStatusReset struct {
	modules.ProductionDependencies
}

// dependencyDisableRenewal prevents contracts from being renewed.
type dependencyDisableRenewal struct {
	modules.ProductionDependencies
}

// Disrupt will block the scan progress of the hostdb. The scan can be started
// by calling Scan on the dependency.
func (d *dependencyBlockScan) Disrupt(s string) bool {
	if d.c == nil {
		d.c = make(chan struct{})
	}
	if s == "BlockScan" {
		<-d.c
	}
	return false
}

// Disrupt prevents contracts from being recovered in
// threadedContractMaintenance.
func (d *dependencyDisableContractRecovery) Disrupt(s string) bool {
	return s == "DisableContractRecovery"
}

// Disrupt prevents SiafileEntries in the upload code from being closed.
func (d *dependencyDisableCloseUploadEntry) Disrupt(s string) bool {
	return s == "disableCloseUploadEntry"
}

// Disrupt will prevent the fields scanInProgress and atomicRecoveryScanHeight
// from being reset after the scan is done.
func (d *dependencyDisableRecoveryStatusReset) Disrupt(s string) bool {
	return s == "disableRecoveryStatusReset"
}

// Disrupt will prevent contracts from being renewed.
func (d *dependencyDisableRenewal) Disrupt(s string) bool {
	return s == "disableRenew"
}

// Scan resumes the blocked scan.
func (d *dependencyBlockScan) Scan() {
	if d.closed {
		return
	}
	close(d.c)
	d.closed = true
}

// newDependencyInterruptContractSaveToDiskAfterDeletion creates a new
// dependency that interrupts the contract being saved to disk after being
// removed from static contracts
func newDependencyInterruptContractSaveToDiskAfterDeletion() *siatest.DependencyInterruptOnceOnKeyword {
	return siatest.NewDependencyInterruptOnceOnKeyword("InterruptContractSaveToDiskAfterDeletion")
}

// newDependencyInterruptDownloadBeforeSendingRevision creates a new dependency
// that interrupts the download on the renter side before sending the signed
// revision to the host.
func newDependencyInterruptDownloadBeforeSendingRevision() *siatest.DependencyInterruptOnceOnKeyword {
	return siatest.NewDependencyInterruptOnceOnKeyword("InterruptDownloadBeforeSendingRevision")
}

// newDependencyInterruptDownloadAfterSendingRevision creates a new dependency
// that interrupts the download on the renter side right after receiving the
// signed revision from the host.
func newDependencyInterruptDownloadAfterSendingRevision() *siatest.DependencyInterruptOnceOnKeyword {
	return siatest.NewDependencyInterruptOnceOnKeyword("InterruptDownloadAfterSendingRevision")
}

// newDependencyInterruptUploadBeforeSendingRevision creates a new dependency
// that interrupts the upload on the renter side before sending the signed
// revision to the host.
func newDependencyInterruptUploadBeforeSendingRevision() *siatest.DependencyInterruptOnceOnKeyword {
	return siatest.NewDependencyInterruptOnceOnKeyword("InterruptUploadBeforeSendingRevision")
}

// newDependencyInterruptUploadAfterSendingRevision creates a new dependency
// that interrupts the upload on the renter side right after receiving the
// signed revision from the host.
func newDependencyInterruptUploadAfterSendingRevision() *siatest.DependencyInterruptOnceOnKeyword {
	return siatest.NewDependencyInterruptOnceOnKeyword("InterruptUploadAfterSendingRevision")
}
