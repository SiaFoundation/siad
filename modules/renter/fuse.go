package renter

// TODO: All fuse systems seem to suffer from sudden memory death syndrome.
// Sudden memory death syndrome is an issue where everything runs fine for a
// short period of time, and then suddenly memory spikes (5 GB/s until death)
// and the process is killed.
//
// The memory spike so SO FAST that I've thus far been completely unable to get
// a trace or memory profile that captures the spike. I have no idea what causes
// the spike, however excessively browsing around the fuse directory in a file
// browser seems to reliably trigger the issue. Perhaps something related to
// Lookup?
//
// My best guess for the cause of SMDS is somewhere there's an infinite loop
// that also happens to allocate memory. I've been unable to replicate this
// without using fuse, however that may just be because fuse is the only system
// we have that allows us to really slam the sia filesystem.

import (
	"context"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"gitlab.com/NebulousLabs/Sia/modules"
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

	dirInfo modules.DirectoryInfo
	siapath modules.SiaPath

	filesystem *fuseFS
}

// Ensure the dir nodes satisfy the required interfaces.
//
// NodeGetattrer provides details about the folder. This one may not be
// strictly necessary, I'm not sure what exact value it adds.
//
// NodeLookuper is necessary to have files added to the filesystem tree.
//
// NodeReaddirer is necessary to list the files in a directory.
var _ = (fs.NodeGetattrer)((*fuseDirnode)(nil))
var _ = (fs.NodeLookuper)((*fuseDirnode)(nil))
var _ = (fs.NodeReaddirer)((*fuseDirnode)(nil))

// fuseFilenode is a fuse node for the fs pacakge that covers a siafile.
//
// Data is fetched using a download streamer. This download streamer needs to be
// closed when the filehandle is released.
type fuseFilenode struct {
	fs.Inode

	staticFileInfo modules.FileInfo
	staticSiapath  modules.SiaPath

	stream modules.Streamer

	filesystem *fuseFS
	mu         sync.Mutex
}

// Ensure the file nodes satisfy the required interfaces.
//
// NodeFlusher is necessary for cleaning up resources such as the download
// streamer.
//
// NodeGetattrer is necessary for providing the filesize to file browsers.
//
// NodeOpener is necessary for opening files to be read.
//
// NodeReader is necessary for reading files.
var _ = (fs.NodeFlusher)((*fuseFilenode)(nil))
var _ = (fs.NodeGetattrer)((*fuseFilenode)(nil))
var _ = (fs.NodeOpener)((*fuseFilenode)(nil))
var _ = (fs.NodeReader)((*fuseFilenode)(nil))

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
	} else if os.IsNotExist(err) {
		return syscall.ENOENT
	}
	return syscall.EIO
}

// Flush is called when a file is being closed.
func (ffn *fuseFilenode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	ffn.mu.Lock()
	defer ffn.mu.Unlock()

	// If a stream was opened for the file, the stream must now be closed.
	if ffn.stream != nil {
		err := ffn.stream.Close()
		if err != nil {
			ffn.filesystem.renter.log.Printf("Unable to close stream for file %v: %v", ffn.staticSiapath, err)
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
	lookupPath, err := fdn.siapath.Join(name)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Unable to determine filepath for %v from directory %v: %v", name, fdn.siapath, err)
		return nil, errToStatus(err)
	}
	fileInfo, fileErr := fdn.filesystem.renter.FileCached(lookupPath)
	if fileErr == nil {
		// Convert the file to an inode.
		filenode := &fuseFilenode{
			staticFileInfo: fileInfo,
			staticSiapath:  lookupPath,
			filesystem:     fdn.filesystem,
		}
		attrs := fs.StableAttr{
			Mode: uint32(fileInfo.Mode()) | syscall.S_IFREG,
		}

		// Set the crticial entry out values.
		//
		// TODO: Set more of these, there are like 20 of them.
		out.Size = fileInfo.Filesize
		out.Mode = attrs.Mode

		inode := fdn.NewInode(ctx, filenode, attrs)
		return inode, errToStatus(nil)
	}

	// Unable to look up a file, might be a dir instead.
	dirInfo, dirErr := fdn.filesystem.renter.staticDirSet.DirInfo(fdn.siapath)
	if dirErr != nil {
		fdn.filesystem.renter.log.Printf("Unable to perform lookup on %v in dir %v; file err %v :: dir err %v", name, fdn.siapath, fileErr, dirErr)
		return nil, syscall.ENOENT
	}

	// We found the directory we want, convert to an inode.
	dirnode := &fuseDirnode{
		dirInfo: dirInfo,
		siapath: lookupPath,

		filesystem: fdn.filesystem,
	}
	dirMode := dirInfo.Mode()
	attrs := fs.StableAttr{
		Mode: uint32(dirMode),
	}
	out.Mode = uint32(dirMode)
	inode := fdn.NewInode(ctx, dirnode, attrs)
	return inode, errToStatus(nil)
}

// Getattr returns the attributes of a fuse file.
func (fdn *fuseDirnode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = uint32(fdn.dirInfo.Mode())
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
	return errToStatus(nil)
}

// Open will open a streamer for the file.
func (ffn *fuseFilenode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	ffn.mu.Lock()
	defer ffn.mu.Unlock()

	_, stream, err := ffn.filesystem.renter.Streamer(ffn.staticSiapath)
	if err != nil {
		ffn.filesystem.renter.log.Printf("Unable to get stream for file %v: %v", ffn.staticSiapath, err)
		return nil, 0, errToStatus(err)
	}
	ffn.stream = stream

	return ffn, 0, errToStatus(nil)
}

// Read will read data from the file and place it in dest.
func (ffn *fuseFilenode) Read(ctx context.Context, f fs.FileHandle, dest []byte, offset int64) (fuse.ReadResult, syscall.Errno) {
	ffn.mu.Lock()
	defer ffn.mu.Unlock()

	_, err := ffn.stream.Seek(offset, io.SeekStart)
	if err != nil {
		ffn.filesystem.renter.log.Printf("Error seeking to offset %v during call to Read in file %s: %v", offset, ffn.staticSiapath.String(), err)
		return nil, errToStatus(err)
	}

	// Ignore both EOF and ErrUnexpectedEOF when doing the ReadFull. If we
	// return ErrUnexpectedEOF, the program will try to read again but with a
	// smaller read size and be confused about how large the file actually is,
	// often dropping parts of the tail of the file.
	n, err := io.ReadFull(ffn.stream, dest)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		ffn.filesystem.renter.log.Printf("Error reading from offset %v during call to Read in file %s: %v", offset, ffn.staticSiapath.String(), err)
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
	fileinfos, err := fdn.filesystem.renter.FileList(fdn.siapath, false, false)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Unable to get file list for fuse directory %v: %v", fdn.siapath, err)
		return nil, errToStatus(err)
	}
	dirinfos, err := fdn.filesystem.renter.DirList(fdn.siapath)
	if err != nil {
		fdn.filesystem.renter.log.Printf("Error fetching dir list for fuse dir %v: %v", fdn.siapath, err)
		return nil, errToStatus(err)
	}

	// Convert the fileinfos to []fuse.DirEntry
	dirEntries := make([]fuse.DirEntry, 0, len(fileinfos)+len(dirinfos))
	for _, fi := range fileinfos {
		dirEntries = append(dirEntries, fuse.DirEntry{
			Mode: uint32(fi.Mode()) | syscall.S_IFREG,
			Name: fi.Name(),
		})
	}
	for _, di := range dirinfos {
		// TODO: Is this expected? Why does calling DirList() on a dir return
		// the dir itself as the first entry? This is clearly intentional in the
		// Sia code.
		if di.SiaPath.String() == fdn.siapath.String() {
			continue
		}
		dirEntries = append(dirEntries, fuse.DirEntry{
			Mode: uint32(di.Mode()),
			Name: di.Name(),
		})
	}

	// The fuse package has a helper to convert a []fuse.DirEntry to a
	// fuse.DirStream, we will use that here.
	return fs.NewListDirStream(dirEntries), errToStatus(nil)
}
