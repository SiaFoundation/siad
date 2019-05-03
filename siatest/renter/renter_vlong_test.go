package renter

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestStresstestSiaFileSet is a vlong test that performs multiple operations
// which modify the siafileset in parallel for a period of time.
func TestStresstestSiaFileSet(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	// Create a group for the test.
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(renterTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Run the test for a set amount of time.
	timer := time.NewTimer(2 * time.Minute)
	stop := make(chan struct{})
	go func() {
		<-timer.C
		close(stop)
	}()
	wg := new(sync.WaitGroup)
	r := tg.Renters()[0]
	// Upload params.
	dataPieces := uint64(1)
	parityPieces := uint64(1)
	// One thread uploads new files.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Get a random directory to upload the file to.
			dirs, err := r.Dirs()
			if err != nil && strings.Contains(err.Error(), siadir.ErrUnknownPath.Error()) {
				continue
			}
			if err != nil && strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			dir := dirs[fastrand.Intn(len(dirs))]
			sp, err := dir.Join(persist.RandomSuffix())
			if err != nil {
				t.Fatal(err)
			}
			// 30% chance for the file to be a 0-byte file.
			size := int(modules.SectorSize) + siatest.Fuzz()
			if fastrand.Intn(3) == 0 {
				size = 0
			}
			// Upload the file
			lf, err := r.FilesDir().NewFile(size)
			if err != nil {
				t.Fatal(err)
			}
			rf, err := r.Upload(lf, sp, dataPieces, parityPieces, false)
			if err != nil && !strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) && !errors.Contains(err, siatest.ErrFileNotTracked) {
				t.Fatal(err)
			}
			if err := r.WaitForUploadRedundancy(rf, 1.0); err != nil && !errors.Contains(err, siatest.ErrFileNotTracked) {
				t.Fatal(err)
			}
			time.Sleep(time.Duration(fastrand.Intn(1000))*time.Millisecond + time.Second) // between 1s and 2s
		}
	}()
	// One thread force uploads new files to an existing siapath.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Get existing files and choose one randomly.
			files, err := r.Files()
			if err != nil {
				t.Fatal(err)
			}
			// If there are no files we try again later.
			if len(files) == 0 {
				time.Sleep(time.Second)
				continue
			}
			// 30% chance for the file to be a 0-byte file.
			size := int(modules.SectorSize) + siatest.Fuzz()
			if fastrand.Intn(3) == 0 {
				size = 0
			}
			// Upload the file.
			sp := files[fastrand.Intn(len(files))].SiaPath
			lf, err := r.FilesDir().NewFile(size)
			if err != nil {
				t.Fatal(err)
			}
			rf, err := r.Upload(lf, sp, dataPieces, parityPieces, true)
			if err != nil && !strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) && !errors.Contains(err, siatest.ErrFileNotTracked) {
				t.Fatal(err)
			}
			if err := r.WaitForUploadRedundancy(rf, 1.0); err != nil && !errors.Contains(err, siatest.ErrFileNotTracked) {
				t.Fatal(err)
			}
			time.Sleep(time.Duration(fastrand.Intn(4000))*time.Millisecond + time.Second) // between 4s and 5s
		}
	}()
	// One thread renames files and sometimes uploads a new file directly
	// afterwards.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Get existing files and choose one randomly.
			files, err := r.Files()
			if err != nil {
				t.Fatal(err)
			}
			// If there are no files we try again later.
			if len(files) == 0 {
				time.Sleep(time.Second)
				continue
			}
			sp := files[fastrand.Intn(len(files))].SiaPath
			err = r.RenterRenamePost(sp, modules.RandomSiaPath())
			if err != nil && !strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
				t.Fatal(err)
			}
			// 50% chance to replace renamed file with new one.
			if fastrand.Intn(2) == 0 {
				lf, err := r.FilesDir().NewFile(int(modules.SectorSize) + siatest.Fuzz())
				if err != nil {
					t.Fatal(err)
				}
				err = r.RenterUploadForcePost(lf.Path(), sp, dataPieces, parityPieces, false)
				if err != nil && !strings.Contains(err.Error(), siafile.ErrPathOverload.Error()) {
					t.Fatal(err)
				}
			}
			time.Sleep(time.Duration(fastrand.Intn(4000))*time.Millisecond + time.Second) // between 4s and 5s
		}
	}()
	// One thread deletes files.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Get existing files and choose one randomly.
			files, err := r.Files()
			if err != nil {
				t.Fatal(err)
			}
			// If there are no files we try again later.
			if len(files) == 0 {
				time.Sleep(time.Second)
				continue
			}
			sp := files[fastrand.Intn(len(files))].SiaPath
			err = r.RenterDeletePost(sp)
			if err != nil && !strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
				t.Fatal(err)
			}
			time.Sleep(time.Duration(fastrand.Intn(5000))*time.Millisecond + time.Second) // between 5s and 6s
		}
	}()
	// One thread creates empty dirs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Get a random directory to create a dir in.
			dirs, err := r.Dirs()
			if err != nil && strings.Contains(err.Error(), siadir.ErrUnknownPath.Error()) {
				continue
			}
			if err != nil && strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			dir := dirs[fastrand.Intn(len(dirs))]
			sp, err := dir.Join(persist.RandomSuffix())
			if err != nil {
				t.Fatal(err)
			}
			if err := r.RenterDirCreatePost(sp); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Duration(fastrand.Intn(500))*time.Millisecond + 500*time.Millisecond) // between 0.5s and 1s
		}
	}()
	// One thread deletes a random directory and sometimes creates an empty one
	// in its place or simply renames it to be a sub dir of a random directory.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Get a random directory to delete.
			dirs, err := r.Dirs()
			if err != nil && strings.Contains(err.Error(), siadir.ErrUnknownPath.Error()) {
				continue
			}
			if err != nil && strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			dir := dirs[fastrand.Intn(len(dirs))]
			// Make sure that dir isn't the root.
			if dir.Equals(modules.RootSiaPath()) {
				continue
			}
			if fastrand.Intn(2) == 0 {
				// 50% chance to delete and recreate the directory.
				if err := r.RenterDirDeletePost(dir); err != nil {
					t.Fatal(err)
				}
				err := r.RenterDirCreatePost(dir)
				// NOTE we could probably avoid ignoring ErrPathOverload if we
				// decided that `siadir.New` returns a potentially existing
				// directory instead.
				if err != nil && !strings.Contains(err.Error(), siadir.ErrPathOverload.Error()) {
					t.Fatal(err)
				}
			} else {
				// 50% chance to rename the directory to be the child of a
				// random existing directory.
				newParent := dirs[fastrand.Intn(len(dirs))]
				newDir, err := newParent.Join(persist.RandomSuffix())
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(newDir.String(), dir.String()) {
					continue // can't rename folder into itself
				}
				if err := r.RenterDirRenamePost(dir, newDir); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(time.Duration(fastrand.Intn(500))*time.Millisecond + 500*time.Millisecond) // between 0.5s and 1s
		}
	}()
	// One thread kills hosts to trigger repairs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// Break out if we only have dataPieces hosts left.
			hosts := tg.Hosts()
			if uint64(len(hosts)) == dataPieces {
				break
			}
			time.Sleep(10 * time.Second)
			// Choose random host.
			host := hosts[fastrand.Intn(len(hosts))]
			if err := tg.RemoveNode(host); err != nil {
				t.Fatal(err)
			}
		}
	}()
	// Wait until threads are done.
	wg.Wait()
}

// TestUploadStreamFailAndRepair kills an upload stream halfway through and
// repairs the file afterwards using the same endpoint.
func TestUploadStreamFailAndRepair(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Add a renter with a dependency that causes an upload to fail after a certain
	// number of chunks.
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	deps := dependencies.NewDependencyDisruptUploadStream(5)
	renterParams.RenterDeps = deps
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// Use upload streaming to upload a file. This should fail in the middle.
	data := fastrand.Bytes(int(10 * modules.SectorSize))
	sp := modules.RandomSiaPath()
	deps.Fail()
	err = renter.RenterUploadStreamPost(bytes.NewReader(data), sp, 1, 1, false)
	deps.Disable()
	if err == nil {
		t.Fatal("upload streaming should fail but didn't")
	}
	// Redundancy should be 0 because the last chunk's upload was interrupted.
	fi, err := renter.RenterFileGet(sp)
	if err != nil {
		t.Fatal(err)
	}
	if fi.File.Redundancy != 0 {
		t.Fatalf("Expected redundancy to be 0 but was %v", fi.File.Redundancy)
	}
	// Repair the file.
	if err := renter.RenterUploadStreamRepairPost(bytes.NewReader(data), sp); err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		fi, err = renter.RenterFileGet(sp)
		if err != nil {
			return err
		}
		// FileSize should be set correctly.
		if fi.File.Filesize != uint64(len(data)) {
			return fmt.Errorf("Filesize should be %v but was %v", len(data), fi.File.Filesize)
		}
		// Redundancy should be 2 after a successful repair.
		if fi.File.Redundancy != 2.0 {
			return fmt.Errorf("Expected redundancy to be 2.0 but was %v", fi.File.Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Make sure we can download the file.
	downloadedData, err := renter.RenterDownloadHTTPResponseGet(sp, 0, uint64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, downloadedData) {
		t.Fatal("downloaded data doesn't match uploaded data")
	}
}
