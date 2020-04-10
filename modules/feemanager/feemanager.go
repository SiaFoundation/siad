package feemanager

import (
	"encoding/hex"
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/threadgroup"
)

const (
	// nebAddressStr is the string representation of the Nebulous Wallet Address
	nebAddressStr = "0e38c99857408b7d2604a1ce20c6776c9e42b105b2de9b0cd1e75baad5ec39e5c5308be19cea"
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
	// out to
	nebAddress = initNebAddress()
)

// initNebAddress initializes the Nebulous wallet address
func initNebAddress() types.UnlockHash {
	var addr types.UnlockHash
	_, err := fmt.Sscan(nebAddressStr, &addr)
	if err != nil {
		panic(err)
	}
	return addr
}

type (
	// FeeManager is responsible for tracking any application fees that are
	// being charged to this siad instance
	FeeManager struct {
		// fees are all the fees that are currently charging this siad instance
		fees map[modules.FeeUID]*appFee

		// currentPayout is how much the payout is going to be for this period
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

	// appFee is the struct that contains information about a fee submitted by
	// an application to the FeeManager
	appFee struct {
		// Address of the developer wallet
		Address types.UnlockHash `json:"address"`

		// Amount of SC that the Fee is for
		Amount types.Currency `json:"amount"`

		// AppUID is a unique Application ID that the fee is for
		AppUID modules.AppUID `json:"appuid"`

		// Cancelled indicates whether or not this fee was cancelled
		Cancelled bool `json:"cancelled"`

		// Offset is the fee's offset in the persist file on disk
		Offset int64 `json:"offset"`

		// PayoutHeight is the blockheight at which the fee will be submitted
		PayoutHeight types.BlockHeight `json:"payoutheight"`

		// Recurring indicates whether or not this fee is a recurring fee and
		// will be charged in the next period as well
		Recurring bool `json:"recurring"`

		// UID is a unique identifier for the Fee
		UID modules.FeeUID `json:"uid"`
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
		fees: make(map[modules.FeeUID]*appFee),

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

	// Subscribe to the consensus set.
	err = cs.ConsensusSetSubscribe(fm, modules.ConsensusChangeRecent, fm.staticTG.StopChan())
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

// PaidFees returns all the paid fees that are being tracked by the FeeManager
func (fm *FeeManager) PaidFees() ([]modules.AppFee, error) {
	// Add thread group
	if err := fm.staticTG.Add(); err != nil {
		return nil, err
	}
	defer fm.staticTG.Done()

	return fm.managedPaidFees()
}

// PendingFees returns all the pending fees that are being tracked by the
// FeeManager
func (fm *FeeManager) PendingFees() ([]modules.AppFee, error) {
	// Add thread group
	if err := fm.staticTG.Add(); err != nil {
		return nil, err
	}
	defer fm.staticTG.Done()

	return fm.managedPendingFees(), nil
}

// SetFee sets a fee for the FeeManager to manage
func (fm *FeeManager) SetFee(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, recurring bool) error {
	if err := fm.staticTG.Add(); err != nil {
		return err
	}
	defer fm.staticTG.Done()
	return fm.callSetFee(address, amount, appUID, recurring)
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

// managedPaidFees returns all the paid fees that are being tracked by the
// FeeManager
func (fm *FeeManager) managedPaidFees() ([]modules.AppFee, error) {
	// Get all fees from disk
	allFees, err := fm.callLoadAllFees()
	if err != nil {
		return nil, err
	}

	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Sort out any cancelled or pending fees
	var paid []modules.AppFee
	for _, fee := range allFees {
		// Skip any cancelled fees
		if fee.Cancelled {
			continue
		}
		// Skip any pending fees
		_, ok := fm.fees[fee.UID]
		if ok {
			continue
		}
		paid = append(paid, modules.AppFee{
			Address:   fee.Address,
			Amount:    fee.Amount,
			AppUID:    fee.AppUID,
			Recurring: fee.Recurring,
			UID:       fee.UID,
		})
	}
	return paid, nil
}

// managedPendingFees returns all the pending fees that are being tracked by the
// FeeManager
func (fm *FeeManager) managedPendingFees() []modules.AppFee {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	var pendingFees []modules.AppFee
	for _, fee := range fm.fees {
		pendingFees = append(pendingFees, modules.AppFee{
			Address:   fee.Address,
			Amount:    fee.Amount,
			AppUID:    fee.AppUID,
			Recurring: fee.Recurring,
			UID:       fee.UID,
		})
	}
	return pendingFees
}
