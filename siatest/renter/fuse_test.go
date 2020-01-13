// +build !windows

package renter

import (
	"bytes"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
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
	testDir := fuseTestDir(t.Name())
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

	// Set the default opts for mounting a fuse directory.
	//
	// NOTE: Can't test 'AllowOther' in this test, if 'AllowOther' is set to
	// true, Linux will complain unless the user has changed the default
	// configuration for fuse established in /etc/fuse.conf.
	defaultOpts := modules.MountOptions{
		ReadOnly:   true,
		AllowOther: false,
	}

	// Try mounting an empty fuse filesystem.
	mountpoint1 := filepath.Join(testDir, "mount1")
	err = os.MkdirAll(mountpoint1, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterFuseMount(mountpoint1, modules.RootSiaPath(), defaultOpts)
	if err != nil {
		t.Fatal(err)
	}

	// Get the list of filesystem mounts and see that the mountpoint is
	// represented correctly.
	fi, err := r.RenterFuse()
	if err != nil {
		t.Fatal(err)
	}
	if len(fi.MountPoints) != 1 {
		t.Fatal("there should be a mountpoint listed")
	}
	if !fi.MountPoints[0].MountOptions.ReadOnly {
		t.Error("ReadOnly should be set to true")
	}
	if fi.MountPoints[0].MountOptions.AllowOther {
		t.Error("AllowOther should be set to false")
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
		t.Error("there should not be any files in the empty fuse filesystem", len(names))
		for _, name := range names {
			t.Log(name)
		}
	}
	_, err = fuseRoot.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
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
	// Check the statfs return values. This works because we only do fuse
	// testing on linux right now.
	var stat syscall.Statfs_t
	err = syscall.Statfs(mountpoint1, &stat)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Bsize == 0 {
		t.Error("expecting non-zero block size")
	}
	if stat.Blocks != stat.Bfree {
		t.Error("expecting the entire filesystem to be free space")
	}
	if stat.Files != 0 {
		t.Error("expecting zero files in filesystem")
	}

	// Try unmounting the fuse filesystem.
	err = r.RenterFuseUnmount(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}

	// Mount fuse to the empty filesystem again, this time upload a file while
	// the system is mounted, then try to read the filesystem from the
	// directory.
	err = r.RenterFuseMount(mountpoint1, modules.RootSiaPath(), defaultOpts)
	if err != nil {
		t.Fatal(err)
	}

	// Upload a file to the renter.
	localFile, remoteFile, err := r.UploadNewFileBlocking(1<<16, 1, 1, false)
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
	_, err = fuseRoot.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
	}
	infos, err = fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Error("the number of infos returned is not 1", len(infos))
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
		t.Log(len(data))
		t.Log(len(localFileData))
		t.Log(data)
		t.Log(localFileData)
		t.Fatal("data from the local file and data from the fuse file do not match")
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Statfs the file in the directory, and the directory that contains the
	// file.
	var dirStat, fileStat syscall.Statfs_t
	err = syscall.Statfs(mountpoint1, &dirStat)
	if err != nil {
		t.Fatal(err)
	}
	err = syscall.Statfs(fusePath, &fileStat)
	if err != nil {
		t.Fatal(err)
	}
	if dirStat.Bsize != fileStat.Bsize {
		t.Error("directory and file are not reporting the same block size - unsure if application will get upset at this")
	}
	if dirStat.Blocks != fileStat.Blocks {
		t.Error("directory and file statfs are reporting a different filesystem size")
	}
	if dirStat.Bfree != fileStat.Bfree {
		t.Error("directory and file statfs are reporting different values for free space")
	}
	if dirStat.Files != fileStat.Files {
		t.Error("directory and file statfs are reporting different values for num files in filesystem")
	}
	if dirStat.Bsize == 0 {
		t.Error("expecting non-zero block size")
	}
	if dirStat.Blocks == dirStat.Bfree {
		t.Error("expecting statfs to suggest that some of the renter space has been consumed", dirStat.Blocks, dirStat.Bfree)
	}
	if dirStat.Files != 1 {
		t.Error("expecting the filesystem to be reporting one file")
	}

	// Create a directory within the root directory and see if the directory
	// appears in the fuse area.
	localfd1Name := "fuse-dir-1"
	localfd1Path := filepath.Join(mountpoint1, localfd1Name)
	localfd1, err := r.FilesDir().CreateDir(localfd1Name)
	if err != nil {
		t.Fatal(err)
	}
	remotefd1, err := r.UploadDirectory(localfd1)
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
	_, err = fuseRoot.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
	}
	infos, err = fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Error("the number of infos returned is not 2", len(infos))
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
	_, err = localfd1Fuse.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
	}
	infos, err = localfd1Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Error("infos appearing in what's supposed to be an empty dir", len(infos))
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
	_, err = localfd1Fuse.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
	}
	infos, err = localfd1Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Error("file uploaded to subdir not appearing as an info", len(infos))
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
		t.Log(len(data))
		t.Log(len(localFileData))
		t.Fatal("data from the local file and data from the fuse file do not match", len(data), len(localFileData))
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
	_, err = localfd1Fuse.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
	}
	infos, err = localfd1Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Error("files uploaded to subdir not appearing as an info", len(infos))
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
	_, err = fuseFile.Seek(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	data2 := make([]byte, 124)
	n, err = io.ReadFull(fuseFile, data2)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(data2) {
		t.Error("did not read all of the expected bytes")
	}
	if bytes.Compare(data2, localFileData[10:134]) != 0 {
		t.Error("data from the local file and data from the fuse file do not match", len(data), len(localFileData))
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Create another directory in the root dir.
	localfd2Name := "fuse-dir-2"
	localfd2Path := filepath.Join(mountpoint1, localfd2Name)
	localfd2, err := r.FilesDir().CreateDir(localfd2Name)
	if err != nil {
		t.Fatal(err)
	}
	remotefd2, err := r.UploadDirectory(localfd2)
	if err != nil {
		t.Fatal(err)
	}

	// Create a file in the new directory. The file in the new directory is
	// sized to take multiple sectors.
	localfd2f1, err := localfd2.NewFile(int(modules.SectorSize + 250))
	if err != nil {
		t.Fatal(err)
	}
	remotefd2f1, err := r.UploadBlocking(localfd2f1, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	localfd2f1Path, err := siaPathToFusePath(remotefd2f1.SiaPath(), modules.RootSiaPath(), mountpoint1)
	if err != nil {
		t.Fatal(err)
	}

	// Try to open the second directory, see if the file is visible.
	localfd2Fuse, err := os.Open(localfd2Path)
	if err != nil {
		t.Fatal(err)
	}
	names, err = localfd2Fuse.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", localfd2Fuse.Close())
	}
	if len(names) != 1 {
		t.Error("the file uploaded to subdir is not appearing", len(names))
	}
	_, err = localfd2Fuse.Seek(0, 0)
	if err != nil {
		t.Error("Unable to seek before readdir", err)
	}
	infos, err = localfd2Fuse.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Error("file uploaded to subdir not appearing as an info", len(infos))
	}
	err = localfd2Fuse.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the overall filesystem has kept up with the updates.
	err = syscall.Statfs(mountpoint1, &stat)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Bsize == 0 {
		t.Error("expecting non-zero block size")
	}
	if stat.Bsize != dirStat.Bsize {
		t.Error("the filesystem size appears to have changed")
	}
	if stat.Blocks == stat.Bfree {
		t.Error("expecting space to be consumed on the filesystem")
	}
	if stat.Files != 4 {
		t.Error("expecting four files in filesystem")
	}

	// Upload a new directory with a new file. The directory and file will be
	// created with non-standard permissions. Part of the test ensures that the
	// non-standard permissions are in place. If all is working correctly, the
	// uploaded file and directory should each have the correct permissions
	// reported by fuse.
	info, err := os.Stat(localfd2Path)
	if err != nil {
		t.Fatal(err)
	}
	defaultDirMode := info.Mode()
	info, err = os.Stat(localfd2f1Path)
	if err != nil {
		t.Fatal(err)
	}
	defaultFileMode := info.Mode()
	// Create a directory with non-standard permissions.
	customDirPath := filepath.Join(r.FilesDir().Path(), "custom-dir-1")
	customDirPerm := defaultDirMode // ^ 040
	customDirSiaPath, err := modules.RootSiaPath().Join("custom-dir-1")
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(customDirPath, customDirPerm)
	if err != nil {
		t.Fatal(err)
	}
	// TODO: Instead of using r.RenterDirCreateWithModePost, upload a file
	// directly. Currently the api does not support direct uploads of an entire
	// directory. This would ideally be replaced and instead just use the
	// RenterUploadPost call that occurs later in the code block.
	err = r.RenterDirCreateWithModePost(customDirSiaPath, customDirPerm)
	if err != nil {
		t.Fatal(err)
	}
	// Create a file within that directory.
	customFilePath := filepath.Join(customDirPath, "custom-file-1")
	customFilePerm := defaultFileMode // ^ 040
	customFileSiaPath, err := customDirSiaPath.Join("custom-file-1")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(customFilePath, os.O_RDWR|os.O_CREATE, customFilePerm)
	if err != nil {
		t.Fatal(err)
	}
	// Write some random data to the file and close it.
	randData := fastrand.Bytes(2500)
	_, err = file.Write(randData)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Upload the new file with custom permissions.
	err = r.RenterUploadPost(customFilePath, customFileSiaPath, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Open the new custom dir and file in fuse and ensure that the modes are
	// set correctly.
	customDirFusePath, err := siaPathToFusePath(customDirSiaPath, modules.RootSiaPath(), mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	customFileFusePath, err := siaPathToFusePath(customFileSiaPath, modules.RootSiaPath(), mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	customDirFuseInfo, err := os.Stat(customDirFusePath)
	if err != nil {
		t.Fatal(err)
	}
	if customDirFuseInfo.Mode() != customDirPerm {
		t.Error("Fuse mode mismatch:", customDirFuseInfo.Mode(), customDirPerm)
	}
	customFileFuseInfo, err := os.Stat(customFileFusePath)
	if err != nil {
		t.Fatal(err)
	}
	if customFileFuseInfo.Mode() != customFilePerm {
		t.Error("Fuse mode mismatch:", customFileFuseInfo.Mode(), customFilePerm)
	}

	// Perform a test where a file is renamed while it is open in fuse.
	// Downloading the file after the rename should continue to be successful.
	// We can rename the file that was created in the custom mode test.
	//
	// Read all of the data, ensure a match between this file and the real file.
	fuseData, err := ioutil.ReadFile(customFileFusePath)
	if err != nil {
		t.Fatal(err)
	}
	sourceData, err := ioutil.ReadFile(customFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fuseData, sourceData) {
		t.Log(len(fuseData))
		t.Log(len(sourceData))
		t.Log(fuseData)
		t.Log(sourceData)
		t.Fatal("custom mode data and source data do not match")
	}
	// Open the custom file in fuse. Note that for this test to provide proper
	// regression coverage, the streamer shouldn't be opened until after the
	// rename, because once the streamer is open the impact of a rename is going
	// to be different.
	fuseFile, err = os.Open(customFileFusePath)
	if err != nil {
		t.Fatal(err)
	}
	// Rename the custom file while it is open.
	customFileRenamedSiaPath, err := customDirSiaPath.Join("custom-file-1-renamed")
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterRenamePost(customFileSiaPath, customFileRenamedSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	renamedFuseData, err := ioutil.ReadAll(fuseFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(renamedFuseData, sourceData) {
		t.Log(renamedFuseData)
		t.Log(sourceData)
		t.Fatal("data mismatch after file was renamed")
	}
	// Close the fuseFile.
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the read-only flag is being respected.
	newFuseFilePath := localfd2Path + "-new"
	_, err = os.Create(newFuseFilePath)
	if err == nil {
		t.Fatal("should not be able to create a file in a read only fuse system")
	}

	// Try to open a file and then write to it.
	localfd2Fuse, err = os.Open(localfd2Path)
	if err != nil {
		t.Fatal(err)
	}
	writeData := fastrand.Bytes(25)
	n, err = localfd2Fuse.Write(writeData)
	if err == nil || n > 0 {
		t.Fatal("should not be able to write to a read only fuse system")
	}
	err = localfd2Fuse.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Try to create a directory in the read-only fuse system.
	err = os.Mkdir(newFuseFilePath, persist.DefaultDiskPermissionsTest)
	if err == nil {
		t.Fatal("should not be able to make a directory in a read-only fuse system")
	}

	// TODO: When read-write fuse is supported, switch over to read-write mode
	// here and begin testing the write features. Extend the concurrency test to
	// probe write features as well, probably by adding more phases.

	// Inode check. Mount the root siafile to a special inode mountpoint then
	// open several files and directoriesk. Grab their inodes. Keep the folder
	// mounted and the files and dirs open while the rest of the tests are
	// running to allow time to pass. At the end of the test, open all of the
	// dirs and files again (so multiple copies are open at once) and check that
	// the inodes all match.
	inodeMount := filepath.Join(testDir, "inodeMount")
	err = os.MkdirAll(inodeMount, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterFuseMount(inodeMount, modules.RootSiaPath(), defaultOpts)
	if err != nil {
		t.Fatal(err)
	}
	inodeFile1Path, err := siaPathToFusePath(remoteFile.SiaPath(), modules.RootSiaPath(), inodeMount)
	if err != nil {
		t.Fatal(err)
	}
	inodeFile1a, err := os.Open(inodeFile1Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err = inodeFile1a.Stat()
	if err != nil {
		t.Fatal(err)
	}
	infoStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("unable to get system stat info on inode file 1")
	}
	inodeFile1aIno := infoStat.Ino
	inodeDir1Path, err := siaPathToFusePath(remotefd1.SiaPath(), modules.RootSiaPath(), inodeMount)
	if err != nil {
		t.Fatal(err)
	}
	inodeDir1a, err := os.Open(inodeDir1Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err = inodeDir1a.Stat()
	if err != nil {
		t.Fatal(err)
	}
	infoStat, ok = info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("unable to get system stat info on inode file 1")
	}
	inodeDir1aIno := infoStat.Ino

	// Spin up a large number of threads, each of which chose a home as either
	// localfd1 or localfd2. Then the threads will create a folder for
	// themselves inside of their home and fill the folder with a unique file
	// for themseleves. Finally, the thread will create a unique mountpoint for
	// itself in the home directory.
	//
	// The threads will block until all 20 threads have completed their setup.
	// Then each of the threads will begin again, this time repeatedly mounting
	// either their home folder or their created folder and then unmounting it,
	// testing the concurrency of the fusemanager.
	//
	// After all threads have completed phase two, the threads will enter a
	// third phase where they all mount root to their mountpoint and then they
	// all open, read, and close the files located in root, causing heavy
	// concurrent access to a small number of files.
	threads := 25
	// One waitgroup per phase.
	var wg1 sync.WaitGroup
	var wg2 sync.WaitGroup
	var wg3 sync.WaitGroup
	wg1.Add(threads)
	wg2.Add(threads)
	wg3.Add(threads)
	// A single error that all of the threads can coordinate through.
	var groupErr error
	var errMu sync.Mutex
	// A single list of directories that need to be unmounted after the test is
	// fully completed.
	var unmounts []string
	var unmountMu sync.Mutex
	for i := 0; i < threads; i++ {
		go func(id int) {
			// Choose a home folder. Use Fuzz(). If Fuzz() is 1, use fd2,
			// otherwise use fd1.
			var home *siatest.LocalDir
			var homeSiaPath modules.SiaPath
			if siatest.Fuzz() < 1 {
				home = localfd1
				homeSiaPath = remotefd1.SiaPath()
			} else {
				home = localfd2
				homeSiaPath = remotefd2.SiaPath()
			}

			// Upload a file to the home directory, include the thread's unique
			// id in the filesize to help with debugging.
			_, err := home.NewFile(int(modules.SectorSize*3) + 100*id)
			if err != nil {
				err = errors.AddContext(err, "unable to create a file in thread home")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg1.Done()
				wg2.Done()
				wg3.Done()
				return
			}

			// Upload a folder to the home directory. This creates a very rich
			// fuse system overall between all of the threads.
			threadDirName := "threadDir" + strconv.Itoa(id)
			threadDir, err := home.CreateDir(threadDirName)
			if err != nil {
				err = errors.AddContext(err, "unable to create a folder in thread home")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg1.Done()
				wg2.Done()
				wg3.Done()
				return
			}
			err = threadDir.PopulateDir(1, 1, 2)
			if err != nil {
				err = errors.AddContext(err, "unable to populate a folder in thread home")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg1.Done()
				wg2.Done()
				wg3.Done()
				return
			}
			threadRemoteDir, err := r.UploadDirectory(threadDir)
			if err != nil {
				err = errors.AddContext(err, "unable to upload a folder in thread home")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg1.Done()
				wg2.Done()
				wg3.Done()
				return
			}

			// Create a mountpoint specific to the thread.
			threadMountName := "threadmount" + strconv.Itoa(id)
			threadMount := filepath.Join(testDir, threadMountName)
			err = os.MkdirAll(threadMount, persist.DefaultDiskPermissionsTest)
			if err != nil {
				err = errors.AddContext(err, "unable to create mountpoint")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg1.Done()
				wg2.Done()
				wg3.Done()
				return
			}

			// Phase one complete, wait for all other threads to finish phase
			// one.
			wg1.Done()
			wg1.Wait()

			// Begin phase two by repeatedly mounting and unmounting either home
			// or the
			mountIters := 25
			for i := 0; i < mountIters; i++ {
				var err error
				var siaPathToMount modules.SiaPath
				if siatest.Fuzz() < 1 {
					siaPathToMount = homeSiaPath
				} else {
					siaPathToMount = threadRemoteDir.SiaPath()
				}
				if err != nil {
					err = errors.AddContext(err, "unable to create mountpoint")
					errMu.Lock()
					groupErr = errors.Compose(groupErr, err)
					errMu.Unlock()
					wg2.Done()
					wg3.Done()
					return
				}
				err = r.RenterFuseMount(threadMount, siaPathToMount, defaultOpts)
				if err != nil {
					err = errors.AddContext(err, "unable to mount thread mount")
					errMu.Lock()
					groupErr = errors.Compose(groupErr, err)
					errMu.Unlock()
					wg2.Done()
					wg3.Done()
					return
				}
				err = r.RenterFuseUnmount(threadMount)
				if err != nil {
					err = errors.AddContext(err, "unable to unmount thread mount")
					errMu.Lock()
					groupErr = errors.Compose(groupErr, err)
					errMu.Unlock()
					wg2.Done()
					wg3.Done()
					return
				}
			}

			// Phase two complete, wait for all other threads to finish phase
			// two.
			wg2.Done()
			wg2.Wait()

			// Phase three. Mount the root, and then repeatedly perform actions
			// on the files and folders in root to verify the concurrency safety
			// of the ro filesystem.
			err = r.RenterFuseMount(threadMount, modules.RootSiaPath(), defaultOpts)
			if err != nil {
				err = errors.AddContext(err, "unable to mount thread mount")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg3.Done()
				return
			}
			readIters := 20
			for i := 0; i < readIters; i++ {
				path := remoteFile.SiaPath()
				fusePath, err := siaPathToFusePath(path, modules.RootSiaPath(), threadMount)
				if err != nil {
					err = errors.AddContext(err, "unable to convert remote file to a fuse path")
					errMu.Lock()
					groupErr = errors.Compose(groupErr, err)
					errMu.Unlock()
					wg3.Done()
					return
				}

				// Sometimes stat the file.
				if siatest.Fuzz() == 0 {
					_, err = os.Stat(fusePath)
					if err != nil {
						err = errors.AddContext(err, "unable to stat a fuse path")
						errMu.Lock()
						groupErr = errors.Compose(groupErr, err)
						errMu.Unlock()
						wg3.Done()
						return
					}
				}

				// Sometimes stop here.
				if siatest.Fuzz() == 0 {
					continue
				}

				fuseFile, err := os.Open(fusePath)
				if err != nil {
					err = errors.AddContext(err, "unable to open fuse file")
					errMu.Lock()
					groupErr = errors.Compose(groupErr, err)
					errMu.Unlock()
					wg3.Done()
					return
				}
				// Sometimes read the file.
				if siatest.Fuzz() == 0 {
					data, err := ioutil.ReadAll(fuseFile)
					if err != nil {
						err = errors.AddContext(err, "unable to read from fuse file")
						errMu.Lock()
						groupErr = errors.Compose(groupErr, err)
						errMu.Unlock()
						wg3.Done()
						return
					}
					localFileData, err := localFile.Data()
					if err != nil {
						err = errors.AddContext(err, "unable to get local file data")
						errMu.Lock()
						groupErr = errors.Compose(groupErr, err)
						errMu.Unlock()
						wg3.Done()
						return
					}
					if bytes.Compare(data, localFileData) != 0 {
						err := errors.New("local file and remote file mismatch")
						errMu.Lock()
						groupErr = errors.Compose(groupErr, err)
						errMu.Unlock()
						wg3.Done()
						return
					}
				}
				err = fuseFile.Close()
				if err != nil {
					err = errors.AddContext(err, "unable to close fuseFile")
					errMu.Lock()
					groupErr = errors.Compose(groupErr, err)
					errMu.Unlock()
					wg3.Done()
					return
				}
			}

			// Read test done, unmount the root.
			err = r.RenterFuseUnmount(threadMount)
			if err != nil {
				err = errors.AddContext(err, "unable to mount thread mount")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg3.Done()
				return
			}

			// Leave either the home path, the thread remote dir, or the root
			// mounted for the user to explore. Do this action before calling
			// 'wg.Wait()' on the final phase.
			var siaPathToMount modules.SiaPath
			if siatest.Fuzz() == -1 {
				siaPathToMount = homeSiaPath
			} else if siatest.Fuzz() == 0 {
				siaPathToMount = threadRemoteDir.SiaPath()
			} else {
				siaPathToMount = modules.RootSiaPath()
			}
			err = r.RenterFuseMount(threadMount, siaPathToMount, defaultOpts)
			if err != nil {
				err = errors.AddContext(err, "unable to mount thread mount")
				errMu.Lock()
				groupErr = errors.Compose(groupErr, err)
				errMu.Unlock()
				wg3.Done()
				return
			}
			unmountMu.Lock()
			unmounts = append(unmounts, threadMount)
			unmountMu.Unlock()

			// Phase three complete.
			wg3.Done()
			wg3.Wait()
		}(i)
	}
	wg3.Wait()
	// Check the groupErr.
	if groupErr != nil {
		t.Fatal(groupErr)
	}

	// Follow up on the inode check created earlier.
	inodeFile1b, err := os.Open(inodeFile1Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err = inodeFile1b.Stat()
	if err != nil {
		t.Fatal(err)
	}
	infoStat, ok = info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("unable to get system stat info on inode file 1")
	}
	if infoStat.Ino != inodeFile1aIno {
		t.Error("inodes do not match for the same file on the same mount", infoStat.Ino, inodeFile1aIno)
	}
	err = inodeFile1b.Close()
	if err != nil {
		t.Fatal(err)
	}

	// check that the inodes still match for the dir.
	inodeDir1b, err := os.Open(inodeDir1Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err = inodeDir1b.Stat()
	if err != nil {
		t.Fatal(err)
	}
	infoStat, ok = info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("unable to get system stat info on inode file 1")
	}
	if infoStat.Ino != inodeDir1aIno {
		t.Error("inodes do not match for the same dir on the same mount", infoStat.Ino, inodeDir1aIno)
	}
	err = inodeDir1b.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Close out the inode check files and mount.
	err = inodeFile1a.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = inodeDir1a.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterFuseUnmount(inodeMount)
	if err != nil {
		t.Fatal(err)
	}

	// A call to Sleep() which can be uncommented that will allow the developer
	// to browse around in the fuse directory on their own after the automated
	// test has completed.
	sleepForDev := func() {
		time.Sleep(time.Second * 100)
	}
	// Hack to get the test to compile when sleepForDev is commented out.
	if sleepForDev == nil {
		t.Fatal("Sleep for dev func is not definied")
	}
	// sleepForDev()

	// Unmount the filesystems.
	err = r.RenterFuseUnmount(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}
	for _, unmount := range unmounts {
		err = r.RenterFuseUnmount(unmount)
	}
}
