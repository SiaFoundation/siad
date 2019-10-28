package renter

// fusemock_test.go implements an entire filesystem for the fuse library that
// exists fully in memory. The filesystem is procdurally generated. The purpose
// of this file is to have a very simple implementation which can be used for
// rapid experimentation when figuring out the 'fs' API.
//
// The first example of an early use of this file was debugging an issue where
// folders could not be opened by a file browser. Getting a much more minimal
// system working where folders were opening successfully in the file browser
// and then comparing the differences between the minimal version and the full
// sia fuse implementation proved to be very successful.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/siatest"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// fuseNode is a node to help build out a fuse system.
type fuseNode struct {
	fs.Inode
}

var _ = (fs.NodeLookuper)((*fuseNode)(nil))
var _ = (fs.NodeReaddirer)((*fuseNode)(nil))

// Lookup finds a dir.
func (fn *fuseNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if name != "one" {
		return nil, syscall.ENOENT
	}

	stable := fs.StableAttr{
		Mode: fuse.S_IFDIR,
	}
	childFN := &fuseNode{}
	child := fn.NewInode(ctx, childFN, stable)
	return child, syscall.F_OK
}

// Readdir will always return one child dir.
func (fn *fuseNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries := []fuse.DirEntry{
		{
			Name: "one",
			Mode: fuse.S_IFDIR,
		},
	}
	return fs.NewListDirStream(entries), syscall.F_OK
}

// TestGeneratedFuse tests a fuse implementation where all folders and files are
// generated on the fly. This test exists to understand what implementation
// details are required to have a working fuse system that's applied to the
// renter.
//
// This test only works on linux.
func TestGeneratedFuse(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	if runtime.GOOS != "linux" {
		t.Skip("Skipping Fuse test on non-Linux OS")
	}
	t.Parallel()
	testDir := siatest.TestDir("fuse", t.Name())
	err := os.MkdirAll(testDir, 0700)
	if err != nil {
		t.Fatal(err)
	}

	// Mount fuse.
	mountpoint := filepath.Join(testDir, "mount")
	err = os.MkdirAll(mountpoint, 0700)
	if err != nil {
		t.Fatal(err)
	}
	root := &fuseNode{}
	server, err := fs.Mount(mountpoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			Debug: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try reading the directory to see its subdir.
	println("calling open on mountpoint")
	fuseRoot, err := os.Open(mountpoint)
	if err != nil {
		t.Fatal(err)
	}
	println("readdirnames")
	names, err := fuseRoot.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", fuseRoot.Close())
	}
	if len(names) != 1 {
		t.Error("child dir is not appearing", len(names))
	}
	println("readdir")
	infos, err := fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Error("the number of infos returned is not 1", len(infos))
	}
	println("calling close on mountpoint")
	err = fuseRoot.Close()
	if err != nil {
		t.Fatal(err)
	}

	// This sleep is here to allow the developer to have time to open the fuse
	// directory in a file browser to inspect everything. The millisecond long
	// sleep that is not commented out exists so that the 'time' package is
	// always used; the developer does not need to keep adding it and deleting
	// it as they switch between wanting the sleep and wanting the test to run
	// fast.
	//
	// time.Sleep(time.Second * 60)
	time.Sleep(time.Millisecond)

	// Unmount fuse.
	err = server.Unmount()
	if err != nil {
		t.Fatal(err)
	}
}
