package renter

import (
	"bytes"
	"crypto/cipher"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"golang.org/x/crypto/twofish"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// A snapshotEntry is an entry within the snapshot table, identifying both the
// snapshot metadata and the other sectors on the host storing the snapshot
// data.
type snapshotEntry struct {
	Meta        modules.UploadedBackup
	DataSectors [4]crypto.Hash // pointers to sectors containing snapshot .sia file
}

var (
	// SnapshotKeySpecifier is the specifier used for deriving the secret used to
	// encrypt a snapshot from the RenterSeed.
	snapshotKeySpecifier = types.Specifier{'s', 'n', 'a', 'p', 's', 'h', 'o', 't'}

	// snapshotTableSpecifier is the specifier used to identify a snapshot entry
	// table stored in a sector.
	snapshotTableSpecifier = types.Specifier{'S', 'n', 'a', 'p', 's', 'h', 'o', 't', 'T', 'a', 'b', 'l', 'e'}
)

// UploadedBackups returns the backups that the renter can download.
func (r *Renter) UploadedBackups() ([]modules.UploadedBackup, error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()
	id := r.mu.RLock()
	defer r.mu.RUnlock(id)
	return r.persist.UploadedBackups, nil
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
	return r.uploadSnapshot(name, dotSia)
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
	var encName [96]byte
	copy(encName[:], name)
	var uid [16]byte
	var found bool
	for _, b := range r.persist.UploadedBackups {
		if b.Name == encName {
			uid = b.UID
			found = true
			break
		}
	}
	if !found {
		return errors.New("no record of a backup with that name")
	}
	// Download snapshot's .sia file.
	dotSia, err := r.downloadSnapshot(uid)
	if err != nil {
		return err
	}
	// Store it in the backup file set.
	if err := ioutil.WriteFile(filepath.Join(r.staticBackupsDir, name), dotSia, 0666); err != nil {
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

// uploadSnapshot uploads a snapshot .sia file to all hosts.
func (r *Renter) uploadSnapshot(name string, dotSia []byte) error {
	meta := modules.UploadedBackup{
		CreationDate: types.CurrentTimestamp(),
		Size:         uint64(len(dotSia)),
	}
	if len(name) > len(meta.Name) {
		return errors.New("name is too long")
	}
	copy(meta.Name[:], name)
	fastrand.Read(meta.UID[:])

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
			// if entryTable is too large to fit in a sector, remove old entries until it fits
			overhead := types.SpecifierLen + aead.Overhead() + aead.NonceSize()
			for len(encoding.Marshal(entryTable))+overhead > int(modules.SectorSize) {
				entryTable = entryTable[1:]
			}
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
	r.persist.UploadedBackups = append(r.persist.UploadedBackups, meta)
	if err := r.saveSync(); err != nil {
		return err
	}

	return nil
}

// downloadSnapshot downloads and returns the specified snapshot.
func (r *Renter) downloadSnapshot(uid [16]byte) (dotSia []byte, err error) {
	if err := r.tg.Add(); err != nil {
		return nil, err
	}
	defer r.tg.Done()

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
			// decrypt the table
			c, err := twofish.NewCipher(secret[:])
			aead, err := cipher.NewGCM(c)
			if err != nil {
				return err
			}
			encTable, err := crypto.DecryptWithNonce(tableSector, aead)
			if err != nil {
				return err
			} else if !bytes.Equal(encTable[:16], snapshotTableSpecifier[:]) {
				return errors.New("index 0 sector does not contain a snapshot table")
			}

			var entryTable []snapshotEntry
			if err := encoding.Unmarshal(encTable[16:], &entryTable); err != nil {
				return err
			}
			// search for the desired snapshot
			var entry *snapshotEntry
			for j := range entryTable {
				if entryTable[j].Meta.UID == uid {
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
				if uint64(len(dotSia)) >= entry.Meta.Size {
					dotSia = dotSia[:entry.Meta.Size]
					break
				}
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
