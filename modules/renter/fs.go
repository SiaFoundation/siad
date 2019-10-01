package renter

import (
	"os"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// FileSystem returns a renter filesystem.
func (r *Renter) FileSystem(root modules.SiaPath) (modules.RenterFS, error) {
	// TODO: check that root is a valid directory
	return &fsImpl{r, root}, nil
}

type fsImpl struct {
	r    *Renter
	root modules.SiaPath
}

func (fs *fsImpl) path(name string) modules.SiaPath {
	sp, _ := modules.NewSiaPath(name)
	return sp
}

type fileInfoShim struct {
	modules.FileInfo
}

func (f fileInfoShim) Name() string       { return f.SiaPath.String() }
func (f fileInfoShim) Size() int64        { return int64(f.Filesize) }
func (f fileInfoShim) Mode() os.FileMode  { return 0666 }
func (f fileInfoShim) ModTime() time.Time { return f.FileInfo.ModTime }
func (f fileInfoShim) IsDir() bool        { return false }
func (f fileInfoShim) Sys() interface{}   { return nil }

type dirInfoShim struct {
	modules.DirectoryInfo
}

func (d dirInfoShim) Name() string       { return d.SiaPath.String() }
func (d dirInfoShim) Size() int64        { return 0 }
func (d dirInfoShim) Mode() os.FileMode  { return 0700 }
func (d dirInfoShim) ModTime() time.Time { return d.DirectoryInfo.MostRecentModTime }
func (d dirInfoShim) IsDir() bool        { return true }
func (d dirInfoShim) Sys() interface{}   { return nil }

func (fs *fsImpl) Stat(name string) (os.FileInfo, error) {
	if strings.HasPrefix(name, ".") {
		// opening a "hidden" siafile results in a panic
		return nil, os.ErrNotExist
	}
	path := fs.path(name)
	fi, err := fs.r.File(path)
	if err != nil {
		// not a file; might be a directory
		di, err := fs.r.staticDirSet.DirInfo(path)
		return dirInfoShim{di}, err
	}
	return fileInfoShim{fi}, nil
}

func (fs *fsImpl) OpenFile(name string, perm int, mode os.FileMode) (modules.RenterFile, error) {
	if perm != os.O_RDONLY {
		return nil, errors.New("read-only filesystem")
	}
	if stat, err := fs.Stat(name); err != nil {
		return nil, err
	} else if stat.IsDir() {
		return &fsDir{
			sp: fs.path(name),
			r:  fs.r,
		}, nil
	}
	sp := fs.path(name)
	_, s, err := fs.r.Streamer(sp)
	if err != nil {
		return nil, err
	}
	return &fsFile{
		sp: sp,
		r:  fs.r,
		s:  s,
	}, nil
}

func (fs *fsImpl) Close() error {
	return nil
}

type fsDir struct {
	sp modules.SiaPath
	r  *Renter
}

func (d *fsDir) Readdir(n int) ([]os.FileInfo, error) {
	fis, err := d.r.FileList(d.sp, false, true)
	if err != nil {
		return nil, err
	}
	dis, err := d.r.DirList(d.sp)
	if err != nil {
		return nil, err
	}
	var infos []os.FileInfo
	for _, di := range dis {
		infos = append(infos, dirInfoShim{di})
	}
	for _, fi := range fis {
		infos = append(infos, fileInfoShim{fi})
	}
	return infos, nil
}

func (d *fsDir) Dirnames(n int) ([]string, error) {
	infos, err := d.Readdir(n)
	names := make([]string, len(infos))
	for i := range names {
		names[i] = infos[i].Name()
	}
	return names, err
}

func (d *fsDir) Name() string {
	return d.sp.String()
}

func (d *fsDir) Stat() (os.FileInfo, error) {
	di, err := d.r.staticDirSet.DirInfo(d.sp)
	return dirInfoShim{di}, err
}

func (d *fsDir) Read(p []byte) (int, error) {
	return 0, errors.New("cannot call Read on directory")
}

func (d *fsDir) Seek(off int64, whence int) (int64, error) {
	return 0, errors.New("cannot call Seek on directory")
}

func (d *fsDir) Close() error {
	return nil
}

type fsFile struct {
	sp modules.SiaPath
	r  *Renter
	s  modules.Streamer
}

func (f *fsFile) Name() string {
	return f.sp.String()
}

func (f *fsFile) Stat() (os.FileInfo, error) {
	fi, err := f.r.File(f.sp)
	return fileInfoShim{fi}, err
}

func (f *fsFile) Readdir(n int) ([]os.FileInfo, error) {
	return nil, errors.New("cannot call Readdir on file")
}

func (f *fsFile) Dirnames(n int) ([]string, error) {
	return nil, errors.New("cannot call Dirnames on file")
}

func (f *fsFile) Read(p []byte) (int, error) {
	return f.s.Read(p)
}

func (f *fsFile) Seek(off int64, whence int) (int64, error) {
	return f.s.Seek(off, whence)
}

func (f *fsFile) Close() error {
	return nil
}
