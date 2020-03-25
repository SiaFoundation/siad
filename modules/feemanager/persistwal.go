package feemanager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	// updateNameInsert is the name of a feemanager update that inserts a fee
	// into the fee persist file.
	updateNameInsert = "FeeManagerInsertFee"

	// updateNamePersist is the name of a feemanager update that writes out the
	// feemanager persistence file.
	updateNamePersist = "FeeManagerPersist"
)

// createInsertUpdate creates a writeaheadlog update for inserting a fee to the
// fee persist file
func createInsertUpdate(fee modules.AppFee) (writeaheadlog.Update, error) {
	// Marshal the fee
	data, err := modules.MarshalFee(fee)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "unable to marshal fee")
	}

	return createInsertUpdateFromRaw(data, fee.Offset)
}

// createInsertUpdateFromRaw creates a writeaheadlog update for inserting a fee
// to the fee persist file from the raw marshalled fee bytes and the offset
func createInsertUpdateFromRaw(data []byte, offset int64) (writeaheadlog.Update, error) {
	// Create update
	return writeaheadlog.Update{
		Name:         updateNameInsert,
		Instructions: encoding.MarshalAll(data, offset),
	}, nil
}

// createPersistUpdate creates a writeaheadlog update for writing out the
// FeeManager persist file
func createPersistUpdate(persist persistence) (writeaheadlog.Update, error) {
	// Encode persist data
	data, err := json.Marshal(persist)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "unable to marshal persist object")
	}
	// Create update
	return writeaheadlog.Update{
		Name:         updateNamePersist,
		Instructions: data,
	}, nil
}

// isFeeManagerUpdate is a helper method that makes sure that a wal update
// belongs to the FeeManager package.
func isFeeManagerUpdate(update writeaheadlog.Update) bool {
	switch update.Name {
	case updateNameInsert, updateNamePersist:
		return true
	default:
		return false
	}
}

// readInsertUpdate unmarshals the insert update's instructions and returns the
// data encoded in the instructions.
func readInsertUpdate(update writeaheadlog.Update) (data []byte, offset int64, err error) {
	if update.Name != updateNameInsert {
		err = errors.New("readInsertUpdate can only read insert updates")
		build.Critical(err)
		return
	}
	err = encoding.UnmarshalAll(update.Instructions, &data, &offset)
	return
}

// applyUpdates applies a number of writeaheadlog updates
func (fm *FeeManager) applyUpdates(updates ...writeaheadlog.Update) error {
	// Open the FeeManager Persistence file. This file is written out fully each
	// time.
	persistFilePath := filepath.Join(fm.staticPersistDir, persistFilename)
	persistFile, err := fm.staticDeps.OpenFile(persistFilePath, os.O_RDWR|os.O_TRUNC|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open feemanger persistence file")
	}

	// Open up the Fee persist file. This file is append open.
	feeFilePath := filepath.Join(fm.staticPersistDir, feePersistFilename)
	feeFile, err := fm.staticDeps.OpenFile(feeFilePath, os.O_WRONLY|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open fee persistence file")
	}
	defer func() {
		if err == nil {
			// If no error occurred we sync and close the file.
			err = errors.Compose(persistFile.Sync(), persistFile.Close(), feeFile.Sync(), feeFile.Close())
		} else {
			// Otherwise we still need to close the file.
			err = errors.Compose(err, persistFile.Close(), feeFile.Close())
		}
	}()

	// Apply updates.
	for _, u := range updates {
		err := func() error {
			switch u.Name {
			case updateNameInsert:
				return fm.readAndApplyInsertUpdate(feeFile, u)
			case updateNamePersist:
				return fm.readAndApplyPersistUpdate(persistFile, u)
			default:
				return fmt.Errorf("Update not recognized: %v", u.Name)
			}
		}()
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// createAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func (fm *FeeManager) createAndApplyTransaction(updates ...writeaheadlog.Update) error {
	// Create the writeaheadlog transaction.
	txn, err := fm.staticWal.NewTransaction(updates)
	if err != nil {
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Apply the updates.
	if err := fm.applyUpdates(updates...); err != nil {
		return errors.AddContext(err, "failed to apply updates")
	}
	// Updates are applied. Let the writeaheadlog know.
	if err := txn.SignalUpdatesApplied(); err != nil {
		return errors.AddContext(err, "failed to signal that updates are applied")
	}
	return nil
}

// readAndApplyInsertUpdate reads the insert update and then applies it.
func (fm *FeeManager) readAndApplyInsertUpdate(file modules.File, update writeaheadlog.Update) error {
	// Decode update.
	data, offset, err := readInsertUpdate(update)
	if err != nil {
		return errors.AddContext(err, "unable to read update")
	}

	// Write to the file
	n, err := file.WriteAt(data, offset)
	if err != nil {
		return err
	}
	if n < len(data) {
		return fmt.Errorf("update was only applied partially - %v / %v", n, len(data))
	}
	return nil
}

// readAndApplyPersistUpdate reads the persist update and then applies it.
func (fm *FeeManager) readAndApplyPersistUpdate(file modules.File, update writeaheadlog.Update) error {
	// Write to the file
	data := update.Instructions
	n, err := file.Write(data)
	if err != nil {
		return err
	}
	if n < len(data) {
		return fmt.Errorf("update was only applied partially - %v / %v", n, len(data))
	}
	return nil
}
