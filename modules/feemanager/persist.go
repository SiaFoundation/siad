package feemanager

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	// feePersistFilename is the filename to be used when persisting the fees on
	// disk
	feePersistFilename = "fees"

	// persistFilename is the filename to be used when persisting FeeManager
	// information on disk
	persistFilename = "feemanager"

	// walFile is the filename of the feemanager's writeaheadlog's file.
	walFile = modules.FeeManagerDir + ".wal"
)

var (
	// logFile is the filename of the FeeManager logger
	logFile = modules.FeeManagerDir + ".staticLog"

	// PayoutInterval is the interval at which the payoutheight is set in the
	// future
	PayoutInterval = build.Select(build.Var{
		Standard: types.BlocksPerMonth,
		Dev:      types.BlocksPerDay,
		Testing:  types.BlocksPerHour,
	}).(types.BlockHeight)
)

// persistence is the structure of the data persisted on disk for the FeeManager
type persistence struct {
	// Persisted information about the FeeManager
	CurrentPayout types.Currency    `json:"currentpayout"`
	MaxPayout     types.Currency    `json:"maxpayout"`
	NextFeeOffset int64             `json:"nextfeeoffset"`
	PayoutHeight  types.BlockHeight `json:"payoutheight"`

	// List of current pending fees
	Fees []modules.AppFee `json:"fees"`
}

// callCancelFee cancels a fee by removing it from the FeeManager's map
func (fm *FeeManager) callCancelFee(feeUID modules.FeeUID) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fee, ok := fm.fees[feeUID]
	if !ok {
		// Fee has already been removed, just return
		return nil
	}

	// Negative Currency check
	if fee.Amount.Cmp(fm.currentPayout) > 0 {
		build.Critical("fee amount is large than the current payout, this should not happen")
		fm.currentPayout = fee.Amount
	}
	fm.currentPayout = fm.currentPayout.Sub(fee.Amount)
	delete(fm.fees, feeUID)
	fee.Cancelled = true

	// Create insert update
	feeUpdate, err := createInsertUpdate(*fee)
	if err != nil {
		return errors.AddContext(err, "unable to create insert update")
	}

	// Create persistence update
	persistUpdate, err := createPersistUpdate(fm.persistData())
	if err != nil {
		return errors.AddContext(err, "unable to create persist update")
	}

	// Apply the updates
	updates := []writeaheadlog.Update{feeUpdate, persistUpdate}
	return fm.createAndApplyTransaction(updates...)
}

// callInitPersist handles all of the persistence initialization, such as
// creating the persistence directory and starting the logger
func (fm *FeeManager) callInitPersist() error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	// Create the persist directories if they do not yet exists
	err := os.MkdirAll(fm.staticPersistDir, 0700)
	if err != nil {
		return err
	}

	// Initialize the logger.
	fm.staticLog, err = persist.NewFileLogger(filepath.Join(fm.staticPersistDir, logFile))
	if err != nil {
		return err
	}
	if err := fm.staticTG.AfterStop(fm.staticLog.Close); err != nil {
		return err
	}

	// Initialize the writeaheadlog.
	options := writeaheadlog.Options{
		StaticLog: fm.staticLog,
		Path:      filepath.Join(fm.staticPersistDir, walFile),
	}
	txns, wal, err := writeaheadlog.NewWithOptions(options)
	if err != nil {
		return err
	}
	if err := fm.staticTG.AfterStop(wal.Close); err != nil {
		return err
	}
	fm.staticWal = wal

	// Apply unapplied wal txns before loading the persistence structure to
	// avoid loading potentially corrupted files.
	if len(txns) > 0 {
		fm.staticLog.Println("Wal initialized", len(txns), "transactions to apply")
	}
	for _, txn := range txns {
		fm.staticLog.Println("applying transaction with", len(txn.Updates), "updates")
		err = fm.applyUpdates(txn.Updates...)
		if err != nil {
			return errors.AddContext(err, "unable to apply wal updates up load")
		}
		err := txn.SignalUpdatesApplied()
		if err != nil {
			return errors.AddContext(err, "unable to signal updates applied")
		}
	}

	// Load the FeeManager Persistence
	err = fm.load()
	if err != nil {
		return err
	}

	return nil
}

// callSetFee sets a fee for the FeeManager to manage
func (fm *FeeManager) callSetFee(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, reoccuring bool) error {
	// Acquire Lock
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Check if we are going to exceed the maxPayout
	newPayout := fm.currentPayout.Add(amount)
	if newPayout.Cmp(fm.maxPayout) > 0 {
		return fmt.Errorf("Cannot set fee as it would cause the MaxPayout of %v to be exceeded", fm.maxPayout.HumanString())
	}

	// Create Fee
	fee := &modules.AppFee{
		Address:    address,
		Amount:     amount,
		AppUID:     appUID,
		Offset:     fm.nextFeeOffset,
		Reoccuring: reoccuring,
		UID:        uniqueID(),
	}

	// Add fee to FeeManager
	_, ok := fm.fees[fee.UID]
	if ok {
		return fmt.Errorf("Fee %v already exists", fee.UID)
	}
	fm.fees[fee.UID] = fee
	fm.currentPayout = newPayout

	// Save the FeeManager
	err := fm.saveFeeAndUpdate(*fee)
	if err != nil {
		return errors.AddContext(err, "unable to save the FeeManager")
	}
	return nil
}

// load loads the FeeManager persistence from disk and creates it if it does not
// exist
func (fm *FeeManager) load() error {
	// Open the FeeManager Persist file.
	file, err := fm.staticDeps.Open(filepath.Join(fm.staticPersistDir, persistFilename))
	if os.IsNotExist(err) {
		// No persistence file exists yet, save to create one
		return fm.save()
	} else if err != nil {
		return errors.AddContext(err, "unable to load persistence")
	}
	defer file.Close()

	// Read the file
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return errors.AddContext(err, "unable to read data from file")
	}

	// Parse the json object.
	var persistData persistence
	err = json.Unmarshal(bytes, &persistData)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshall data")
	}

	// Load persistence data into the FeeManager
	return fm.loadPersistData(persistData)
}

// loadAllFees loads all the fees from the Fee Persist file
func (fm *FeeManager) loadAllFees() ([]modules.AppFee, error) {
	// Open the Fee Persist file
	fileName := filepath.Join(fm.staticPersistDir, feePersistFilename)
	file, err := fm.staticDeps.Open(fileName)
	if err != nil {
		return []modules.AppFee{}, errors.AddContext(err, "unable to load fee persist file")
	}
	defer file.Close()

	// Read the file
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return []modules.AppFee{}, errors.AddContext(err, "unable to read data from file")
	}

	// Unmarshal the fees.
	fees, err := modules.UnmarshalFees(bytes)
	if err != nil {
		return []modules.AppFee{}, errors.AddContext(err, "unable to unmarshal data")
	}
	return fees, nil
}

// loadPersistData loads the persisted data into the FeeManager
func (fm *FeeManager) loadPersistData(persistData persistence) error {
	// Load initial values
	fm.currentPayout = persistData.CurrentPayout
	fm.maxPayout = persistData.MaxPayout
	fm.payoutHeight = persistData.PayoutHeight
	fm.nextFeeOffset = persistData.NextFeeOffset

	// Load Fees
	for i := 0; i < len(persistData.Fees); i++ {
		fee := persistData.Fees[i]
		// Check if data is already loaded
		_, ok := fm.fees[fee.UID]
		if ok {
			return fmt.Errorf("Fee %v already loaded into FeeManager", fee.UID)
		}
		fm.fees[fee.UID] = &fee
	}
	return nil
}

// persistData returns the persisted data in the format to be stored on disk
func (fm *FeeManager) persistData() persistence {
	var fees []modules.AppFee
	for _, fee := range fm.fees {
		fees = append(fees, *fee)
	}
	return persistence{
		CurrentPayout: fm.currentPayout,
		MaxPayout:     fm.maxPayout,
		NextFeeOffset: fm.nextFeeOffset,
		PayoutHeight:  fm.payoutHeight,
		Fees:          fees,
	}
}

// save saves the FeeManager persistence data to disk
func (fm *FeeManager) save() error {
	update, err := createPersistUpdate(fm.persistData())
	if err != nil {
		return errors.AddContext(err, "unable to create persist update")
	}
	return fm.createAndApplyTransaction(update)
}

// saveFeeAndUpdate creates the insert update for the fee, updates the
// nextFeeOffset of the FeeManager, and saves all changes to disk
func (fm *FeeManager) saveFeeAndUpdate(fee modules.AppFee) error {
	// Marshal the fee and create update
	data, err := modules.MarshalFee(fee)
	if err != nil {
		return errors.AddContext(err, "unable to marshal fee")
	}
	feeUpdate, err := createInsertUpdateFromRaw(data, fee.Offset)
	if err != nil {
		return errors.AddContext(err, "unable to create fee update")
	}

	// Update the FeeManager's nextFeeOffset and create the peristence update
	fm.nextFeeOffset += int64(len(data))
	persistUpdate, err := createPersistUpdate(fm.persistData())
	if err != nil {
		return errors.AddContext(err, "unable to create persist update")
	}
	updates := []writeaheadlog.Update{feeUpdate, persistUpdate}
	return fm.createAndApplyTransaction(updates...)
}
