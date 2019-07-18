package renter

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

// snapshotworker.go houses code that is common to all of the snapshot related
// jobs that a worker may have to perform.

// managedDownloadSnapshotTable downloads the snapshot entry table from the
// worker's host.
//
// TODO: remove the session as a required input since we'll be using the
// worker's session instead.
//
// TODO: switch this from being a method on the renter to being a method on the
// worker. After the worker has a session in it, this will remove the need to
// have a snapshot table.
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

	// download the entry table
	tableSector, err := session.DownloadIndex(0, 0, uint32(modules.SectorSize))
	if err != nil {
		if strings.Contains(err.Error(), "invalid sector bounds") {
			// host is not storing any data yet; return an empty table.
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
		return nil, nil
	}

	var entryTable []snapshotEntry
	if err := encoding.Unmarshal(encTable[16:], &entryTable); err != nil {
		return nil, err
	}
	return entryTable, nil
}
