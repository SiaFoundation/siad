package renter

import (
	"context"
	"io"
	"math"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// fuseDirnode is a fuse node for the fs package that covers a siadir.
//
// NOTE: The fuseDirnode is _very_hot_ in that it gets hit rapidly and
// concurrently and generally consumes a lot of CPU. If adding a mutex to
// fuseDirnode, make as many variables static as possible and ensure that only
// the non-hot functions need to use the mutex.
//
// In particular, the Lookup function should be computationally efficient.
type fuseDirnode struct {
	fs.Inode

	staticDirInfo       modules.DirectoryInfo
	staticSiaPath modules.SiaPath

	filesystem *fuseFS
}

// Ensure the dir nodes satisfy the required interfaces.
//
// NodeAccesser is necessary for telling certain programs that it is okay to
// access the file.
//
// NodeGetattrer provides details about the folder. This one may not be
// strictly necessary, I'm not sure what exact value it adds.
//
// NodeLookuper is necessary to have files added to the filesystem tree.
//
// NodeReaddirer is necessary to list the files in a directory.
//
// NodeStatfser is necessary to provide information about the filesystem that
// contains the directory.
var _ = (fs.NodeAccesser)((*fuseDirnode)(nil))
var _ = (fs.NodeGetattrer)((*fuseDirnode)(nil))
var _ = (fs.NodeLookuper)((*fuseDirnode)(nil))
var _ = (fs.NodeReaddirer)((*fuseDirnode)(nil))
var _ = (fs.NodeStatfser)((*fuseDirnode)(nil))

// fuseFilenode is a fuse node for the fs package that covers a siafile.
//
// Data is fetched using a download streamer. This download streamer needs to be
// closed when the filehandle is released.
type fuseFilenode struct {
	fs.Inode

	staticFileInfo modules.FileInfo
	staticSiaPath  modules.SiaPath

	// Not static - the stream is created separately from the node.
	stream modules.Streamer

	filesystem *fuseFS
	mu         sync.Mutex
}

// Ensure the file nodes satisfy the required interfaces.
//
// NodeAccesser is necessary for telling certain programs that it is okay to
// access the file.
//
// NodeFlusher is necessary for cleaning up resources such as the download
// streamer.
//
// NodeGetattrer is necessary for providing the filesize to file browsers.
//
// NodeOpener is necessary for opening files to be read.
//
// NodeReader is necessary for reading files.
//
// NodeStatfser is necessary to provide information about the filesystem that
// contains the file.
var _ = (fs.NodeAccesser)((*fuseFilenode)(nil))
var _ = (fs.NodeFlusher)((*fuseFilenode)(nil))
var _ = (fs.NodeGetattrer)((*fuseFilenode)(nil))
var _ = (fs.NodeOpener)((*fuseFilenode)(nil))
var _ = (fs.NodeReader)((*fuseFilenode)(nil))
var _ = (fs.NodeStatfser)((*fuseFilenode)(nil))

// fuseRoot is the root directory for a mounted fuse filesystem.
type fuseFS struct {
	fuseDirnode
	readOnly bool
	root     *fuseDirnode

	renter *Renter
	server *fuse.Server
}

// errToStatus converts an error to a syscall.Errno
func errToStatus(err error) syscall.Errno {
	if err == nil {
		return syscall.F_OK
	} else if errors.IsOSNotExist(err) {
		return syscall.ENOENT
	}
	return syscall.EIO
}

// Access reports whether a file can be accessed by the caller.
func (ffn *fuseFilenode) Access(ctx context.Context, mask uint32) syscall.Errno {
	// Lazy option: always tell the program that it has access permission.
	return syscall.F_OK
}

// Access reports whether a file can be accessed by the caller.
func (fdn *fuseDirnode) Access(ctx context.Context, mask uint32) syscall.Errno {
	// Lazy option: always tell the program that it has access permission.
	return syscall.F_OK
}

// Flush is called when a file is being closed.
func (ffn *fuseFilenode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	ffn.mu.Lock()
	defer ffn.mu.Unlock()

	// If a stream was opened for the file, the stream must now be closed.
	if ffn.stream != nil {
		err := ffn.stream.Close()
		if err != nil {
			ffn.filesystem.renter.log.Printf("Unable to close stream for file %v: %v", ffn.staticSiaPath, err)
			return errToStatus(err)
		}
	}

	return errToStatus(nil)
}

// Lookup is a directory call that returns the file in the directory associated
// with the provided name. When a file browser is opening folders with lots of
// files, this method can be called thousands of times concurrently in a single
// second. It goes without saying that this method needs to be very fast.
func (fdn *fuseDirnode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	lookupPath, err := fdn.staticSiaPath.Join(name)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Unable to determine filepath for %v from directory %v: %v", name, fdn.staticSiaPath, err)
		return nil, errToStatus(err)
	}
	fileInfo, fileErr := fdn.filesystem.renter.FileCached(lookupPath)
	if fileErr == nil {
		// Convert the file to an inode.
		filenode := &fuseFilenode{
			staticFileInfo: fileInfo,
			staticSiaPath:  lookupPath,
			filesystem:     fdn.filesystem,
		}
		attrs := fs.StableAttr{
			Ino:  fileInfo.UID,
			Mode: fuse.S_IFREG,
		}

		// Set the crticial entry out values.
		//
		// TODO: Set more of these, there are like 20 of them.
		out.Ino = fileInfo.UID
		out.Size = fileInfo.Filesize
		out.Mode = uint32(fileInfo.Mode())

		inode := fdn.NewInode(ctx, filenode, attrs)
		return inode, errToStatus(nil)
	}

	// Unable to look up a file, might be a dir instead.
	staticDirInfo, dirErr := fdn.filesystem.renter.staticFileSystem.DirInfo(lookupPath)
	if dirErr != nil {
		fdn.filesystem.renter.log.Printf("Unable to perform lookup on %v in dir %v; file err %v :: dir err %v", name, fdn.staticSiaPath, fileErr, dirErr)
		return nil, errToStatus(dirErr)
	}

	// We found the directory we want, convert to an inode.
	dirnode := &fuseDirnode{
		staticDirInfo:       staticDirInfo,
		staticSiaPath: lookupPath,

		filesystem: fdn.filesystem,
	}
	attrs := fs.StableAttr{
		Ino:  dirnode.staticDirInfo.UID,
		Mode: fuse.S_IFDIR,
	}
	out.Ino = dirnode.staticDirInfo.UID
	out.Mode = uint32(staticDirInfo.Mode())
	inode := fdn.NewInode(ctx, dirnode, attrs)
	return inode, errToStatus(nil)
}

// Getattr returns the attributes of a fuse dir.
func (fdn *fuseDirnode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = uint32(fdn.staticDirInfo.Mode())
	out.Ino = fdn.staticDirInfo.UID
	return errToStatus(nil)
}

// Getattr returns the attributes of a fuse file.
//
// NOTE: When ffmpeg is running on a video, it spams Getattr on the open file.
// Getattr should try to minimize lock contention and should run very quickly if
// possible.
func (ffn *fuseFilenode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Size = ffn.staticFileInfo.Filesize
	out.Mode = uint32(ffn.staticFileInfo.Mode()) | syscall.S_IFREG
	out.Ino = ffn.staticFileInfo.UID
	return errToStatus(nil)
}

// Open will open a streamer for the file.
func (ffn *fuseFilenode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	ffn.mu.Lock()
	defer ffn.mu.Unlock()

	_, stream, err := ffn.filesystem.renter.Streamer(ffn.staticSiaPath, false)
	if err != nil {
		ffn.filesystem.renter.log.Printf("Unable to get stream for file %v: %v", ffn.staticSiaPath, err)
		return nil, 0, errToStatus(err)
	}
	ffn.stream = stream

	return ffn, 0, errToStatus(nil)
}

// Read will read data from the file and place it in dest.
func (ffn *fuseFilenode) Read(ctx context.Context, f fs.FileHandle, dest []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	// TODO: Right now only one call to Read from a file can be in effect at
	// once, based on the way the streamer and the read call has been
	// implemented. As the streamer gets updated to more readily support
	// multiple concurrrent streams at once, this method can be re-implemented
	// to greatly increase speeds.
	ffn.mu.Lock()
	defer ffn.mu.Unlock()

	_, err := ffn.stream.Seek(offset, io.SeekStart)
	if err != nil {
		ffn.filesystem.renter.log.Printf("Error seeking to offset %v during call to Read in file %s: %v", offset, ffn.staticSiaPath.String(), err)
		return nil, errToStatus(err)
	}

	// Ignore both EOF and ErrUnexpectedEOF when doing the ReadFull. If we
	// return ErrUnexpectedEOF, the program will try to read again but with a
	// smaller read size and be confused about how large the file actually is,
	// often dropping parts of the tail of the file.
	n, err := io.ReadFull(ffn.stream, dest)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		ffn.filesystem.renter.log.Printf("Error reading from offset %v during call to Read in file %s: %v", offset, ffn.staticSiaPath.String(), err)
		return nil, errToStatus(err)
	}

	// Format the data in a way fuse understands and return.
	return fuse.ReadResultData(dest[:n]), errToStatus(nil)
}

// Readdir will return a dirstream that can be used to look at all of the files
// in the directory.
func (fdn *fuseDirnode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	// Load the directory stream from the renter.
	//
	// TODO: Should we use the cached file list here?
	fileinfos, err := fdn.filesystem.renter.FileList(fdn.staticSiaPath, false, false)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Unable to get file list for fuse directory %v: %v", fdn.staticSiaPath, err)
		return nil, errToStatus(err)
	}
	dirinfos, err := fdn.filesystem.renter.DirList(fdn.staticSiaPath)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Error fetching dir list for fuse dir %v: %v", fdn.staticSiaPath, err)
		return nil, errToStatus(err)
	}

	// Convert the fileinfos to []fuse.DirEntry
	dirEntries := make([]fuse.DirEntry, 0, len(fileinfos)+len(dirinfos))
	for _, fi := range fileinfos {
		dirEntries = append(dirEntries, fuse.DirEntry{
			Mode: uint32(fi.Mode()) | fuse.S_IFREG,
			Name: fi.Name(),
		})
	}
	for _, di := range dirinfos {
		if di.SiaPath.String() == fdn.staticSiaPath.String() {
			continue
		}
		dirEntries = append(dirEntries, fuse.DirEntry{
			Mode: uint32(di.Mode()) | fuse.S_IFDIR,
			Name: di.Name(),
		})
	}

	// The fuse package has a helper to convert a []fuse.DirEntry to a
	// fuse.DirStream, we will use that here.
	return fs.NewListDirStream(dirEntries), errToStatus(nil)
}

// setStatfsOut is a method that will set the StatfsOut fields which are
// consistent across the fuse filesystem.
func (ffs *fuseFS) setStatfsOut(out *fuse.StatfsOut) error {
	// Get the allowance for the renter. This can be used to determine the total
	// amount of space available.
	settings, err := ffs.renter.Settings()
	if err != nil {
		return errors.AddContext(err, "unable to fetch renter settings")
	}
	totalStorage := settings.Allowance.ExpectedStorage

	// Get fileinfo for the root directory and use that to compute the amount of
	// storage in use and the number of files in the filesystem.
	dirs, err := ffs.renter.DirList(modules.RootSiaPath())
	if err != nil {
		return errors.AddContext(err, "unable to fetch root directory infos")
	}
	if len(dirs) < 1 {
		return errors.New("calling DirList on root directory returned no results")
	}
	rootDir := dirs[0]
	usedStorage := rootDir.AggregateSize
	numFiles := uint64(rootDir.AggregateNumFiles)

	// Compute the amount of storage that's available.
	//
	// TODO: Could be more accurate if this value is small based on the amount
	// of money remaining in the allowance and in the contracts.
	var availStorage uint64
	if totalStorage > usedStorage {
		availStorage = totalStorage - usedStorage
	}

	// TODO: This is just totally made up.
	blockSize := uint32(1 << 16)

	// Set all of the out fields.
	out.Blocks = totalStorage / uint64(blockSize)
	out.Bfree = availStorage / uint64(blockSize)
	out.Bavail = availStorage / uint64(blockSize)
	out.Files = numFiles
	out.Ffree = 1e6 // TODO: Not really sure what to do here. Description is "free file nodes in filesystem".
	out.Bsize = blockSize
	out.NameLen = math.MaxUint32 // There is no actual limit.
	out.Frsize = blockSize
	return nil
}

// Statfs will return the statfs fields for this directory.
func (fdn *fuseDirnode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	err := fdn.filesystem.setStatfsOut(out)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Error fetching statfs for fuse dir %v: %v", fdn.staticSiaPath, err)
		return errToStatus(err)
	}
	return errToStatus(nil)
}

// Statfs will return the statfs fields for this file.
func (ffn *fuseFilenode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	err := ffn.filesystem.setStatfsOut(out)
	if err != nil {
		ffn.filesystem.renter.log.Printf("Error fetching statfs for fuse file %v: %v", ffn.staticSiaPath, err)
		return errToStatus(err)
	}
	return errToStatus(nil)
}
