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
	testDir := siatest.TestDir("fuse", t.Name())
	if err := os.MkdirAll(testDir, siatest.DefaultDiskPermissions); err != nil {
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
	err = os.MkdirAll(mountpoint1, siatest.DefaultDiskPermissions)
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterFuseMount(mountpoint1, modules.RootSiaPath(), true)
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

	// Try unmounting the fuse filesystem.
	err = r.RenterFuseUnmount(mountpoint1)
	if err != nil {
		t.Fatal(err)
	}

	// Mount fuse to the emtpy filesystem again, this time upload a file while
	// the system is mounted, then try to read the filesystem from the
	// directory.
	err = r.RenterFuseMount(mountpoint1, modules.RootSiaPath(), true)
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
		t.Error("data from the local file and data from the fuse file do not match")
	}
	err = fuseFile.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Stat the file in the fuse directory.
	fuseStat, err := os.Stat(fusePath)
	if err != nil {
		t.Fatal(err)
	}
	localStat, err := os.Stat(localFile.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fuseStat.IsDir() != localStat.IsDir() {
		t.Error("IsDir mismatch")
	}
	if fuseStat.Size() != localStat.Size() {
		t.Error("size mismatch")
	}
	if fuseStat.Name() != localStat.Name() {
		t.Error("name mismatch")
	}
	if fuseStat.Mode() != localStat.Mode() {
		t.Error("mode mismatch")
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
	_, err = r.UploadBlocking(localfd2f1, 1, 1, false)
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
	err = os.Mkdir(newFuseFilePath, siatest.DefaultDiskPermissions)
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
	err = os.MkdirAll(inodeMount, siatest.DefaultDiskPermissions)
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterFuseMount(inodeMount, modules.RootSiaPath(), true)
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
	info, err := inodeFile1a.Stat()
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
	// thrid phase where they all mount root to their mountpoint and then they
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
			err = os.MkdirAll(threadMount, siatest.DefaultDiskPermissions)
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
				err = r.RenterFuseMount(threadMount, siaPathToMount, true)
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
			err = r.RenterFuseMount(threadMount, modules.RootSiaPath(), true)
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
			err = r.RenterFuseMount(threadMount, siaPathToMount, true)
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
		t.Error("inodes do not match for the same file on the same mount")
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
		t.Error("inodes do not match for the same file on the same mount")
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
		println("Automated tests completed, dev can interact with FUSE now.")
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
