package dependencies

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// HostRejectAllSessionLocks is a dependency injection for the host that will
// cause the host to reject all contracts as though they do not exist.
type HostRejectAllSessionLocks struct {
	started bool
	mu      sync.Mutex

	modules.ProductionDependencies
}

// Disrupt will interpret a signal from the host and tell the host to pretend it
// has no record of the contract.
func (d *HostRejectAllSessionLocks) Disrupt(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.started {
		return false
	}
	return s == "loopLockNoRecordOfThatContract"
}

// StartRejectingLocks will activate the dependency.
func (d *HostRejectAllSessionLocks) StartRejectingLocks() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.started = true
}

// HostExpireEphemeralAccounts is a dependency injection for the host that will
// expire ephemeral accounts as soon as they get pruned
type HostExpireEphemeralAccounts struct {
	modules.ProductionDependencies
}

// Disrupt will interpret a signal from the host and tell the host to force
// expire all ephemeral accounts on the next prune cycle
func (d *HostExpireEphemeralAccounts) Disrupt(s string) bool {
	return s == "expireEphemeralAccounts"
}

// NewDependencyHostDiskTrouble creates a new dependency that disrupts storage
// folder operations due to disk trouble
func NewDependencyHostDiskTrouble() *DependencyInterruptOnceOnKeyword {
	return newDependencyInterruptOnceOnKeyword("diskTrouble")
}

// NewHostMaxEphemeralAccountRiskReached is a dependency injection for the host
// that will ensure the ephemeral account max saved delta is reached by
// persisting with a set latency.
func NewHostMaxEphemeralAccountRiskReached(duration time.Duration) modules.Dependencies {
	return newDependencyAddLatency("errMaxRiskReached", duration)
}
