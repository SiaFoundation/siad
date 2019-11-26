package renter

import (
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	logFile       = modules.RenterDir + ".log"
	repairLogFile = "repair.log"
	// PersistFilename is the filename to be used when persisting renter
	// information to a JSON file
	PersistFilename = "renter.json"
	// SiaDirMetadata is the name of the metadata file for the sia directory
	SiaDirMetadata = ".siadir"
	// walFile is the filename of the renter's writeaheadlog's file.
	walFile = modules.RenterDir + ".wal"
	// repairLoopFilename is the filename to be used when persisting bubble
	// updates that are called from the repair loop
	repairLoopFilename = "repairloop.json"
)

var (
	//ErrBadFile is an error when a file does not qualify as .sia file
	ErrBadFile = errors.New("not a .sia file")
	// ErrIncompatible is an error when file is not compatible with current
	// version
	ErrIncompatible = errors.New("file is not compatible with current version")
	// ErrNoNicknames is an error when no nickname is given
	ErrNoNicknames = errors.New("at least one nickname must be supplied")
	// ErrNonShareSuffix is an error when the suffix of a file does not match
	// the defined share extension
	ErrNonShareSuffix = errors.New("suffix of file must be " + modules.SiaFileExtension)

	settingsMetadata = persist.Metadata{
		Header:  "Renter Persistence",
		Version: persistVersion,
	}

	shareHeader  = [15]byte{'S', 'i', 'a', ' ', 'S', 'h', 'a', 'r', 'e', 'd', ' ', 'F', 'i', 'l', 'e'}
	shareVersion = "0.4"

	// Persist Version Numbers
	persistVersion040 = "0.4"
	persistVersion133 = "1.3.3"
	persistVersion140 = "1.4.0"
	persistVersion142 = "1.4.2"
)

type (
	// persist contains all of the persistent renter data.
	persistence struct {
		MaxDownloadSpeed int64
		MaxUploadSpeed   int64
		UploadedBackups  []modules.UploadedBackup
		SyncedContracts  []types.FileContractID
	}
)

// saveSync stores the current renter data to disk and then syncs to disk.
func (r *Renter) saveSync() error {
	return persist.SaveJSON(settingsMetadata, r.persist, filepath.Join(r.persistDir, PersistFilename))
}

// managedLoadSettings fetches the saved renter data from disk.
func (r *Renter) managedLoadSettings() error {
	r.persist = persistence{}
	err := persist.LoadJSON(settingsMetadata, &r.persist, filepath.Join(r.persistDir, PersistFilename))
	if os.IsNotExist(err) {
		// No persistence yet, set the defaults and continue.
		r.persist.MaxDownloadSpeed = DefaultMaxDownloadSpeed
		r.persist.MaxUploadSpeed = DefaultMaxUploadSpeed
		id := r.mu.Lock()
		err = r.saveSync()
		r.mu.Unlock(id)
		if err != nil {
			return err
		}
	} else if err == persist.ErrBadVersion {
		// Outdated version, try the 040 to 133 upgrade.
		err = convertPersistVersionFrom040To133(filepath.Join(r.persistDir, PersistFilename))
		if err != nil {
			r.log.Println("WARNING: 040 to 133 renter upgrade failed, trying 133 to 140 next", err)
		}
		// Then upgrade from 133 to 140.
		oldContracts := r.hostContractor.OldContracts()
		err = r.convertPersistVersionFrom133To140(filepath.Join(r.persistDir, PersistFilename), oldContracts)
		if err != nil {
			r.log.Println("WARNING: 133 to 140 renter upgrade failed", err)
		}
		// Then upgrade from 140 to 142.
		err = r.convertPersistVersionFrom140To142(filepath.Join(r.persistDir, PersistFilename))
		if err != nil {
			r.log.Println("WARNING: 140 to 142 renter upgrade failed", err)
			// Nothing left to try.
			return err
		}
		r.log.Println("Renter upgrade successful")
		// Re-load the settings now that the file has been upgraded.
		return r.managedLoadSettings()
	} else if err != nil {
		return err
	}

	// Set the bandwidth limits on the contractor, which was already initialized
	// without bandwidth limits.
	return r.setBandwidthLimits(r.persist.MaxDownloadSpeed, r.persist.MaxUploadSpeed)
}

// managedInitPersist handles all of the persistence initialization, such as creating
// the persistence directory and starting the logger.
func (r *Renter) managedInitPersist() error {
	// Create the persist and filesystem directories if they do not yet exist.
	//
	// Note: the os package needs to be used here instead of the renter's
	// CreateDir method because the staticDirSet has not been initialized yet.
	// The directory is needed before the staticDirSet can be initialized
	// because the wal needs the directory to be created and the staticDirSet
	// needs the wal.
	fsRoot := filepath.Join(r.persistDir, modules.FileSystemRoot)
	err := os.MkdirAll(fsRoot, 0700)
	if err != nil {
		return err
	}

	// Initialize the logger.
	r.log, err = persist.NewFileLogger(filepath.Join(r.persistDir, logFile))
	if err != nil {
		return err
	}
	if err := r.tg.AfterStop(r.log.Close); err != nil {
		return err
	}
	r.repairLog, err = persist.NewFileLogger(filepath.Join(r.persistDir, repairLogFile))
	if err != nil {
		return err
	}
	if err := r.tg.AfterStop(r.repairLog.Close); err != nil {
		return err
	}

	// Initialize the writeaheadlog.
	options := writeaheadlog.Options{
		StaticLog: r.log,
		Path:      filepath.Join(r.persistDir, walFile),
	}
	txns, wal, err := writeaheadlog.NewWithOptions(options)
	if err != nil {
		return err
	}
	if err := r.tg.AfterStop(wal.Close); err != nil {
		return err
	}

	// Apply unapplied wal txns before loading the persistence structure to
	// avoid loading potentially corrupted files.
	if len(txns) > 0 {
		r.log.Println("Wal initialized", len(txns), "transactions to apply")
	}
	for _, txn := range txns {
		applyTxn := true
		r.log.Println("applying transaction with", len(txn.Updates), "updates")
		for _, update := range txn.Updates {
			if siafile.IsSiaFileUpdate(update) {
				r.log.Println("Applying a siafile update:", update.Name)
				if err := siafile.ApplyUpdates(update); err != nil {
					return errors.AddContext(err, "failed to apply SiaFile update")
				}
			} else if siadir.IsSiaDirUpdate(update) {
				r.log.Println("Applying a siadir update:", update.Name)
				if err := siadir.ApplyUpdates(update); err != nil {
					return errors.AddContext(err, "failed to apply SiaDir update")
				}
			} else {
				r.log.Println("wal update not applied, marking transaction as not applied")
				applyTxn = false
			}
		}
		if applyTxn {
			if err := txn.SignalUpdatesApplied(); err != nil {
				return err
			}
		}
	}

	// Create the filesystem.
	fs, err := filesystem.New(fsRoot, r.log, wal)
	if err != nil {
		return err
	}

	// Initialize the wal, staticFileSet and the staticDirSet. With the
	// staticDirSet finish the initialization of the files directory
	r.wal = wal
	r.staticFileSystem = fs

	// Load the prior persistence structures.
	if err := r.managedLoadSettings(); err != nil {
		return errors.AddContext(err, "failed to load renter's persistence structrue")
	}

	// Create the essential dirs in the filesystem.
	err = fs.NewSiaDir(modules.HomeSiaPath())
	if err != nil && err != filesystem.ErrExists {
		return err
	}
	err = fs.NewSiaDir(modules.UserSiaPath())
	if err != nil && err != filesystem.ErrExists {
		return err
	}
	err = fs.NewSiaDir(modules.SnapshotsSiaPath())
	if err != nil && err != filesystem.ErrExists {
		return err
	}
	return nil
}
