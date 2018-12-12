package siadir

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// ApplyUpdates  applies a number of writeaheadlog updates to the corresponding
// SiaDir. This method can apply updates from different SiaDirs and should only
// be run before the SiaDirs are loaded from disk right after the startup of
// siad. Otherwise we might run into concurrency issues.
func ApplyUpdates(updates ...writeaheadlog.Update) error {
	// Apply updates.
	for _, u := range updates {
		err := applyUpdate(u)
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// applyUpdate applies the wal update
func applyUpdate(update writeaheadlog.Update) error {
	switch update.Name {
	case updateDeleteName:
		// Delete from disk
		err := os.RemoveAll(readDeleteUpdate(update))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	case updateMetadataName:
		// Decode update.
		metadata, err := readMetadataUpdate(update)
		if err != nil {
			return err
		}
		// Write data.
		if err := metadata.save(); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("Update not recognized: %v", update.Name)
	}
}

// readDeleteUpdate unmarshals the update's instructions and returns the
// encoded path.
func readDeleteUpdate(update writeaheadlog.Update) string {
	if !isDeleteUpdate(update) {
		err := errors.New("readDeleteUpdate can't read non-delete updates")
		build.Critical(err)
		return ""
	}
	return string(update.Instructions)
}

// createAndSaveAllMetadataUpdates creates a path on disk to the provided
// siaPath and make sure that all the parent directories have metadata files.
func createAndSaveAllMetadataUpdates(siaPath, rootDir string) ([]writeaheadlog.Update, error) {
	// Create path to directory
	if err := os.MkdirAll(filepath.Join(rootDir, siaPath), 0700); err != nil {
		return nil, err
	}

	// Create metadata
	var updates []writeaheadlog.Update
	for {
		siaPath = filepath.Dir(siaPath)
		if siaPath == "." {
			siaPath = ""
		}
		_, update, err := createDirMetadata(siaPath, rootDir)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update...)
		if siaPath == "" {
			break
		}
	}
	return updates, nil
}

// createMetadataUpdate is a helper method which creates a writeaheadlog update for
// updating the siaDir metadata
func createMetadataUpdate(data []byte) writeaheadlog.Update {
	// Create update
	return writeaheadlog.Update{
		Name:         updateMetadataName,
		Instructions: encoding.MarshalAll(data),
	}
}

// readMetadataUpdate unmarshals the update's instructions and returns the
// metadata encoded in the instructions.
func readMetadataUpdate(update writeaheadlog.Update) (metadata siaDirMetadata, err error) {
	if !isMetadataUpdate(update) {
		err = errors.New("readMetadataUpdate can't read non-metadata updates")
		build.Critical(err)
		return
	}
	var data []byte
	err = encoding.UnmarshalAll(update.Instructions, &data)
	if err != nil {
		return siaDirMetadata{}, err
	}
	err = json.Unmarshal(data, &metadata)
	return
}

// saveMetadataUpdate save the metadata
func saveMetadataUpdate(md siaDirMetadata) ([]writeaheadlog.Update, error) {
	// Marshal the metadata.
	metadata, err := json.Marshal(md)
	if err != nil {
		return nil, err
	}
	// Otherwise we can create and return the updates.
	return []writeaheadlog.Update{createMetadataUpdate(metadata)}, nil
}

// applyUpdates applies updates to the SiaDir.
func (sd *SiaDir) applyUpdates(updates ...writeaheadlog.Update) (err error) {
	// Apply updates.
	for _, u := range updates {
		err := applyUpdate(u)
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// createAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func (sd *SiaDir) createAndApplyTransaction(updates ...writeaheadlog.Update) error {
	// This should never be called on a deleted directory.
	if sd.deleted {
		return errors.New("shouldn't apply updates on deleted directory")
	}
	// Create the writeaheadlog transaction.
	txn, err := sd.wal.NewTransaction(updates)
	if err != nil {
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Apply the updates.
	if err := sd.applyUpdates(updates...); err != nil {
		return errors.AddContext(err, "failed to apply updates")
	}
	// Updates are applied. Let the writeaheadlog know.
	if err := txn.SignalUpdatesApplied(); err != nil {
		return errors.AddContext(err, "failed to signal that updates are applied")
	}
	return nil
}

// createDeleteUpdate is a helper method that creates a writeaheadlog for
// deleting a directory.
func (sd *SiaDir) createDeleteUpdate() writeaheadlog.Update {
	return writeaheadlog.Update{
		Name:         updateDeleteName,
		Instructions: []byte(filepath.Join(sd.staticMetadata.RootDir, sd.staticMetadata.SiaPath)),
	}
}

// saveDir saves the whole SiaDir atomically.
func (sd *SiaDir) saveDir() error {
	metadataUpdates, err := sd.saveMetadataUpdate()
	if err != nil {
		return err
	}
	return sd.createAndApplyTransaction(metadataUpdates...)
}

// saveMetadataUpdate saves the metadata of the SiaDir
func (sd *SiaDir) saveMetadataUpdate() ([]writeaheadlog.Update, error) {
	return saveMetadataUpdate(sd.staticMetadata)
}
