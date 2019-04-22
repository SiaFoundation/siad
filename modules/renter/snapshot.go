package renter

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// SnapshotKeySpecifier is the specifier used for deriving the secret used to
	// encrypt a snapshot from the RenterSeed.
	snapshotKeySpecifier = types.Specifier{'s', 'n', 'a', 'p', 's', 'h', 'o', 't'}
)

// TakeSnapshot creates a backup of the renter which is uploaded to the sia
// network as a snapshot and can be retrieved using only the seed.
func (r *Renter) TakeSnapshot() error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.managedTakeSnapshot()
}

// managedTakeSnapshot creates a backup of the renter which is uploaded to the
// sia network as a snapshot and can be retrieved using only the seed.
func (r *Renter) managedTakeSnapshot() error {
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
	// Get a temporary location for the backup.
	backupName := fmt.Sprint(time.Now().Unix())
	backupDst := filepath.Join(os.TempDir(), backupName)
	// Create the backup and delte it afterwards.
	if err := r.managedCreateBackup(backupDst, secret[:32]); err != nil {
		return errors.AddContext(err, "failed to create backup for snapshot")
	}
	defer os.Remove(backupDst)
	// Open the backup for uploading.
	backup, err := os.Open(backupDst)
	if err != nil {
		return errors.AddContext(err, "failed to open backup for uploading")
	}
	defer backup.Close()
	// Prepare the siapath.
	sp, err := modules.NewSiaPath(backupName)
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
	// Upload the backup.
	if err := r.managedUploadStreamFromReader(up, backup, true); err != nil {
		return errors.AddContext(err, "failed to upload backup")
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
	return r.UploadSnapshot(sp.String(), dotSia)
}

// SnapshotMetadata contains metadata about a snapshot backup.
type SnapshotMetadata struct {
	Name         [96]byte
	UID          [16]byte
	CreationDate types.Timestamp
	Size         uint64 // size of snapshot .sia file
}

// A snapshotEntry is an entry within the snapshot table, identifying both the
// snapshot metadata and the other sectors on the host storing the snapshot
// data.
type snapshotEntry struct {
	Meta        SnapshotMetadata
	DataSectors [4]crypto.Hash // pointers to sectors containing snapshot .sia file
}

// UploadSnapshot uploads a snapshot .sia file to all hosts.
func (r *Renter) UploadSnapshot(name string, dotSia []byte) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	meta := SnapshotMetadata{
		CreationDate: types.CurrentTimestamp(),
		Size:         uint64(len(dotSia)),
	}
	copy(meta.Name[:], name)
	fastrand.Read(meta.UID[:])

	contracts := r.hostContractor.Contracts()

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

	// upload the siafile and update the entry table for each host
	var numSuccessful int
	for i := range contracts {
		hostKey := contracts[i].HostPublicKey
		utility, ok := r.hostContractor.ContractUtility(hostKey)
		if !ok || !utility.GoodForUpload {
			continue
		}
		err := func() error {
			host, err := r.hostContractor.Session(hostKey, r.tg.StopChan())
			if err != nil {
				return err
			}
			// upload the siafile, creating a snapshotEntry
			entry := snapshotEntry{Meta: meta}
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
			// update the entry table
			var entryTable []snapshotEntry
			if err := encoding.Unmarshal(tableSector, &entryTable); err != nil {
				return err
			}
			entryTable = append(entryTable, entry)
			// if entryTable is too large to fit in a sector, remove old entries until it fits
			for len(encoding.Marshal(entryTable)) > int(modules.SectorSize) {
				entryTable = entryTable[1:]
			}
			copy(tableSector, encoding.Marshal(entryTable))
			// replace the new entry table
			if _, err := host.Replace(tableSector, 0); err != nil {
				return err
			}
			return nil
		}()
		if err != nil {
			r.log.Printf("Uploading snapshot to host %v failed: %v", hostKey, err)
			continue
		}
		numSuccessful++
	}
	if numSuccessful == 0 {
		return errors.New("failed to upload to at least one host")
	}
	r.persist.Snapshots = append(r.persist.Snapshots, meta)
	if err := r.saveSync(); err != nil {
		return err
	}

	return nil
}

// DownloadSnapshot downloads the specified snapshot.
func (r *Renter) DownloadSnapshot(m SnapshotMetadata) (dotSia []byte, err error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()

	contracts := r.hostContractor.Contracts()

	// try each host individually
	for i := range contracts {
		err := func() error {
			host, err := r.hostContractor.Session(contracts[i].HostPublicKey, r.tg.StopChan())
			if err != nil {
				return err
			}
			// download the entry table
			tableSector, err := host.DownloadIndex(0, 0, uint32(modules.SectorSize))
			if err != nil {
				return err
			}
			var entryTable []snapshotEntry
			if err := encoding.Unmarshal(tableSector, &entryTable); err != nil {
				return err
			}
			// search for the desired snapshot
			var entry *snapshotEntry
			for j := range entryTable {
				if entryTable[j].Meta.UID == m.UID {
					entry = &entryTable[j]
					break
				}
			}
			if entry == nil {
				return errors.New("entry table does not contain snapshot")
			}
			// download the entry
			dotSia = nil
			rem := entry.Meta.Size
			for _, root := range entry.DataSectors {
				size := rem
				if size > modules.SectorSize {
					size = modules.SectorSize
				}
				data, err := host.Download(root, 0, uint32(size))
				if err != nil {
					return err
				}
				dotSia = append(dotSia, data...)
				rem -= size
			}
			return nil
		}()
		if err != nil {
			r.log.Printf("Downloading backup from host %v failed: %v", contracts[i].HostPublicKey, err)
			continue
		}
		return dotSia, nil
	}
	return nil, errors.New("could not download backup from any host")
}

// AvailableSnapshot returns the snapshots that the renter can download.
func (r *Renter) AvailableSnapshot() ([]SnapshotMetadata, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	id := r.mu.RLock()
	defer r.mu.RUnlock(id)
	return r.persist.Snapshots, nil
}
