package dependencies

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// DependencyWithDisableAndEnable adds the ability to disable the dependency
type DependencyWithDisableAndEnable struct {
	disabled bool
	modules.ProductionDependencies
	mu  sync.Mutex
	str string
}

// NewDependencyContractRenewalFail creates a new dependency that simulates
// getting an error while renewing a contract.
func NewDependencyContractRenewalFail() *DependencyWithDisableAndEnable {
	return newDependencywithDisableAndEnable("ContractRenewFail")
}

// newDependencywithDisableAndEnable creates a new
// DependencyWithDisableAndEnable from a given disrupt key.
func newDependencywithDisableAndEnable(str string) *DependencyWithDisableAndEnable {
	return &DependencyWithDisableAndEnable{
		str: str,
	}
}

// Disrupt returns true if the correct string is provided and the dependency has
// not been disabled.
func (d *DependencyWithDisableAndEnable) Disrupt(s string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.disabled && s == d.str
}

// Disable sets the flag to true to make sure that the dependency will fail.
func (d *DependencyWithDisableAndEnable) Disable() {
	d.mu.Lock()
	d.disabled = true
	d.mu.Unlock()
}

// Enable sets the flag to false to make sure that the dependency won't fail.
func (d *DependencyWithDisableAndEnable) Enable() {
	d.mu.Lock()
	d.disabled = false
	d.mu.Unlock()
}
