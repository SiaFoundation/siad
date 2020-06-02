package dependencies

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// DependencyCustomNebulousAddress will use a custom address for the Nebulous
// address when processing a fee.
type DependencyCustomNebulousAddress struct {
	modules.ProductionDependencies

	address types.UnlockHash
	mu      sync.Mutex
}

// NebulousAddress returns the custom address of the dependency.
func (d *DependencyCustomNebulousAddress) NebulousAddress() types.UnlockHash {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.address
}

// SetAddress sets the address field of the dependency.
func (d *DependencyCustomNebulousAddress) SetAddress(addr types.UnlockHash) {
	d.mu.Lock()
	d.address = addr
	d.mu.Unlock()
}
