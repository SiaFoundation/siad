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
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// MountInfo returns the list of currently mounted FUSE filesystems.
func (r *Renter) MountInfo() []modules.MountInfo {
	if err := r.tg.Add(); err != nil {
		return nil
	}
	defer r.tg.Done()
	id := r.mu.Lock()
	defer r.mu.Unlock(id)
	var infos []modules.MountInfo
	for mountPoint, fs := range r.fuseMounts {
		infos = append(infos, modules.MountInfo{
			MountPoint: mountPoint,
			SiaPath:    fs.sp,
		})
	}
	return infos
}

// Mount mounts the files under the specified siapath under the 'mountPoint' folder on
// the local filesystem.
func (r *Renter) Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	id := r.mu.Lock()
	_, ok := r.fuseMounts[mountPoint]
	r.mu.Unlock(id)
	if ok {
		return errors.New("already mounted")
	}
	if !opts.ReadOnly {
		return errors.New("writable FUSE is not supported")
	}

	fs := &fuseFS{
		FileSystem: pathfs.NewDefaultFileSystem(),
		sp:         sp,
		r:          r,
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

	id = r.mu.Lock()
	r.fuseMounts[mountPoint] = fs
	r.mu.Unlock(id)
	return nil
}

// Unmount unmounts the FUSE filesystem currently mounted at mountPoint.
func (r *Renter) Unmount(mountPoint string) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	id := r.mu.Lock()
	f, ok := r.fuseMounts[mountPoint]
	delete(r.fuseMounts, mountPoint)
	r.mu.Unlock(id)

	if !ok {
		return errors.New("nothing mounted at that path")
	}
	return errors.AddContext(f.srv.Unmount(), "failed to unmount filesystem")
}

// fuseFS implements pathfs.FileSystem using a modules.Renter.
type fuseFS struct {
	pathfs.FileSystem
	srv *fuse.Server
	r   *Renter
	sp  modules.SiaPath
}

// path converts name to a siapath.
func (fs *fuseFS) path(name string) modules.SiaPath {
	sp, err := modules.NewSiaPath(name)
	if err != nil {
		build.Critical("invalid FUSE path:", name, err)
	}
	return sp
}

// errToStatus converts a Go error to a fuse.Status code and returns it. The Go
// error is written to the renter's log.
func (fs *fuseFS) errToStatus(op, name string, err error) fuse.Status {
	if err == nil {
		return fuse.OK
	} else if os.IsNotExist(err) {
		return fuse.ENOENT
	}
	fs.r.log.Printf("%v %v: %v", op, name, err)
	return fuse.EIO
}

func (fs *fuseFS) stat(name string) (os.FileInfo, error) {
	if strings.HasPrefix(name, ".") {
		// opening a "hidden" siafile results in a panic
		return nil, os.ErrNotExist
	}
	path := fs.path(name)
	fi, err := fs.r.File(path)
	if err != nil {
		// not a file; might be a directory
		return fs.r.staticDirSet.DirInfo(path)
	}
	return fi, nil
}

// GetAttr implements pathfs.FileSystem.
func (fs *fuseFS) GetAttr(name string, _ *fuse.Context) (*fuse.Attr, fuse.Status) {
	stat, err := fs.stat(name)
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
	sp := fs.path(name)
	fis, err := fs.r.FileList(sp, false, true)
	if err != nil {
		return nil, fs.errToStatus("OpenDir", name, err)
	}
	dis, err := fs.r.DirList(sp)
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
	if stat, err := fs.stat(name); err != nil {
		return nil, fs.errToStatus("Open", name, err)
	} else if stat.IsDir() {
		return nil, fuse.EISDIR
	}
	sp := fs.path(name)
	_, s, err := fs.r.Streamer(sp)
	if err != nil {
		return nil, fs.errToStatus("Open", name, err)
	}
	return &fuseFile{
		File: nodefs.NewDefaultFile(),
		sp:   sp,
		fs:   fs,
		s:    s,
	}, fuse.OK
}

// fuseFile implements nodefs.File using a modules.Renter.
type fuseFile struct {
	nodefs.File
	sp modules.SiaPath
	fs *fuseFS
	s  modules.Streamer
	mu sync.Mutex
}

// Read implements nodefs.File.
func (f *fuseFile) Read(p []byte, off int64) (fuse.ReadResult, fuse.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.s.Seek(off, io.SeekStart); err != nil {
		return nil, f.fs.errToStatus("Read", f.sp.String(), err)
	}
	n, err := f.s.Read(p)
	if err != nil && err != io.EOF {
		return nil, f.fs.errToStatus("Read", f.sp.String(), err)
	}
	return fuse.ReadResultData(p[:n]), fuse.OK
}
