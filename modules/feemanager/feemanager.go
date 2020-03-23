package feemanager

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/threadgroup"
)

type (
	// FeeManager is responsible for tracking any application fees that are
	// being charged to this siad instance
	FeeManager struct {
		// Dependencies
		staticCS     modules.ConsensusSet
		staticWallet modules.Wallet

		// Utilities
		staticDeps       modules.Dependencies
		staticLog        *persist.Logger
		staticPersistDir string
		staticTG         threadgroup.ThreadGroup

		mu sync.RWMutex
	}
)

// Close closes the FeeManager
func (fm *FeeManager) Close() error {
	return fm.staticTG.Stop()
}

// New creates a new FeeManager
func New(cs modules.ConsensusSet, w modules.Wallet, persistDir string) (*FeeManager, error) {
	return NewCustomFeeManager(cs, w, persistDir, "", modules.ProdDependencies)
}

// NewCustomFeeManager creates a new FeeManager using custom dependencies and
// custom server string
func NewCustomFeeManager(cs modules.ConsensusSet, w modules.Wallet, persistDir, serverStr string, deps modules.Dependencies) (*FeeManager, error) {
	return &FeeManager{}, nil
}
