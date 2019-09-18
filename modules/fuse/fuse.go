package fuse

import (
	"errors"
	"io"
	"log"
	"os"
	"path"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
)

type Info struct {
	SiaPath modules.SiaPath
	Mount   string
}

type FUSE struct {
	r modules.Renter

	srv  *fuse.Server
	root string
	sp   modules.SiaPath
}

func New(r modules.Renter) *FUSE {
	return &FUSE{
		r: r,
	}
}

func (f *FUSE) Info() Info {
	return Info{
		SiaPath: f.sp,
		Mount:   f.root,
	}
}

func (f *FUSE) Mount(root string, sp modules.SiaPath) error {
	if f.srv != nil {
		return errors.New("already mounted")
	}
	rfs, err := f.r.FileSystem(sp)
	if err != nil {
		return err
	}
	fs := &fuseFS{
		FileSystem: pathfs.NewDefaultFileSystem(),
		rfs:        rfs,
	}
	nfs := pathfs.NewPathNodeFs(fs, nil)
	// we need to call `Mount` rather than `MountRoot` because we want to define
	// the FUSE mount flag `AllowOther`, which enables non-permissioned users to
	// access the FUSE mount. This makes life easier in Docker.
	mountOpts := &fuse.MountOptions{
		AllowOther:   true,
		MaxReadAhead: 1,
	}
	server, _, err := nodefs.Mount(root, nfs.Root(), mountOpts, nil)
	if err != nil {
		return err
	}
	go server.Serve()
	f.srv = server
	return nil
}

func (f *FUSE) Unmount() error {
	if f.srv == nil {
		return errors.New("no server mounted")
	}
	err := f.srv.Unmount()
	if err != nil {
		return err
	}
	f.srv = nil
	return nil
}

func errToStatus(op, name string, err error) fuse.Status {
	if err == nil {
		return fuse.OK
	} else if os.IsNotExist(err) {
		return fuse.ENOENT
	}
	log.Printf("%v %v: %v", op, name, err)
	return fuse.EIO
}

type fuseFS struct {
	pathfs.FileSystem
	rfs modules.RenterFS
}

// GetAttr implements pathfs.FileSystem.
func (fs *fuseFS) GetAttr(name string, _ *fuse.Context) (*fuse.Attr, fuse.Status) {
	stat, err := fs.rfs.Stat(name)
	if err != nil {
		return nil, errToStatus("GetAttr", name, err)
	}
	var mode uint32
	if stat.IsDir() {
		mode = fuse.S_IFDIR
	} else {
		mode = fuse.S_IFREG
	}
	return &fuse.Attr{
		Size:  uint64(stat.Size()),
		Mode:  mode | uint32(stat.Mode()),
		Mtime: uint64(stat.ModTime().Unix()),
	}, fuse.OK
}

// OpenDir implements pathfs.FileSystem.
func (fs *fuseFS) OpenDir(name string, _ *fuse.Context) ([]fuse.DirEntry, fuse.Status) {
	dir, err := fs.rfs.OpenFile(name, 0, 0)
	if err != nil {
		return nil, errToStatus("OpenDir", name, err)
	}
	defer dir.Close()
	files, err := dir.Readdir(-1)
	if err != nil {
		return nil, errToStatus("OpenDir", name, err)
	}
	entries := make([]fuse.DirEntry, len(files))
	for i, f := range files {
		// When we don't remove the prepending path from the name, then FUSE won't
		// show any subdirectory files.
		name := path.Base(f.Name())
		mode := uint32(f.Mode())
		if f.IsDir() {
			mode |= fuse.S_IFDIR
		} else {
			mode |= fuse.S_IFREG
		}
		entries[i] = fuse.DirEntry{
			Name: name,
			Mode: mode,
		}
	}
	return entries, fuse.OK
}

// Open implements pathfs.FileSystem.
func (fs *fuseFS) Open(name string, flags uint32, _ *fuse.Context) (file nodefs.File, code fuse.Status) {
	flags &= fuse.O_ANYWRITE | uint32(os.O_APPEND)
	rf, err := fs.rfs.OpenFile(name, int(flags), 0)
	if err != nil {
		return nil, errToStatus("Open", name, err)
	}
	return &fuseFile{
		File: nodefs.NewDefaultFile(),
		rf:   rf,
	}, fuse.OK
}

/*

// Create implements pathfs.FileSystem.
func (fs *fuseFS) Create(name string, flags uint32, mode uint32, _ *fuse.Context) (file nodefs.File, code fuse.Status) {
	rf, err := fs.rfs.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_RDWR, os.FileMode(mode))
	if err != nil {
		return nil, errToStatus("Create", name, err)
	}
	return &fuseFile{
		File: nodefs.NewDefaultFile(),
		rf:   rf,
	}, fuse.OK
}


// Unlink implements pathfs.FileSystem.
func (fs *fuseFS) Unlink(name string, _ *fuse.Context) (code fuse.Status) {
	if err := fs.rfs.Remove(name); err != nil {
		return errToStatus("Unlink", name, err)
	}
	return fuse.OK
}

// Rename implements pathfs.FileSystem.
func (fs *fuseFS) Rename(oldName string, newName string, context *fuse.Context) (code fuse.Status) {
	if err := fs.rfs.Rename(oldName, newName); err != nil {
		return errToStatus("Rename", oldName, err)
	}
	return fuse.OK
}

// Mkdir implements pathfs.FileSystem.
func (fs *fuseFS) Mkdir(name string, mode uint32, context *fuse.Context) (code fuse.Status) {
	if err := fs.rfs.MkdirAll(name, os.FileMode(mode)); err != nil {
		return errToStatus("Mkdir", name, err)
	}
	return fuse.OK
}

// Rmdir implements pathfs.FileSystem.
func (fs *fuseFS) Rmdir(name string, _ *fuse.Context) (code fuse.Status) {
	if err := fs.rfs.RemoveAll(name); err != nil {
		return errToStatus("Rmdir", name, err)
	}
	return fuse.OK
}

// Chmod implements pathfs.FileSystem.
func (fs *fuseFS) Chmod(name string, mode uint32, context *fuse.Context) (code fuse.Status) {
	if err := fs.rfs.Chmod(name, os.FileMode(mode)); err != nil {
		return errToStatus("Chmod", name, err)
	}
	return fuse.OK
}

*/

type fuseFile struct {
	nodefs.File
	rf modules.RenterFile
	mu sync.Mutex
}

func (f *fuseFile) Read(p []byte, off int64) (fuse.ReadResult, fuse.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.rf.Seek(off, io.SeekStart); err != nil {
		return nil, errToStatus("Read", f.rf.Name(), err)
	}
	n, err := f.rf.Read(p)
	if err != nil && err != io.EOF {
		return nil, errToStatus("Read", f.rf.Name(), err)
	}
	return fuse.ReadResultData(p[:n]), fuse.OK
}

/*

func (f *fuseFile) Write(p []byte, off int64) (written uint32, code fuse.Status) {
	n, err := f.rf.WriteAt(p, off)
	if err != nil {
		return 0, errToStatus("Write", f.rf.Name(), err)
	}
	return uint32(n), fuse.OK
}

func (f *fuseFile) Truncate(size uint64) fuse.Status {
	if err := f.rf.Truncate(int64(size)); err != nil {
		return errToStatus("Truncate", f.rf.Name(), err)
	}
	return fuse.OK
}

func (f *fuseFile) Flush() fuse.Status {
	return fuse.OK
}

func (f *fuseFile) Release() {
	if err := f.rf.Close(); err != nil {
		_ = errToStatus("Release", f.rf.Name(), err)
	}
}

func (f *fuseFile) Fsync(flags int) fuse.Status {
	if err := f.rf.Sync(); err != nil {
		return errToStatus("Fsync", f.rf.Name(), err)
	}
	return fuse.OK
}

*/
