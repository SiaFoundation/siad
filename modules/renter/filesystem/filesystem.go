package filesystem

import (
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

var (
	// ErrNotExist is returned when a file or folder can't be found on disk.
	ErrNotExist = errors.New("path does not exist")

	// ErrExists is returned when a file or folder already exists at a given
	// location.
	ErrExists = errors.New("a file or folder already exists at the specified path")
)

type (
	// FileSystem implements a thread-safe filesystem for Sia for loading
	// SiaFiles, SiaDirs and potentially other supported Sia types in the
	// future.
	FileSystem struct {
		DNode
	}

	// node is a struct that contains the commmon fields of every node.
	node struct {
		staticParent *DNode
		staticName   string
		staticUID    threadUID
		staticWal    *writeaheadlog.WAL
		threads      map[threadUID]threadInfo
		threadUID    threadUID
		mu           *sync.Mutex
	}

	// threadInfo contains useful information about the thread accessing the
	// SiaDirSetEntry
	threadInfo struct {
		callingFiles []string
		callingLines []int
		lockTime     time.Time
	}

	threadUID uint64
)

// newNode is a convenience function to initialize a node.
func newNode(parent *DNode, name string, uid threadUID, wal *writeaheadlog.WAL) node {
	return node{
		staticParent: parent,
		staticName:   name,
		staticUID:    newThreadUID(),
		staticWal:    wal,
		threads:      make(map[threadUID]threadInfo),
		threadUID:    uid,
		mu:           new(sync.Mutex),
	}
}

// newThreadType created a threadInfo entry for the threadMap
func newThreadType() threadInfo {
	tt := threadInfo{
		callingFiles: make([]string, threadDepth+1),
		callingLines: make([]int, threadDepth+1),
		lockTime:     time.Now(),
	}
	for i := 0; i <= threadDepth; i++ {
		_, tt.callingFiles[i], tt.callingLines[i], _ = runtime.Caller(2 + i)
	}
	return tt
}

// newThreadUID returns a random threadUID to be used as the threadUID in the
// threads map of the node.
func newThreadUID() threadUID {
	return threadUID(fastrand.Uint64n(math.MaxUint64))
}

// close removes a thread from the node's threads map. This should only be
// called from within other 'close' methods.
func (n *node) _close() {
	if _, exists := n.threads[n.threadUID]; !exists {
		build.Critical("threaduid doesn't exist in threads map: ", n.threadUID, len(n.threads))
	}
	delete(n.threads, n.threadUID)
}

// staticPath returns the absolute path of the node on disk.
func (n *node) staticPath() string {
	path := n.staticName
	for parent := n.staticParent; parent != nil; parent = parent.staticParent {
		path = filepath.Join(parent.staticName, path)
	}
	return path
}

// New creates a new FileSystem at the specified root path. The folder will be
// created if it doesn't exist already.
func New(root string, wal *writeaheadlog.WAL) (*FileSystem, error) {
	if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return nil, errors.AddContext(err, "failed to create root dir")
	}
	return &FileSystem{
		DNode: DNode{
			// The root doesn't require a parent, the name is its absolute path for convenience and it doesn't require a uid.
			node:        newNode(nil, root, 0, wal),
			directories: make(map[string]*DNode),
			files:       make(map[string]*FNode),
		},
	}, nil
}

// AddSiaFileFromReader adds an existing SiaFile to the set and stores it on
// disk. If the exact same file already exists, this is a no-op. If a file
// already exists with a different UID, the UID will be updated and a unique
// path will be chosen. If no file exists, the UID will be updated but the path
// remains the same.
func (fs *FileSystem) AddSiaFileFromReader(rs io.ReadSeeker, siaPath modules.SiaPath) error {
	// Create dir and open it.
	dirSiaPath, err := siaPath.Dir()
	if err != nil {
		return err
	}
	if err := fs.managedNewSiaDir(dirSiaPath); err != nil {
		return err
	}
	dir, err := fs.managedOpenDir(dirSiaPath.String())
	if err != nil {
		return err
	}
	defer dir.Close()
	// Add the file to the dir.
	return dir.managedNewSiaFileFromReader(siaPath.Name(), rs)
}

// DeleteDir deletes a dir from the filesystem. The dir will be marked as
// 'deleted' which should cause all remaining instances of the dir to be close
// shortly. Only when all instances of the dir are closed it will be removed
// from the tree. This means that as long as the deletion is in progress, no new
// file of the same path can be created and the existing file can't be opened
// until all instances of it are closed.
func (fs *FileSystem) DeleteDir(siaPath modules.SiaPath) error {
	return fs.managedDeleteDir(siaPath.String())
}

// DeleteFile deletes a file from the filesystem. The file will be marked as
// 'deleted' which should cause all remaining instances of the file to be closed
// shortly. Only when all instances of the file are closed it will be removed
// from the tree. This means that as long as the deletion is in progress, no new
// file of the same path can be created and the existing file can't be opened
// until all instances of it are closed.
func (fs *FileSystem) DeleteFile(siaPath modules.SiaPath) error {
	return fs.managedDeleteFile(siaPath.String())
}

// DirInfo returns the Directory Information of the siadir
func (fs *FileSystem) DirInfo(siaPath modules.SiaPath) (modules.DirectoryInfo, error) {
	dir, err := fs.managedOpenDir(siaPath.String())
	if err != nil {
		return modules.DirectoryInfo{}, nil
	}
	defer dir.Close()
	di, err := dir.managedInfo(siaPath)
	if err != nil {
		return modules.DirectoryInfo{}, err
	}
	di.SiaPath = siaPath
	return di, nil
}

// FileInfo returns the File Information of the siafile
func (fs *FileSystem) FileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	return fs.managedFileInfo(siaPath, offline, goodForRenew, contracts)
}

// List lists the files and directories within a SiaDir.
func (fs *FileSystem) List(siaPath modules.SiaPath, recursive, cached bool, offlineMap, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) ([]modules.FileInfo, []modules.DirectoryInfo, error) {
	return fs.managedList(siaPath, recursive, cached, offlineMap, goodForRenewMap, contractsMap)
}

// FileExists checks to see if a file with the provided siaPath already exists
// in the renter. This will also return 'false' if the file is inaccessible due
// to other reasons than not existing since the renter usually only cares
// whether the file is accessible.
func (fs *FileSystem) FileExists(siaPath modules.SiaPath) bool {
	path := fs.FilePath(siaPath)
	_, err := os.Stat(path)
	return err == nil
}

// FileList returns all of the files that the filesystem has in the folder
// specified by siaPath. If cached is true, this method will used cached values
// for health, redundancy etc.
func (fs *FileSystem) FileList(siaPath modules.SiaPath, recursive, cached bool, offlineMap map[string]bool, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) ([]modules.FileInfo, []modules.DirectoryInfo, error) {
	return fs.managedList(siaPath, recursive, cached, offlineMap, goodForRenewMap, contractsMap)
}

// FilePath converts a SiaPath into a file's system path.
func (fs *FileSystem) FilePath(siaPath modules.SiaPath) string {
	return siaPath.SiaFileSysPath(fs.staticName)
}

// NewSiaDir creates the folder for the specified siaPath.
func (fs *FileSystem) NewSiaDir(siaPath modules.SiaPath) error {
	return fs.managedNewSiaDir(siaPath)
}

// NewSiaFile creates a SiaFile at the specified siaPath.
func (fs *FileSystem) NewSiaFile(siaPath modules.SiaPath, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	// Create SiaDir for file.
	dirSiaPath, err := siaPath.Dir()
	if err != nil {
		return err
	}
	if err := fs.NewSiaDir(dirSiaPath); err != nil {
		return errors.AddContext(err, fmt.Sprintf("failed to create SiaDir %v for SiaFile %v", dirSiaPath.String(), siaPath.String()))
	}
	return fs.managedNewSiaFile(siaPath.String(), source, ec, mk, fileSize, fileMode, disablePartialUpload)
}

// ReadDir is a wrapper of ioutil.ReadDir which takes a SiaPath as an argument
// instead of a system path.
func (fs *FileSystem) ReadDir(siaPath modules.SiaPath) ([]os.FileInfo, error) {
	dirPath := siaPath.SiaDirSysPath(fs.staticName)
	return ioutil.ReadDir(dirPath)
}

// DirPath converts a SiaPath into a dir's system path.
func (fs *FileSystem) DirPath(siaPath modules.SiaPath) string {
	return siaPath.SiaDirSysPath(fs.staticName)
}

// Root returns the root system path of the FileSystem.
func (fs *FileSystem) Root() string {
	return fs.DirPath(modules.RootSiaPath())
}

// FileSiaPath returns the SiaPath of a file node.
func (fs *FileSystem) FileSiaPath(n *FNode) (sp modules.SiaPath) {
	return fs.staticSiaPath(&n.node)
}

// DirSiaPath returns the SiaPath of a dir node.
func (fs *FileSystem) DirSiaPath(n *DNode) (sp modules.SiaPath) {
	return fs.staticSiaPath(&n.node)
}

// UpdateDirMetadata updates the metadata of a SiaDir.
func (fs *FileSystem) UpdateDirMetadata(siaPath modules.SiaPath, metadata siadir.Metadata) error {
	panic("not implemented yet")
}

// staticSiaPath returns the SiaPath of a node.
func (fs *FileSystem) staticSiaPath(n *node) (sp modules.SiaPath) {
	if err := sp.FromSysPath(n.staticPath(), fs.staticName); err != nil {
		build.Critical("FileSystem.managedSiaPath: should never fail", err)
	}
	return sp
}

// Stat is a wrapper for os.Stat which takes a SiaPath as an argument instead of
// a system path.
func (fs *FileSystem) Stat(siaPath modules.SiaPath) (os.FileInfo, error) {
	path := siaPath.SiaDirSysPath(fs.staticName)
	return os.Stat(path)
}

// Walk is a wrapper for filepath.Walk which takes a SiaPath as an argument
// instead of a system path.
func (fs *FileSystem) Walk(siaPath modules.SiaPath, walkFn filepath.WalkFunc) error {
	dirPath := siaPath.SiaDirSysPath(fs.staticName)
	return filepath.Walk(dirPath, walkFn)
}

// WriteFile is a wrapper for ioutil.WriteFile which takes a SiaPath as an
// argument instead of a system path.
func (fs *FileSystem) WriteFile(siaPath modules.SiaPath, data []byte, perm os.FileMode) error {
	path := siaPath.SiaFileSysPath(fs.staticName)
	return ioutil.WriteFile(path, data, perm)
}

// NewSiaFileFromLegacyData creates a new SiaFile from data that was previously loaded
// from a legacy file.
func (fs *FileSystem) NewSiaFileFromLegacyData(fd siafile.FileData) (*FNode, error) {
	// Get file's SiaPath.
	sp, err := modules.SiaFilesSiaPath().Join(fd.Name)
	if err != nil {
		return nil, err
	}
	// Get siapath of dir.
	dirSiaPath, err := sp.Dir()
	if err != nil {
		return nil, err
	}
	// Create the dir if it doesn't exist.
	if err := fs.NewSiaDir(dirSiaPath); err != nil {
		return nil, err
	}
	// Open dir.
	dir, err := fs.managedOpenDir(dirSiaPath.String())
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	// Add the file to the dir.
	return dir.managedNewSiaFileFromLegacyData(sp.Name(), fd)
}

// OpenSiaDir opens a SiaDir and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) OpenSiaDir(siaPath modules.SiaPath) (*DNode, error) {
	fmt.Printf("OpenSiaDir Start: '%v'\n", siaPath.String())
	defer fmt.Printf("OpenSiaDir Stop: '%v'\n", siaPath.String())
	return fs.managedOpenSiaDir(siaPath)
}

// OpenSiaFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) OpenSiaFile(siaPath modules.SiaPath) (*FNode, error) {
	fmt.Printf("OpenSiaFile Start: '%v'\n", siaPath.String())
	defer fmt.Printf("OpenSiaFile Stop: '%v'\n", siaPath.String())
	return fs.managedOpenFile(siaPath.String())
}

// RenameFile renames the file with oldSiaPath to newSiaPath.
func (fs *FileSystem) RenameFile(oldSiaPath, newSiaPath modules.SiaPath) error {
	// Open SiaDir for file at old location.
	oldDirSiaPath, err := oldSiaPath.Dir()
	if err != nil {
		return err
	}
	oldDir, err := fs.managedOpenSiaDir(oldDirSiaPath)
	if err != nil {
		return err
	}
	defer oldDir.Close()
	// Open the file.
	sf, err := oldDir.managedOpenFile(oldSiaPath.Name())
	if err == ErrNotExist {
		return ErrNotExist
	}
	if err != nil {
		return errors.AddContext(err, "failed to open file for renaming")
	}
	defer sf.Close()

	// Create and Open SiaDir for file at new location.
	newDirSiaPath, err := newSiaPath.Dir()
	if err != nil {
		return err
	}
	if err := fs.NewSiaDir(newDirSiaPath); err != nil {
		return errors.AddContext(err, fmt.Sprintf("failed to create SiaDir %v for SiaFile %v", newDirSiaPath.String(), oldSiaPath.String()))
	}
	newDir, err := fs.managedOpenSiaDir(newDirSiaPath)
	if err != nil {
		return err
	}
	defer newDir.Close()
	// Rename the file.
	return sf.managedRename(newSiaPath.Name(), oldDir, newDir)
}

// RenameDir takes an existing directory and changes the path. The original
// directory must exist, and there must not be any directory that already has
// the replacement path.  All sia files within directory will also be renamed
func (fs *FileSystem) RenameDir(oldPath, newPath modules.SiaPath) error {
	panic("not implemented yet")
}

// managedDeleteFile opens the parent folder of the file to delete and calls
// managedDeleteFile on it.
func (fs *FileSystem) managedDeleteFile(path string) error {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(path)
	var dir *DNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(path))
		if err != nil {
			return errors.AddContext(err, "failed to open parent dir of file")
		}
		// Close the dir since we are not returning it. The open file keeps it
		// loaded in memory.
		defer dir.Close()
	}
	return dir.managedDeleteFile(fileName)
}

// managedDeleteDir opens the parent folder of the dir to delete and calls
// managedDelete on it.
func (fs *FileSystem) managedDeleteDir(path string) error {
	// Open the folder that contains the file.
	dirPath, _ := filepath.Split(path)
	var dir *DNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(path))
		if err != nil {
			return errors.AddContext(err, "failed to open parent dir of file")
		}
		// Close the dir since we are not returning it. The open file keeps it
		// loaded in memory.
		defer dir.Close()
	}
	return dir.managedDelete()
}

// managedFileInfo returns the FileInfo of the siafile.
func (fs *FileSystem) managedFileInfo(siaPath modules.SiaPath, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	// Open the file.
	file, err := fs.managedOpenFile(siaPath.String())
	if err != nil {
		return modules.FileInfo{}, err
	}
	defer file.Close()
	return file.managedFileInfo(siaPath, offline, goodForRenew, contracts)
}

// managedList returns the files and dirs within the SiaDir specified by siaPath.
func (fs *FileSystem) managedList(siaPath modules.SiaPath, recursive, cached bool, offlineMap map[string]bool, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) (fis []modules.FileInfo, dis []modules.DirectoryInfo, _ error) {
	// Open the folder.
	dir, err := fs.managedOpenDir(siaPath.String())
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to open folder specified by FileList")
	}
	defer dir.Close()
	// Prepare a pool of workers.
	numThreads := 20
	dirLoadChan := make(chan *DNode, numThreads)
	fileLoadChan := make(chan *FNode, numThreads)
	var fisMu, disMu sync.Mutex
	dirWorker := func() {
		for sd := range dirLoadChan {
			var di modules.DirectoryInfo
			var err error
			if sd.node.staticPath() == fs.staticPath() {
				di, err = sd.managedInfo(modules.RootSiaPath())
			} else {
				di, err = sd.managedInfo(fs.staticSiaPath(&sd.node))
			}
			sd.Close()
			if err == ErrNotExist {
				continue
			}
			if err != nil {
				// TODO: Add logging
				continue
			}
			disMu.Lock()
			dis = append(dis, di)
			disMu.Unlock()
		}
	}
	fileWorker := func() {
		for sf := range fileLoadChan {
			var fi modules.FileInfo
			var err error
			if cached {
				fi, err = sf.staticCachedInfo(fs.staticSiaPath(&sf.node), offlineMap, goodForRenewMap, contractsMap)
			} else {
				fi, err = sf.managedFileInfo(fs.staticSiaPath(&sf.node), offlineMap, goodForRenewMap, contractsMap)
			}
			sf.Close()
			if err == ErrNotExist {
				continue
			}
			if err != nil {
				// TODO: Add logging
				continue
			}
			fisMu.Lock()
			fis = append(fis, fi)
			fisMu.Unlock()
		}
	}
	// Spin the workers up.
	var wg sync.WaitGroup
	for i := 0; i < numThreads; i++ {
		wg.Add(1)
		go func() {
			dirWorker()
			wg.Done()
		}()
		wg.Add(1)
		go func() {
			fileWorker()
			wg.Done()
		}()
	}
	err = dir.managedList(recursive, cached, fileLoadChan, dirLoadChan)
	// Signal the workers that we are done adding work and wait for them to
	// finish any pending work.
	close(dirLoadChan)
	close(fileLoadChan)
	wg.Wait()
	return fis, dis, err
}

// managedNewSiaDir creates the folder at the specified siaPath.
func (fs *FileSystem) managedNewSiaDir(siaPath modules.SiaPath) error {
	dirPath := siaPath.SiaDirSysPath(fs.staticName)
	_, err := siadir.New(dirPath, fs.staticName, fs.staticWal)
	if os.IsExist(err) {
		return nil // nothing to do
	}
	return err
}

// managedOpenFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) managedOpenFile(path string) (*FNode, error) {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(path)
	var dir *DNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(path))
		if err != nil {
			return nil, errors.AddContext(err, "failed to open parent dir of file")
		}
		// Close the dir since we are not returning it. The open file keeps it
		// loaded in memory.
		defer dir.Close()
	}
	return dir.managedOpenFile(fileName)
}

// managedNewSiaFile opens the parent folder of the new SiaFile and calls
// managedNewSiaFile on it.
func (fs *FileSystem) managedNewSiaFile(path string, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(path)
	var dir *DNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(path))
		if err != nil {
			return errors.AddContext(err, "failed to open parent dir of new file")
		}
		defer dir.Close()
	}
	return dir.managedNewSiaFile(fileName, source, ec, mk, fileSize, fileMode, disablePartialUpload)
}

// managedOpenSiaDir opens a SiaDir and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) managedOpenSiaDir(siaPath modules.SiaPath) (*DNode, error) {
	dir, err := fs.DNode.managedOpenDir(siaPath.String())
	if err != nil {
		return nil, err
	}
	return dir, nil
}
