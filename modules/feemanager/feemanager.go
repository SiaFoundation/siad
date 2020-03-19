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
		w  modules.Wallet
		cs modules.ConsensusSet

		// Utilities
		tg               threadgroup.ThreadGroup
		staticPersistDir string
		log              *persist.Logger
		deps             modules.Dependencies
		mu               sync.RWMutex
	}
)

// Close closes the FeeManager
func (fm *FeeManager) Close() error {
	return fm.tg.Stop()
}

// New creates a new FeeManager
func New(cs modules.ConsensusSet, w modules.Wallet, persistDir string) (*FeeManager, error) {
	return NewCustomFeeManager(cs, w, persistDir, modules.ProdDependencies, "")
}

// NewCustomFeeManager creates a new FeeManager using custom dependencies and
// custom server string
func NewCustomFeeManager(cs modules.ConsensusSet, w modules.Wallet, persistDir string, deps modules.Dependencies, serverStr string) (*FeeManager, error) {
	return &FeeManager{}, nil
}
