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
	"strings"
	"syscall"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/siatest"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	generatedFSDepth = 2
	foldersPerFolder = 2
	filesPerFolder   = 2
)

// fuseNode is a node to help build out a fuse system.
type fuseNode struct {
	fs.Inode

	name  string
	depth uint64
	ino   uint64
}

var _ = (fs.NodeAccesser)((*fuseNode)(nil))
var _ = (fs.NodeGetattrer)((*fuseNode)(nil))
var _ = (fs.NodeLookuper)((*fuseNode)(nil))
var _ = (fs.NodeOpener)((*fuseNode)(nil))
var _ = (fs.NodeReaddirer)((*fuseNode)(nil))
var _ = (fs.NodeReader)((*fuseNode)(nil))
var _ = (fs.NodeStatfser)((*fuseNode)(nil))

// Access determines whether or not a caller has permission to access a file.
func (fn *fuseNode) Access(ctx context.Context, mask uint32) syscall.Errno {
	return syscall.F_OK
}

// Getattr will return the mode of the node, and the size if the node is a file.
func (fn *fuseNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Ino = fn.ino
	if strings.Contains(fn.name, "file") {
		out.Mode = 0644
		out.Size = 26
	} else {
		out.Mode = 0666
	}
	return syscall.F_OK
}

// Lookup finds a dir.
func (fn *fuseNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Return ENOENT if the name doesn't match the pattern for dir names. Could
	// make this check a regex.
	if !strings.Contains(name, "file") && len(name) > 1 && name != "one" {
		return nil, syscall.ENOENT
	}
	// Return ENOENT if the depth of the current node is the full FS depth.
	if fn.depth > generatedFSDepth {
		return nil, syscall.ENOENT
	}

	// Set the lookup depth. The depth is used to determine when we stop
	// generating more files and folders for the filesystem.
	lookupDepth := fn.depth + 1

	// Determine the inode of the file being looked up. The inode is derived by
	// the parent and by the name. By multiplying the parent inode by 1000, we
	// can ensure that this inode will be unique so long as the parent inode was
	// unique.
	lookupIno := fn.ino * 1000
	if lookupIno < fn.ino {
		panic("overflow error when gneerating inode number")
	}
	if name == "one" {
		lookupIno += 1
	} else if strings.Contains(name, "file") {
		trim := strings.Trim(name, "file")
		lookupIno += 100
		lookupIno += uint64(byte(trim[0]))
	} else {
		lookupIno += 2
		lookupIno += uint64(byte(name[0]))
	}

	// Set the stable attributes of the file based on the name.
	var stable fs.StableAttr
	stable.Ino = lookupIno
	out.Ino = lookupIno
	if strings.Contains(name, "file") {
		stable.Mode = fuse.S_IFREG
		out.Mode = 0644
		out.Size = 26
	} else {
		stable.Mode = fuse.S_IFDIR
		out.Mode = 0666
	}

	childFN := &fuseNode{
		name:  name,
		depth: lookupDepth,
		ino:   lookupIno,
	}
	child := fn.NewInode(ctx, childFN, stable)
	return child, syscall.F_OK
}

// Open will no-op and return an "opened" file.
func (fn *fuseNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	return fn, 0, syscall.F_OK
}

// Readdir will always return one child dir.
func (fn *fuseNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Add a directory.
	var entries []fuse.DirEntry
	if fn.depth < generatedFSDepth {
		entries = append(entries, fuse.DirEntry{
			Name: "one",
			Mode: fuse.S_IFDIR | 0666,
		})
	}

	// Add more directories.
	if fn.depth < generatedFSDepth {
		for i := 1; i < foldersPerFolder; i++ {
			entries = append(entries, fuse.DirEntry{
				Name: string([]byte{byte(i + 48)}),
				Mode: fuse.S_IFDIR | 0666,
			})
		}
	}

	// Add more files.
	for i := 0; i < filesPerFolder; i++ {
		entries = append(entries, fuse.DirEntry{
			Name: "file" + string([]byte{byte(i + 48)}),
			Mode: fuse.S_IFREG | 0644,
		})
	}

	return fs.NewListDirStream(entries), syscall.F_OK
}

// Read will return generated data for a file.
func (fn *fuseNode) Read(ctx context.Context, f fs.FileHandle, dest []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	output := []byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b', 'b'}
	copy(dest, output)
	return fuse.ReadResultData(dest), syscall.F_OK
}

// Statfs will provide information about the filesystem that holds the inode.
func (fn *fuseNode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	out.Bsize = 4096
	out.Blocks = 100
	out.Bfree = 90
	out.Bavail = 80
	out.Files = 70
	out.Ffree = 60
	out.NameLen = 5
	out.Frsize = 4096
	out.Padding = 0
	return syscall.F_OK
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
	root := &fuseNode{
		depth: 0,
		ino:   1000,
	}
	server, err := fs.Mount(mountpoint, root, &fs.Options{
		MountOptions: fuse.MountOptions{
			// Debug: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try reading the directory to see its subdir.
	fuseRoot, err := os.Open(mountpoint)
	if err != nil {
		t.Fatal(err)
	}
	names, err := fuseRoot.Readdirnames(0)
	if err != nil {
		t.Fatal(err, "error early lets go", fuseRoot.Close())
	}
	if len(names) != foldersPerFolder+filesPerFolder {
		t.Error("wrong number of names", len(names), foldersPerFolder, filesPerFolder)
	}
	_, err = fuseRoot.Seek(0, 0)
	if err != nil {
		t.Fatal("unable to reset dir seek")
	}
	infos, err := fuseRoot.Readdir(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != foldersPerFolder+filesPerFolder {
		t.Error("wrong number of infos", len(infos), foldersPerFolder, filesPerFolder)
	}
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
	// time.Sleep(time.Second * 120)
	time.Sleep(time.Millisecond)

	// Unmount fuse.
	err = server.Unmount()
	if err != nil {
		t.Fatal(err)
	}
}
