package contractmanager

import (
	"encoding/hex"
	"fmt"
	"path/filepath"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/modules"
)

type (
	// storageFolderRemoval indicates a storage folder that has been removed
	// from the WAL.
	storageFolderRemoval struct {
		Index uint16
		Path  string
	}
)

// commitStorageFolderRemoval will finalize a storage folder removal from the
// contract manager.
func (wal *writeAheadLog) commitStorageFolderRemoval(sfr storageFolderRemoval) {
	// Close any open file handles.
	wal.cm.sectorMu.Lock()
	defer wal.cm.sectorMu.Unlock()
	sf, exists := wal.cm.storageFolders[sfr.Index]
	if exists {
		delete(wal.cm.storageFolders, sfr.Index)
	}
	if exists && sf.metadataFile != nil {
		err := sf.metadataFile.Close()
		if err != nil {
			wal.cm.log.Printf("Error: unable to close metadata file as storage folder %v is removed\n", sf.path)
		}
	}
	if exists && sf.sectorFile != nil {
		err := sf.sectorFile.Close()
		if err != nil {
			wal.cm.log.Printf("Error: unable to close sector file as storage folder %v is removed\n", sf.path)
		}
	}

	// Delete the files.
	err := wal.cm.dependencies.RemoveFile(filepath.Join(sfr.Path, metadataFile))
	if err != nil {
		wal.cm.log.Printf("Error: unable to remove metadata file as storage folder %v is removed\n", sfr.Path)
	}
	err = wal.cm.dependencies.RemoveFile(filepath.Join(sfr.Path, sectorFile))
	if err != nil {
		wal.cm.log.Printf("Error: unable to reomve sector file as storage folder %v is removed\n", sfr.Path)
	}
}

// RemoveStorageFolder will delete a storage folder from the contract manager,
// moving all of the sectors in the storage folder to new storage folders.
func (cm *ContractManager) RemoveStorageFolder(index uint16, force bool) error {
	err := cm.tg.Add()
	if err != nil {
		return err
	}
	defer cm.tg.Done()

	// Retrieve the specified storage folder.
	cm.sectorMu.Lock()
	sf, exists := cm.storageFolders[index]
	if !exists {
		cm.sectorMu.Unlock()
		return errStorageFolderNotFound
	}
	cm.sectorMu.Unlock()

	// Lock the storage folder for the duration of the operation.
	sf.mu.Lock()
	defer sf.mu.Unlock()

	// create a unique alert ID per storage folder remove and unregister it after completion.
	alertID := modules.AlertID("cm-remove-folder-" + hex.EncodeToString(fastrand.Bytes(12)))
	defer cm.staticAlerter.UnregisterAlert(alertID)

	cm.staticAlerter.RegisterAlert(alertID,
		fmt.Sprintf("Removing %s folder %s",
			modules.FilesizeUnits(uint64(len(sf.usage))*64*modules.SectorSize),
			sf.path),
		"folder op", modules.SeverityInfo)

	// Clear out the sectors in the storage folder.
	_, err = cm.wal.managedEmptyStorageFolder(index, 0)
	if err != nil && !force {
		return err
	}

	// Wait for a synchronize to confirm that all of the moves have succeeded
	// in full.
	cm.wal.mu.Lock()
	syncChan := cm.wal.syncChan
	cm.wal.mu.Unlock()
	<-syncChan

	// Submit a storage folder removal to the WAL and wait until the update is
	// synced.
	cm.wal.mu.Lock()
	cm.wal.appendChange(stateChange{
		StorageFolderRemovals: []storageFolderRemoval{{
			Index: index,
			Path:  sf.path,
		}},
	})

	// Wait until the removal action has been synchronized.
	syncChan = cm.wal.syncChan
	cm.wal.mu.Unlock()
	<-syncChan
	return nil
}
