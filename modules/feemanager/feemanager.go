package feemanager

import (
	"encoding/hex"
	"strings"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/threadgroup"
)

var (
	// defaultMaxPayout is the default maximum amount that can be paid out in a
	// given period
	defaultMaxPayout = types.SiacoinPrecision.Mul64(10e3)

	// Nil dependency errors.
	errNilCS     = errors.New("cannot create FeeManager with nil consensus set")
	errNilWallet = errors.New("cannot create FeeManager with nil wallet")

	// Enforce that FeeManager satisfies the modules.FeeManager interface.
	_ modules.FeeManager = (*FeeManager)(nil)

	// nebAddress is the address that Nebulous's share of the fees will be paid
	// out to.
	//
	// TODO - set
	nebAddress = types.UnlockHash{}
)

type (
	// FeeManager is responsible for tracking any application fees that are
	// being charged to this siad instance
	FeeManager struct {
		// fees are all the fees that are currently charging this siad instance
		fees map[modules.FeeUID]*modules.AppFee

		// currentPayout is how much is going to the payout is going to be for this period
		currentPayout types.Currency

		// maxPayout is the maximum amount that will get paid out per period
		maxPayout types.Currency

		// payoutHeight is the blockheight at which the next payout will be
		// submitted
		payoutHeight types.BlockHeight

		// nextFeeOffset is the offset of the next fee in the fee persist file
		nextFeeOffset int64

		// Dependencies
		staticCS     modules.ConsensusSet
		staticWallet modules.Wallet

		// Utilities
		staticDeps       modules.Dependencies
		staticLog        *persist.Logger
		staticPersistDir string
		staticTG         threadgroup.ThreadGroup
		staticWal        *writeaheadlog.WAL

		mu sync.RWMutex
	}
)

// New creates a new FeeManager
func New(cs modules.ConsensusSet, w modules.Wallet, persistDir string) (*FeeManager, error) {
	return NewCustomFeeManager(cs, w, persistDir, "", modules.ProdDependencies)
}

// NewCustomFeeManager creates a new FeeManager using custom dependencies and
// custom server string
func NewCustomFeeManager(cs modules.ConsensusSet, w modules.Wallet, persistDir, serverStr string, deps modules.Dependencies) (*FeeManager, error) {
	// Check for nil inputs
	if cs == nil {
		return nil, errNilCS
	}
	if w == nil {
		return nil, errNilWallet
	}

	// Create FeeManager
	fm := &FeeManager{
		// Initialize map
		fees: make(map[modules.FeeUID]*modules.AppFee),

		// Set defaults
		maxPayout: defaultMaxPayout,

		// Set Deps
		staticCS:     cs,
		staticWallet: w,

		// Set Utilities
		staticPersistDir: persistDir,
		staticDeps:       deps,
	}

	// Initialize the FeeManager persistence
	err := fm.callInitPersist()
	if err != nil {
		return nil, errors.AddContext(err, "unable to initialize the FeeManager's persistence")
	}

	// Unsubscribe on shutdown
	err = fm.staticTG.OnStop(func() error {
		cs.Unsubscribe(fm)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Subscribe to the consensus set in a separate goroutine.
	done := make(chan struct{})
	defer close(done)
	err = cs.ConsensusSetSubscribe(fm, modules.ConsensusChangeRecent, fm.staticTG.StopChan())
	if err != nil && strings.Contains(err.Error(), threadgroup.ErrStopped.Error()) {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	// Check to see if we are synced and process any Fees
	if fm.staticCS.Synced() {
		// If we are synced set the payoutHeight
		fm.payoutHeight = fm.staticCS.Height() + PayoutInterval
	}

	return fm, nil
}

// uniqueID creates a random unique FeeUID.
func uniqueID() modules.FeeUID {
	return modules.FeeUID(hex.EncodeToString(fastrand.Bytes(20)))
}

// CancelFee cancels a fee by removing it from the FeeManager's map
func (fm *FeeManager) CancelFee(feeUID modules.FeeUID) error {
	// Add thread group
	if err := fm.staticTG.Add(); err != nil {
		return err
	}
	defer fm.staticTG.Done()
	return fm.callCancelFee(feeUID)
}

// Close closes the FeeManager
func (fm *FeeManager) Close() error {
	return fm.staticTG.Stop()
}

// Fees returns all the fees that are being tracked by the FeeManager
func (fm *FeeManager) Fees() (pending []modules.AppFee, paid []modules.AppFee, err error) {
	// Add thread group
	if err := fm.staticTG.Add(); err != nil {
		return nil, nil, err
	}
	defer fm.staticTG.Done()

	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Get all fees from disk
	allFees, err := fm.loadAllFees()
	if err != nil {
		return nil, nil, err
	}

	// Sort in pending and paid
	for _, fee := range allFees {
		// Skip any cancelled fees
		if fee.Cancelled {
			continue
		}
		_, ok := fm.fees[fee.UID]
		if ok {
			pending = append(pending, fee)
		} else {
			paid = append(pending, fee)
		}
	}

	return pending, paid, nil
}

// SetFee sets a fee for the FeeManager to manage
func (fm *FeeManager) SetFee(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, reoccuring bool) error {
	if err := fm.staticTG.Add(); err != nil {
		return err
	}
	defer fm.staticTG.Done()
	return fm.callSetFee(address, amount, appUID, reoccuring)
}

// Settings returns the settings of the FeeManager
func (fm *FeeManager) Settings() (modules.FeeManagerSettings, error) {
	if err := fm.staticTG.Add(); err != nil {
		return modules.FeeManagerSettings{}, err
	}
	defer fm.staticTG.Done()

	fm.mu.Lock()
	defer fm.mu.Unlock()

	return modules.FeeManagerSettings{
		CurrentPayout: fm.currentPayout,
		MaxPayout:     fm.maxPayout,
		PayoutHeight:  fm.payoutHeight,
	}, nil
}
