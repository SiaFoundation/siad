package feemanager

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
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
	// ErrFeeNotFound is returned if a fee is not found in the FeeManager
	ErrFeeNotFound = errors.New("fee not found")

	// logFile is the filename of the FeeManager logger
	logFile = modules.FeeManagerDir + ".log"

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
	Fees []appFee `json:"fees"`
}

// callCancelFee cancels a fee by removing it from the FeeManager's map
func (fm *FeeManager) callCancelFee(feeUID modules.FeeUID) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	fee, ok := fm.fees[feeUID]
	if !ok {
		return ErrFeeNotFound
	}

	// Negative Currency check
	if fee.Amount.Cmp(fm.currentPayout) > 0 {
		build.Critical("fee amount is larger than the current payout, this should not happen")
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
	err := os.MkdirAll(fm.staticPersistDir, modules.DefaultDirPerm)
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

// callLoadAllFees loads all the fees from the Fee Persist file
func (fm *FeeManager) callLoadAllFees() ([]appFee, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Open the Fee Persist file
	fileName := filepath.Join(fm.staticPersistDir, feePersistFilename)
	file, err := fm.staticDeps.Open(fileName)
	if err != nil {
		return []appFee{}, errors.AddContext(err, "unable to load fee persist file")
	}
	defer file.Close()

	// Read the file
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return []appFee{}, errors.AddContext(err, "unable to read data from file")
	}

	// Unmarshal the fees.
	fees, err := unmarshalFees(bytes)
	if err != nil {
		return []appFee{}, errors.AddContext(err, "unable to unmarshal data")
	}
	return fees, nil
}

// callSetFee sets a fee for the FeeManager to manage
func (fm *FeeManager) callSetFee(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, recurring bool) error {
	// Acquire Lock
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Check if we are going to exceed the maxPayout
	newPayout := fm.currentPayout.Add(amount)
	if newPayout.Cmp(fm.maxPayout) > 0 {
		return fmt.Errorf("Cannot set fee with amount of %v as it would cause the MaxPayout of %v to be exceeded", amount.HumanString(), fm.maxPayout.HumanString())
	}

	// Create Fee
	fee := &appFee{
		Address:   address,
		Amount:    amount,
		AppUID:    appUID,
		Offset:    fm.nextFeeOffset,
		Recurring: recurring,
		UID:       uniqueID(),
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
	var fees []appFee
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
func (fm *FeeManager) saveFeeAndUpdate(fee appFee) error {
	// Marshal the fee and create update
	// Create a buffer.
	var buf bytes.Buffer
	err := fee.marshalSia(&buf)
	if err != nil {
		return errors.AddContext(err, "unable to marshal fee")
	}
	feeUpdate, err := createInsertUpdateFromRaw(buf.Bytes(), fee.Offset)
	if err != nil {
		return errors.AddContext(err, "unable to create fee update")
	}

	// Update the FeeManager's nextFeeOffset and create the persistence update
	fm.nextFeeOffset += int64(buf.Len())
	persistUpdate, err := createPersistUpdate(fm.persistData())
	if err != nil {
		return errors.AddContext(err, "unable to create persist update")
	}
	updates := []writeaheadlog.Update{feeUpdate, persistUpdate}
	return fm.createAndApplyTransaction(updates...)
}

// marshalSia implements the encoding.SiaMarshaler interface.
func (fee *appFee) marshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Encode(fee.Address)
	e.Encode(fee.Amount)
	e.Encode(fee.AppUID)
	e.WriteBool(fee.Cancelled)
	e.Encode(fee.Offset)
	e.WriteBool(fee.Recurring)
	e.Encode(fee.UID)
	return e.Err()
}

// unmarshalSia implements the encoding.SiaUnmarshaler interface.
func (fee *appFee) unmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&fee.Address)
	d.Decode(&fee.Amount)
	d.Decode(&fee.AppUID)
	fee.Cancelled = d.NextBool()
	d.Decode(&fee.Offset)
	fee.Recurring = d.NextBool()
	d.Decode(&fee.UID)
	return d.Err()
}

// unmarshalFees unmarshals the sia encoded fees.
func unmarshalFees(raw []byte) (fees []appFee, err error) {
	// Create the buffer.
	r := bytes.NewBuffer(raw)
	// Unmarshal the fees one by one until EOF or a different error occur.
	for {
		var fee appFee
		if err = fee.unmarshalSia(r); err == io.EOF {
			break
		} else if err != nil {
			return nil, errors.AddContext(err, "unable to unmarshal fee")
		}
		fees = append(fees, fee)
	}
	return fees, nil
}
