package feemanager

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/threadgroup"
)

var (
	// Nil dependency errors.
	errNilCS     = errors.New("cannot create FeeManager with nil consensus set")
	errNilWallet = errors.New("cannot create FeeManager with nil wallet")

	// nebAddress is the nebulous address that is used to send Nebulous its cut
	// of the application fees.
	nebAddress = [32]byte{14, 56, 201, 152, 87, 64, 139, 125, 38, 4, 161, 206, 32, 198, 119, 108, 158, 66, 177, 5, 178, 222, 155, 12, 209, 231, 91, 170, 213, 236, 57, 197}

	// Enforce that FeeManager satisfies the modules.FeeManager interface.
	_ modules.FeeManager = (*FeeManager)(nil)
)

var (
	// ErrFeeNotFound is returned if a fee is not found in the FeeManager
	ErrFeeNotFound = errors.New("fee not found")
)

var (
	// PayoutInterval is the interval at which the payoutheight is set in the
	// future
	PayoutInterval = build.Select(build.Var{
		Standard: types.BlocksPerMonth,
		Dev:      types.BlocksPerDay,
		Testing:  types.BlocksPerHour,
	}).(types.BlockHeight)
)

type (
	// FeeManager is responsible for tracking any application fees that are
	// being charged to this siad instance
	FeeManager struct {
		// fees are all the fees that are currently charging this siad instance
		fees map[modules.FeeUID]*modules.AppFee

		common *feeManagerCommon
		mu     sync.RWMutex
	}

	// feeManagerCommon contains fields that are common to all of the subsystems
	// in the fee manager.
	feeManagerCommon struct {
		// Dependencies
		staticCS     modules.ConsensusSet
		staticWallet modules.Wallet

		// Subsystems
		persist *persistSubsystem

		// Utilities
		staticDeps modules.Dependencies
		staticLog  *persist.Logger
		staticTG   threadgroup.ThreadGroup
	}
)

// New creates a new FeeManager.
func New(cs modules.ConsensusSet, w modules.Wallet, persistDir string) (*FeeManager, error) {
	return NewCustomFeeManager(cs, w, persistDir, modules.ProdDependencies)
}

// NewCustomFeeManager creates a new FeeManager using custom dependencies.
func NewCustomFeeManager(cs modules.ConsensusSet, w modules.Wallet, persistDir string, deps modules.Dependencies) (*FeeManager, error) {
	// Check for nil inputs
	if cs == nil {
		return nil, errNilCS
	}
	if w == nil {
		return nil, errNilWallet
	}

	// Create the persist directory.
	err := os.MkdirAll(persistDir, modules.DefaultDirPerm)
	if err != nil {
		return nil, errors.AddContext(err, "unable to make fee manager persist directory")
	}

	// Create the common struct.
	common := &feeManagerCommon{
		staticCS:     cs,
		staticWallet: w,

		staticDeps: deps,
	}
	// Create FeeManager
	fm := &FeeManager{
		fees: make(map[modules.FeeUID]*modules.AppFee),

		common: common,
	}
	// Create the persist subsystem.
	ps := &persistSubsystem{
		staticPersistDir: persistDir,

		common: common,
	}
	common.persist = ps
	// Create the sync coordinator
	sc := &syncCoordinator{
		common: common,
	}
	ps.syncCoordinator = sc

	// Initialize the logger.
	common.staticLog, err = persist.NewFileLogger(filepath.Join(ps.staticPersistDir, LogFile))
	if err != nil {
		return nil, errors.AddContext(err, "unable to create logger")
	}
	if err := common.staticTG.AfterStop(common.staticLog.Close); err != nil {
		return nil, errors.AddContext(err, "unable to set up an AfterStop to close logger")
	}

	// Initialize the FeeManager persistence
	err = fm.callInitPersist()
	if err != nil {
		return nil, errors.AddContext(err, "unable to initialize the FeeManager's persistence")
	}

	// Subscribe to the consensus set.
	err = cs.ConsensusSetSubscribe(fm, modules.ConsensusChangeRecent, common.staticTG.StopChan())
	if err != nil {
		return nil, err
	}
	// Unsubscribe on shutdown
	err = common.staticTG.OnStop(func() error {
		cs.Unsubscribe(fm)
		return nil
	})
	if err != nil {
		return nil, err
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
	if err := fm.common.staticTG.Add(); err != nil {
		return err
	}
	defer fm.common.staticTG.Done()

	// Erase the fee from memory.
	_, exists := fm.fees[feeUID]
	if !exists {
		return ErrFeeNotFound
	}
	fm.mu.Lock()
	delete(fm.fees, feeUID)
	fm.mu.Unlock()

	// Mark a cancellation of the fee on disk.
	return fm.common.persist.callPersistFeeCancelation(feeUID)
}

// Close closes the FeeManager
func (fm *FeeManager) Close() error {
	return fm.common.staticTG.Stop()
}

// PaidFees returns all the paid fees that are being tracked by the FeeManager
func (fm *FeeManager) PaidFees() ([]modules.AppFee, error) {
	// Add thread group
	if err := fm.common.staticTG.Add(); err != nil {
		return nil, err
	}
	defer fm.common.staticTG.Done()

	var paidFees []modules.AppFee
	fm.mu.Lock()
	for _, fee := range fm.fees {
		if fee.PaymentCompleted {
			paidFees = append(paidFees, *fee)
		}
	}
	fm.mu.Unlock()

	// TODO: Sort by timestamp?

	return paidFees, nil
}

// PendingFees returns all the pending fees that are being tracked by the
// FeeManager
func (fm *FeeManager) PendingFees() ([]modules.AppFee, error) {
	// Add thread group
	if err := fm.common.staticTG.Add(); err != nil {
		return nil, err
	}
	defer fm.common.staticTG.Done()

	var pendingFees []modules.AppFee
	fm.mu.Lock()
	for _, fee := range fm.fees {
		if !fee.PaymentCompleted {
			pendingFees = append(pendingFees, *fee)
		}
	}
	fm.mu.Unlock()

	// TODO: Sort by timestamp?

	return pendingFees, nil
}

// AddFee adds a fee to the fee manager.
func (fm *FeeManager) AddFee(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, recurring bool) error {
	if err := fm.common.staticTG.Add(); err != nil {
		return err
	}
	defer fm.common.staticTG.Done()

	fm.common.persist.mu.Lock()
	nextPayoutHeight := fm.common.persist.nextPayoutHeight
	fm.common.persist.mu.Unlock()

	// Create the fee.
	fee := modules.AppFee{
		Address:          address,
		Amount:           amount,
		AppUID:           appUID,
		PaymentCompleted: false,
		PayoutHeight:     nextPayoutHeight + PayoutInterval, // Don't do this in the next payout, but the following.
		Recurring:        recurring,
		Timestamp:        time.Now().Unix(),
		UID:              uniqueID(),
	}

	// Add the fee. Don't need to check for existence because we just generated
	// a unique ID.
	fm.mu.Lock()
	fm.fees[fee.UID] = &fee
	fm.mu.Unlock()

	// Persist the fee.
	return fm.common.persist.callPersistNewFee(fee)
}

// Settings returns the settings of the FeeManager
func (fm *FeeManager) Settings() (modules.FeeManagerSettings, error) {
	if err := fm.common.staticTG.Add(); err != nil {
		return modules.FeeManagerSettings{}, err
	}
	defer fm.common.staticTG.Done()

	fm.common.persist.mu.Lock()
	nextPayoutHeight := fm.common.persist.nextPayoutHeight
	fm.common.persist.mu.Unlock()

	return modules.FeeManagerSettings{
		PayoutHeight: nextPayoutHeight,
	}, nil
}
