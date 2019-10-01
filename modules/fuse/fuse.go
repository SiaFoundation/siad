package fuse

import (
	"errors"
	"io"
	"os"
	"path"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
)

// Info contains information about the current FUSE mount.
type Info struct {
	SiaPath modules.SiaPath
	Mount   string
}

// FUSE wraps a modules.Renter to implement FUSE bindings.
type FUSE struct {
	r modules.Renter

	srv  *fuse.Server
	root string
	sp   modules.SiaPath

	mu sync.Mutex
}

// New initializes a FUSE instance.
func New(r modules.Renter) *FUSE {
	return &FUSE{
		r: r,
	}
}

// Info reports information about the FUSE instance.
func (f *FUSE) Info() Info {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Info{
		SiaPath: f.sp,
		Mount:   f.root,
	}
}

// Mount mounts the files under the specified siapath under the 'root' folder on
// the local filesystem.
func (f *FUSE) Mount(root string, sp modules.SiaPath) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	f.sp = sp
	f.root = root
	return nil
}

// Unmount unmounts the current FUSE mount.
func (f *FUSE) Unmount() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.srv == nil {
		return errors.New("no server mounted")
	}
	err := f.srv.Unmount()
	if err != nil {
		return err
	}
	f.srv = nil
	f.sp = modules.SiaPath{}
	f.root = ""
	return nil
}

// errToStatus converts a Go error to a fuse.Status code.
func errToStatus(op, name string, err error) fuse.Status {
	if err == nil {
		return fuse.OK
	} else if os.IsNotExist(err) {
		return fuse.ENOENT
	}
	// TODO: log to a file
	//log.Printf("%v %v: %v", op, name, err)
	return fuse.EIO
}

// fuseFS implements pathfs.FileSystem using a modules.Renter.
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

// fuseFile implements nodefs.File using a modules.Renter.
type fuseFile struct {
	nodefs.File
	rf modules.RenterFile
	mu sync.Mutex
}

// Read implements nodefs.File.
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
