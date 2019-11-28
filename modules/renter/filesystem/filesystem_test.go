package filesystem

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/build"
)

// newTestFileSystemWithFile creates a new FileSystem and SiaFile and makes sure
// that they are linked
func newTestFileSystemWithFile(name string) (*FileNode, *FileSystem, error) {
	dir := testDir(name)
	fs := newTestFileSystem(dir)
	sp := modules.RandomSiaPath()
	fs.AddTestSiaFile(sp)
	sf, err := fs.OpenSiaFile(sp)
	return sf, fs, err
}

// testDir creates a testing directory for a filesystem test.
func testDir(name string) string {
	dir := build.TempDir(name, filepath.Join("filesystem"))
	if err := os.MkdirAll(dir, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return dir
}

// newSiaPath creates a new siapath from the specified string.
func newSiaPath(path string) modules.SiaPath {
	sp, err := modules.NewSiaPath(path)
	if err != nil {
		panic(err)
	}
	return sp
}

// newTestFileSystem creates a new filesystem for testing.
func newTestFileSystem(root string) *FileSystem {
	wal, _ := newTestWAL()
	fs, err := New(root, persist.NewLogger(ioutil.Discard), wal)
	if err != nil {
		panic(err.Error())
	}
	return fs
}

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL() (*writeaheadlog.WAL, string) {
	// Create the wal.
	walsDir := filepath.Join(os.TempDir(), "wals")
	if err := os.MkdirAll(walsDir, 0700); err != nil {
		panic(err)
	}
	walFilePath := filepath.Join(walsDir, hex.EncodeToString(fastrand.Bytes(8)))
	_, wal, err := writeaheadlog.New(walFilePath)
	if err != nil {
		panic(err)
	}
	return wal, walFilePath
}

// AddTestSiaFile is a convenience method to add a SiaFile for testing to a
// FileSystem.
func (fs *FileSystem) AddTestSiaFile(siaPath modules.SiaPath) {
	if err := fs.AddTestSiaFileWithErr(siaPath); err != nil {
		panic(err)
	}
}

// AddTestSiaFileWithErr is a convenience method to add a SiaFile for testing to
// a FileSystem.
func (fs *FileSystem) AddTestSiaFileWithErr(siaPath modules.SiaPath) error {
	ec, err := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		return err
	}
	err = fs.NewSiaFile(siaPath, "", ec, crypto.GenerateSiaKey(crypto.TypeDefaultRenter), uint64(fastrand.Intn(100)), persist.DefaultDiskPermissionsTest, false)
	if err != nil {
		return err
	}
	return nil
}

// TestNew tests creating a new FileSystem.
func TestNew(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Check fields.
	if fs.parent != nil {
		t.Fatalf("fs.parent shoud be 'nil' but wasn't")
	}
	if *fs.name != "" {
		t.Fatalf("fs.staticName should be %v but was %v", "", *fs.name)
	}
	if *fs.path != root {
		t.Fatalf("fs.path should be %v but was %v", root, *fs.path)
	}
	if fs.threads == nil || len(fs.threads) != 0 {
		t.Fatal("fs.threads is not an empty initialized map")
	}
	if fs.threadUID != 0 {
		t.Fatalf("fs.threadUID should be 0 but was %v", fs.threadUID)
	}
	if fs.directories == nil || len(fs.directories) != 0 {
		t.Fatal("fs.directories is not an empty initialized map")
	}
	if fs.files == nil || len(fs.files) != 0 {
		t.Fatal("fs.files is not an empty initialized map")
	}
	// Create the filesystem again at the same location.
	_ = newTestFileSystem(*fs.path)
}

// TestNewSiaDir tests if creating a new directory using NewSiaDir creates the
// correct folder structure.
func TestNewSiaDir(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create dir /sub/foo
	sp := newSiaPath("sub/foo")
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	// The whole path should exist.
	if _, err := os.Stat(filepath.Join(root, sp.String())); err != nil {
		t.Fatal(err)
	}
}

// TestNewSiaFile tests if creating a new file using NewSiaFiles creates the
// correct folder structure and file.
func TestNewSiaFile(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create file /sub/foo/file
	sp := newSiaPath("sub/foo/file")
	fs.AddTestSiaFile(sp)
	if err := fs.NewSiaDir(sp); err != ErrExists {
		t.Fatal("err should be ErrExists but was", err)
	}
	if _, err := os.Stat(filepath.Join(root, sp.String())); !os.IsNotExist(err) {
		t.Fatal("there should be no dir on disk")
	}
	if _, err := os.Stat(filepath.Join(root, sp.String()+modules.SiaFileExtension)); err != nil {
		t.Fatal(err)
	}
	// Create a file in the root dir.
	sp = newSiaPath("file")
	fs.AddTestSiaFile(sp)
	if err := fs.NewSiaDir(sp); err != ErrExists {
		t.Fatal("err should be ErrExists but was", err)
	}
	if _, err := os.Stat(filepath.Join(root, sp.String())); !os.IsNotExist(err) {
		t.Fatal("there should be no dir on disk")
	}
	if _, err := os.Stat(filepath.Join(root, sp.String()+modules.SiaFileExtension)); err != nil {
		t.Fatal(err)
	}
}

func (d *DirNode) checkNode(numThreads, numDirs, numFiles int) error {
	if len(d.threads) != numThreads {
		return fmt.Errorf("Expected d.threads to have length %v but was %v", numThreads, len(d.threads))
	}
	if len(d.directories) != numDirs {
		return fmt.Errorf("Expected %v subdirectories in the root but got %v", numDirs, len(d.directories))
	}
	if len(d.files) != numFiles {
		return fmt.Errorf("Expected %v files in the root but got %v", numFiles, len(d.files))
	}
	return nil
}

// TestOpenSiaDir confirms that a previoiusly created SiaDir can be opened and
// that the filesystem tree is extended accordingly in the process.
func TestOpenSiaDir(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create dir /foo
	sp := newSiaPath("foo")
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	// Open the newly created dir.
	foo, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer foo.Close()
	// Create dir /sub/foo
	sp = newSiaPath("sub/foo")
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	// Open the newly created dir.
	sd, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer sd.Close()
	// Confirm the integrity of the root node.
	if err := fs.checkNode(0, 2, 0); err != nil {
		t.Fatal(err)
	}
	// Open the root node manually and confirm that they are the same.
	rootSD, err := fs.OpenSiaDir(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.checkNode(len(rootSD.threads), len(rootSD.directories), len(rootSD.files)); err != nil {
		t.Fatal(err)
	}
	// Confirm the integrity of the /sub node.
	subNode, exists := fs.directories["sub"]
	if !exists {
		t.Fatal("expected root to contain the 'sub' node")
	}
	if *subNode.name != "sub" {
		t.Fatalf("subNode name should be 'sub' but was %v", *subNode.name)
	}
	if path := filepath.Join(*subNode.parent.path, *subNode.name); path != *subNode.path {
		t.Fatalf("subNode path should be %v but was %v", path, *subNode.path)
	}
	if err := subNode.checkNode(0, 1, 0); err != nil {
		t.Fatal(err)
	}
	// Confirm the integrity of the /sub/foo node.
	fooNode, exists := subNode.directories["foo"]
	if !exists {
		t.Fatal("expected /sub to contain /sub/foo")
	}
	if *fooNode.name != "foo" {
		t.Fatalf("fooNode name should be 'foo' but was %v", *fooNode.name)
	}
	if path := filepath.Join(*fooNode.parent.path, *fooNode.name); path != *fooNode.path {
		t.Fatalf("fooNode path should be %v but was %v", path, *fooNode.path)
	}
	if err := fooNode.checkNode(1, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Open the newly created dir again.
	sd2, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer sd2.Close()
	// They should have different UIDs.
	if sd.threadUID == 0 {
		t.Fatal("threaduid shouldn't be 0")
	}
	if sd2.threadUID == 0 {
		t.Fatal("threaduid shouldn't be 0")
	}
	if sd.threadUID == sd2.threadUID {
		t.Fatal("sd and sd2 should have different threaduids")
	}
	if len(sd.threads) != 2 || len(sd2.threads) != 2 {
		t.Fatal("sd and sd2 should both have 2 threads registered")
	}
	_, exists1 := sd.threads[sd.threadUID]
	_, exists2 := sd.threads[sd2.threadUID]
	_, exists3 := sd2.threads[sd.threadUID]
	_, exists4 := sd2.threads[sd2.threadUID]
	if exists := exists1 && exists2 && exists3 && exists4; !exists {
		t.Fatal("sd and sd1's threads don't contain the right uids")
	}
	// Open /sub manually and make sure that subDir and sdSub are consistent.
	sdSub, err := fs.OpenSiaDir(newSiaPath("sub"))
	if err != nil {
		t.Fatal(err)
	}
	defer sdSub.Close()
	if err := subNode.checkNode(1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := sdSub.checkNode(1, 1, 0); err != nil {
		t.Fatal(err)
	}
}

// TestOpenSiaFile confirms that a previously created SiaFile can be opened and
// that the filesystem tree is extended accordingly in the process.
func TestOpenSiaFile(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create file /file
	sp := newSiaPath("file")
	fs.AddTestSiaFile(sp)
	// Open the newly created file.
	sf, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer sf.Close()
	// Confirm the integrity of the file.
	if *sf.name != "file" {
		t.Fatalf("name of file should be file but was %v", *sf.name)
	}
	if *sf.path != filepath.Join(root, (*sf.name)+modules.SiaFileExtension) {
		t.Fatal("file has wrong path", *sf.path)
	}
	if sf.parent != &fs.DirNode {
		t.Fatalf("parent of file should be %v but was %v", &fs.node, sf.parent)
	}
	if sf.threadUID == 0 {
		t.Fatal("threaduid wasn't set")
	}
	if len(sf.threads) != 1 {
		t.Fatalf("len(threads) should be 1 but was %v", len(sf.threads))
	}
	if _, exists := sf.threads[sf.threadUID]; !exists {
		t.Fatal("threaduid doesn't exist in threads map")
	}
	// Confirm the integrity of the root node.
	if len(fs.threads) != 0 {
		t.Fatalf("Expected fs.threads to have length 0 but was %v", len(fs.threads))
	}
	if len(fs.directories) != 0 {
		t.Fatalf("Expected 0 subdirectories in the root but got %v", len(fs.directories))
	}
	if len(fs.files) != 1 {
		t.Fatalf("Expected 1 file in the root but got %v", len(fs.files))
	}
	// Create file /sub/file
	sp = newSiaPath("/sub1/sub2/file")
	fs.AddTestSiaFile(sp)
	// Open the newly created file.
	sf2, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer sf2.Close()
	// Confirm the integrity of the file.
	if *sf2.name != "file" {
		t.Fatalf("name of file should be file but was %v", *sf2.name)
	}
	if *sf2.parent.name != "sub2" {
		t.Fatalf("parent of file should be %v but was %v", "sub", *sf2.parent.name)
	}
	if sf2.threadUID == 0 {
		t.Fatal("threaduid wasn't set")
	}
	if len(sf2.threads) != 1 {
		t.Fatalf("len(threads) should be 1 but was %v", len(sf2.threads))
	}
	// Confirm the integrity of the "sub2" folder.
	sub2 := sf2.parent
	if err := sub2.checkNode(0, 0, 1); err != nil {
		t.Fatal(err)
	}
	if _, exists := sf2.threads[sf2.threadUID]; !exists {
		t.Fatal("threaduid doesn't exist in threads map")
	}
	// Confirm the integrity of the "sub1" folder.
	sub1 := sub2.parent
	if err := sub1.checkNode(0, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, exists := sf2.threads[sf2.threadUID]; !exists {
		t.Fatal("threaduid doesn't exist in threads map")
	}
}

// TestCloseSiaDir tests that closing an opened directory shrinks the tree
// accordingly.
func TestCloseSiaDir(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create dir /sub/foo
	sp := newSiaPath("sub1/foo")
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	// Open the newly created dir.
	sd, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(sd.threads) != 1 {
		t.Fatalf("There should be 1 thread in sd.threads but got %v", len(sd.threads))
	}
	if len(sd.parent.threads) != 0 {
		t.Fatalf("The parent shouldn't have any threads but had %v", len(sd.parent.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sd.parent.directories) != 1 {
		t.Fatalf("The parent should have 1 directory but got %v", len(sd.parent.directories))
	}
	// After closing it the thread should be gone.
	sd.Close()
	if err := fs.checkNode(0, 0, 0); err != nil {
		t.Fatal(err)
	}
	// Open the dir again. This time twice.
	sd1, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	sd2, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(sd1.threads) != 2 || len(sd2.threads) != 2 {
		t.Fatalf("There should be 2 threads in sd.threads but got %v", len(sd1.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sd1.parent.directories) != 1 || len(sd2.parent.directories) != 1 {
		t.Fatalf("The parent should have 1 directory but got %v", len(sd.parent.directories))
	}
	// Close one instance.
	sd1.Close()
	if len(sd1.threads) != 1 || len(sd2.threads) != 1 {
		t.Fatalf("There should be 1 thread in sd.threads but got %v", len(sd1.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sd1.parent.directories) != 1 || len(sd2.parent.directories) != 1 {
		t.Fatalf("The parent should have 1 directory but got %v", len(sd.parent.directories))
	}
	// Close the second one.
	sd2.Close()
	if len(fs.threads) != 0 {
		t.Fatalf("There should be 0 threads in fs.threads but got %v", len(fs.threads))
	}
	if len(sd1.threads) != 0 || len(sd2.threads) != 0 {
		t.Fatalf("There should be 0 threads in sd.threads but got %v", len(sd1.threads))
	}
	if len(fs.directories) != 0 {
		t.Fatalf("There should be 0 directories in fs.directories but got %v", len(fs.directories))
	}
}

// TestCloseSiaFile tests that closing an opened file shrinks the tree
// accordingly.
func TestCloseSiaFile(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create file /sub/file
	sp := newSiaPath("sub/file")
	fs.AddTestSiaFile(sp)
	// Open the newly created file.
	sf, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.threads) != 1 {
		t.Fatalf("There should be 1 thread in sf.threads but got %v", len(sf.threads))
	}
	if len(sf.parent.threads) != 0 {
		t.Fatalf("The parent shouldn't have any threads but had %v", len(sf.parent.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sf.parent.files) != 1 {
		t.Fatalf("The parent should have 1 file but got %v", len(sf.parent.files))
	}
	// After closing it the thread should be gone.
	sf.Close()
	if len(fs.threads) != 0 {
		t.Fatalf("There should be 0 threads in fs.threads but got %v", len(fs.threads))
	}
	if len(sf.threads) != 0 {
		t.Fatalf("There should be 0 threads in sd.threads but got %v", len(sf.threads))
	}
	if len(fs.files) != 0 {
		t.Fatalf("There should be 0 files in fs.files but got %v", len(fs.files))
	}
	// Open the file again. This time twice.
	sf1, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	sf2, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	if len(sf1.threads) != 2 || len(sf2.threads) != 2 {
		t.Fatalf("There should be 2 threads in sf1.threads but got %v", len(sf1.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sf1.parent.files) != 1 || len(sf2.parent.files) != 1 {
		t.Fatalf("The parent should have 1 file but got %v", len(sf1.parent.files))
	}
	// Close one instance.
	sf1.Close()
	if len(sf1.threads) != 1 || len(sf2.threads) != 1 {
		t.Fatalf("There should be 1 thread in sf1.threads but got %v", len(sf1.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 dir in fs.directories but got %v", len(fs.directories))
	}
	if len(sf1.parent.files) != 1 || len(sf2.parent.files) != 1 {
		t.Fatalf("The parent should have 1 file but got %v", len(sf1.parent.files))
	}
	if len(sf1.parent.parent.directories) != 1 {
		t.Fatalf("The root should have 1 directory but had %v", len(sf1.parent.parent.directories))
	}
	// Close the second one.
	sf2.Close()
	if len(fs.threads) != 0 {
		t.Fatalf("There should be 0 threads in fs.threads but got %v", len(fs.threads))
	}
	if len(sf1.threads) != 0 || len(sf2.threads) != 0 {
		t.Fatalf("There should be 0 threads in sd.threads but got %v", len(sf1.threads))
	}
	if len(fs.directories) != 0 {
		t.Fatalf("There should be 0 directories in fs.directories but got %v", len(fs.directories))
	}
	if len(sf1.parent.files) != 0 || len(sf2.parent.files) != 0 {
		t.Fatalf("The parent should have 0 files but got %v", len(sf1.parent.files))
	}
	if len(sf1.parent.parent.directories) != 0 {
		t.Fatalf("The root should have 0 directories but had %v", len(sf1.parent.parent.directories))
	}
}

// TestDeleteFile tests that deleting a file works as expected and that certain
// edge cases are covered.
func TestDeleteFile(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Add a file to the root dir.
	sp := newSiaPath("foo")
	fs.AddTestSiaFile(sp)
	// Open the file.
	sf, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	// File shouldn't be deleted yet.
	if sf.Deleted() {
		t.Fatal("foo is deleted before calling delete")
	}
	// Delete it using the filesystem.
	if err := fs.DeleteFile(sp); err != nil {
		t.Fatal(err)
	}
	// Check that the open instance is marked as deleted.
	if !sf.Deleted() {
		t.Fatal("foo shuld be marked as deleted but wasn't")
	}
	// Check that we can't open another instance of foo and that we can't create
	// an new file at the same path.
	if _, err := fs.OpenSiaFile(sp); err != ErrNotExist {
		t.Fatal("err should be ErrNotExist but was:", err)
	}
	if err := fs.AddTestSiaFileWithErr(sp); err != nil {
		t.Fatal("err should be nil but was:", err)
	}
}

// TestDeleteDirectory tests if deleting a directory correctly and recursively
// removes the dir.
func TestDeleteDirectory(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Add some files.
	fs.AddTestSiaFile(newSiaPath("dir/foo/bar/file1"))
	fs.AddTestSiaFile(newSiaPath("dir/foo/bar/file2"))
	fs.AddTestSiaFile(newSiaPath("dir/foo/bar/file3"))
	// Delete "foo"
	if err := fs.DeleteDir(newSiaPath("/dir/foo")); err != nil {
		t.Fatal(err)
	}
	// Check that /dir still exists.
	if _, err := os.Stat(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	// Check that /dir is empty.
	if fis, err := ioutil.ReadDir(filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	} else if len(fis) != 1 {
		for i, fi := range fis {
			t.Logf("fi%v: %v", i, fi.Name())
		}
		t.Fatalf("expected 1 file in 'dir' but contains %v files", len(fis))
	}
}

// TestRenameFile tests if renaming a single file works as expected.
func TestRenameFile(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Add a file to the root dir.
	foo := newSiaPath("foo")
	foobar := newSiaPath("foobar")
	barfoo := newSiaPath("bar/foo")
	fs.AddTestSiaFile(foo)
	// Rename the file.
	if err := fs.RenameFile(foo, foobar); err != nil {
		t.Fatal(err)
	}
	// Check if the file was renamed.
	if _, err := fs.OpenSiaFile(foo); err != ErrNotExist {
		t.Fatal("expected ErrNotExist but got:", err)
	}
	sf, err := fs.OpenSiaFile(foobar)
	if err != nil {
		t.Fatal("expected ErrNotExist but got:", err)
	}
	sf.Close()
	// Rename the file again. This time it changes to a non-existent folder.
	if err := fs.RenameFile(foobar, barfoo); err != nil {
		t.Fatal(err)
	}
	sf, err = fs.OpenSiaFile(barfoo)
	if err != nil {
		t.Fatal("expected ErrNotExist but got:", err)
	}
	sf.Close()
}

// TestThreadedAccess tests rapidly opening and closing files and directories
// from multiple threads to check the locking conventions.
func TestThreadedAccess(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Specify the file structure for the test.
	filePaths := []string{
		"f0",
		"f1",
		"f2",

		"d0/f0", "d0/f1", "d0/f2",
		"d1/f0", "d1/f1", "d1/f2",
		"d2/f0", "d2/f1", "d2/f2",

		"d0/d0/f0", "d0/d0/f1", "d0/d0/f2",
		"d0/d1/f0", "d0/d1/f1", "d0/d1/f2",
		"d0/d2/f0", "d0/d2/f1", "d0/d2/f2",

		"d1/d0/f0", "d1/d0/f1", "d1/d0/f2",
		"d1/d1/f0", "d1/d1/f1", "d1/d1/f2",
		"d1/d2/f0", "d1/d2/f1", "d1/d2/f2",

		"d2/d0/f0", "d2/d0/f1", "d2/d0/f2",
		"d2/d1/f0", "d2/d1/f1", "d2/d1/f2",
		"d2/d2/f0", "d2/d2/f1", "d2/d2/f2",
	}
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	for _, fp := range filePaths {
		fs.AddTestSiaFile(newSiaPath(fp))
	}
	// Create a few threads which open files
	var wg sync.WaitGroup
	numThreads := 5
	maxNumActions := uint64(50000)
	numActions := uint64(0)
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if atomic.LoadUint64(&numActions) >= maxNumActions {
					break
				}
				atomic.AddUint64(&numActions, 1)
				sp := newSiaPath(filePaths[fastrand.Intn(len(filePaths))])
				sf, err := fs.OpenSiaFile(sp)
				if err != nil {
					t.Fatal(err)
				}
				sf.Close()
			}
		}()
	}
	// Create a few threads which open dirs
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if atomic.LoadUint64(&numActions) >= maxNumActions {
					break
				}
				atomic.AddUint64(&numActions, 1)
				sp := newSiaPath(filePaths[fastrand.Intn(len(filePaths))])
				sp, err := sp.Dir()
				if err != nil {
					t.Fatal(err)
				}
				sd, err := fs.OpenSiaDir(sp)
				if err != nil {
					t.Fatal(err)
				}
				sd.Close()
			}
		}()
	}
	wg.Wait()

	// Check the root's integrity. Since all files and dirs were closed, the
	// node's maps should reflect that.
	if len(fs.threads) != 0 {
		t.Fatalf("fs should have 0 threads but had %v", len(fs.threads))
	}
	if len(fs.directories) != 0 {
		t.Fatalf("fs should have 0 directories but had %v", len(fs.directories))
	}
	if len(fs.files) != 0 {
		t.Fatalf("fs should have 0 files but had %v", len(fs.files))
	}
}

// TestSiaDirRename tests the Rename method of the siadirset.
func TestSiaDirRename(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Prepare a filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	os.RemoveAll(root)
	fs := newTestFileSystem(root)

	// Specify a directory structure for this test.
	var dirStructure = []string{
		"dir1",
		"dir1/subdir1",
		"dir1/subdir1/subsubdir1",
		"dir1/subdir1/subsubdir2",
		"dir1/subdir1/subsubdir3",
		"dir1/subdir2",
		"dir1/subdir2/subsubdir1",
		"dir1/subdir2/subsubdir2",
		"dir1/subdir2/subsubdir3",
		"dir1/subdir3",
		"dir1/subdir3/subsubdir1",
		"dir1/subdir3/subsubdir2",
		"dir1/subdir3/subsubdir3",
	}
	// Specify a function that's executed in parallel which continuously saves dirs
	// to disk.
	stop := make(chan struct{})
	wg := new(sync.WaitGroup)
	f := func(entry *DirNode) {
		defer wg.Done()
		defer entry.Close()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := entry.UpdateMetadata(siadir.Metadata{})
			if err != nil {
				t.Error(err)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Create the structure and spawn a goroutine that keeps saving the structure
	// to disk for each directory.
	for _, dir := range dirStructure {
		sp, err := modules.NewSiaPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		err = fs.NewSiaDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := fs.OpenSiaDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to spawn goroutine. It's not realistic to assume that all dirs
		// are loaded.
		if fastrand.Intn(2) == 0 {
			wg.Add(1)
			go f(entry)
		} else {
			entry.Close()
		}
	}
	// Wait a second for the goroutines to write to disk a few times.
	time.Sleep(time.Second)
	// Rename dir1 to dir2.
	oldPath, err1 := modules.NewSiaPath(dirStructure[0])
	newPath, err2 := modules.NewSiaPath("dir2")
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}
	if err := fs.RenameDir(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	// Wait another second for more writes to disk after renaming the dir before
	// killing the goroutines.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(time.Second)
	// Make sure we can't open any of the old folders on disk but we can open the
	// new ones.
	for _, dir := range dirStructure {
		oldDir, err1 := modules.NewSiaPath(dir)
		newDir, err2 := oldDir.Rebase(oldPath, newPath)
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		// Open entry with old dir. Shouldn't work.
		_, err := fs.OpenSiaDir(oldDir)
		if err != ErrNotExist {
			t.Fatal("shouldn't be able to open old path", oldDir.String(), err)
		}
		// Open entry with new dir. Should succeed.
		entry, err := fs.OpenSiaDir(newDir)
		if err != nil {
			t.Fatal(err)
		}
		defer entry.Close()
		// Check path of entry.
		if expectedPath := fs.DirPath(newDir); *entry.path != expectedPath {
			t.Fatalf("entry should have path '%v' but was '%v'", expectedPath, entry.path)
		}
	}
}

// TestAddSiaFileFromReader tests the AddSiaFileFromReader method's behavior.
func TestAddSiaFileFromReader(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a fileset with file.
	sf, sfs, err := newTestFileSystemWithFile(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Add the existing file to the set again this shouldn't do anything.
	sr, err := sf.SnapshotReader()
	if err != nil {
		t.Fatal(err)
	}
	d, err := ioutil.ReadAll(sr)
	sr.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := sfs.AddSiaFileFromReader(bytes.NewReader(d), sfs.FileSiaPath(sf)); err != nil {
		t.Fatal(err)
	}
	numSiaFiles := 0
	err = sfs.Walk(modules.RootSiaPath(), func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) == modules.SiaFileExtension {
			numSiaFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// There should be 1 siafile.
	if numSiaFiles != 1 {
		t.Fatalf("Found %v siafiles but expected %v", numSiaFiles, 1)
	}
	// Load the same siafile again, but change the UID.
	b, err := ioutil.ReadFile(sf.SiaFilePath())
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(b)
	newSF, newChunks, err := siafile.LoadSiaFileFromReaderWithChunks(reader, sf.SiaFilePath(), sfs.staticWal)
	if err != nil {
		t.Fatal(err)
	}
	// Save the file to a temporary location with the new uid.
	newSF.UpdateUniqueID()
	newSF.SetSiaFilePath(sf.SiaFilePath() + "_tmp")
	if err := newSF.SaveWithChunks(newChunks); err != nil {
		t.Fatal(err)
	}
	// Grab the pre-import UID after changing it.
	preImportUID := newSF.UID()
	// Import the file. This should work because the files no longer share the same
	// UID.
	b, err = ioutil.ReadFile(newSF.SiaFilePath())
	if err != nil {
		t.Fatal(err)
	}
	// Remove file at temporary location after reading it.
	if err := os.Remove(newSF.SiaFilePath()); err != nil {
		t.Fatal(err)
	}
	reader = bytes.NewReader(b)
	var newSFSiaPath modules.SiaPath
	if err := newSFSiaPath.FromSysPath(sf.SiaFilePath(), sfs.Root()); err != nil {
		t.Fatal(err)
	}
	if err := sfs.AddSiaFileFromReader(reader, newSFSiaPath); err != nil {
		t.Fatal(err)
	}
	// Reload newSF with the new expected path.
	newSFPath := filepath.Join(filepath.Dir(sf.SiaFilePath()), newSFSiaPath.String()+"_1"+modules.SiaFileExtension)
	newSF, err = siafile.LoadSiaFile(newSFPath, sfs.staticWal)
	if err != nil {
		t.Fatal(err)
	}
	// sf and newSF should have the same pieces.
	for chunkIndex := uint64(0); chunkIndex < sf.NumChunks(); chunkIndex++ {
		piecesOld, err1 := sf.Pieces(chunkIndex)
		piecesNew, err2 := newSF.Pieces(chunkIndex)
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(piecesOld, piecesNew) {
			t.Log("piecesOld: ", piecesOld)
			t.Log("piecesNew: ", piecesNew)
			t.Fatal("old pieces don't match new pieces")
		}
	}
	numSiaFiles = 0
	err = sfs.Walk(modules.RootSiaPath(), func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) == modules.SiaFileExtension {
			numSiaFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// There should be 2 siafiles.
	if numSiaFiles != 2 {
		t.Fatalf("Found %v siafiles but expected %v", numSiaFiles, 2)
	}
	// The UID should have changed.
	if newSF.UID() == preImportUID {
		t.Fatal("newSF UID should have changed after importing the file")
	}
	if !strings.HasSuffix(newSF.SiaFilePath(), "_1"+modules.SiaFileExtension) {
		t.Fatal("SiaFile should have a suffix but didn't")
	}
	// Should be able to open the new file from disk.
	if _, err := os.Stat(newSF.SiaFilePath()); err != nil {
		t.Fatal(err)
	}
}

// TestSiaFileSetDeleteOpen checks that deleting an entry from the set followed
// by creating a Siafile with the same name without closing the deleted entry
// works as expected.
func TestSiaFileSetDeleteOpen(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create filesystem.
	sfs := newTestFileSystem(testDir(t.Name()))
	siaPath := modules.RandomSiaPath()
	rc, _ := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
	fileSize := uint64(100)
	source := ""
	sk := crypto.GenerateSiaKey(crypto.TypeDefaultRenter)
	fileMode := os.FileMode(persist.DefaultDiskPermissionsTest)

	// Repeatedly create a SiaFile and delete it while still keeping the entry
	// around. That should only be possible without errors if the correctly
	// delete the entry from the set.
	var entries []*FileNode
	for i := 0; i < 10; i++ {
		// Create SiaFile
		up := modules.FileUploadParams{
			Source:              source,
			SiaPath:             siaPath,
			ErasureCode:         rc,
			DisablePartialChunk: true,
		}
		err := sfs.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, sk, fileSize, fileMode, up.DisablePartialChunk)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := sfs.OpenSiaFile(up.SiaPath)
		if err != nil {
			t.Fatal(err)
		}
		// Delete SiaFile
		if err := sfs.DeleteFile(sfs.FileSiaPath(entry)); err != nil {
			t.Fatal(err)
		}
		// The map should be empty.
		if len(sfs.files) != 0 {
			t.Fatal("SiaFileMap should have 1 file")
		}
		// Append the entry to make sure we can close it later.
		entries = append(entries, entry)
	}
	// The SiaFile shouldn't exist anymore.
	_, err := sfs.OpenSiaFile(siaPath)
	if err != ErrNotExist {
		t.Fatal("SiaFile shouldn't exist anymore")
	}
	// Close the entries.
	for _, entry := range entries {
		entry.Close()
	}
}

// TestSiaFileSetOpenClose tests that the threadCount of the siafile is
// incremented and decremented properly when Open() and Close() are called
func TestSiaFileSetOpenClose(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestFileSystemWithFile(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.FileSiaPath(entry)
	exists, _ := sfs.FileExists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm 1 file is in memory
	if len(sfs.files) != 1 {
		t.Fatalf("Expected SiaFileSet map to be of length 1, instead is length %v", len(sfs.files))
	}

	// Confirm threadCount is incremented properly
	if len(entry.threads) != 1 {
		t.Fatalf("Expected threadMap to be of length 1, got %v", len(entry.threads))
	}

	// Close SiaFileSetEntry
	entry.Close()

	// Confirm that threadCount was decremented
	if len(entry.threads) != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", len(entry.threads))
	}

	// Confirm file and partialsSiaFile were removed from memory
	if len(sfs.files) != 0 {
		t.Fatalf("Expected SiaFileSet map to contain 0 files, instead is length %v", len(sfs.files))
	}

	// Open siafile again and confirm threadCount was incremented
	entry, err = sfs.OpenSiaFile(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.threads) != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", len(entry.threads))
	}
}

// TestFilesInMemory confirms that files are added and removed from memory
// as expected when files are in use and not in use
func TestFilesInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestFileSystemWithFile(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.FileSiaPath(entry)
	exists, _ := sfs.FileExists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 file in memory.
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 files in memory, got:", len(sfs.files))
	}
	// Close File
	entry.Close()
	// Confirm there are no files in memory
	if len(sfs.files) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.files))
	}

	// Test accessing the same file from two separate threads
	//
	// Open file
	entry1, err := sfs.OpenSiaFile(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}
	// Access the file again
	entry2, err := sfs.OpenSiaFile(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}
	// Close one of the file instances
	entry1.Close()
	// Confirm there is still only has 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first file
	entry2.Close()
	// Confirm there is no file in memory
	if len(sfs.files) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.files))
	}
}

// TestRenameFileInMemory confirms that threads that have access to a file
// will continue to have access to the file even it another thread renames it
func TestRenameFileInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile and corresponding combined siafile.
	entry, sfs, err := newTestFileSystemWithFile(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.FileSiaPath(entry)
	exists, _ := sfs.FileExists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm there are 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}

	// Test renaming an instance of a file
	//
	// Access file with another instance
	entry2, err := sfs.OpenSiaFile(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that renter still only has 2 files in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}
	_, err = os.Stat(entry.SiaFilePath())
	if err != nil {
		println("err2", err.Error())
	}
	// Rename second instance
	newSiaPath := modules.RandomSiaPath()
	err = sfs.RenameFile(siaPath, newSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are still only 1 file in memory as renaming doesn't add
	// the new name to memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}
	// Close instance of renamed file
	entry2.Close()
	// Confirm there are still only 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}
	// Close other instance of second file
	entry.Close()
	// Confirm there is no file in memory
	if len(sfs.files) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.files))
	}
}

// TestDeleteFileInMemory confirms that threads that have access to a file
// will continue to have access to the file even it another thread deletes it
func TestDeleteFileInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestFileSystemWithFile(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.FileSiaPath(entry)
	exists, _ := sfs.FileExists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm there is 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}

	// Test deleting an instance of a file
	//
	// Access the file again
	entry2, err := sfs.OpenSiaFile(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 file in memory
	if len(sfs.files) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.files))
	}
	// delete and close instance of file
	if err := sfs.DeleteFile(siaPath); err != nil {
		t.Fatal(err)
	}
	entry2.Close()
	// There should be no file in the set after deleting it.
	if len(sfs.files) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.files))
	}
	// confirm other instance is still in memory by calling methods on it
	if !entry.Deleted() {
		t.Fatal("Expected file to be deleted")
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first file
	entry.Close()
	// Confirm renter has one file in memory
	if len(sfs.files) != 0 {
		t.Fatal("Expected 0 file in memory, got:", len(sfs.files))
	}
}

// TestDeleteCorruptSiaFile confirms that the siafileset will delete a siafile
// even if it cannot be opened
func TestDeleteCorruptSiaFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create siafileset
	_, sfs, err := newTestFileSystemWithFile(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create siafile on disk with random bytes
	siaPath, err := modules.NewSiaPath("badFile")
	if err != nil {
		t.Fatal(err)
	}
	siaFilePath := siaPath.SiaFileSysPath(sfs.Root())
	err = ioutil.WriteFile(siaFilePath, fastrand.Bytes(100), 0666)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm the siafile cannot be opened
	_, err = sfs.OpenSiaFile(siaPath)
	if err == nil || err == ErrNotExist {
		t.Fatal("expected open to fail for read error but instead got:", err)
	}

	// Delete the siafile
	err = sfs.DeleteFile(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm the file is no longer on disk
	_, err = os.Stat(siaFilePath)
	if !os.IsNotExist(err) {
		t.Fatal("Expected err to be that file does not exists but was:", err)
	}
}

// TestSiaDirDelete tests the DeleteDir method of the siafileset.
func TestSiaDirDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Prepare a siadirset
	root := filepath.Join(testDir(t.Name()), "fs-root")
	os.RemoveAll(root)
	fs := newTestFileSystem(root)

	// Specify a directory structure for this test.
	var dirStructure = []string{
		"dir1",
		"dir1/subdir1",
		"dir1/subdir1/subsubdir1",
		"dir1/subdir1/subsubdir2",
		"dir1/subdir1/subsubdir3",
		"dir1/subdir2",
		"dir1/subdir2/subsubdir1",
		"dir1/subdir2/subsubdir2",
		"dir1/subdir2/subsubdir3",
		"dir1/subdir3",
		"dir1/subdir3/subsubdir1",
		"dir1/subdir3/subsubdir2",
		"dir1/subdir3/subsubdir3",
	}
	// Specify a function that's executed in parallel which continuously saves a
	// file to disk.
	stop := make(chan struct{})
	wg := new(sync.WaitGroup)
	f := func(entry *FileNode) {
		defer wg.Done()
		defer entry.Close()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := entry.SaveHeader()
			if err != nil && !strings.Contains(err.Error(), "can't call createAndApplyTransaction on deleted file") {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Create the structure and spawn a goroutine that keeps saving the structure
	// to disk for each directory.
	for _, dir := range dirStructure {
		sp, err := modules.NewSiaPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		err = fs.NewSiaDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := fs.OpenSiaDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to close the dir.
		if fastrand.Intn(2) == 0 {
			entry.Close()
		}
		// Create a file in the dir.
		fileSP, err := sp.Join(hex.EncodeToString(fastrand.Bytes(16)))
		if err != nil {
			t.Fatal(err)
		}
		ec, _ := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
		up := modules.FileUploadParams{Source: "", SiaPath: fileSP, ErasureCode: ec}
		err = fs.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.TypeDefaultRenter), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
		if err != nil {
			t.Fatal(err)
		}
		sf, err := fs.OpenSiaFile(up.SiaPath)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to spawn goroutine. It's not realistic to assume that all dirs
		// are loaded.
		if fastrand.Intn(2) == 0 {
			wg.Add(1)
			go f(sf)
		} else {
			sf.Close()
		}
	}
	// Wait a second for the goroutines to write to disk a few times.
	time.Sleep(time.Second)
	// Delete dir1.
	sp, err := modules.NewSiaPath("dir1")
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.DeleteDir(sp); err != nil {
		t.Fatal(err)
	}

	// Wait another second for more writes to disk after renaming the dir before
	// killing the goroutines.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(time.Second)
	// The root siafile dir should be empty except for 1 .siadir file.
	files, err := fs.ReadDir(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		for _, file := range files {
			t.Log("Found ", file.Name())
		}
		t.Fatalf("There should be %v files/folders in the root dir but found %v\n", 1, len(files))
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) != modules.SiaDirExtension &&
			filepath.Ext(file.Name()) != modules.PartialsSiaFileExtension {
			t.Fatal("Encountered unexpected file:", file.Name())
		}
	}
}

// TestSiaDirRenameWithFiles tests the RenameDir method of the filesystem with
// files.
func TestSiaDirRenameWithFiles(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Prepare a filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	os.RemoveAll(root)
	fs := newTestFileSystem(root)

	// Prepare parameters for siafiles.
	rc, _ := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
	fileSize := uint64(100)
	source := ""
	sk := crypto.GenerateSiaKey(crypto.TypeDefaultRenter)
	fileMode := os.FileMode(persist.DefaultDiskPermissionsTest)

	// Specify a directory structure for this test.
	var dirStructure = []string{
		"dir1",
		"dir1/subdir1",
		"dir1/subdir1/subsubdir1",
		"dir1/subdir1/subsubdir2",
		"dir1/subdir1/subsubdir3",
		"dir1/subdir2",
		"dir1/subdir2/subsubdir1",
		"dir1/subdir2/subsubdir2",
		"dir1/subdir2/subsubdir3",
		"dir1/subdir3",
		"dir1/subdir3/subsubdir1",
		"dir1/subdir3/subsubdir2",
		"dir1/subdir3/subsubdir3",
	}
	// Specify a function that's executed in parallel which continuously saves a
	// file to disk.
	stop := make(chan struct{})
	wg := new(sync.WaitGroup)
	f := func(entry *FileNode) {
		defer wg.Done()
		defer entry.Close()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := entry.SaveHeader()
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Create the structure and spawn a goroutine that keeps saving the structure
	// to disk for each directory.
	for _, dir := range dirStructure {
		sp, err := modules.NewSiaPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		err = fs.NewSiaDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := fs.OpenSiaDir(sp)
		// 50% chance to close the dir.
		if fastrand.Intn(2) == 0 {
			entry.Close()
		}
		// Create a file in the dir.
		fileSP, err := sp.Join(hex.EncodeToString(fastrand.Bytes(16)))
		if err != nil {
			t.Fatal(err)
		}
		err = fs.NewSiaFile(fileSP, source, rc, sk, fileSize, fileMode, true)
		if err != nil {
			t.Fatal(err)
		}
		sf, err := fs.OpenSiaFile(fileSP)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to spawn goroutine. It's not realistic to assume that all dirs
		// are loaded.
		if fastrand.Intn(2) == 0 {
			wg.Add(1)
			go f(sf)
		} else {
			sf.Close()
		}
	}
	// Wait a second for the goroutines to write to disk a few times.
	time.Sleep(time.Second)
	// Rename dir1 to dir2.
	oldPath, err1 := modules.NewSiaPath(dirStructure[0])
	newPath, err2 := modules.NewSiaPath("dir2")
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}
	if err := fs.RenameDir(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	// Wait another second for more writes to disk after renaming the dir before
	// killing the goroutines.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(time.Second)
	// Make sure we can't open any of the old folders/files on disk but we can open
	// the new ones.
	for _, dir := range dirStructure {
		oldDir, err1 := modules.NewSiaPath(dir)
		newDir, err2 := oldDir.Rebase(oldPath, newPath)
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		// Open entry with old dir. Shouldn't work.
		_, err := fs.OpenSiaDir(oldDir)
		if err != ErrNotExist {
			t.Fatal("shouldn't be able to open old path", oldDir.String(), err)
		}
		// Old dir shouldn't exist.
		if _, err = fs.Stat(oldDir); !os.IsNotExist(err) {
			t.Fatal(err)
		}
		// Open entry with new dir. Should succeed.
		entry, err := fs.OpenSiaDir(newDir)
		if err != nil {
			t.Fatal(err)
		}
		defer entry.Close()
		// New dir should contain 1 siafile.
		fis, err := fs.ReadDir(newDir)
		if err != nil {
			t.Fatal(err)
		}
		numFiles := 0
		for _, fi := range fis {
			if !fi.IsDir() && filepath.Ext(fi.Name()) == modules.SiaFileExtension {
				numFiles++
			}
		}
		if numFiles != 1 {
			t.Fatalf("there should be 1 file in the new dir not %v", numFiles)
		}
		// Check siapath of entry.
		if entry.managedAbsPath() != fs.DirPath(newDir) {
			t.Fatalf("entry should have path '%v' but was '%v'", fs.DirPath(newDir), entry.managedAbsPath())
		}
	}
}

// TestLazySiaDir tests that siaDir correctly reads and sets the lazySiaDir
// field.
func TestLazySiaDir(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create dir /foo
	sp := newSiaPath("foo")
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	// Open the newly created dir.
	foo, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer foo.Close()
	// Get the siadir.
	sd, err := foo.siaDir()
	if err != nil {
		t.Fatal(err)
	}
	// Lazydir should be set.
	if *foo.lazySiaDir != sd {
		t.Fatal(err)
	}
	// Fetching foo from root should also have lazydir set.
	fooRoot := fs.directories["foo"]
	if *fooRoot.lazySiaDir != sd {
		t.Fatal("fooRoot doesn't have lazydir set")
	}
	// Open foo again.
	foo2, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer foo2.Close()
	// Lazydir should already be loaded.
	if *foo2.lazySiaDir != sd {
		t.Fatal("foo2.lazySiaDir isn't set correctly", foo2.lazySiaDir)
	}
}

// TestLazySiaDir tests that siaDir correctly reads and sets the lazySiaDir
// field.
func TestOpenCloseRoot(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)

	rootNode, err := fs.OpenSiaDir(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	rootNode.Close()
}

// TestFailedOpenFileFolder makes sure that a failed call to OpenSiaFile or
// OpensiaDir doesn't leave any nodes dangling in memory.
func TestFailedOpenFileFolder(t *testing.T) {
	if testing.Short() && !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create dir /sub1/sub2
	sp := newSiaPath("sub1/sub2")
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	// Prepare a path to "foo"
	foo, err := sp.Join("foo")
	if err != nil {
		t.Fatal(err)
	}
	// Open "foo" as a dir.
	_, err = fs.OpenSiaDir(foo)
	if err != ErrNotExist {
		t.Fatal("err should be ErrNotExist but was", err)
	}
	if len(fs.files) != 0 || len(fs.directories) != 0 {
		t.Fatal("Expected 0 files and folders but got", len(fs.files), len(fs.directories))
	}
	// Open "foo" as a file.
	_, err = fs.OpenSiaDir(foo)
	if err != ErrNotExist {
		t.Fatal("err should be ErrNotExist but was", err)
	}
	if len(fs.files) != 0 || len(fs.directories) != 0 {
		t.Fatal("Expected 0 files and folders but got", len(fs.files), len(fs.directories))
	}
}
