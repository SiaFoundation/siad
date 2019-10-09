package renter

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// copyFile is a helper function to copy a file to a destination.
func copyFile(fromPath, toPath string) error {
	err := os.MkdirAll(filepath.Dir(toPath), 0700)
	if err != nil {
		return err
	}
	from, err := os.Open(fromPath)
	if err != nil {
		return err
	}
	to, err := os.OpenFile(toPath, os.O_RDWR|os.O_CREATE, 0700)
	if err != nil {
		return err
	}
	_, err = io.Copy(to, from)
	if err != nil {
		return err
	}
	if err = from.Close(); err != nil {
		return err
	}
	if err = to.Close(); err != nil {
		return err
	}
	return nil
}

// deleteDuringDownloadAndStream will download and stream a file in parallel, it
// will then sleep to ensure the download and stream have downloaded some data,
// then it will delete the file
func deleteDuringDownloadAndStream(r *siatest.TestNode, rf *siatest.RemoteFile, t *testing.T, wg *sync.WaitGroup, sleep time.Duration) {
	defer wg.Done()
	wgDelete := new(sync.WaitGroup)
	// Download the file
	wgDelete.Add(1)
	go func() {
		defer wgDelete.Done()
		_, _, err := r.DownloadToDisk(rf, false)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Stream the File
	wgDelete.Add(1)
	go func() {
		defer wgDelete.Done()
		_, err := r.Stream(rf)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Delete the file
	wgDelete.Add(1)
	go func() {
		defer wgDelete.Done()
		// Wait to ensure download and stream have started
		time.Sleep(sleep)
		err := r.RenterDeletePost(rf.SiaPath())
		if err != nil {
			t.Error(err)
		}
	}()

	// Wait for the method's go routines to finish
	wgDelete.Wait()

}

// renameDuringDownloadAndStream will download and stream a file in parallel, it
// will then sleep to ensure the download and stream have downloaded some data,
// then it will rename the file
func renameDuringDownloadAndStream(r *siatest.TestNode, rf *siatest.RemoteFile, t *testing.T, wg *sync.WaitGroup, sleep time.Duration) {
	defer wg.Done()
	wgRename := new(sync.WaitGroup)
	// Download the file
	wgRename.Add(1)
	go func() {
		defer wgRename.Done()
		_, _, err := r.DownloadToDisk(rf, false)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Stream the File
	wgRename.Add(1)
	go func() {
		defer wgRename.Done()
		_, err := r.Stream(rf)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Rename the file
	wgRename.Add(1)
	go func() {
		defer wgRename.Done()
		// Wait to ensure download and stream have started
		time.Sleep(sleep)
		var err error
		rf, err = r.Rename(rf, modules.RandomSiaPath())
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Wait for the method's go routines to finish
	wgRename.Wait()
}
