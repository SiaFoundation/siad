package filesystem

import (
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
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
		DirNode
	}

	// node is a struct that contains the commmon fields of every node.
	node struct {
		// fields that all copies of a node share.
		path      *string
		parent    *DirNode
		name      *string
		staticWal *writeaheadlog.WAL
		threads   map[threadUID]struct{} // tracks all the threadUIDs of evey copy of the node
		staticLog *persist.Logger
		staticUID threadUID
		mu        *sync.Mutex

		// fields that differ between copies of the same node.
		threadUID threadUID // unique ID of a copy of a node
	}
	threadUID uint64
)

// newNode is a convenience function to initialize a node.
func newNode(parent *DirNode, path, name string, uid threadUID, wal *writeaheadlog.WAL, log *persist.Logger) node {
	return node{
		path:      &path,
		parent:    parent,
		name:      &name,
		staticLog: log,
		staticUID: newThreadUID(),
		staticWal: wal,
		threads:   make(map[threadUID]struct{}),
		threadUID: uid,
		mu:        new(sync.Mutex),
	}
}

// managedLockWithParent is a helper method which correctly acquires the lock of
// a node and it's parent. If no parent it available it will return 'nil'. In
// either case the node and potential parent will be locked after the call.
func (n *node) managedLockWithParent() *DirNode {
	var parent *DirNode
	for {
		// If a parent exists, we need to lock it while closing a child.
		n.mu.Lock()
		parent = n.parent
		n.mu.Unlock()
		if parent != nil {
			parent.mu.Lock()
		}
		n.mu.Lock()
		if n.parent != parent {
			n.mu.Unlock()
			parent.mu.Unlock()
			continue // try again
		}
		break
	}
	return parent
}

// newThreadUID returns a random threadUID to be used as the threadUID in the
// threads map of the node.
func newThreadUID() threadUID {
	return threadUID(fastrand.Uint64n(math.MaxUint64))
}

// closeNode removes a thread from the node's threads map. This should only be
// called from within other 'close' methods.
func (n *node) closeNode() {
	if _, exists := n.threads[n.threadUID]; !exists {
		build.Critical("threaduid doesn't exist in threads map: ", n.threadUID, len(n.threads))
	}
	delete(n.threads, n.threadUID)
}

// absPath returns the absolute path of the node.
func (n *node) absPath() string {
	return *n.path
}

// managedAbsPath returns the absolute path of the node.
func (n *node) managedAbsPath() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.absPath()
}

// New creates a new FileSystem at the specified root path. The folder will be
// created if it doesn't exist already.
func New(root string, log *persist.Logger, wal *writeaheadlog.WAL) (*FileSystem, error) {
	fs := &FileSystem{
		DirNode: DirNode{
			// The root doesn't require a parent, a name or uid.
			node:        newNode(nil, root, "", 0, wal, log),
			directories: make(map[string]*DirNode),
			files:       make(map[string]*FileNode),
			lazySiaDir:  new(*siadir.SiaDir),
		},
	}
	// Prepare root folder.
	err := fs.NewSiaDir(modules.RootSiaPath())
	if err != nil && !errors.Contains(err, ErrExists) {
		return nil, err
	}
	return fs, nil
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

// CachedFileInfo returns the cached File Information of the siafile
func (fs *FileSystem) CachedFileInfo(siaPath modules.SiaPath) (modules.FileInfo, error) {
	return fs.managedFileInfo(siaPath, true, nil, nil, nil)
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
	return fs.managedFileInfo(siaPath, false, offline, goodForRenew, contracts)
}

// List lists the files and directories within a SiaDir.
func (fs *FileSystem) List(siaPath modules.SiaPath, recursive, cached bool, offlineMap, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) ([]modules.FileInfo, []modules.DirectoryInfo, error) {
	return fs.managedList(siaPath, recursive, cached, offlineMap, goodForRenewMap, contractsMap)
}

// FileExists checks to see if a file with the provided siaPath already exists
// in the renter.
func (fs *FileSystem) FileExists(siaPath modules.SiaPath) (bool, error) {
	path := fs.FilePath(siaPath)
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

// FilePath converts a SiaPath into a file's system path.
func (fs *FileSystem) FilePath(siaPath modules.SiaPath) string {
	return siaPath.SiaFileSysPath(fs.managedAbsPath())
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
	dirPath := siaPath.SiaDirSysPath(fs.managedAbsPath())
	return ioutil.ReadDir(dirPath)
}

// DirPath converts a SiaPath into a dir's system path.
func (fs *FileSystem) DirPath(siaPath modules.SiaPath) string {
	return siaPath.SiaDirSysPath(fs.managedAbsPath())
}

// Root returns the root system path of the FileSystem.
func (fs *FileSystem) Root() string {
	return fs.DirPath(modules.RootSiaPath())
}

// FileSiaPath returns the SiaPath of a file node.
func (fs *FileSystem) FileSiaPath(n *FileNode) (sp modules.SiaPath) {
	return fs.managedSiaPath(&n.node)
}

// DirSiaPath returns the SiaPath of a dir node.
func (fs *FileSystem) DirSiaPath(n *DirNode) (sp modules.SiaPath) {
	return fs.managedSiaPath(&n.node)
}

// UpdateDirMetadata updates the metadata of a SiaDir.
func (fs *FileSystem) UpdateDirMetadata(siaPath modules.SiaPath, metadata siadir.Metadata) error {
	dir, err := fs.OpenSiaDir(siaPath)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.UpdateMetadata(metadata)
}

// managedSiaPath returns the SiaPath of a node.
func (fs *FileSystem) managedSiaPath(n *node) (sp modules.SiaPath) {
	if err := sp.FromSysPath(n.managedAbsPath(), fs.managedAbsPath()); err != nil {
		build.Critical("FileSystem.managedSiaPath: should never fail", err)
	}
	return sp
}

// Stat is a wrapper for os.Stat which takes a SiaPath as an argument instead of
// a system path.
func (fs *FileSystem) Stat(siaPath modules.SiaPath) (os.FileInfo, error) {
	path := siaPath.SiaDirSysPath(fs.managedAbsPath())
	return os.Stat(path)
}

// Walk is a wrapper for filepath.Walk which takes a SiaPath as an argument
// instead of a system path.
func (fs *FileSystem) Walk(siaPath modules.SiaPath, walkFn filepath.WalkFunc) error {
	dirPath := siaPath.SiaDirSysPath(fs.managedAbsPath())
	return filepath.Walk(dirPath, walkFn)
}

// WriteFile is a wrapper for ioutil.WriteFile which takes a SiaPath as an
// argument instead of a system path.
func (fs *FileSystem) WriteFile(siaPath modules.SiaPath, data []byte, perm os.FileMode) error {
	path := siaPath.SiaFileSysPath(fs.managedAbsPath())
	return ioutil.WriteFile(path, data, perm)
}

// NewSiaFileFromLegacyData creates a new SiaFile from data that was previously loaded
// from a legacy file.
func (fs *FileSystem) NewSiaFileFromLegacyData(fd siafile.FileData) (*FileNode, error) {
	// Get file's SiaPath.
	sp, err := modules.UserSiaPath().Join(fd.Name)
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
func (fs *FileSystem) OpenSiaDir(siaPath modules.SiaPath) (*DirNode, error) {
	return fs.managedOpenSiaDir(siaPath)
}

// OpenSiaFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) OpenSiaFile(siaPath modules.SiaPath) (*FileNode, error) {
	sf, err := fs.managedOpenFile(siaPath.String())
	if err != nil {
		return nil, err
	}
	return sf, nil
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
func (fs *FileSystem) RenameDir(oldSiaPath, newSiaPath modules.SiaPath) error {
	// Open SiaDir for parent dir at old location.
	oldDirSiaPath, err := oldSiaPath.Dir()
	if err != nil {
		return err
	}
	oldDir, err := fs.managedOpenSiaDir(oldDirSiaPath)
	if err != nil {
		return err
	}
	defer func() {
		oldDir.Close()
	}()
	// Open the dir to rename.
	sd, err := oldDir.managedOpenDir(oldSiaPath.Name())
	if err == ErrNotExist {
		return ErrNotExist
	}
	if err != nil {
		return errors.AddContext(err, "failed to open file for renaming")
	}
	defer func() {
		sd.Close()
	}()

	// Create and Open parent SiaDir for dir at new location.
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
	defer func() {
		newDir.Close()
	}()
	// Rename the dir.
	err = sd.managedRename(newSiaPath.Name(), oldDir, newDir)
	return err
}

// managedDeleteFile opens the parent folder of the file to delete and calls
// managedDeleteFile on it.
func (fs *FileSystem) managedDeleteFile(relPath string) error {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(relPath)
	var dir *DirNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DirNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(relPath))
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
	// Open the dir.
	dir, err := fs.managedOpenDir(path)
	if err != nil {
		return errors.AddContext(err, "failed to open parent dir of file")
	}
	// Close the dir since we are not returning it. The open file keeps it
	// loaded in memory.
	defer dir.Close()
	return dir.managedDelete()
}

// managedFileInfo returns the FileInfo of the siafile.
func (fs *FileSystem) managedFileInfo(siaPath modules.SiaPath, cached bool, offline map[string]bool, goodForRenew map[string]bool, contracts map[string]modules.RenterContract) (modules.FileInfo, error) {
	// Open the file.
	file, err := fs.managedOpenFile(siaPath.String())
	if err != nil {
		return modules.FileInfo{}, err
	}
	defer file.Close()
	if cached {
		return file.staticCachedInfo(siaPath)
	}
	return file.managedFileInfo(siaPath, offline, goodForRenew, contracts)
}

// managedList returns the files and dirs within the SiaDir specified by siaPath.
// offlineMap, goodForRenewMap and contractMap don't need to be provided if
// 'recursive' is set to 'true'.
func (fs *FileSystem) managedList(siaPath modules.SiaPath, recursive, cached bool, offlineMap map[string]bool, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) (fis []modules.FileInfo, dis []modules.DirectoryInfo, _ error) {
	// Open the folder.
	dir, err := fs.managedOpenDir(siaPath.String())
	if err != nil {
		return nil, nil, errors.AddContext(err, "failed to open folder specified by FileList")
	}
	defer dir.Close()
	// Prepare a pool of workers.
	numThreads := 40
	dirLoadChan := make(chan *DirNode, numThreads)
	fileLoadChan := make(chan *FileNode, numThreads)
	var fisMu, disMu sync.Mutex
	dirWorker := func() {
		for sd := range dirLoadChan {
			var di modules.DirectoryInfo
			var err error
			if sd.managedAbsPath() == fs.managedAbsPath() {
				di, err = sd.managedInfo(modules.RootSiaPath())
			} else {
				di, err = sd.managedInfo(fs.managedSiaPath(&sd.node))
			}
			sd.Close()
			if err == ErrNotExist {
				continue
			}
			if err != nil {
				fs.staticLog.Debugf("Failed to get DirectoryInfo of '%v': %v", sd.managedAbsPath(), err)
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
				fi, err = sf.staticCachedInfo(fs.managedSiaPath(&sf.node))
			} else {
				fi, err = sf.managedFileInfo(fs.managedSiaPath(&sf.node), offlineMap, goodForRenewMap, contractsMap)
			}
			sf.Close()
			if err == ErrNotExist {
				continue
			}
			if err != nil {
				fs.staticLog.Debugf("Failed to get FileInfo of '%v': %v", sf.managedAbsPath(), err)
				continue
			}
			fisMu.Lock()
			fis = append(fis, fi)
			fisMu.Unlock()
		}
	}
	// Spin the workers up.
	var wg sync.WaitGroup
	for i := 0; i < numThreads/2; i++ {
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
	sort.Slice(dis, func(i, j int) bool {
		return dis[i].SiaPath.String() < dis[j].SiaPath.String()
	})
	sort.Slice(fis, func(i, j int) bool {
		return fis[i].SiaPath.String() < fis[j].SiaPath.String()
	})
	return fis, dis, err
}

// managedNewSiaDir creates the folder at the specified siaPath.
func (fs *FileSystem) managedNewSiaDir(siaPath modules.SiaPath) error {
	dirPath := siaPath.SiaDirSysPath(fs.managedAbsPath())
	_, err := siadir.New(dirPath, fs.managedAbsPath(), fs.staticWal)
	if os.IsExist(err) {
		return nil // nothing to do
	}
	return err
}

// managedOpenFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) managedOpenFile(relPath string) (*FileNode, error) {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(relPath)
	var dir *DirNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DirNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(relPath))
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
func (fs *FileSystem) managedNewSiaFile(relPath string, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(relPath)
	var dir *DirNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.DirNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(relPath))
		if err != nil {
			return errors.AddContext(err, "failed to open parent dir of new file")
		}
		defer dir.Close()
	}
	return dir.managedNewSiaFile(fileName, source, ec, mk, fileSize, fileMode, disablePartialUpload)
}

// managedOpenSiaDir opens a SiaDir and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) managedOpenSiaDir(siaPath modules.SiaPath) (*DirNode, error) {
	if siaPath.IsRoot() {
		return fs.DirNode.managedCopy(), nil
	}
	dir, err := fs.DirNode.managedOpenDir(siaPath.String())
	if err != nil {
		return nil, err
	}
	return dir, nil
}
