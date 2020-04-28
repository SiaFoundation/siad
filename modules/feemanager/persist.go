package feemanager

import (
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// logFile is the filename of the FeeManager logger.
	logFile = modules.FeeManagerDir + ".log"

	// PersistFilename is the filename to be used when persisting FeeManager
	// information on disk
	PersistFilename = "feemanager.dat"

	// MetadataHeader defines the header for the persist file.
	MetadataHeader = "Fee Manager\n"

	// MetadataVersion defines the version for the persist file.
	MetadataVersion = "v1.4.9\n"
)

const (
	// diskSectorSize puts an upper bound on the size of a persist entry, and
	// also on the size of the encoded object for the persist header.
	// Consistency for this file depends on the fact that the header is smaller
	// than one disk sector, and then also on the fact that disk sectors are
	// always updated atomically in hardware.
	diskSectorSize = 512

	// persistHeaderSize sets the on-disk size of the persist header.
	persistHeaderSize = 4096
)

type (
	// PersistHeader defines the data that goes at the head of the persist file.
	PersistHeader struct {
		// Metadata contains the persist metadata identifying the type and
		// version of the file.
		persist.Metadata

		// NextPayoutHeight is the height at which the next fee payout happens.
		NextPayoutHeight types.BlockHeight

		// LatestSyncedOffest is the latest offset that has a confirmed fsync,
		// data up to and including this point should be reliable.
		LatestSyncedOffset uint64
	}

	// persistSubsystem contains the state for the persistence of the fee
	// manager.
	//
	// NOTE: The persistSubsystem should never lock the syncCoordinator.
	persistSubsystem struct {
		// nextPayoutHeight and latestOffset are the latest known in-memory
		// values for these variables. They may not have been synced yet, and
		// therefore could be ahead of the persist file.
		nextPayoutHeight types.BlockHeight
		latestOffset     uint64

		// persistFile is the file handle of the file where data is written to.
		persistFile *os.File

		// Utilities
		staticCommon          *feeManagerCommon
		staticPersistDir      string
		staticSyncCoordinator *syncCoordinator
		mu                    sync.Mutex
	}

	// syncCoordinator is a struct which ensures only one syncing thread runs at
	// a time, and ensures that the data on disk is always consistent.
	//
	// The syncCoordinator will grab a mutex on the persistSubsystem while the
	// syncCoordinator is locked, meaning that nothing else should be allowed to
	// grab the syncCoordinator mutex to prevent deadlocks. The mutex inside of
	// the syncCoordinator exists only to protect the 'threadRunning' bool, the
	// other fields are all protected by the fact that only one syncing thread
	// ever runs at a time.
	//
	// NOTE: While the persistSubsystem should never lock the syncCoordinator,
	// the syncCoordinator can lock the persistSubsystem
	syncCoordinator struct {
		// threadRunning indicates whether or not there is a syncing thread
		// already running.
		threadRunning bool

		// externLatestSyncedOffset is the offset of the persist subsystem as of
		// the most recent successful sync. Same for
		// externLatestSyncedPayoutHeight.
		//
		// These variables are extern because they are only ever accessed or
		// updated by the sync persist thread, and there is only one of those
		// running at a time ever.
		externLatestSyncedPayoutHeight types.BlockHeight
		externLatestSyncedOffset       uint64

		staticCommon *feeManagerCommon
		mu           sync.Mutex
	}
)

// externWritePersistHeader will write the persist header to the first 512 bytes
// of 'fh' using a WriteAt call. The extern limits this function to being called
// by the syncing thread, and there can only be one syncing thread running at a
// time.
func externWritePersistHeader(fh *os.File, latestSyncedOffset uint64, nextPayoutHeight types.BlockHeight) error {
	encodedHeader := encoding.Marshal(PersistHeader{
		Metadata: persist.Metadata{
			Header:  MetadataHeader,
			Version: MetadataVersion,
		},
		NextPayoutHeight:   nextPayoutHeight,
		LatestSyncedOffset: latestSyncedOffset,
	})
	if len(encodedHeader) > diskSectorSize {
		return errors.New("encoded header is too large")
	}

	// Write the file header.
	_, err := fh.WriteAt(encodedHeader, 0)
	if err != nil {
		return errors.AddContext(err, "header write failed")
	}
	return nil
}

// callInitPersist handles all of the persistence initialization, such as
// creating the persistence directory
func (fm *FeeManager) callInitPersist() error {
	// Define shorter name helper for staticPersit
	ps := fm.staticCommon.staticPersist

	// Check if the fee manager file exists.
	filename := filepath.Join(ps.staticPersistDir, PersistFilename)
	_, err := os.Stat(filename)
	if err != nil && !os.IsNotExist(err) {
		return errors.AddContext(err, "unable to stat persist file")
	}
	if os.IsNotExist(err) {
		// Error will only be returned if there is an error with newPersist
		return errors.AddContext(fm.newPersist(filename), "unable to create a new persist setup for the fee manager")
	}

	// Open the fee manager file handle.
	fh, err := os.OpenFile(filename, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "could not open persist file")
	}
	ps.persistFile = fh
	fm.staticCommon.staticTG.AfterStop(func() error {
		return ps.persistFile.Close()
	})

	// Read the header.
	headerBytes := make([]byte, persistHeaderSize)
	_, err = fh.Read(headerBytes)
	if err != nil {
		return errors.AddContext(err, "could not read fee manager persist header")
	}

	// Decode the persist header.
	var ph PersistHeader
	err = encoding.Unmarshal(headerBytes, &ph)
	if err != nil {
		return errors.AddContext(err, "could not parse fee manager persist header")
	}

	// Check the metadata.
	if ph.Header != MetadataHeader {
		return errors.AddContext(err, "bad metadata header in persist file")
	}
	if ph.Version != MetadataVersion {
		return errors.AddContext(err, "bad metadata version in persist file")
	}

	// Set the offset and payout.
	ps.nextPayoutHeight = ph.NextPayoutHeight
	ps.latestOffset = ph.LatestSyncedOffset
	ps.staticSyncCoordinator.externLatestSyncedOffset = ph.LatestSyncedOffset
	ps.staticSyncCoordinator.externLatestSyncedPayoutHeight = ph.NextPayoutHeight

	// Read through all of the non-corrupt fees.
	entryBytes := make([]byte, ph.LatestSyncedOffset-persistHeaderSize)
	_, err = fh.Read(entryBytes)
	if err != nil {
		return errors.AddContext(err, "unable to read the synced portion of persist file")
	}
	for i := 0; i < len(entryBytes); i += persistEntrySize {
		// Integrate this entry.
		err = fm.integrateEntry(entryBytes[i : i+persistEntrySize])
		if err != nil {
			return errors.AddContext(err, "parsing a persist entry failed")
		}
	}
	return nil
}

// callPersistFeeCancellation will write a fee cancellation to the persist file.
func (ps *persistSubsystem) callPersistFeeCancelation(feeUID modules.FeeUID) error {
	entry := createCancelFeeEntry(feeUID)
	return ps.managedAppendEntry(entry)
}

// callPersistFeeUpdate will persist a fee update to the persist file.
func (ps *persistSubsystem) callPersistFeeUpdate(feeUID modules.FeeUID, payoutHeight types.BlockHeight) error {
	entry := createUpdateFeeEntry(feeUID, payoutHeight)
	return ps.managedAppendEntry(entry)
}

// callPersistNewFee will persist a new fee to disk.
func (ps *persistSubsystem) callPersistNewFee(fee modules.AppFee) error {
	entry := createAddFeeEntry(fee)
	return ps.managedAppendEntry(entry)
}

// managedAppendEntry will take a new encoded entry and append it to the persist
// file.
func (ps *persistSubsystem) managedAppendEntry(entry [persistEntrySize]byte) error {
	ps.mu.Lock()
	entryBytes := entry[:]
	_, err := ps.persistFile.WriteAt(entryBytes, int64(ps.latestOffset))
	if err == nil {
		// Only update the total size if the append happened without issues.
		ps.latestOffset += persistEntrySize
	}
	ps.mu.Unlock()
	if err != nil {
		return errors.AddContext(err, "unable to append entry")
	}

	// Ensure that the new update is synced and the header of the persist file
	// gets updated accordingly.
	return ps.staticSyncCoordinator.managedSyncPersist()
}

// newPersist is called if there is no existing persist file.
func (fm *FeeManager) newPersist(filename string) error {
	// Open Persist file
	fh, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to create persist file for fee manager")
	}

	ps := fm.staticCommon.staticPersist
	ps.persistFile = fh
	fm.staticCommon.staticTG.AfterStop(func() error {
		return ps.persistFile.Close()
	})

	// Set the offset and save the header.
	ps.latestOffset = persistHeaderSize
	return ps.staticSyncCoordinator.managedSyncPersist()
}

// managedSyncPersist will ensure that the persist is synced and that the
// persist header is updated to reflect any new writes.
func (sc *syncCoordinator) managedSyncPersist() error {
	// Determine whether there is another thread performing a sync.
	sc.mu.Lock()
	threadRunning := sc.threadRunning
	if threadRunning {
		// Another thread is running, that thread will complete the job this
		// thread was spun up to complete.
		sc.mu.Unlock()
		return nil
	}
	// This will become the new thread, so set threadRunning to true.
	sc.threadRunning = true
	sc.mu.Unlock()

	// Spin up a thread to perform the fsync tasks.
	ps := sc.staticCommon.staticPersist
	err := ps.staticCommon.staticTG.Add()
	if err != nil {
		return errors.AddContext(err, "failed to sync persist")
	}
	go func() {
		defer ps.staticCommon.staticTG.Done()
		sc.managedSyncPersistLoop()
	}()
	return nil
}

// managedSyncPersistLoop will perform fsyncs on the persist file and update the
// persist file header accordingly until there is nothing more to sync.
func (sc *syncCoordinator) managedSyncPersistLoop() {
	ps := sc.staticCommon.staticPersist
	// Perform syncs in a loop until there is no sync job to perform. Doing
	// things in a loop allows us to cover multiple file write calls with a
	// single fsync when there is a lot of writing happening in quick
	// succession.
	for {
		// Grab the latest written offset.
		ps.mu.Lock()
		latestOffset := ps.latestOffset
		nextPayoutHeight := ps.nextPayoutHeight
		ps.mu.Unlock()

		// Block until the persistFile completes a sync, guaranteeing that
		// everything up to and including the latest offset is synced.
		err := ps.persistFile.Sync()
		if err != nil {
			ps.staticCommon.staticLog.Critical("Unable to sync persist file:", err)
		}

		// Update the latestSyncedOffset now that we have confidence about a new
		// latestSyncedOffset. Note that new writes may have happened since
		// calling Sync, but we have saved the value from before syncing, so
		// this is safe.
		err = externWritePersistHeader(ps.persistFile, latestOffset, nextPayoutHeight)
		if err != nil {
			ps.staticCommon.staticLog.Critical("Unable to write persist file header:", err)
		}

		// Block until the header completes the sync.
		err = ps.persistFile.Sync()
		if err != nil {
			ps.staticCommon.staticLog.Critical("Unable to sync persist file:", err)
		}

		// Update the latest synced values in the sc, and then determine whether
		// another sync is necessary. The value 'sc.threadRunning' needs to be
		// updated atomically with learning that there is no other sync which
		// needs to be performed, which requires locking both the 'sc' and the
		// 'ps' at the same time. This is safe so long as the ps never locks the
		// sc, which it does not.
		die := false
		sc.mu.Lock()
		sc.externLatestSyncedOffset = latestOffset
		sc.externLatestSyncedPayoutHeight = nextPayoutHeight
		ps.mu.Lock()
		if sc.externLatestSyncedOffset == ps.latestOffset && sc.externLatestSyncedPayoutHeight == ps.nextPayoutHeight {
			// No new updates since writing the header and syncing, this thread
			// can exit.
			sc.threadRunning = false
			die = true
		}
		ps.mu.Unlock()
		sc.mu.Unlock()
		if die {
			return
		}
	}
}
