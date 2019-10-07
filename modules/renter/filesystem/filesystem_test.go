package filesystem

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/build"
)

// testDir creates a testing directory for a filesystem test.
func testDir(name string) string {
	dir := build.TempDir(name, filepath.Join("filesystem"))
	if err := os.MkdirAll(dir, 0777); err != nil {
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
	fs, err := New(root, wal)
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

// AddTestSiaFile is a convenience method to add a SiaFile for testing to a FileSystem.
func (fs *FileSystem) AddTestSiaFile(siaPath modules.SiaPath) {
	ec, err := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		panic(err)
	}
	err = fs.NewSiaFile(siaPath, "", ec, crypto.GenerateSiaKey(crypto.TypeDefaultRenter), uint64(fastrand.Intn(100)), 0777, true)
	if err != nil {
		panic(err)
	}
}

// TestNew tests creating a new FileSystem.
func TestNew(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Check fields.
	if fs.staticParent != nil {
		t.Fatalf("fs.parent shoud be 'nil' but wasn't")
	}
	if fs.staticName != root {
		t.Fatalf("fs.staticName should be %v but was %v", root, fs.staticName)
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
	_ = newTestFileSystem(fs.staticName)
}

// TestNewSiaDir tests if creating a new directory using NewSiaDir creates the
// correct folder structure.
func TestNewSiaDir(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
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

// TestNewSiaDir tests if creating a new directory using NewSiaDir creates the
// correct folder structure.
func TestNewSiaFile(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create file /sub/foo/file
	sp := newSiaPath("sub/foo/file")
	fs.AddTestSiaFile(sp)
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, sp.String())); err != nil {
		t.Fatal(err)
	}
	// Create a file in the root dir.
	sp = newSiaPath("file")
	fs.AddTestSiaFile(sp)
	if err := fs.NewSiaDir(sp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, sp.String())); err != nil {
		t.Fatal(err)
	}
}

// TestOpenSiaDir confirms that a previoiusly created SiaDir can be opened and
// that the filesystem tree is extended accordingly in the process.
func TestOpenSiaDir(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
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
	if len(fs.threads) != 0 {
		t.Fatalf("Expected fs.threads to have length 0 but was %v", len(fs.threads))
	}
	if len(fs.directories) != 2 {
		t.Fatalf("Expected 2 subdirectories in the root but got %v", len(fs.directories))
	}
	if len(fs.files) != 0 {
		t.Fatalf("Expected 0 files in the root but got %v", len(fs.files))
	}
	// Confirm the integrity of the /sub node.
	subNode, exists := fs.directories["sub"]
	if !exists {
		t.Fatal("expected root to contain the 'sub' node")
	}
	if subNode.staticName != "sub" {
		t.Fatalf("subNode name should be 'sub' but was %v", subNode.staticName)
	}
	if len(subNode.threads) != 0 {
		t.Fatalf("expected 0 threads in subNode but got %v", len(subNode.threads))
	}
	if len(subNode.directories) != 1 {
		t.Fatalf("Expected 1 subdirectory in the root but got %v", len(subNode.directories))
	}
	if len(subNode.files) != 0 {
		t.Fatalf("Expected 0 files in the root but got %v", len(subNode.files))
	}
	// Confirm the integrity of the /sub/foo node.
	fooNode, exists := subNode.directories["foo"]
	if !exists {
		t.Fatal("expected /sub to contain /sub/foo")
	}
	if fooNode.staticName != "foo" {
		t.Fatalf("fooNode name should be 'foo' but was %v", fooNode.staticName)
	}
	if len(fooNode.threads) != 1 {
		t.Fatalf("expected 1 thread in fooNode but got %v", len(fooNode.threads))
	}
	if len(fooNode.directories) != 0 {
		t.Fatalf("Expected 0 subdirectory in the fooNode but got %v", len(fooNode.directories))
	}
	if len(fooNode.files) != 0 {
		t.Fatalf("Expected 0 files in the root but got %v", len(fooNode.files))
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
	if len(subNode.threads) != 1 || len(sdSub.threads) != 1 {
		t.Fatal("subNode and sdSub should both have 1 thread registered")
	}
	if len(subNode.directories) != 1 || len(sdSub.directories) != 1 {
		t.Fatal("subNode and sdSub should both have 1 subdir")
	}
	if len(subNode.files) != 0 || len(sdSub.files) != 0 {
		t.Fatal("subNode and sdSub should both have 0 files")
	}
}

// TestOpenSiaFile confirms that a previously created SiaFile can be opened and
// that the filesystem tree is extended accordingly in the process.
func TestOpenSiaFile(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
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
	if sf.staticName != "file" {
		t.Fatalf("name of file should be file but was %v", sf.staticName)
	}
	if sf.staticParent != &fs.dNode {
		t.Fatalf("parent of file should be %v but was %v", &fs.node, sf.staticParent)
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
	sp = newSiaPath("/sub/file")
	fs.AddTestSiaFile(sp)
	// Open the newly created file.
	sf2, err := fs.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	defer sf2.Close()
	// Confirm the integrity of the file.
	if sf2.staticName != "file" {
		t.Fatalf("name of file should be file but was %v", sf2.staticName)
	}
	if sf2.staticParent.staticName != "sub" {
		t.Fatalf("parent of file should be %v but was %v", "sub", sf2.staticParent.staticName)
	}
	if sf2.threadUID == 0 {
		t.Fatal("threaduid wasn't set")
	}
	if len(sf2.threads) != 1 {
		t.Fatalf("len(threads) should be 1 but was %v", len(sf2.threads))
	}
	// Confirm the integrity of the "sub" folder.
	sub := sf2.staticParent
	if len(sub.threads) != 0 {
		t.Fatalf("Expected sub.threads to have length 0 but was %v", len(sub.threads))
	}
	if len(sub.directories) != 0 {
		t.Fatalf("Expected 0 subdirectories in sub but got %v", len(sub.directories))
	}
	if len(sub.files) != 1 {
		t.Fatalf("Expected 1 file in sub but got %v", len(sub.files))
	}
	if _, exists := sf2.threads[sf2.threadUID]; !exists {
		t.Fatal("threaduid doesn't exist in threads map")
	}
}

// TestCloseSiaDir tests that closing an opened directory shrinks the tree
// accordingly.
func TestCloseSiaDir(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
	// Create filesystem.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	// Create dir /sub/foo
	sp := newSiaPath("sub/foo")
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
	if len(sd.staticParent.threads) != 0 {
		t.Fatalf("The parent shouldn't have any threads but had %v", len(sd.staticParent.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sd.staticParent.directories) != 1 {
		t.Fatalf("The parent should have 1 directory but got %v", len(sd.staticParent.directories))
	}
	// After closing it the thread should be gone.
	sd.Close()
	if len(fs.threads) != 0 {
		t.Fatalf("There should be 0 threads in fs.threads but got %v", len(fs.threads))
	}
	if len(sd.threads) != 0 {
		t.Fatalf("There should be 0 threads in sd.threads but got %v", len(sd.threads))
	}
	if len(fs.directories) != 0 {
		t.Fatalf("There should be 0 directories in fs.directories but got %v", len(fs.directories))
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
	if len(sd1.staticParent.directories) != 1 || len(sd2.staticParent.directories) != 1 {
		t.Fatalf("The parent should have 1 directory but got %v", len(sd.staticParent.directories))
	}
	// Close one instance.
	sd1.Close()
	if len(sd1.threads) != 1 || len(sd2.threads) != 1 {
		t.Fatalf("There should be 1 thread in sd.threads but got %v", len(sd1.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sd1.staticParent.directories) != 1 || len(sd2.staticParent.directories) != 1 {
		t.Fatalf("The parent should have 1 directory but got %v", len(sd.staticParent.directories))
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
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
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
	if len(sf.staticParent.threads) != 0 {
		t.Fatalf("The parent shouldn't have any threads but had %v", len(sf.staticParent.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 directory in fs.directories but got %v", len(fs.directories))
	}
	if len(sf.staticParent.files) != 1 {
		t.Fatalf("The parent should have 1 file but got %v", len(sf.staticParent.files))
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
	if len(sf1.staticParent.files) != 1 || len(sf2.staticParent.files) != 1 {
		t.Fatalf("The parent should have 1 file but got %v", len(sf1.staticParent.files))
	}
	// Close one instance.
	sf1.Close()
	if len(sf1.threads) != 1 || len(sf2.threads) != 1 {
		t.Fatalf("There should be 1 thread in sf1.threads but got %v", len(sf1.threads))
	}
	if len(fs.directories) != 1 {
		t.Fatalf("There should be 1 dir in fs.directories but got %v", len(fs.directories))
	}
	if len(sf1.staticParent.files) != 1 || len(sf2.staticParent.files) != 1 {
		t.Fatalf("The parent should have 1 file but got %v", len(sf1.staticParent.files))
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
	if len(sf1.staticParent.files) != 0 || len(sf2.staticParent.files) != 0 {
		t.Fatalf("The parent should have 0 files but got %v", len(sf1.staticParent.files))
	}
}

// TestThreadedAccess tests rapidly opening and closing files and directories
// from multiple threads to check the locking conventions.
func TestThreadedAccess(t *testing.T) {
	if testing.Short && !build.VLONG {
		t.SkipNow()
	}
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
	if len(fs.files) != 0 {
		t.Fatalf("fs should have 0 files but had %v", len(fs.files))
	}
	if len(fs.directories) != 0 {
		t.Fatalf("fs should have 0 directories but had %v", len(fs.directories))
	}
}
