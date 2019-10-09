package fuse

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

func TestFUSE(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	if runtime.GOOS != "linux" {
		t.Skip("Skipping FUSE test on non-Linux OS")
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := siatest.TestDir("fuse", t.Name())
	if err := os.MkdirAll(testDir, 0777); err != nil {
		t.Fatal(err)
	}
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

	// We should be able to download the first file.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, _, err = r.DownloadToDisk(rf, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a fuse mountpoint and mount subDir there.
	mountpoint := filepath.Join(testDir, "mnt")
	if err := os.MkdirAll(mountpoint, 0777); err != nil {
		t.Fatal(err)
	}
	err = r.RenterFUSEMount(r.SiaPath(subDir.Path()), mountpoint, true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.RenterFUSEUnmount()

	// We should be able to see the siafile in the directory.
	infos, err := ioutil.ReadDir(filepath.Join(mountpoint, "subDir"))
	if err != nil {
		t.Fatal(err)
	} else if len(infos) != 1 || infos[0].Name() != lf.FileName() || infos[0].Size() != 100 {
		t.Fatal("wrong dir info")
	}

	// Read the file and verify its contents
	data, err := ioutil.ReadFile(filepath.Join(mountpoint, "subDir", lf.FileName()))
	if err != nil {
		t.Fatal(err)
	}
	if err := lf.Equal(data); err != nil {
		t.Fatal(err)
	}
}
