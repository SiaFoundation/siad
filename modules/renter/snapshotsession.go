package renter

// snapshotsession.go contains methods related to fetching snapshot data over a
// session with a host.
//
// TODO: The implementation for managedDownloadSnapshotTable currently silences
// several errors, these errors should be handled explicitly.

import (
	"bytes"
	"strings"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/contractor"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
)

// managedDownloadSnapshotTable will fetch the snapshot table from the host.
func (r *Renter) managedDownloadSnapshotTable(session contractor.Session) ([]snapshotEntry, error) {
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

	// Download the table of snapshots that the host is storing.
	tableSector, err := session.DownloadIndex(0, 0, uint32(modules.SectorSize))
	if err != nil {
		if strings.Contains(err.Error(), "invalid sector bounds") {
			// host is not storing any data yet; return an empty table.
			//
			// TODO: Should retrun an error that the host does not have any
			// snapshots / has not been prepared for snapshots yet.
			return nil, nil
		}
		return nil, err
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
		//
		// TODO: Should return an error that decryption failed / there appears
		// to be corruption.
		return nil, nil
	}

	var entryTable []snapshotEntry
	if err := encoding.Unmarshal(encTable[16:], &entryTable); err != nil {
		return nil, err
	}
	return entryTable, nil
}

// callDownloadSnapshotTable downloads the snapshot entry table from the
// worker's host.
func (r *Renter) callFetchHostBackups(session contractor.Session) ([]modules.UploadedBackup, error) {
	entryTable, err := r.managedDownloadSnapshotTable(session)
	if err != nil {
		return nil, errors.AddContext(err, "unable to download snapshot table")
	}

	// Format the response and return the response to the requester.
	uploadedBackups := make([]modules.UploadedBackup, len(entryTable))
	for i, e := range entryTable {
		uploadedBackups[i] = modules.UploadedBackup{
			Name:           string(bytes.TrimRight(e.Name[:], string(0))),
			UID:            e.UID,
			CreationDate:   e.CreationDate,
			Size:           e.Size,
			UploadProgress: 100,
		}
	}
	return uploadedBackups, nil
}
