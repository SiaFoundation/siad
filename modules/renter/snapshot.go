package renter

import (
	"bytes"
	"crypto/cipher"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/contractor"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"golang.org/x/crypto/twofish"
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

var (
	// SnapshotKeySpecifier is the specifier used for deriving the secret used to
	// encrypt a snapshot from the RenterSeed.
	snapshotKeySpecifier = types.Specifier{'s', 'n', 'a', 'p', 's', 'h', 'o', 't'}

	// snapshotTableSpecifier is the specifier used to identify a snapshot entry
	// table stored in a sector.
	snapshotTableSpecifier = types.Specifier{'S', 'n', 'a', 'p', 's', 'h', 'o', 't', 'T', 'a', 'b', 'l', 'e'}
)

// calcSnapshotUploadProgress calculates the upload progress of a snapshot.
func calcSnapshotUploadProgress(fileUploadProgress float64, dotSiaUploadProgress float64) float64 {
	return 0.8*fileUploadProgress + 0.2*dotSiaUploadProgress
}

// UploadedBackups returns the backups that the renter can download.
func (r *Renter) UploadedBackups() ([]modules.UploadedBackup, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	id := r.mu.RLock()
	defer r.mu.RUnlock(id)
	backups := append([]modules.UploadedBackup(nil), r.persist.UploadedBackups...)
	return backups, nil
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

	// Open the backup for uploading.
	backup, err := os.Open(src)
	if err != nil {
		return errors.AddContext(err, "failed to open backup for uploading")
	}
	defer backup.Close()
	// TODO: verify that src is actually a backup file

	// Prepare the siapath.
	sp, err := modules.NewSiaPath(name)
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
	ec, err := siafile.NewRSSubCode(int(dataPieces), int(parityPieces), crypto.SegmentSize)
	if err != nil {
		return err
	}
	up := modules.FileUploadParams{
		SiaPath:     sp,
		ErasureCode: ec,
		Force:       false,
	}
	// Begin uploading the backup.
	if err := r.managedUploadStreamFromReader(up, backup, true); err != nil {
		return errors.AddContext(err, "failed to upload backup")
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

	// TODO: this needs to be spawned again if interrupted by shutdown
	go func() {
		err := func() error {
			// Wait for the upload to finish.
			//
			// TODO: do this without polling
			for {
				offlineMap, goodForRenewMap, contractsMap := r.managedContractUtilityMaps()
				info, err := r.staticBackupFileSet.FileInfo(sp, offlineMap, goodForRenewMap, contractsMap)
				if err != nil {
					return err
				}
				if info.Health <= siafile.RemoteRepairDownloadThreshold {
					break
				}
				// record current UploadProgress
				meta.UploadProgress = calcSnapshotUploadProgress(info.UploadProgress, 0)
				if err := r.managedSaveSnapshot(meta); err != nil {
					return err
				}
				// sleep for a bit before trying again
				select {
				case <-time.After(uploadPollInterval):
				case <-r.tg.StopChan():
					return errors.New("interrupted by shutdown")
				}
			}

			// Grab the entry for the uploaded backup's siafile.
			entry, err := r.staticBackupFileSet.Open(sp)
			if err != nil {
				return errors.AddContext(err, "failed to get entry for snapshot")
			}
			defer entry.Close()
			// Read the siafile from disk.
			sr, err := entry.SnapshotReader()
			if err != nil {
				return err
			}
			defer sr.Close()
			dotSia, err := ioutil.ReadAll(sr)
			if err != nil {
				return err
			}
			// Upload the snapshot to the network.
			meta.UploadProgress = calcSnapshotUploadProgress(100, 0)
			meta.Size = uint64(len(dotSia))
			return r.managedUploadSnapshot(meta, dotSia)
		}()
		if err != nil {
			r.log.Println("Failed to upload snapshot:", err)
		}
	}()
	return nil
}

// DownloadBackup downloads the specified backup.
func (r *Renter) DownloadBackup(dst string, name string) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	// Open the destination.
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()
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
	if err := ioutil.WriteFile(filepath.Join(r.staticBackupsDir, name+modules.SiaFileExtension), dotSia, 0666); err != nil {
		return err
	}
	// Load the .sia file.
	siaPath, err := modules.NewSiaPath(name)
	if err != nil {
		return err
	}
	entry, err := r.staticBackupFileSet.Open(siaPath)
	if err != nil {
		return err
	}
	defer entry.Close()
	// Use .sia file to download snapshot.
	s := r.managedStreamer(entry.Snapshot())
	defer s.Close()
	_, err = io.Copy(dstFile, s)
	return err
}

func (r *Renter) managedUploadSnapshotHost(meta modules.UploadedBackup, dotSia []byte, host contractor.Session) error {
	// Get the wallet seed.
	ws, _, err := r.w.PrimarySeed()
	if err != nil {
		return errors.AddContext(err, "failed to get wallet's primary seed")
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := proto.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, snapshotKeySpecifier)
	defer fastrand.Read(secret[:])

	// split the snapshot .sia file into sectors
	var sectors [][]byte
	for buf := bytes.NewBuffer(dotSia); buf.Len() > 0; {
		sector := make([]byte, modules.SectorSize)
		copy(sector, buf.Next(len(sector)))
		sectors = append(sectors, sector)
	}
	if len(sectors) > 4 {
		return errors.New("snapshot is too large")
	}

	// upload the siafile, creating a snapshotEntry
	var name [96]byte
	copy(name[:], meta.Name)
	entry := snapshotEntry{
		Name:         name,
		UID:          meta.UID,
		CreationDate: meta.CreationDate,
		Size:         meta.Size,
	}
	for j, piece := range sectors {
		root, err := host.Upload(piece)
		if err != nil {
			return err
		}
		entry.DataSectors[j] = root
	}

	// download the current entry table
	tableSector, err := host.DownloadIndex(0, 0, uint32(modules.SectorSize))
	if err != nil {
		return err
	}
	// decrypt the table
	c, err := twofish.NewCipher(secret[:])
	aead, err := cipher.NewGCM(c)
	if err != nil {
		return err
	}
	encTable, err := crypto.DecryptWithNonce(tableSector, aead)

	// check that the sector actually contains an entry table. If so, we
	// should replace the old table; if not, we should swap the old
	// sector to the end, but not delete it.
	haveValidTable := err == nil && bytes.Equal(encTable[:16], snapshotTableSpecifier[:])

	// update the entry table
	var entryTable []snapshotEntry
	if haveValidTable {
		if err := encoding.Unmarshal(encTable[16:], &entryTable); err != nil {
			return err
		}
	}
	entryTable = append(entryTable, entry)

	// if entryTable is too large to fit in a sector, repeatedly remove the
	// oldest entry until it fits
	sort.Slice(r.persist.UploadedBackups, func(i, j int) bool {
		return r.persist.UploadedBackups[i].CreationDate > r.persist.UploadedBackups[j].CreationDate
	})
	overhead := types.SpecifierLen + aead.Overhead() + aead.NonceSize()
	for len(encoding.Marshal(entryTable))+overhead > int(modules.SectorSize) {
		entryTable = entryTable[:len(entryTable)-1]
	}

	// encode and encrypt the table
	newTable := make([]byte, modules.SectorSize-uint64(aead.Overhead()+aead.NonceSize()))
	copy(newTable[:16], snapshotTableSpecifier[:])
	copy(newTable[16:], encoding.Marshal(entryTable))
	tableSector = crypto.EncryptWithNonce(newTable, aead)

	// swap the new entry table into index 0 and delete the old one
	// (unless it wasn't an entry table)
	if _, err := host.Replace(tableSector, 0, haveValidTable); err != nil {
		return err
	}
	return nil
}

func (r *Renter) managedSaveSnapshot(meta modules.UploadedBackup) error {
	id := r.mu.RLock()
	defer r.mu.RUnlock(id)
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
	// snapshot. We check the size by encoding a slice of snapshotEntrys, adding
	// the encryption overhead, and removing elements from the slice until the
	// encoded size fits on the host.
	//
	// NOTE: snapshotEntrys are constant-size, so we don't need to initialize
	// them beyond a simple 'make'.
	entryTable := make([]snapshotEntry, len(r.persist.UploadedBackups))
	c, _ := twofish.NewCipher(make([]byte, 32))
	aead, _ := cipher.NewGCM(c)
	overhead := types.SpecifierLen + aead.Overhead() + aead.NonceSize()
	for len(encoding.Marshal(entryTable))+overhead > int(modules.SectorSize) {
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

// managedUploadSnapshot uploads a snapshot .sia file to all hosts.
func (r *Renter) managedUploadSnapshot(meta modules.UploadedBackup, dotSia []byte) error {
	contracts := r.hostContractor.Contracts()

	// count good hosts
	var total int
	for _, c := range contracts {
		if c.Utility.GoodForUpload {
			total++
		}
	}

	// upload the siafile and update the entry table for each host
	var succeeded int
	for _, c := range contracts {
		if !c.Utility.GoodForUpload {
			continue
		}
		err := func() error {
			host, err := r.hostContractor.Session(c.HostPublicKey, r.tg.StopChan())
			if err != nil {
				return err
			}
			defer host.Close()
			return r.managedUploadSnapshotHost(meta, dotSia, host)
		}()
		if err != nil {
			r.log.Printf("Uploading snapshot to host %v failed: %v", c.HostPublicKey, err)
			continue
		}
		succeeded++
		pct := 100 * float64(succeeded) / float64(total)
		meta.UploadProgress = calcSnapshotUploadProgress(100, pct)
		if err := r.managedSaveSnapshot(meta); err != nil {
			return err
		}
	}
	if succeeded == 0 {
		r.log.Println("WARN: Failed to upload snapshot to at least one host")
	}
	// save final version of snapshot
	meta.UploadProgress = calcSnapshotUploadProgress(100, 100)
	if err := r.managedSaveSnapshot(meta); err != nil {
		return err
	}

	return nil
}

// managedDownloadSnapshotTable downloads the snapshot entry table from the specified host.
func (r *Renter) managedDownloadSnapshotTable(host contractor.Session) ([]snapshotEntry, error) {
	// Get the wallet seed.
	ws, _, err := r.w.PrimarySeed()
	if err != nil {
		return nil, errors.AddContext(err, "failed to get wallet's primary seed")
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := proto.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, snapshotKeySpecifier)
	defer fastrand.Read(secret[:])

	// download the entry table
	tableSector, err := host.DownloadIndex(0, 0, uint32(modules.SectorSize))
	if err != nil {
		return nil, err
	}
	// decrypt the table
	c, err := twofish.NewCipher(secret[:])
	aead, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}
	encTable, err := crypto.DecryptWithNonce(tableSector, aead)
	if err != nil || !bytes.Equal(encTable[:16], snapshotTableSpecifier[:]) {
		// either the first sector was not an entry table, or it got corrupted
		// somehow; either way, it's not retrievable, so we'll treat this as
		// equivalent to having no entry table at all. This is not an error; it
		// just means that when we upload a snapshot, we'll have to create a new
		// table.
		return nil, nil
	}

	var entryTable []snapshotEntry
	if err := encoding.Unmarshal(encTable[16:], &entryTable); err != nil {
		return nil, err
	}
	return entryTable, nil
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
	rs := proto.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, snapshotKeySpecifier)
	defer fastrand.Read(secret[:])

	contracts := r.hostContractor.Contracts()

	// try each host individually
	for i := range contracts {
		err := func() error {
			host, err := r.hostContractor.Session(contracts[i].HostPublicKey, r.tg.StopChan())
			if err != nil {
				return err
			}
			defer host.Close()
			entryTable, err := r.managedDownloadSnapshotTable(host)
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
				data, err := host.Download(root, 0, uint32(modules.SectorSize))
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
				Name:           string(bytes.TrimRight(entry.Name[:], string(0))),
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
					Name:           string(bytes.TrimRight(e.Name[:], string(0))),
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
		// Build a set of the snapshots we already have.
		known := make(map[[16]byte]struct{})
		id := r.mu.RLock()
		for _, ub := range r.persist.UploadedBackups {
			known[ub.UID] = struct{}{}
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
		err := func() error {
			// Download the host's entry table.
			host, err := r.hostContractor.Session(c.HostPublicKey, r.tg.StopChan())
			if err != nil {
				return err
			}
			defer host.Close()
			entryTable, err := r.managedDownloadSnapshotTable(host)
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
					r.log.Println("Located new snapshot", ub.Name, "on host", c.HostPublicKey)
					known[ub.UID] = struct{}{}
					if err := r.managedSaveSnapshot(ub); err != nil {
						return err
					}
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
				if err := r.managedUploadSnapshotHost(ub, dotSia, host); err != nil {
					return err
				}
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
		r.log.Println("Synchronized snapshots on host", c.HostPublicKey)
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
