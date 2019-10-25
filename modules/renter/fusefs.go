package renter

import (
	"io"
	"os"
	"path"
	"sync"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// fuseFile implements nodefs.File using a modules.Renter.
type fuseFile struct {
	nodefs.File

	fs     *fuseFS
	mu     sync.Mutex
	path   modules.SiaPath
	stream modules.Streamer
}

// fuseFS implements pathfs.FileSystem using a modules.Renter.
type fuseFS struct {
	pathfs.FileSystem
	renter *Renter
	root   modules.SiaPath
	srv    *fuse.Server
}

// errToStatus converts a Go error to a fuse.Status code and returns it.
func errToStatus(err error) fuse.Status {
	if err == nil {
		return fuse.OK
	} else if os.IsNotExist(err) {
		return fuse.ENOENT
	}
	return fuse.EIO
}

// Read implements nodefs.File.
func (f *fuseFile) Read(p []byte, off int64) (fuse.ReadResult, fuse.Status) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, err := f.stream.Seek(off, io.SeekStart); err != nil {
		f.fs.renter.log.Printf("Error seeking to offset %v during call to Read in file %s: %v", off, f.path.String(), err)
		return nil, errToStatus(err)
	}
	n, err := f.stream.Read(p)
	if err != nil && err != io.EOF {
		f.fs.renter.log.Printf("Error reading from offset %v during call to Read in file %s: %v", off, f.path.String(), err)
		return nil, errToStatus(err)
	}
	return fuse.ReadResultData(p[:n]), fuse.OK
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
	sp, err := fs.root.Join(name)
	if err != nil {
		fs.renter.log.Printf("Error calling Join on %v and %v: %v", fs.root, name, err)
		return nil, errToStatus(err)
	}
	stat, err := fs.stat(sp)
	if err != nil {
		fs.renter.log.Printf("Error calling GetAttr on %v: %v", name, err)
		return nil, errToStatus(err)
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
	sp, err := fs.root.Join(name)
	if err != nil {
		fs.renter.log.Printf("Error calling Join on %v and %v: %v", fs.root, name, err)
		return nil, errToStatus(err)
	}
	fis, err := fs.renter.FileList(sp, false, true)
	if err != nil {
		fs.renter.log.Printf("Error fetching file list on call to OpenDir for dir %v: %v", name, err)
		return nil, errToStatus(err)
	}
	dis, err := fs.renter.DirList(sp)
	if err != nil {
		fs.renter.log.Printf("Error fetching dir list on call to OpenDir for dir %v: %v", name, err)
		return nil, errToStatus(err)
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
	sp, err := modules.NewSiaPath(name)
	if err != nil {
		fs.renter.log.Printf("Error calling NewSiaPath on %v: %v", name, err)
		return nil, errToStatus(err)
	}
	if stat, err := fs.stat(sp); err != nil {
		fs.renter.log.Printf("Error getting stat on %v in call to Open: %v", name, err)
		return nil, errToStatus(err)
	} else if stat.IsDir() {
		return nil, fuse.EISDIR
	}
	_, s, err := fs.renter.Streamer(sp)
	if err != nil {
		fs.renter.log.Printf("Error getting streamer on %v in call to Open: %v", name, err)
		return nil, errToStatus(err)
	}
	return &fuseFile{
		File:   nodefs.NewDefaultFile(),
		path:   sp,
		fs:     fs,
		stream: s,
	}, fuse.OK
}
