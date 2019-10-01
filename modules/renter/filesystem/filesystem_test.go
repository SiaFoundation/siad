package filesystem

import (
	"os"
	"path/filepath"
	"testing"

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

// newTestFileSystem creates a new filesystem for testing.
func newTestFileSystem(root string) *FileSystem {
	fs, err := New(root)
	if err != nil {
		panic(err.Error())
	}
	return fs
}

// TestNew tests creating a new FileSystem.
func TestNew(t *testing.T) {
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
