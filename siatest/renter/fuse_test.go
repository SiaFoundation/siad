package renter

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"

	"gitlab.com/NebulousLabs/errors"
)

// siaPathToFusePath will return the location that a file should exist on disk
// for a mounted fuse point. The full calculation requires knowing the siapath,
// the fuse root, and the mountpoint on distk.
func siaPathToFusePath(sp modules.SiaPath, fuseRoot modules.SiaPath, mountpoint string) (string, error) {
	rebased, err := sp.Rebase(modules.RootSiaPath(), fuseRoot)
	if err != nil {
		return "", errors.AddContext(err, "unable to rebase the siapath")
	}
	split := strings.Split(rebased.String(), "/")
	fusePath := mountpoint
	for len(split) > 0 {
		fusePath = filepath.Join(fusePath, split[0])
		split = split[1:]
	}
	return fusePath, nil
}

// TestFuse tests the renter's Fuse filesystem support. This test is only run on Linux.
func TestFuse(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	if runtime.GOOS != "linux" {
		t.Skip("Skipping Fuse test on non-Linux OS")
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
	r := tg.Renters()[0]

	// Try mounting an empty fuse filesystem.
	mountpoint1 := filepath.Join(testDir, "mount1")
	err = os.MkdirAll(mountpoint1, 0777)
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterFuseMount(modules.RootSiaPath(), mountpoint1, true)
	if err != nil {
		t.Fatal(err)
	}

	// Try reading the empty fuse directory.
	fuseRoot, err := os.Open(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	names, err := fuseRoot.Readdirnames(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Error("there should not be any files in the empty fuse filesystem")
	}
	infos, err := fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Error("the number of infos returned is not 0", len(infos))
	}
	err = fuseRoot.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Try unmounting the fuse filesystem.
	err = r.RenterFuseUnmount(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}

	// Mount fuse to the emtpy filesystem again, this time upload a file while
	// the system is mounted, then try to read the filesystem from the
	// directory.
	err = r.RenterFuseMount(modules.RootSiaPath(), mountpoint1, true)
	if err != nil {
		t.Fatal(err)
	}

	// Upload a file to the renter.
	localFile, remoteFile, err := r.UploadNewFileBlocking(100, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// Try reading the directory to see if the file is listed.
	fuseRoot, err = os.Open(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	names, err = fuseRoot.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", fuseRoot.Close())
	}
	if len(names) != 1 {
		t.Error("the uploaded file is not appearing as a file of the directory", len(names))
	}
	infos, err = fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		// TODO: Need to figure out what is preventing Readdir from returning
		// actual entries, and need to fix that.
		t.Log("the number of infos returned is not 1", len(infos))
	}
	err = fuseRoot.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Read that file from the fuse directory.
	path := remoteFile.SiaPath()
	fusePath, err := siaPathToFusePath(path, modules.RootSiaPath(), mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	fuseFile, err := os.Open(fusePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := ioutil.ReadAll(fuseFile)
	if err != nil {
		t.Error(err)
	}
	localFileData, err := localFile.Data()
	if err != nil {
		t.Error(err)
	}
	if bytes.Compare(data, localFileData) != 0 {
		t.Error("data from the local file and data from the fuse file do not match")
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Create a directory within the root directory and see if the directory
	// appears in the fuse area.
	//
	// TODO: The r.RenterDirCreatePost call should probably be replaced by a
	// call to r.Mkdir - where Mkdir is method on the testnode object, that way
	// we can add blocking elements and such to the siatest package later on if
	// the implementation or structure around uploading a directory changes.
	localfd1Name := "fuse-dir-1"
	localfd1Path := filepath.Join(mountpoint1, localfd1Name)
	localfd1, err := r.FilesDir().CreateDir(localfd1Name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.UploadDirectory(localfd1)
	if err != nil {
		t.Fatal(err)
	}

	// See if the directory showed up in fuse.
	fuseRoot, err = os.Open(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	names, err = fuseRoot.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", fuseRoot.Close())
	}
	if len(names) != 2 {
		t.Error("the uploaded dir is not appearing as a file of the directory", len(names))
	}
	infos, err = fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		// TODO: Need to figure out what is preventing Readdir from returning
		// actual entries, and need to fix that.
		t.Log("the number of infos returned is not 2", len(infos))
	}
	err = fuseRoot.Close()
	if err != nil {
		t.Fatal(err)
	}

	// See if we can open the new directory in fuse.
	localfd1Fuse, err := os.Open(localfd1Path)
	if err != nil {
		t.Fatal(err)
	}
	names, err = localfd1Fuse.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", localfd1Fuse.Close())
	}
	if len(names) != 0 {
		t.Error("files appearing in what's supposed to be an empty dir", len(names))
	}
	infos, err = localfd1Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		// TODO: Need to figure out what is preventing Readdir from returning
		// actual entries, and need to fix that.
		t.Log("infos appearing in what's supposed to be an empty dir", len(infos))
	}
	err = localfd1Fuse.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Create a file in the new directory. The file in the new directory is
	// sized to take multiple sectors.
	localfd1f1, err := localfd1.NewFile(int(modules.SectorSize + 250))
	if err != nil {
		t.Fatal(err)
	}
	remotefd1f1, err := r.UploadBlocking(localfd1f1, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// See if we can open the new directory in fuse.
	localfd1Fuse, err = os.Open(localfd1Path)
	if err != nil {
		t.Fatal(err)
	}
	names, err = localfd1Fuse.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", localfd1Fuse.Close())
	}
	if len(names) != 1 {
		t.Error("the file uploaded to subdir is not appearing", len(names))
	}
	infos, err = localfd1Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		// TODO: Need to figure out what is preventing Readdir from returning
		// actual entries, and need to fix that.
		t.Log("file uploaded to subdir not appearing as an info", len(infos))
	}
	err = localfd1Fuse.Close()
	if err != nil {
		t.Fatal(err)
	}

	// See if we can read the new file in fuse.
	path = remotefd1f1.SiaPath()
	fusePath, err = siaPathToFusePath(path, modules.RootSiaPath(), mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	fuseFile, err = os.Open(fusePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err = ioutil.ReadAll(fuseFile)
	if err != nil {
		t.Error(err)
	}
	localFileData, err = localfd1f1.Data()
	if err != nil {
		t.Error(err)
	}
	if bytes.Compare(data, localFileData) != 0 {
		t.Error("data from the local file and data from the fuse file do not match", len(data), len(localFileData))
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Upload another, larger file.
	localfd1f2, err := localfd1.NewFile((int(modules.SectorSize*4) + siatest.Fuzz()))
	if err != nil {
		t.Fatal(err)
	}
	remotefd1f2, err := r.UploadBlocking(localfd1f2, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// See if we can open the new directory in fuse.
	localfd1Fuse, err = os.Open(localfd1Path)
	if err != nil {
		t.Fatal(err)
	}
	names, err = localfd1Fuse.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", localfd1Fuse.Close())
	}
	if len(names) != 2 {
		t.Error("the file uploaded to subdir is not appearing", len(names))
	}
	infos, err = localfd1Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		// TODO: Need to figure out what is preventing Readdir from returning
		// actual entries, and need to fix that.
		t.Log("files uploaded to subdir not appearing as an info", len(infos))
	}
	err = localfd1Fuse.Close()
	if err != nil {
		t.Fatal(err)
	}

	// See if we can read the new file in fuse.
	path = remotefd1f2.SiaPath()
	fusePath, err = siaPathToFusePath(path, modules.RootSiaPath(), mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	fuseFile, err = os.Open(fusePath)
	if err != nil {
		t.Fatal(err)
	}
	data, err = ioutil.ReadAll(fuseFile)
	if err != nil {
		t.Error(err)
	}
	localFileData, err = localfd1f2.Data()
	if err != nil {
		t.Error(err)
	}
	if bytes.Compare(data, localFileData) != 0 {
		t.Error("data from the local file and data from the fuse file do not match", len(data), len(localFileData))
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Open the file again, this time with random seeks and reads. Ensure the
	// data still matches the local file.
	//
	// TODO: Finish this.
	fuseFile, err = os.Open(fusePath)
	if err != nil {
		t.Fatal(err)
	}
	data1 := make([]byte, 245)
	n, err := io.ReadFull(fuseFile, data1)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data1) {
		t.Error("expected to get", len(data1), "bytes, got", n)
	}
	if bytes.Compare(data1, localFileData[0:245]) != 0 {
		t.Error("data from the local file and data from the fuse file do not match", len(data), len(localFileData))
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// TODO: Need concurrency testing as well.

	// This sleep is here to allow the developer to have time to open the fuse
	// directory in a file browser to inspect everything. The millisecond long
	// sleep that is not commented out exists so that the 'time' package is
	// always used; the developer does not need to keep adding it and deleting
	// it as they switch between wanting the sleep and wanting the test to run
	// fast.
	//
	// time.Sleep(time.Second * 60)
	time.Sleep(time.Millisecond * 60)

	// Unmount the filesystem.
	err = r.RenterFuseUnmount(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}

	/*
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
		err = r.RenterFuseMount(r.SiaPath(subDir.Path()), mountpoint, true)
		if err != nil {
			t.Fatal(err)
		}
		defer r.RenterFuseUnmount()

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
		// compare metadata
		files, err := r.RenterFilesGet(false)
		if err != nil {
			t.Fatal(err)
		}
		fi := files.Files[0]
		stat, err := os.Stat(filepath.Join(mountpoint, "subDir", lf.FileName()))
		if err != nil {
			t.Fatal(err)
		}
		if stat.IsDir() != fi.IsDir() {
			t.Error("IsDir mismatch:", stat.IsDir(), fi.IsDir())
		}
		if stat.Size() != fi.Size() {
			t.Error("Size mismatch:", stat.Size(), fi.Size())
		}
		if !stat.ModTime().Round(time.Minute).Equal(fi.ModTime().Round(time.Minute)) {
			t.Error("ModTime mismatch:", stat.ModTime().Round(time.Minute), fi.ModTime().Round(time.Minute))
		}
		if filepath.Join("subDir", stat.Name()) != fi.Name() {
			t.Error("Name mismatch:", filepath.Join("subDir", stat.Name()), fi.Name())
		}
		if stat.Mode() != fi.Mode() {
			t.Error("Mode mismatch:", stat.Mode(), fi.Mode())
		}
	*/
}
