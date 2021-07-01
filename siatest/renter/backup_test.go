package renter

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/siatest"
	"go.sia.tech/siad/types"
)

// TestCreateLoadBackup tests that creating a backup with the /renter/backup
// endpoint works as expected and that it can be loaded with the
// /renter/recoverbackup endpoint.
func TestCreateLoadBackup(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Create a subdir in the renter's files folder.
	r := tg.Renters()[0]
	subDir, err := r.FilesDir().CreateDir("subDir")
	if err != nil {
		t.Fatal(err)
	}
	// Add a file to that dir.
	lf, err := subDir.NewFile(100)
	if err != nil {
		t.Fatal(err)
	}
	// Upload the file.
	dataPieces := uint64(len(tg.Hosts()) - 1)
	parityPieces := uint64(1)
	rf, err := r.UploadBlocking(lf, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	dirMDPath := filepath.Join(r.Dir, modules.RenterDir, modules.FileSystemRoot, modules.UserFolder.String(), "subDir", modules.SiaDirExtension)
	if _, err := os.Stat(dirMDPath); os.IsNotExist(err) {
		t.Fatalf(".siadir file at %v doesn't exist", dirMDPath)
	}
	// Delete the file locally.
	if err := lf.Delete(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirMDPath); os.IsExist(err) {
		t.Fatal(".siadir file does exist:", dirMDPath)
	}
	// Create a backup.
	backupPath := filepath.Join(r.FilesDir().Path(), "test.backup")
	err = r.RenterCreateLocalBackupPost(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	// Get current allowance.
	rg, err := r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	allowance := rg.Settings.Allowance
	// Reset allowance.
	if err := r.RenterAllowanceCancelPost(); err != nil {
		t.Fatal(err)
	}
	// Recover the backup into the same renter. No new files should appear but
	// the allowance should be set again.
	if err := r.RenterRecoverLocalBackupPost(backupPath); err != nil {
		t.Fatal(err)
	}
	files, err := r.Files(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("expected 1 file but got", len(files))
	}
	rg, err = r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rg.Settings.Allowance, allowance) {
		t.Fatal("allowance doesn't match allowance before reset")
	}
	// Get the renter's seed.
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Shut down the renter.
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}
	// Start a new renter from the same seed. Disable its health and repair
	// loops to avoid updating the .siadir file.
	rt := node.RenterTemplate
	rt.PrimarySeed = wsg.PrimarySeed
	nodes, err := tg.AddNodes(rt)
	if err != nil {
		t.Fatal(err)
	}
	r = nodes[0]
	// Recover the backup.
	if err := r.RenterRecoverLocalBackupPost(backupPath); err != nil {
		t.Fatal(err)
	}
	// The .siadir file should also be recovered.
	dirMDPath = filepath.Join(r.Dir, modules.RenterDir, modules.FileSystemRoot, modules.UserFolder.String(), "subDir", modules.SiaDirExtension)
	if _, err := os.Stat(dirMDPath); os.IsNotExist(err) {
		t.Fatalf(".siadir file at %v doesn't exist", dirMDPath)
	}
	// There shouldn't be a .siadir_1 file as we don't replace existing .siadir
	// files.
	if _, err := os.Stat(dirMDPath + "_1"); os.IsExist(err) {
		t.Fatal(".siadir_1 file does exist:", err)
	}
	// The file should be available and ready for download again.
	if _, _, err := r.DownloadByStream(rf); err != nil {
		t.Fatal(err)
	}
	// Delete the file and upload another file to the same siapath. This one should
	// have the same siapath but not the same UID.
	if err := r.RenterFileDeletePost(rf.SiaPath()); err != nil {
		t.Fatal(err)
	}
	subDir, err = r.FilesDir().CreateDir("subDir")
	if err != nil {
		t.Fatal(err)
	}
	lf, err = subDir.NewFileWithName(lf.FileName(), 100)
	if err != nil {
		t.Fatal(err)
	}
	rf, err = r.UploadBlocking(lf, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	// Recover the backup again. Now there should be another file with a suffix at
	// the end.
	if err := r.RenterRecoverLocalBackupPost(backupPath); err != nil {
		t.Fatal(err)
	}
	fis, err := r.RenterFilesGet(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(fis.Files) != 2 {
		t.Fatalf("Expected 2 files but got %v", len(fis.Files))
	}
	sp, err := modules.NewSiaPath(rf.SiaPath().String() + "_1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileGet(sp)
	if err != nil {
		t.Fatal(err)
	}
	// The .siadir file should still exist.
	if _, err := os.Stat(dirMDPath); os.IsNotExist(err) {
		t.Fatal(".siadir file doesn't exist")
	}
	// There shouldn't be a .siadir_1 file as we don't replace existing .siadir
	// files.
	if _, err := os.Stat(dirMDPath + "_1"); !os.IsNotExist(err) {
		t.Fatal(".siadir_1 file does exist")
	}
}

// TestInterruptBackup tests that the renter can resume uploading a backup after
// restarting.
func TestInterruptBackup(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   3,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Create a subdir in the renter's files folder.
	r := tg.Renters()[0]
	subDir, err := r.FilesDir().CreateDir("subDir")
	if err != nil {
		t.Fatal(err)
	}
	// Add a file to that dir.
	lf, err := subDir.NewFile(100)
	if err != nil {
		t.Fatal(err)
	}
	// Upload the file.
	dataPieces := uint64(len(tg.Hosts()) - 1)
	parityPieces := uint64(1)
	_, err = r.UploadBlocking(lf, dataPieces, parityPieces, false)
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Create a snapshot.
	if err := r.RenterCreateBackupPost("foo"); err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
	// The snapshot should be listed and not 100% uploaded.
	ubs, err := r.RenterBackups()
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	} else if len(ubs.Backups) != 1 {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal("expected one backup, got", ubs)
	} else if ubs.Backups[0].UploadProgress == 100 {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal("backup should not be 100% uploaded")
	}

	// Restart the renter node.
	if err := r.RestartNode(); err != nil {
		t.Fatal(err)
	}

	// The snapshot should still be listed and incomplete.
	ubs, err = r.RenterBackups()
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	} else if len(ubs.Backups) != 1 {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal("expected one backup, got", ubs)
	} else if ubs.Backups[0].UploadProgress == 100 {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal("backup should not be 100% uploaded")
	}

	// Wait for the snapshot to finish uploading.
	err = build.Retry(60, time.Second, func() error {
		ubs, _ := r.RenterBackups()
		if len(ubs.Backups) != 1 {
			return fmt.Errorf("expected one backup for %v", len(ubs.Backups))
		}
		if ubs.Backups[0].UploadProgress != 100 {
			return fmt.Errorf("backup not uploaded, upload progress is %v", ubs.Backups[0].UploadProgress)
		}
		return nil
	})
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
}

// TestRemoteBackup tests creating and loading remote backups.
func TestRemoteBackup(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Test Params.
	filesSize := int(20e3)

	// Create a testgroup.
	// Need 5 hosts to address an NDF with the snapshot upload code.
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Create a subdir in the renter's files folder.
	r := tg.Renters()[0]
	subDir, err := r.FilesDir().CreateDir("subDir")
	if err != nil {
		t.Fatal(err)
	}
	// Add a file to that dir.
	lf, err := subDir.NewFile(filesSize)
	if err != nil {
		t.Fatal(err)
	}
	// Upload the file.
	dataPieces := uint64(2) // for use with 5 hosts, minimizes exposure to the upload failure NDF
	parityPieces := uint64(1)
	rf, err := r.UploadBlocking(lf, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	// Create a snapshot.
	createSnapshot := func(name string) error {
		if err := r.RenterCreateBackupPost(name); err != nil {
			return err
		}
		// wait for backup to upload
		return build.Retry(60, time.Second, func() error {
			ubs, _ := r.RenterBackups()
			for _, ub := range ubs.Backups {
				if ub.Name != name {
					continue
				} else if ub.UploadProgress != 100 {
					return fmt.Errorf("backup not uploaded: %v", ub.UploadProgress)
				}
				return nil
			}
			return errors.New("backup not found")
		})
	}
	if err := createSnapshot("foo"); err != nil {
		t.Fatal(err)
	}
	// Create a snapshot with the same name again. This should fail.
	err = createSnapshot("foo")
	if err == nil || !strings.Contains(err.Error(), filesystem.ErrExists.Error()) {
		t.Fatal("creating a snapshot with the same name should fail", err)
	}
	// Delete the file locally.
	if err := lf.Delete(); err != nil {
		t.Fatal(err)
	}

	// Upload another file and take another snapshot.
	lf2, err := subDir.NewFile(filesSize)
	if err != nil {
		t.Fatal(err)
	}
	rf2, err := r.UploadBlocking(lf2, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	if err := createSnapshot("bar"); err != nil {
		t.Fatal(err)
	}
	if err := lf2.Delete(); err != nil {
		t.Fatal(err)
	}

	// Both snapshots should be listed.
	ubs, err := r.RenterBackups()
	if err != nil {
		t.Fatal(err)
	} else if len(ubs.Backups) != 2 {
		t.Fatal("expected two backups, got", ubs)
	}

	// Delete both files and restore the first snapshot.
	if err := r.RenterFileDeletePost(rf.SiaPath()); err != nil {
		t.Fatal(err)
	}
	if err := r.RenterFileDeletePost(rf2.SiaPath()); err != nil {
		t.Fatal(err)
	}
	if err := r.RenterRecoverBackupPost("foo"); err != nil {
		t.Fatal(err)
	}
	// We should be able to download the first file.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, _, err = r.DownloadToDisk(rf, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The second file should still fail.
	if _, _, err := r.DownloadToDisk(rf2, false); err == nil {
		t.Fatal("expected second file to be unavailable")
	}
	// Delete the first file again.
	if err := r.RenterFileDeletePost(rf.SiaPath()); err != nil {
		t.Fatal(err)
	}

	// Restore the second snapshot.
	if err := r.RenterRecoverBackupPost("bar"); err != nil {
		t.Fatal(err)
	}
	// We should be able to download both files now.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, _, err = r.DownloadToDisk(rf, false)
		if err != nil {
			return err
		}
		_, _, err = r.DownloadToDisk(rf2, false)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Regression test for a bug where RenterFilesGet would fail when snapshots
	// existed.
	_, err = r.RenterFilesGet(false)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm siadir exists by querying directory
	rd, err := r.RenterDirGet(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.Directories) != 2 {
		t.Fatal("Expected root and 1 subdirectory but got", rd.Directories)
	}
	if len(rd.Files) != 0 {
		t.Fatal("Expected 0 files but got", rd.Files)
	}
	rd, err = r.RenterDirGet(rd.Directories[1].SiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.Directories) != 1 {
		t.Fatal("expected only root directory but got", rd.Directories)
	}
	if len(rd.Files) != 2 {
		t.Fatal("Expected 2 files but got", rd.Files)
	}

	// Delete the renter entirely and create a new renter with the same seed.
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed := wsg.PrimarySeed
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.PrimarySeed = seed
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r = nodes[0]

	// Wait for the recovery process to complete.
	err = build.Retry(60, time.Second, func() error {
		// Both snapshots should be listed.
		ubs, err = r.RenterBackups()
		if err != nil {
			return err
		} else if len(ubs.Backups) != 2 {
			return fmt.Errorf("expected two backups, got %v", ubs.Backups)
		} else if len(ubs.SyncedHosts) != 5 {
			return fmt.Errorf("expected five hosts with synced backups, got %v", len(ubs.SyncedHosts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Restore the second snapshot.
	if err := r.RenterRecoverBackupPost("bar"); err != nil {
		t.Fatal(err)
	}
	// We should be able to download both files now.
	if _, _, err := r.DownloadToDisk(rf, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.DownloadToDisk(rf2, false); err != nil {
		t.Fatal(err)
	}

	// Confirm siadir exists by querying directory
	rd, err = r.RenterDirGet(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.Directories) != 2 {
		t.Fatal("Expected root and 1 subdirectory but got", rd.Directories)
	}
	if len(rd.Files) != 0 {
		t.Fatal("Expected 0 files but got", rd.Files)
	}
	rd, err = r.RenterDirGet(rd.Directories[1].SiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(rd.Directories) != 1 {
		t.Fatal("expected only root directory but got", rd.Directories)
	}
	if len(rd.Files) != 2 {
		t.Fatal("Expected 2 files but got", rd.Files)
	}

	// Get the list of contracts so we know what hosts to check for backups.
	contracts, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}

	// Set a ratelimit on the renter to ensure that it takes time for the
	// concurrent downloads to complete, so that the backup query does have to
	// wait through a queue.
	err = r.RenterRateLimitPost(25e3, 25e3)
	if err != nil {
		t.Fatal(err)
	}

	// Test coverage intended for workers in the renter. Ensure that the renter
	// can balance having a queue'd download and also having a queue'd request
	// to fetch the list of backups from a particular host.
	var wg sync.WaitGroup
	threads := 3
	wg.Add(threads)
	for i := 0; i < threads; i++ {
		// Queue a bunch of threads to download files in the background.
		go func() {
			defer wg.Done()

			// Download both files and return. This is here to saturate the
			// workers and ensure that any request to download the list of
			// backups from a host has to wait for a queue of downloads.
			if _, _, err := r.DownloadToDisk(rf, false); err != nil {
				t.Error(err)
			}
			if _, _, err := r.DownloadToDisk(rf2, false); err != nil {
				t.Error(err)
			}
		}()
	}
	// Wait for all of the threads to finish before returning.
	defer wg.Wait()

	// While the downloads are happening in the background, request the list of
	// backups from a host.
	for _, c := range contracts.ActiveContracts {
		backups, err := r.RenterBackupsOnHost(c.HostPublicKey)
		if err != nil {
			t.Fatal(err)
		}
		if len(backups.Backups) != 2 {
			for i, backup := range backups.Backups {
				t.Logf("%v: %v", i, backup.Name)
			}
			t.Error("Wrong number of backups detected", len(backups.Backups), 2)
		}
	}

	// Error check, find out what happens when you call BackupsOnHost with a bad
	// pubkey.
	_, err = r.RenterBackupsOnHost(types.SiaPublicKey{})
	if err == nil {
		t.Error(err)
	}
}

// TestBackupRenew tests that a backup can be restored after a set of contract
// has been renewed.
func TestBackupRenew(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Test Params.
	filesSize := int(20e3)

	// Create a testgroup.
	//
	// Need 5 hosts to address an NDF with the snapshot upload code.
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Create a subdir in the renter's files folder.
	r := tg.Renters()[0]
	subDir, err := r.FilesDir().CreateDir("subDir")
	if err != nil {
		t.Fatal(err)
	}
	// Add a file to that dir.
	lf, err := subDir.NewFile(filesSize)
	if err != nil {
		t.Fatal(err)
	}
	// Upload the file.
	dataPieces := uint64(2) // for use with 5 hosts, minimizes exposure to the upload failure NDF
	parityPieces := uint64(1)
	rf, err := r.UploadBlocking(lf, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	// Create a snapshot.
	createSnapshot := func(name string) error {
		if err := r.RenterCreateBackupPost(name); err != nil {
			return err
		}
		// wait for backup to upload
		return build.Retry(60, time.Second, func() error {
			ubs, _ := r.RenterBackups()
			for _, ub := range ubs.Backups {
				if ub.Name != name {
					continue
				} else if ub.UploadProgress != 100 {
					return fmt.Errorf("backup not uploaded: %v", ub.UploadProgress)
				}
				return nil
			}
			return errors.New("backup not found")
		})
	}
	if err := createSnapshot("foo"); err != nil {
		t.Fatal(err)
	}
	// Delete the file locally.
	if err := lf.Delete(); err != nil {
		t.Fatal(err)
	}

	// Snapshot should be listed.
	ubs, err := r.RenterBackups()
	if err != nil {
		t.Fatal(err)
	} else if len(ubs.Backups) != 1 {
		t.Fatal("expected one backup, got", ubs)
	}

	// Renew all contracts and then wait for them to expire.
	err = siatest.RenewContractsByRenewWindow(r, tg)
	if err != nil {
		t.Fatal(err)
	}

	numRetries := 0
	build.Retry(600, 100*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			err = tg.Miners()[0].MineBlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		numRetries++
		return siatest.CheckExpectedNumberOfContracts(r, len(tg.Hosts()), 0, 0, 0, len(tg.Hosts()), 0)
	})

	// Recover from backups.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			err = tg.Miners()[0].MineBlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		numRetries++
		return r.RenterRecoverBackupPost("foo")
	})
	if err != nil {
		t.Fatal(err)
	}
	// We should be able to download the first file.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, _, err = r.DownloadToDisk(rf, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Delete the renter entirely and create a new renter with the same seed.
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed := wsg.PrimarySeed
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}
	renterParams := node.Renter(filepath.Join(testDir, "renter_recovered"))
	renterParams.PrimarySeed = seed
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r = nodes[0]

	// Recover from backups.
	err = build.Retry(600, 100*time.Millisecond, func() error {
		return r.RenterRecoverBackupPost("foo")
	})
	if err != nil {
		t.Fatal(err)
	}
	// We should be able to download the first file.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, _, err = r.DownloadToDisk(rf, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
