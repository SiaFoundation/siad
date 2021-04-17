package host

import (
	"errors"
	"time"

	"go.sia.tech/siad/types"
)

var (
	// ErrObligationLocked is returned if the file contract being requested is
	// currently locked. The lock can be in place if there is a storage proof
	// being submitted, if there is another renter altering the contract, or if
	// there have been network connections with have not resolved yet.
	ErrObligationLocked = errors.New("the requested file contract is currently locked")
)

// managedLockStorageObligation puts a storage obligation under lock in the
// host.
func (h *Host) managedLockStorageObligation(soid types.FileContractID) {
	// Check if a lock has been created for this storage obligation. If not,
	// create one. The map must be accessed under lock, but the request for the
	// storage lock must not be made under lock.
	h.mu.Lock()
	lo, exists := h.lockedStorageObligations[soid]
	if !exists {
		lo = &lockedObligation{}
		h.lockedStorageObligations[soid] = lo
	}
	lo.n++
	h.mu.Unlock()

	lo.mu.Lock()
}

// managedTryLockStorageObligation attempts to put a storage obligation under
// lock, returning an error if the lock cannot be obtained.
func (h *Host) managedTryLockStorageObligation(soid types.FileContractID, timeout time.Duration) error {
	// Check if a lock has been created for this storage obligation. If not,
	// create one. The map must be accessed under lock, but the request for the
	// storage lock must not be made under lock.
	h.mu.Lock()
	lo, exists := h.lockedStorageObligations[soid]
	if !exists {
		lo = &lockedObligation{}
		h.lockedStorageObligations[soid] = lo
	}
	lo.n++
	h.mu.Unlock()

	if lo.mu.TryLockTimed(timeout) {
		return nil
	}
	// Locking failed. Decrement the counter again.
	h.mu.Lock()
	lo.n--
	if lo.n == 0 {
		delete(h.lockedStorageObligations, soid)
	}
	h.mu.Unlock()
	return ErrObligationLocked
}

// managedUnlockStorageObligation takes a storage obligation out from under lock
// in the host.
func (h *Host) managedUnlockStorageObligation(soid types.FileContractID) {
	// Check if a lock has been created for this storage obligation. The map
	// must be accessed under lock, but the request for the unlock must not
	// be made under lock.
	h.mu.Lock()
	lo, exists := h.lockedStorageObligations[soid]
	if !exists {
		h.log.Critical(errObligationUnlocked)
		return
	}
	lo.n--
	if lo.n == 0 {
		delete(h.lockedStorageObligations, soid)
	}
	h.mu.Unlock()

	lo.mu.Unlock()
}
