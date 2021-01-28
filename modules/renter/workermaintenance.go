package renter

import (
	"sync"
	"time"
)

type (
	// workerMaintenanceState contains variables that help track the state of
	// the worker's (RHP3) maintenance tasks. These tasks need to be successful
	// to ensure a good working interaction with the host using the RHP3
	// protocol.
	//
	// For example, to perform an async download job the worker needs a valid
	// price table and a funded ephemeral account. If the worker fails to
	// successfully acquire this necessary setup, it has to be put on a
	// cooldown.
	workerMaintenanceState struct {
		consecutiveFailures uint64
		cooldownUntil       time.Time
		recentErr           error
		recentErrTime       time.Time

		// If any of these flags is false, the worker has gone into a
		// maintenance cooldown and can only be reset if of these flags are true
		accountRefillSucceeded        bool
		accountSyncSucceeded          bool
		priceTableUpdateSucceeded     bool
		revisionsMismatchFixSucceeded bool

		mu sync.Mutex
	}
)

// incrementMaintenanceCooldown is called if the host has a failed
// interaction with the host's RHP3 protocol, it increments the consecutive
// failures and sets the given error is recent failure.
func (wms *workerMaintenanceState) incrementMaintenanceCooldown(err error) time.Time {
	wms.cooldownUntil = cooldownUntil(wms.consecutiveFailures)
	wms.consecutiveFailures++
	wms.recentErr = err
	wms.recentErrTime = time.Now()
	return wms.cooldownUntil
}

// maintenanceSucceeded indicates whether the worker was able to successfully
// perform its (RHP3) maintenance tasks. If this is the case it can go off of
// the maintenance cooldown.
func (wms *workerMaintenanceState) maintenanceSucceeded() bool {
	return wms.accountSyncSucceeded &&
		wms.accountRefillSucceeded &&
		wms.priceTableUpdateSucceeded &&
		wms.revisionsMismatchFixSucceeded
}

// managedMaintenanceCooldownStatus is a helper function that returns
// information about the maintenance cooldown. It returns whether the worker is
// on cooldown, the recent error string and the cooldown duration.
func (wms *workerMaintenanceState) managedMaintenanceCooldownStatus() (bool, time.Duration, error) {
	wms.mu.Lock()
	defer wms.mu.Unlock()

	onCooldown := time.Now().Before(wms.cooldownUntil)
	var cdDuration time.Duration
	if onCooldown {
		cdDuration = wms.cooldownUntil.Sub(time.Now())
	}

	return onCooldown, cdDuration, wms.recentErr
}

// managedResetMaintenanceCooldown resets the worker's cooldown after a
// successful interaction with the host that involved the RHP3 protocol.
func (wms *workerMaintenanceState) tryResetMaintenanceCooldown() time.Time {
	if wms.maintenanceSucceeded() {
		wms.consecutiveFailures = 0
		wms.cooldownUntil = time.Time{}
	}
	return wms.cooldownUntil
}

// managedMaintenanceRecentError is a helper function that returns the recent
// maintenance error
func (w *worker) managedMaintenanceRecentError() error {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()
	return wms.recentErr
}

// managedMaintenanceSucceeded is a helper function that returns whether the
// maintenance state indicates all maintenance tasks succeeded.
func (w *worker) managedMaintenanceSucceeded() bool {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()

	return wms.maintenanceSucceeded()
}

// managedOnMaintenanceCooldown returns true if the worker's on cooldown due to
// failures in the worker's (RHP3) maintenance tasks.
func (w *worker) managedOnMaintenanceCooldown() bool {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()

	return time.Now().Before(wms.cooldownUntil)
}

// managedTrackAccountRefillErr tracks the outcome of an account refill, this
// method will increment the cooldown on failure, and attempt a cooldown reset
// on success. It returns the cooldown until.
func (w *worker) managedTrackAccountRefillErr(err error) time.Time {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()
	wms.accountRefillSucceeded = err == nil
	if wms.accountRefillSucceeded {
		return wms.tryResetMaintenanceCooldown()
	}
	return wms.incrementMaintenanceCooldown(err)
}

// managedTrackAccountSyncErr tracks the outcome of an account sync, this method
// will increment the cooldown on failure, and attempt a cooldown reset on
// success. It returns the cooldown until.
func (w *worker) managedTrackAccountSyncErr(err error) time.Time {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()
	wms.accountSyncSucceeded = err == nil
	if wms.accountSyncSucceeded {
		return wms.tryResetMaintenanceCooldown()
	}
	return wms.incrementMaintenanceCooldown(err)
}

// managedTrackPriceTableUpdateErr tracks the outcome of a price table update,
// this method will increment the cooldown on failure, and attempt a cooldown
// reset on success. It returns the cooldown until.
func (w *worker) managedTrackPriceTableUpdateErr(err error) time.Time {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()
	wms.priceTableUpdateSucceeded = err == nil
	if wms.priceTableUpdateSucceeded {
		return wms.tryResetMaintenanceCooldown()
	}
	return wms.incrementMaintenanceCooldown(err)
}

// managedTrackRevisionMismatchFix tracks the outcome of an attempted revision
// mismatch fix, this method will increment the cooldown on failure, and attempt
// a cooldown reset on success. It returns the cooldown until.
func (w *worker) managedTrackRevisionMismatchFixErr(err error) {
	wms := w.staticMaintenanceState
	wms.mu.Lock()
	defer wms.mu.Unlock()
	wms.revisionsMismatchFixSucceeded = err == nil
	if wms.revisionsMismatchFixSucceeded {
		wms.tryResetMaintenanceCooldown()
		return
	}
	wms.incrementMaintenanceCooldown(err)
	return
}

// newMaintenanceState will initialize a maintenance state on the worker.
func (w *worker) newMaintenanceState() {
	if w.staticMaintenanceState != nil {
		w.renter.log.Critical("maintenancestate already exists")
	}
	w.staticMaintenanceState = new(workerMaintenanceState)
}
