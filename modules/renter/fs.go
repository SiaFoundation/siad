package renter

import (
	"bytes"
	"io"
	"os"
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
	path := fs.path(name)
	fi, err := fs.r.File(path)
	if err == nil {
		return fileInfoShim{fi}, nil
	}
	di, err := fs.r.staticDirSet.DirInfo(path)
	return dirInfoShim{di}, err
}

func (fs *fsImpl) OpenFile(name string, perm int, mode os.FileMode) (modules.RenterFile, error) {
	if perm != os.O_RDONLY {
		return nil, errors.New("read-only filesystem")
	}
	if _, err := fs.Stat(name); err != nil {
		return nil, err
	}
	return &fsFile{
		sp: fs.path(name),
		r:  fs.r,
	}, nil
}

func (fs *fsImpl) Close() error {
	return nil
}

type fsFile struct {
	sp modules.SiaPath
	r  *Renter
}

func (f *fsFile) Name() string {
	return f.sp.String()
}

func (f *fsFile) Stat() (os.FileInfo, error) {
	fi, err := f.r.File(f.sp)
	return fileInfoShim{fi}, err
}

func (f *fsFile) Readdir(n int) ([]os.FileInfo, error) {
	fis, err := f.r.FileList(f.sp, false, true)
	if err != nil {
		return nil, err
	}
	dis, err := f.r.DirList(f.sp)
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

func (f *fsFile) Dirnames(n int) ([]string, error) {
	infos, err := f.Readdir(n)
	names := make([]string, len(infos))
	for i := range names {
		names[i] = infos[i].Name()
	}
	return names, err
}

func (f *fsFile) ReadAt(p []byte, off int64) (int, error) {
	var buf bytes.Buffer
	_, _, err := f.r.Download(modules.RenterDownloadParameters{
		SiaPath:    f.sp,
		Httpwriter: &buf,
		Offset:     uint64(off),
		Length:     uint64(len(p)),
	})
	n := copy(p, buf.Bytes())
	if err == nil && n != len(p) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func (f *fsFile) Close() error {
	return nil
}
