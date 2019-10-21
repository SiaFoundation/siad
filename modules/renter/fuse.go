package renter

import (
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// MountInfo returns the list of currently mounted FUSE filesystems.
func (r *Renter) MountInfo() []modules.MountInfo {
	return r.staticFUSEManager.mountInfo()
}

// Mount mounts the files under the specified siapath under the 'mountPoint' folder on
// the local filesystem.
func (r *Renter) Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) error {
	return r.staticFUSEManager.mount(mountPoint, sp, opts)
}

// Unmount unmounts the FUSE filesystem currently mounted at mountPoint.
func (r *Renter) Unmount(mountPoint string) error {
	return r.staticFUSEManager.unmount(mountPoint)
}

// A fuseManager manages mounted FUSE filesystems.
type fuseManager struct {
	mountPoints map[string]*fuseFS

	mu          sync.Mutex
	r           *Renter
}

// mountInfo returns the list of currently mounted FUSE filesystems.
func (fm *fuseManager) mountInfo() []modules.MountInfo {
	if err := fm.r.tg.Add(); err != nil {
		return nil
	}
	defer fm.r.tg.Done()
	fm.mu.Lock()
	defer fm.mu.Unlock()
	var infos []modules.MountInfo
	for mountPoint, fs := range fm.mountPoints {
		infos = append(infos, modules.MountInfo{
			MountPoint: mountPoint,
			SiaPath:    fs.root,
		})
	}
	return infos
}

// Close unmounts all currently-mounted filesystems.
func (fm *fuseManager) Close() error {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	// unmount any mounted FUSE filesystems
	for path, fs := range fm.mountPoints {
		delete(fm.mountPoints, path)
		fs.srv.Unmount()
	}
	return nil
}

// newFUSEManager returns a new fuseManager.
func newFUSEManager(r *Renter) *fuseManager {
	return &fuseManager{
		mountPoints: make(map[string]*fuseFS),
		r:           r,
	}
}

// mount mounts the files under the specified siapath under the 'mountPoint' folder on
// the local filesystem.
func (fm *fuseManager) mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) error {
	if err := fm.r.tg.Add(); err != nil {
		return err
	}
	defer fm.r.tg.Done()
	fm.mu.Lock()
	_, ok := fm.mountPoints[mountPoint]
	fm.mu.Unlock()
	if ok {
		return errors.New("already mounted")
	}
	if !opts.ReadOnly {
		return errors.New("writable FUSE is not supported")
	}

	fs := &fuseFS{
		FileSystem: pathfs.NewDefaultFileSystem(),
		root:       sp,
		renter:     fm.r,
	}
	nfs := pathfs.NewPathNodeFs(fs, nil)
	// we need to call `Mount` rather than `MountRoot` because we want to define
	// the FUSE mount flag `AllowOther`, which enables non-permissioned users to
	// access the FUSE mount. This makes life easier in Docker.
	mountOpts := &fuse.MountOptions{
		AllowOther:   true,
		MaxReadAhead: 1,
	}
	server, _, err := nodefs.Mount(mountPoint, nfs.Root(), mountOpts, nil)
	if err != nil {
		return err
	}
	go server.Serve()
	fs.srv = server

	fm.mu.Lock()
	fm.mountPoints[mountPoint] = fs
	fm.mu.Unlock()
	return nil
}

// unmount unmounts the FUSE filesystem currently mounted at mountPoint.
func (fm *fuseManager) unmount(mountPoint string) error {
	if err := fm.r.tg.Add(); err != nil {
		return err
	}
	defer fm.r.tg.Done()

	fm.mu.Lock()
	f, ok := fm.mountPoints[mountPoint]
	delete(fm.mountPoints, mountPoint)
	fm.mu.Unlock()

	if !ok {
		return errors.New("nothing mounted at that path")
	}
	return errors.AddContext(f.srv.Unmount(), "failed to unmount filesystem")
}

// fuseFS implements pathfs.FileSystem using a modules.Renter.
type fuseFS struct {
	pathfs.FileSystem
	srv    *fuse.Server
	renter *Renter
	root   modules.SiaPath
}

// path converts name to a siapath.
func (fs *fuseFS) path(name string) (modules.SiaPath, bool) {
	if strings.HasPrefix(name, ".") {
		// opening a "hidden" siafile results in a panic
		return modules.SiaPath{}, false
	}
	sp, err := modules.NewSiaPath(name)
	return sp, err == nil
}

// errToStatus converts a Go error to a fuse.Status code and returns it. The Go
// error is written to the renter's log.
func (fs *fuseFS) errToStatus(op, name string, err error) fuse.Status {
	if err == nil {
		return fuse.OK
	} else if os.IsNotExist(err) {
		return fuse.ENOENT
	}
	fs.renter.log.Printf("%v %v: %v", op, name, err)
	return fuse.EIO
}

// stat returns the os.FileInfo for the named file.
func (fs *fuseFS) stat(path modules.SiaPath) (os.FileInfo, error) {
	fi, err := fs.renter.File(path)
	if err != nil {
		// not a file; might be a directory
		return fs.renter.staticDirSet.DirInfo(path)
	}
	return fi, nil
}

// GetAttr implements pathfs.FileSystem.
func (fs *fuseFS) GetAttr(name string, _ *fuse.Context) (*fuse.Attr, fuse.Status) {
	if name == "" {
		name = fs.root.String()
	}
	sp, ok := fs.path(name)
	if !ok {
		return nil, fuse.ENOENT
	}
	stat, err := fs.stat(sp)
	if err != nil {
		return nil, fs.errToStatus("GetAttr", name, err)
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
	sp, ok := fs.path(name)
	if !ok {
		return nil, fuse.ENOENT
	}
	fis, err := fs.renter.FileList(sp, false, true)
	if err != nil {
		return nil, fs.errToStatus("OpenDir", name, err)
	}
	dis, err := fs.renter.DirList(sp)
	if err != nil {
		return nil, fs.errToStatus("OpenDir", name, err)
	}

	entries := make([]fuse.DirEntry, 0, len(fis)+len(dis))
	for _, f := range fis {
		entries = append(entries, fuse.DirEntry{
			Name: path.Base(f.Name()),
			Mode: uint32(f.Mode()) | fuse.S_IFREG,
		})
	}
	for _, d := range dis {
		entries = append(entries, fuse.DirEntry{
			Name: path.Base(d.Name()),
			Mode: uint32(d.Mode()) | fuse.S_IFDIR,
		})
	}
	return entries, fuse.OK
}

// Open implements pathfs.FileSystem.
func (fs *fuseFS) Open(name string, flags uint32, _ *fuse.Context) (file nodefs.File, code fuse.Status) {
	if int(flags&fuse.O_ANYWRITE) != os.O_RDONLY {
		return nil, fuse.EROFS // read-only filesystem
	}
	sp, ok := fs.path(name)
	if !ok {
		return nil, fuse.ENOENT
	}
	if stat, err := fs.stat(sp); err != nil {
		return nil, fs.errToStatus("Open", name, err)
	} else if stat.IsDir() {
		return nil, fuse.EISDIR
	}
	_, s, err := fs.renter.Streamer(sp)
	if err != nil {
		return nil, fs.errToStatus("Open", name, err)
	}
	return &fuseFile{
		File:   nodefs.NewDefaultFile(),
		path:   sp,
		fs:     fs,
		stream: s,
	}, fuse.OK
}

// fuseFile implements nodefs.File using a modules.Renter.
type fuseFile struct {
	nodefs.File
	path   modules.SiaPath
	fs     *fuseFS
	stream modules.Streamer
	mu     sync.Mutex
}

// Read implements nodefs.File.
func (f *fuseFile) Read(p []byte, off int64) (fuse.ReadResult, fuse.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.stream.Seek(off, io.SeekStart); err != nil {
		return nil, f.fs.errToStatus("Read", f.path.String(), err)
	}
	n, err := f.stream.Read(p)
	if err != nil && err != io.EOF {
		return nil, f.fs.errToStatus("Read", f.path.String(), err)
	}
	return fuse.ReadResultData(p[:n]), fuse.OK
}
