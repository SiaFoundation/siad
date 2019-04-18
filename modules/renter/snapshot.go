package renter

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

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

	// Redundancy settings for uploading snapshots. They are supposed to have a
	// high redundancy.
	snapshotDataPieces   = 1
	snapshotParityPieces = 40
)

// CreateSnapshot creates a backup of the renter which is uploaded to the sia
// network as a snapshot and can be retrieved using only the seed.
func (r *Renter) CreateSnapshot() error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	return r.managedCreateSnapshot()
}

func (r *Renter) managedCreateSnapshot() error {
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
	backupName := fmt.Sprint(time.Now().Unix)
	backupDst := filepath.Join(os.TempDir(), backupName)
	// Create the backup.
	if err := r.managedCreateBackup(backupDst, secret[:32]); err != nil {
		return errors.AddContext(err, "failed to create backup for snapshot")
	}
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
	ec, err := siafile.NewRSSubCode(snapshotDataPieces, snapshotParityPieces, crypto.SegmentSize)
	if err != nil {
		return err
	}
	up := modules.FileUploadParams{
		SiaPath:     sp,
		ErasureCode: ec,
		Force:       false,
	}
	// Upload the backup.
	if err := r.UploadStreamFromReader(up, backup); err != nil {
		return errors.AddContext(err, "failed to upload backup")
	}
	panic("not implemented yet")
	return nil
}
