package renter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// snapshotKeySpecifier is the specifier used for deriving the secret used
	// to encrypt a snapshot from the RenterSeed.
	snapshotKeySpecifier = types.NewSpecifier("snapshot")

	// snapshotTableSpecifier is the specifier used to identify a snapshot entry
	// table stored in a sector.
	snapshotTableSpecifier = types.NewSpecifier("SnapshotTable")
)

var (
	// errEmptyContract is returned when we are trying to read from an empty
	// contract, this can be the case when downloading a snapshot from a host
	// that does not have one yet.
	errEmptyContract = errors.New("empty contract")

	// maxSnapshotUploadTime defines the total amount of time that the renter
	// will allocate to complete an upload of a snapshot .sia file to all hosts.
	// This is done with each host in parallel, and the .sia file is not
	// expected to exceed a few megabytes even for very large renters.
	maxSnapshotUploadTime = build.Select(build.Var{
		Standard: time.Minute * 15,
		Testnet:  time.Minute * 15,
		Dev:      time.Minute * 3,
		Testing:  time.Minute,
	}).(time.Duration)
)

// A snapshotEntry is an entry within the snapshot table, identifying both the
// snapshot metadata and the other sectors on the host storing the snapshot
// data.
type snapshotEntry struct {
	Name         [96]byte
	UID          [16]byte
	CreationDate types.Timestamp
	Size         uint64         // size of snapshot .sia file
	DataSectors  [4]crypto.Hash // pointers to sectors containing snapshot .sia file
}

// calcSnapshotUploadProgress calculates the upload progress of a snapshot.
func calcSnapshotUploadProgress(fileUploadProgress float64, dotSiaUploadProgress float64) float64 {
	return 0.8*fileUploadProgress + 0.2*dotSiaUploadProgress
}

// UploadedBackups returns the backups that the renter can download, along with
// a list of which contracts are storing all known backups.
func (r *Renter) UploadedBackups() ([]modules.UploadedBackup, []types.SiaPublicKey, error) {
	if err := r.tg.Add(); err != nil {
		return nil, nil, err
	}
	defer r.tg.Done()
	id := r.mu.RLock()
	defer r.mu.RUnlock(id)
	backups := append([]modules.UploadedBackup(nil), r.persist.UploadedBackups...)
	hosts := make([]types.SiaPublicKey, 0, len(r.persist.SyncedContracts))
	for _, c := range r.hostContractor.Contracts() {
		for _, id := range r.persist.SyncedContracts {
			if c.ID == id {
				hosts = append(hosts, c.HostPublicKey)
				break
			}
		}
	}
	return backups, hosts, nil
}

// BackupsOnHost returns the backups stored on a particular host. This operation
// can take multiple minutes if the renter is performing many other operations
// on this host, however this operation is given high priority over other types
// of operations.
func (r *Renter) BackupsOnHost(hostKey types.SiaPublicKey) ([]modules.UploadedBackup, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()

	// Find the relevant worker.
	w, err := r.staticWorkerPool.callWorker(hostKey)
	if err != nil {
		return nil, errors.AddContext(err, "host not found in the worker table")
	}

	return w.FetchBackups(r.tg.StopCtx())
}

// UploadBackup creates a backup of the renter which is uploaded to the sia
// network as a snapshot and can be retrieved using only the seed.
func (r *Renter) UploadBackup(src, name string) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.managedUploadBackup(src, name)
}

// managedUploadBackup creates a backup of the renter which is uploaded to the
// sia network as a snapshot and can be retrieved using only the seed.
func (r *Renter) managedUploadBackup(src, name string) error {
	if len(name) > 96 {
		return errors.New("name is too long")
	}

	// Check if snapshot already exists.
	if r.managedSnapshotExists(name) {
		s := fmt.Sprintf("snapshot with name '%s' already exists", name)
		return errors.AddContext(filesystem.ErrExists, s)
	}

	// Open the backup for uploading.
	backup, err := os.Open(src)
	if err != nil {
		return errors.AddContext(err, "failed to open backup for uploading")
	}
	defer func() {
		err = errors.Compose(err, backup.Close())
	}()

	// Prepare the siapath.
	sp, err := modules.BackupFolder.Join(name)
	if err != nil {
		return err
	}
	// Create upload params with high redundancy.
	allowance := r.hostContractor.Allowance()
	dataPieces := allowance.Hosts / 10
	if dataPieces == 0 {
		dataPieces = 1
	}
	parityPieces := allowance.Hosts - dataPieces
	ec, err := modules.NewRSSubCode(int(dataPieces), int(parityPieces), crypto.SegmentSize)
	if err != nil {
		return err
	}
	up := modules.FileUploadParams{
		SiaPath:     sp,
		ErasureCode: ec,
		Force:       false,

		CipherType: crypto.TypeDefaultRenter,
	}
	// Begin uploading the backup. When the upload finishes, the backup .sia
	// file will be uploaded by r.threadedSynchronizeSnapshots and then deleted.
	fileNode, err := r.callUploadStreamFromReader(up, backup)
	if err != nil {
		return errors.AddContext(err, "failed to upload backup")
	}
	err = fileNode.Close()
	if err != nil {
		return errors.AddContext(err, "unable to close fileNode while uploading a backup")
	}
	// Save initial snapshot entry.
	meta := modules.UploadedBackup{
		Name:           name,
		CreationDate:   types.CurrentTimestamp(),
		Size:           0,
		UploadProgress: 0,
	}
	fastrand.Read(meta.UID[:])
	if err := r.managedSaveSnapshot(meta); err != nil {
		return err
	}

	return nil
}

// DownloadBackup downloads the specified backup.
func (r *Renter) DownloadBackup(dst string, name string) (err error) {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Open the destination.
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, dstFile.Close())
	}()
	// search for backup
	if len(name) > 96 {
		return errors.New("no record of a backup with that name")
	}
	var uid [16]byte
	var found bool
	id := r.mu.RLock()
	for _, b := range r.persist.UploadedBackups {
		if b.Name == name {
			uid = b.UID
			found = true
			break
		}
	}
	r.mu.RUnlock(id)
	if !found {
		return errors.New("no record of a backup with that name")
	}
	// Download snapshot's .sia file.
	_, dotSia, err := r.managedDownloadSnapshot(uid)
	if err != nil {
		return err
	}
	// Store it in the backup file set.
	backupSiaPath, err := modules.BackupFolder.Join(name)
	if err != nil {
		return err
	}
	if err := r.staticFileSystem.WriteFile(backupSiaPath, dotSia, 0666); err != nil {
		return err
	}
	// Load the .sia file.
	siaPath, err := modules.BackupFolder.Join(name)
	if err != nil {
		return err
	}
	entry, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, entry.Close())
	}()
	// Use .sia file to download snapshot.
	snap, err := entry.Snapshot(siaPath)
	if err != nil {
		return err
	}
	s := r.managedStreamer(snap, false)
	_, err = io.Copy(dstFile, s)
	return errors.Compose(err, s.Close())
}

// managedSnapshotExists returns true if a snapshot with a given name already
// exists.
func (r *Renter) managedSnapshotExists(name string) bool {
	id := r.mu.Lock()
	defer r.mu.Unlock(id)
	for _, ub := range r.persist.UploadedBackups {
		if ub.Name == name {
			return true
		}
	}
	return false
}

// managedSaveSnapshot saves snapshot metadata to disk.
func (r *Renter) managedSaveSnapshot(meta modules.UploadedBackup) error {
	id := r.mu.Lock()
	defer r.mu.Unlock(id)
	// Check whether we've already saved this snapshot.
	for i, ub := range r.persist.UploadedBackups {
		if ub.UID == meta.UID {
			if ub == meta {
				// nothing changed
				return nil
			}
			// something changed; overwrite existing entry
			r.persist.UploadedBackups[i] = meta
			return r.saveSync()
		}
	}
	// Append the new snapshot.
	r.persist.UploadedBackups = append(r.persist.UploadedBackups, meta)
	// Trim the set of snapshots if necessary. Hosts can only store a finite
	// number of snapshots, so if we exceed that number, we kick out the oldest
	// snapshot. We check the size by encoding a slice of snapshotEntrys and
	// removing elements from the slice until the encoded size fits on the host.
	entryTable := make([]snapshotEntry, len(r.persist.UploadedBackups))
	for len(encoding.Marshal(entryTable)) > int(modules.SectorSize) {
		entryTable = entryTable[1:]
	}
	// Sort by CreationDate (youngest-to-oldest) and remove excess elements from
	// the end.
	sort.Slice(r.persist.UploadedBackups, func(i, j int) bool {
		return r.persist.UploadedBackups[i].CreationDate > r.persist.UploadedBackups[j].CreationDate
	})
	r.persist.UploadedBackups = r.persist.UploadedBackups[:len(entryTable)]
	// Save the result.
	if err := r.saveSync(); err != nil {
		return err
	}
	return nil
}

// managedDownloadSnapshotTable will fetch the snapshot table from the host.
func (r *Renter) managedDownloadSnapshotTable(host *worker) ([]snapshotEntry, error) {
	// Get the wallet seed.
	ws, _, err := r.w.PrimarySeed()
	if err != nil {
		return nil, errors.AddContext(err, "failed to get wallet's primary seed")
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, snapshotKeySpecifier)
	defer fastrand.Read(secret[:])

	// Create an empty entryTable
	var entryTable []snapshotEntry

	// Fetch the contract and see if it's empty, if that is the case return an
	// empty entryTable and appropriate error.
	contract, ok := r.hostContractor.ContractByPublicKey(host.staticHostPubKey)
	if !ok {
		return nil, errors.New("failed to host contract")
	}
	if contract.Size() == 0 {
		return entryTable, errEmptyContract
	}

	// Download the table of snapshots that the host is storing.
	tableSector, err := host.ReadOffset(r.tg.StopCtx(), categorySnapshotDownload, 0, modules.SectorSize)
	if err != nil {
		return nil, errors.AddContext(err, "unable to perform a download by index on this contract")
	}

	// decrypt the table
	c, _ := crypto.NewSiaKey(crypto.TypeThreefish, secret[:])
	encTable, err := c.DecryptBytesInPlace(tableSector, 0)
	if err != nil || !bytes.Equal(encTable[:16], snapshotTableSpecifier[:]) {
		// either the first sector was not an entry table, or it got corrupted
		// somehow; either way, it's not retrievable, so we'll treat this as
		// equivalent to having no entry table at all. This is not an error; it
		// just means that when we upload a snapshot, we'll have to create a new
		// table.
		return nil, errors.AddContext(err, "error decrypting bytes")
	}

	if err := encoding.Unmarshal(encTable[16:], &entryTable); err != nil {
		return nil, errors.AddContext(err, "error unmarshaling the entry table")
	}
	return entryTable, nil
}

// managedUploadSnapshot uploads a snapshot .sia file to all hosts.
func (r *Renter) managedUploadSnapshot(meta modules.UploadedBackup, dotSia []byte) error {
	// Grab all of the workers that are good for upload.
	var total int
	workers := r.staticWorkerPool.callWorkers()
	for i := 0; i < len(workers); i++ {
		if workers[i].staticCache().staticContractUtility.GoodForUpload {
			workers[total] = workers[i]
			total++
		}
	}
	workers = workers[:total]

	// Submit a job to each worker. Make sure the response channel has enough
	// room in the buffer for all results, this way workers are not being
	// blocked when returning their results.
	maxWait, cancel := context.WithTimeout(r.tg.StopCtx(), maxSnapshotUploadTime)
	defer cancel()
	responseChan := make(chan *jobUploadSnapshotResponse, len(workers))
	queued := 0
	for _, w := range workers {
		job := &jobUploadSnapshot{
			staticSiaFileData: dotSia,

			staticResponseChan: responseChan,

			jobGeneric: newJobGeneric(maxWait, w.staticJobUploadSnapshotQueue, meta),
		}

		// If a job is not added correctly, count this as a failed response.
		if w.staticJobUploadSnapshotQueue.callAdd(job) {
			queued++
		}
	}

	// Iteratively grab the responses from the workers.
	responses := 0
	successes := 0

LOOP:
	for responses < queued {
		var resp *jobUploadSnapshotResponse
		select {
		case resp = <-responseChan:
		case <-maxWait.Done():
			break LOOP
		}
		responses++

		// Update the progress.
		pct := 100 * float64(responses) / float64(total)
		meta.UploadProgress = calcSnapshotUploadProgress(100, pct)
		err := r.managedSaveSnapshot(meta)
		if err != nil {
			r.log.Println("Error saving snapshot during upload:", err)
			continue
		}

		// Log any error.
		if resp.staticErr != nil {
			r.log.Debugln("snapshot upload failed:", resp.staticErr)
			continue
		}
		successes++
	}

	// Check for shutdown.
	select {
	case <-r.tg.StopChan():
		return errors.New("renter is shutting down")
	default:
	}

	// Check if there were too few successes to count this as a successful
	// backup. A 1/3 success rate is really quite arbitrary, picked because it
	// ~feels~ like that should be enough to give the user security, but really
	// who knows. Like really we should probably be looking at the total number
	// of hosts in the allowance and comparing against that.
	if successes < total/3 {
		r.log.Printf("Unable to save snapshot effectively, wanted %v but only got %v successful snapshot backups", total, successes)
		return fmt.Errorf("needed at least %v successes, only got %v", total/3, successes)
	}

	// Save the final version of the snapshot. Represent the progress at 100%
	// even though not every host may have our snapshot. Really we only need 1
	// working host to do a full recovery.
	meta.UploadProgress = calcSnapshotUploadProgress(100, 100)
	if err := r.managedSaveSnapshot(meta); err != nil {
		return errors.AddContext(err, "error saving snapshot after upload completed")
	}
	return nil
}

// managedDownloadSnapshot downloads and returns the specified snapshot.
func (r *Renter) managedDownloadSnapshot(uid [16]byte) (ub modules.UploadedBackup, dotSia []byte, err error) {
	if err := r.tg.Add(); err != nil {
		return modules.UploadedBackup{}, nil, err
	}
	defer r.tg.Done()

	// Get the wallet seed.
	ws, _, err := r.w.PrimarySeed()
	if err != nil {
		return modules.UploadedBackup{}, nil, errors.AddContext(err, "failed to get wallet's primary seed")
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, snapshotKeySpecifier)
	defer fastrand.Read(secret[:])

	// try downloading from each host in serial, prioritizing the hosts that are
	// GoodForUpload, then GoodForRenew
	contracts := r.hostContractor.Contracts()
	sort.Slice(contracts, func(i, j int) bool {
		if contracts[i].Utility.GoodForUpload == contracts[j].Utility.GoodForUpload {
			return contracts[i].Utility.GoodForRenew && !contracts[j].Utility.GoodForRenew
		}
		return contracts[i].Utility.GoodForUpload && !contracts[j].Utility.GoodForUpload
	})
	for i := range contracts {
		err := func() (err error) {
			w, err := r.staticWorkerPool.callWorker(contracts[i].HostPublicKey)
			if err != nil {
				return err
			}
			// download the snapshot table
			entryTable, err := w.DownloadSnapshotTable(r.tg.StopCtx())
			if err != nil {
				return err
			}

			// search for the desired snapshot
			var entry *snapshotEntry
			for j := range entryTable {
				if entryTable[j].UID == uid {
					entry = &entryTable[j]
					break
				}
			}
			if entry == nil {
				return errors.New("entry table does not contain snapshot")
			}
			// download the entry
			dotSia = nil
			for _, root := range entry.DataSectors {
				data, err := w.ReadSector(r.tg.StopCtx(), categorySnapshotDownload, root, 0, modules.SectorSize)
				if err != nil {
					return err
				}
				dotSia = append(dotSia, data...)
				if uint64(len(dotSia)) >= entry.Size {
					dotSia = dotSia[:entry.Size]
					break
				}
			}
			ub = modules.UploadedBackup{
				Name:           string(bytes.TrimRight(entry.Name[:], types.RuneToString(0))),
				UID:            entry.UID,
				CreationDate:   entry.CreationDate,
				Size:           entry.Size,
				UploadProgress: 100,
			}
			return nil
		}()
		if err != nil {
			r.log.Printf("Downloading backup from host %v failed: %v", contracts[i].HostPublicKey, err)
			continue
		}
		return ub, dotSia, nil
	}
	return modules.UploadedBackup{}, nil, errors.New("could not download backup from any host")
}

// threadedSynchronizeSnapshots continuously scans hosts to ensure that all
// current hosts are storing all known snapshots.
func (r *Renter) threadedSynchronizeSnapshots() {
	if err := r.tg.Add(); err != nil {
		return
	}
	defer r.tg.Done()
	// calcOverlap takes a host's entry table and the set of known snapshots,
	// and calculates which snapshots the host is missing and which snapshots it
	// has that we don't.
	calcOverlap := func(entryTable []snapshotEntry, known map[[16]byte]struct{}) (unknown []modules.UploadedBackup, missing [][16]byte) {
		missingMap := make(map[[16]byte]struct{}, len(known))
		for uid := range known {
			missingMap[uid] = struct{}{}
		}
		for _, e := range entryTable {
			if _, ok := known[e.UID]; !ok {
				unknown = append(unknown, modules.UploadedBackup{
					Name:           string(bytes.TrimRight(e.Name[:], types.RuneToString(0))),
					UID:            e.UID,
					CreationDate:   e.CreationDate,
					Size:           e.Size,
					UploadProgress: 100,
				})
			}
			delete(missingMap, e.UID)
		}
		for uid := range missingMap {
			missing = append(missing, uid)
		}
		return
	}

	// Build a set of which contracts are synced.
	syncedContracts := make(map[types.FileContractID]struct{})
	id := r.mu.RLock()
	for _, fcid := range r.persist.SyncedContracts {
		syncedContracts[fcid] = struct{}{}
	}
	r.mu.RUnlock(id)

	for {
		// Can't do anything if the wallet is locked.
		if unlocked, _ := r.w.Unlocked(); !unlocked {
			select {
			case <-time.After(snapshotSyncSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}
		r.staticWorkerPool.callUpdate()

		// First, process any snapshot siafiles that may have finished uploading.
		root := modules.BackupFolder
		var mu sync.Mutex
		flf := func(info modules.FileInfo) {
			// Make sure we only look at a single info at a time.
			mu.Lock()
			defer mu.Unlock()

			// locate corresponding entry
			id = r.mu.RLock()
			var meta modules.UploadedBackup
			found := false
			for _, meta = range r.persist.UploadedBackups {
				sp, _ := info.SiaPath.Rebase(modules.BackupFolder, modules.RootSiaPath())
				if meta.Name == sp.String() {
					found = true
					break
				}
			}
			r.mu.RUnlock(id)
			if !found {
				r.log.Println("Could not locate entry for file in backup set")
				return
			}

			// record current UploadProgress
			meta.UploadProgress = calcSnapshotUploadProgress(info.UploadProgress, 0)
			if err := r.managedSaveSnapshot(meta); err != nil {
				r.log.Println("Could not save upload progress:", err)
				return
			}

			if modules.NeedsRepair(info.Health) {
				// not ready for upload yet
				return
			}
			r.log.Println("Uploading snapshot", info.SiaPath)
			err := func() error {
				// Grab the entry for the uploaded backup's siafile.
				entry, err := r.staticFileSystem.OpenSiaFile(info.SiaPath)
				if err != nil {
					return errors.AddContext(err, "failed to get entry for snapshot")
				}
				// Read the siafile from disk.
				sr, err := entry.SnapshotReader()
				if err != nil {
					return errors.Compose(err, entry.Close())
				}
				// NOTE: The snapshot reader needs to be closed before
				// entry.Close is called, and unfortunately also before
				// managedUploadSnapshot is called, because the snapshot reader
				// holds a lock on the underlying dotsia, which is also actively
				// a part of the repair system. The repair system locks the
				// renter then tries to grab a lock on the siafile, while
				// 'managedUploadSnapshot' can independently try to grab a lock
				// on the renter while holding the snapshot.

				// TODO: Calling ReadAll here is not really acceptable because
				// for larger renters, the snapshot can be very large. For a
				// user with 20 TB of data, the snapshot is going to be 2 GB
				// large, which on its own exceeds our goal for the total memory
				// consumption of the renter, and it's not even that much data.
				//
				// Need to work around the snapshot reader lock issue as well.
				dotSia, err := ioutil.ReadAll(sr)
				if err := sr.Close(); err != nil {
					return errors.Compose(err, entry.Close())
				}

				// Upload the snapshot to the network.
				meta.UploadProgress = calcSnapshotUploadProgress(100, 0)
				meta.Size = uint64(len(dotSia))
				if err := r.managedUploadSnapshot(meta, dotSia); err != nil {
					return errors.Compose(err, entry.Close())
				}

				// Close out the entry.
				err = entry.Close()
				if err != nil {
					return err
				}

				// Delete the local siafile.
				if err := r.staticFileSystem.DeleteFile(info.SiaPath); err != nil {
					return err
				}
				return nil
			}()
			if err != nil {
				r.log.Println("Failed to upload snapshot .sia:", err)
			}
		}
		offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
		err := r.staticFileSystem.List(root, true, offlineMap, goodForRenewMap, contractsMap, flf, func(modules.DirectoryInfo) {})
		if err != nil {
			r.log.Println("Could not get un-uploaded snapshots:", err)
		}

		// Build a set of the snapshots we already have.
		known := make(map[[16]byte]struct{})
		id := r.mu.RLock()
		for _, ub := range r.persist.UploadedBackups {
			if ub.UploadProgress == 100 {
				known[ub.UID] = struct{}{}
			} else {
				// signal r.threadedUploadAndRepair to keep uploading the snapshot
				select {
				case r.uploadHeap.repairNeeded <- struct{}{}:
				default:
				}
			}
		}
		r.mu.RUnlock(id)

		// Select an unsynchronized host.
		contracts := r.hostContractor.Contracts()
		var found bool
		var c modules.RenterContract
		for _, c = range contracts {
			// ignore bad contracts
			if !c.Utility.GoodForRenew || !c.Utility.GoodForUpload {
				continue
			}
			if _, ok := syncedContracts[c.ID]; !ok {
				found = true
				break
			}
		}
		if !found {
			// No unsychronized hosts; drop any irrelevant contracts, then sleep
			// for a while before trying again
			if len(contracts) != 0 {
				syncedContracts = make(map[types.FileContractID]struct{})
				for _, c := range contracts {
					if !c.Utility.GoodForRenew || !c.Utility.GoodForUpload {
						continue
					}
					syncedContracts[c.ID] = struct{}{}
				}
			}
			select {
			case <-time.After(snapshotSyncSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}

		// Synchronize the host.
		r.log.Debugln("Synchronizing snapshots on host", c.HostPublicKey)
		err = func() (err error) {
			// Get the right worker for the host.
			w, err := r.staticWorkerPool.callWorker(c.HostPublicKey)
			if err != nil {
				return err
			}

			// download the snapshot table
			entryTable, err := w.DownloadSnapshotTable(r.tg.StopCtx())
			if err != nil {
				return err
			}

			// Calculate which snapshots the host doesn't have, and which
			// snapshots it does have that we haven't seen before.
			unknown, missing := calcOverlap(entryTable, known)

			// If *any* snapshots are new, mark all other hosts as not
			// synchronized.
			if len(unknown) != 0 {
				for fcid := range syncedContracts {
					delete(syncedContracts, fcid)
				}
				// Record the new snapshots; they'll be replicated to the other
				// hosts in subsequent iterations of the synchronization loop.
				for _, ub := range unknown {
					known[ub.UID] = struct{}{}
					if err := r.managedSaveSnapshot(ub); err != nil {
						return err
					}
					r.log.Printf("Located new snapshot %q on host %v", ub.Name, c.HostPublicKey)
				}
			}

			// Upload any snapshots that the host is missing.
			//
			// TODO: instead of returning immediately upon encountering an
			// error, we should probably continue trying to upload the other
			// snapshots.
			for _, uid := range missing {
				ub, dotSia, err := r.managedDownloadSnapshot(uid)
				if err != nil {
					// TODO: if snapshot can't be found on any host, delete it
					return err
				}
				if err := w.UploadSnapshot(r.tg.StopCtx(), ub, dotSia); err != nil {
					return err
				}
				r.log.Printf("Replicated missing snapshot %q to host %v", ub.Name, c.HostPublicKey)
			}
			return nil
		}()
		if err != nil {
			r.log.Println("Failed to synchronize snapshots on host:", err)
			// sleep for a bit to prevent retrying the same host repeatedly in a
			// tight loop
			select {
			case <-time.After(snapshotSyncSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}
		// Mark the contract as synchronized.
		syncedContracts[c.ID] = struct{}{}
		// Commit the set of synchronized hosts.
		id = r.mu.Lock()
		r.persist.SyncedContracts = r.persist.SyncedContracts[:0]
		for fcid := range syncedContracts {
			r.persist.SyncedContracts = append(r.persist.SyncedContracts, fcid)
		}
		if err := r.saveSync(); err != nil {
			r.log.Println("Failed to update set of synced hosts:", err)
		}
		r.mu.Unlock(id)
	}
}
