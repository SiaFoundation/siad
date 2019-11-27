package filesystem

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// DirNode is a node which references a SiaDir.
	DirNode struct {
		node

		directories map[string]*DirNode
		files       map[string]*FileNode

		// lazySiaDir is the SiaDir of the DirNode. 'lazy' means that it will
		// only be loaded on demand and destroyed as soon as the length of
		// 'threads' reaches 0.
		lazySiaDir **siadir.SiaDir
	}
)

// Close calls close on the DirNode and also removes the dNode from its parent
// if it's no longer being used and if it doesn't have any children which are
// currently in use. This happens iteratively for all parent as long as
// removing a child resulted in them not having any children left.
func (n *DirNode) Close() {
	// If a parent exists, we need to lock it while closing a child.
	parent := n.node.managedLockWithParent()

	// call private close method.
	n.closeDirNode()

	// Remove node from parent if there are no more children after this close.
	removeDir := len(n.threads) == 0 && len(n.directories) == 0 && len(n.files) == 0
	if parent != nil && removeDir {
		parent.removeDir(n)
	}

	// Unlock child and parent.
	n.mu.Unlock()
	if parent != nil {
		parent.mu.Unlock()
		// Check if the parent needs to be removed from its parent too.
		parent.managedTryRemoveFromParentsIteratively()
	}
}

// Delete is a wrapper for SiaDir.Delete.
func (n *DirNode) Delete() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return err
	}
	return sd.Delete()
}

// Dir will return a child dir of this directory if it exists.
func (n *DirNode) Dir(name string) (*DirNode, error) {
	n.mu.Lock()
	node, err := n.openDir(name)
	n.mu.Unlock()
	return node, errors.AddContext(err, "unable to open child directory")
}

// DirReader is a wrapper for SiaDir.DirReader.
func (n *DirNode) DirReader() (*siadir.DirReader, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return nil, err
	}
	return sd.DirReader()
}

// File will return a child file of this directory if it exists.
func (n *DirNode) File(name string) (*FileNode, error) {
	n.mu.Lock()
	node, err := n.openFile(name)
	n.mu.Unlock()
	return node, errors.AddContext(err, "unable to open child file")
}

// Metadata is a wrapper for SiaDir.Metadata.
func (n *DirNode) Metadata() (siadir.Metadata, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if os.IsNotExist(err) {
		return siadir.Metadata{}, ErrNotExist
	}
	if err != nil {
		return siadir.Metadata{}, err
	}
	return sd.Metadata(), nil
}

// Path is a wrapper for SiaDir.Path.
func (n *DirNode) Path() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return "", err
	}
	return sd.Path(), nil
}

// UpdateMetadata is a wrapper for SiaDir.UpdateMetadata.
func (n *DirNode) UpdateMetadata(md siadir.Metadata) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return err
	}
	return sd.UpdateMetadata(md)
}

// managedList returns the files and dirs within the SiaDir specified by siaPath.
// offlineMap, goodForRenewMap and contractMap don't need to be provided if
// 'cached' is set to 'true'.
func (n *DirNode) managedList(fsRoot string, siaPath modules.SiaPath, recursive, cached bool, offlineMap map[string]bool, goodForRenewMap map[string]bool, contractsMap map[string]modules.RenterContract) (fis []modules.FileInfo, dis []modules.DirectoryInfo, _ error) {
	// Prepare a pool of workers.
	numThreads := 40
	dirLoadChan := make(chan *DirNode, numThreads)
	fileLoadChan := make(chan *FileNode, numThreads)
	var fisMu, disMu sync.Mutex
	dirWorker := func() {
		for sd := range dirLoadChan {
			var di modules.DirectoryInfo
			var err error
			if sd.managedAbsPath() == fsRoot {
				di, err = sd.managedInfo(modules.RootSiaPath())
			} else {
				di, err = sd.managedInfo(nodeSiaPath(fsRoot, &sd.node))
			}
			sd.Close()
			if err == ErrNotExist {
				continue
			}
			if err != nil {
				n.staticLog.Debugf("Failed to get DirectoryInfo of '%v': %v", sd.managedAbsPath(), err)
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
				fi, err = sf.staticCachedInfo(nodeSiaPath(fsRoot, &sf.node))
			} else {
				fi, err = sf.managedFileInfo(nodeSiaPath(fsRoot, &sf.node), offlineMap, goodForRenewMap, contractsMap)
			}
			sf.Close()
			if err == ErrNotExist {
				continue
			}
			if err != nil {
				n.staticLog.Debugf("Failed to get FileInfo of '%v': %v", sf.managedAbsPath(), err)
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
	err := n._managedList(recursive, cached, fileLoadChan, dirLoadChan)
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

// managedList returns the files and dirs within the SiaDir.
func (n *DirNode) _managedList(recursive, cached bool, fileLoadChan chan *FileNode, dirLoadChan chan *DirNode) error {
	// Get DirectoryInfo of dir itself.
	dirLoadChan <- n.managedCopy()
	// Read dir.
	fis, err := ioutil.ReadDir(n.managedAbsPath())
	if err != nil {
		return err
	}
	for _, info := range fis {
		// Skip non-siafiles and non-dirs.
		if !info.IsDir() && filepath.Ext(info.Name()) != modules.SiaFileExtension {
			continue
		}
		// Handle dir.
		if info.IsDir() {
			// Open the dir.
			n.mu.Lock()
			dir, err := n.openDir(info.Name())
			n.mu.Unlock()
			if err != nil {
				return err
			}
			// Hand a copy to the worker. It will handle closing it.
			dirLoadChan <- dir.managedCopy()
			// Call managedList on the child if 'recursive' was specified.
			if recursive {
				err = dir._managedList(recursive, cached, fileLoadChan, dirLoadChan)
			}
			dir.Close()
			if err != nil {
				return err
			}
			continue
		}
		// Handle file.
		n.mu.Lock()
		file, err := n.openFile(strings.TrimSuffix(info.Name(), modules.SiaFileExtension))
		n.mu.Unlock()
		if err != nil {
			n.staticLog.Debugf("managedList failed to open file: %v", err)
			continue
		}
		fileLoadChan <- file.managedCopy()
		file.Close()
	}
	return nil
}

// close calls the common close method.
func (n *DirNode) closeDirNode() {
	n.node.closeNode()
	// If no more threads use the directory we delete the SiaDir to invalidate
	// the cache.
	if len(n.threads) == 0 {
		*n.lazySiaDir = nil
	}
}

// managedClose calls close while holding the node's lock.
func (n *DirNode) managedClose() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closeDirNode()
}

// managedNewSiaFileFromReader will read a siafile and its chunks from the given
// reader and add it to the directory. This will always load the file from the
// given reader.
func (n *DirNode) managedNewSiaFileFromExisting(sf *siafile.SiaFile, chunks siafile.Chunks) error {
	// Get the initial path of the siafile.
	path := sf.SiaFilePath()
	// Check if the path is taken.
	currentPath, exists := n.managedUniquePrefix(path, sf.UID())
	if exists {
		return nil // file already exists
	}
	// Either the file doesn't exist yet or we found a filename that doesn't
	// exist. Update the UID for safety and set the correct siafilepath.
	sf.UpdateUniqueID()
	sf.SetSiaFilePath(currentPath)
	// Save the file to disk.
	if err := sf.SaveWithChunks(chunks); err != nil {
		return err
	}
	// Add the node to the dir.
	fileName := strings.TrimSuffix(filepath.Base(currentPath), modules.SiaFileExtension)
	fn := &FileNode{
		node:    newNode(n, currentPath, fileName, 0, n.staticWal, n.staticLog),
		SiaFile: sf,
	}
	n.files[fileName] = fn
	return nil
}

// managedNewSiaFileFromLegacyData adds an existing SiaFile to the filesystem
// using the provided siafile.FileData object.
func (n *DirNode) managedNewSiaFileFromLegacyData(fileName string, fd siafile.FileData) (*FileNode, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Check if the path is taken.
	path := filepath.Join(n.absPath(), fileName+modules.SiaFileExtension)
	if _, err := os.Stat(filepath.Join(path)); !os.IsNotExist(err) {
		return nil, ErrExists
	}
	// Check if the file or folder exists already.
	key := strings.TrimSuffix(fileName, modules.SiaFileExtension)
	if exists := n.childExists(key); exists {
		return nil, ErrExists
	}
	// Otherwise create the file.
	sf, err := siafile.NewFromLegacyData(fd, path, n.staticWal)
	if err != nil {
		return nil, err
	}
	// Add it to the node.
	fn := &FileNode{
		node:    newNode(n, path, key, 0, n.staticWal, n.staticLog),
		SiaFile: sf,
	}
	n.files[key] = fn
	return fn.managedCopy(), nil
}

// managedUniquePrefix returns a new path for the siafile with the given path
// and uid by adding a suffix to the current path and incrementing it as long as
// the resulting path is already taken.
func (n *DirNode) managedUniquePrefix(path string, uid siafile.SiafileUID) (string, bool) {
	suffix := 0
	currentPath := path
	for {
		fileName := strings.TrimSuffix(filepath.Base(currentPath), modules.SiaFileExtension)
		oldFile, err := n.managedOpenFile(fileName)
		exists := err == nil
		if exists && oldFile.UID() == uid {
			oldFile.Close()
			return "", true // skip file since it already exists
		} else if exists {
			// Exists: update currentPath and fileName
			suffix++
			currentPath = strings.TrimSuffix(path, modules.SiaFileExtension)
			currentPath = fmt.Sprintf("%v_%v%v", currentPath, suffix, modules.SiaFileExtension)
			fileName = filepath.Base(currentPath)
			oldFile.Close()
			continue
		}
		break
	}
	return currentPath, false
}

// managedSiaDir calls siaDir while holding the node's lock.
func (n *DirNode) managedSiaDir() (*siadir.SiaDir, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.siaDir()
}

// siaDir is a wrapper for the lazySiaDir field.
func (n *DirNode) siaDir() (*siadir.SiaDir, error) {
	if *n.lazySiaDir != nil {
		return *n.lazySiaDir, nil
	}
	sd, err := siadir.LoadSiaDir(n.absPath(), modules.ProdDependencies, n.staticWal)
	if os.IsNotExist(err) {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	*n.lazySiaDir = sd
	return sd, nil
}

// managedTryRemoveFromParentsIteratively will remove the DirNode from its
// parent if it doesn't have any more files or directories as children. It will
// do so iteratively until it reaches an acestor with children.
func (n *DirNode) managedTryRemoveFromParentsIteratively() {
	n.mu.Lock()
	child := n
	parent := n.parent
	n.mu.Unlock()

	// Iteratively try to remove from parents as long as children got
	// removed.
	removeDir := true
	for removeDir && parent != nil {
		parent.mu.Lock()
		child.mu.Lock()
		removeDir = len(child.threads)+len(child.directories)+len(child.files) == 0
		if removeDir {
			parent.removeDir(child)
		}
		child.mu.Unlock()
		child, parent = parent, parent.parent
		child.mu.Unlock() // parent became child
	}
}

// managedDelete recursively deletes a dNode from disk.
func (n *DirNode) managedDelete() error {
	// If there is a parent lock it.
	parent := n.managedLockWithParent()
	if parent != nil {
		defer parent.mu.Unlock()
	}
	defer n.mu.Unlock()
	// Get contents of dir.
	dirsToLock := n.childDirs()
	var filesToDelete []*FileNode
	var lockedNodes []*node
	for _, file := range n.childFiles() {
		file.mu.Lock()
		file.Lock()
		lockedNodes = append(lockedNodes, &file.node)
		filesToDelete = append(filesToDelete, file)
	}
	// Unlock all locked nodes regardless of errors.
	defer func() {
		for _, file := range filesToDelete {
			file.Unlock()
		}
		for _, node := range lockedNodes {
			node.mu.Unlock()
		}
	}()
	// Lock dir and all open children. Remember in which order we acquired the
	// locks.
	for len(dirsToLock) > 0 {
		// Get next dir.
		d := dirsToLock[0]
		dirsToLock = dirsToLock[1:]
		// Lock the dir.
		d.mu.Lock()
		lockedNodes = append(lockedNodes, &d.node)
		// Remember the open files.
		for _, file := range d.files {
			file.mu.Lock()
			file.Lock()
			lockedNodes = append(lockedNodes, &file.node)
			filesToDelete = append(filesToDelete, file)
		}
		// Add the open dirs to dirsToLock.
		dirsToLock = append(dirsToLock, d.childDirs()...)
	}
	// Delete the dir.
	dir, err := n.siaDir()
	if err != nil {
		return err
	}
	err = dir.Delete()
	if err != nil {
		return err
	}
	// Remove the dir from the parent if it exists.
	if n.parent != nil {
		n.parent.removeDir(n)
	}
	// Delete all the open files in memory.
	for _, file := range filesToDelete {
		file.UnmanagedSetDeleted(true)
	}
	return nil
}

// managedDeleteFile deletes the file with the given name from the directory.
func (n *DirNode) managedDeleteFile(fileName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Check if the file is open in memory. If it is delete it.
	sf, exists := n.files[fileName]
	if exists {
		err := sf.managedDelete()
		if err != nil {
			return err
		}
		n.removeFile(sf)
		return nil
	}
	// Otherwise simply delete the file.
	err := os.Remove(filepath.Join(n.absPath(), fileName+modules.SiaFileExtension))
	if os.IsNotExist(err) {
		return ErrNotExist
	}
	return err
}

// managedInfo builds and returns the DirectoryInfo of a SiaDir.
func (n *DirNode) managedInfo(siaPath modules.SiaPath) (modules.DirectoryInfo, error) {
	// Grab the siadir metadata
	metadata, err := n.Metadata()
	if err != nil {
		return modules.DirectoryInfo{}, err
	}
	aggregateMaxHealth := math.Max(metadata.AggregateHealth, metadata.AggregateStuckHealth)
	maxHealth := math.Max(metadata.Health, metadata.StuckHealth)
	return modules.DirectoryInfo{
		// Aggregate Fields
		AggregateHealth:              metadata.AggregateHealth,
		AggregateLastHealthCheckTime: metadata.AggregateLastHealthCheckTime,
		AggregateMaxHealth:           aggregateMaxHealth,
		AggregateMaxHealthPercentage: modules.HealthPercentage(aggregateMaxHealth),
		AggregateMinRedundancy:       metadata.AggregateMinRedundancy,
		AggregateMostRecentModTime:   metadata.AggregateModTime,
		AggregateNumFiles:            metadata.AggregateNumFiles,
		AggregateNumStuckChunks:      metadata.AggregateNumStuckChunks,
		AggregateNumSubDirs:          metadata.AggregateNumSubDirs,
		AggregateSize:                metadata.AggregateSize,
		AggregateStuckHealth:         metadata.AggregateStuckHealth,

		// SiaDir Fields
		Health:              metadata.Health,
		LastHealthCheckTime: metadata.LastHealthCheckTime,
		MaxHealth:           maxHealth,
		MaxHealthPercentage: modules.HealthPercentage(maxHealth),
		MinRedundancy:       metadata.MinRedundancy,
		MostRecentModTime:   metadata.ModTime,
		NumFiles:            metadata.NumFiles,
		NumStuckChunks:      metadata.NumStuckChunks,
		NumSubDirs:          metadata.NumSubDirs,
		DirSize:             metadata.Size,
		StuckHealth:         metadata.StuckHealth,
		SiaPath:             siaPath,
		UID:                 n.staticUID,
	}, nil
}

// childDirs is a convenience method to return the directories field of a DNode
// as a slice.
func (n *DirNode) childDirs() []*DirNode {
	dirs := make([]*DirNode, 0, len(n.directories))
	for _, dir := range n.directories {
		dirs = append(dirs, dir)
	}
	return dirs
}

// managedExists returns 'true' if a file or folder with the given name already
// exists within the dir.
func (n *DirNode) childExists(name string) bool {
	// Check the ones in memory first.
	if _, exists := n.files[name]; exists {
		return true
	}
	if _, exists := n.directories[name]; exists {
		return true
	}
	// Check that no dir or file exists on disk.
	_, errFile := os.Stat(filepath.Join(n.absPath(), name))
	_, errDir := os.Stat(filepath.Join(n.absPath(), name+modules.SiaFileExtension))
	return !os.IsNotExist(errFile) || !os.IsNotExist(errDir)
}

// childFiles is a convenience method to return the files field of a DNode as a
// slice.
func (n *DirNode) childFiles() []*FileNode {
	files := make([]*FileNode, 0, len(n.files))
	for _, file := range n.files {
		files = append(files, file)
	}
	return files
}

// managedNewSiaFile creates a new SiaFile in the directory.
func (n *DirNode) managedNewSiaFile(fileName string, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Make sure we don't have a file or folder with that name already.
	if exists := n.childExists(fileName); exists {
		return ErrExists
	}
	_, err := siafile.New(filepath.Join(n.absPath(), fileName+modules.SiaFileExtension), source, n.staticWal, ec, mk, fileSize, fileMode, nil, disablePartialUpload)
	return errors.AddContext(err, "NewSiaFile: failed to create file")
}

// managedNewSiaDir creates the SiaDir with the given dirName as its child.
func (n *DirNode) managedNewSiaDir(dirName string, rootPath string, mode os.FileMode) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Check if a file already exists with that name.
	if _, exists := n.files[dirName]; exists {
		return ErrExists
	}
	// Check that no dir or file exists on disk.
	_, err := os.Stat(filepath.Join(n.absPath(), dirName+modules.SiaFileExtension))
	if !os.IsNotExist(err) {
		return ErrExists
	}
	_, err = siadir.New(filepath.Join(n.absPath(), dirName), rootPath, mode, n.staticWal)
	if os.IsExist(err) {
		return nil
	}
	return err
}

// managedOpenFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (n *DirNode) managedOpenFile(fileName string) (*FileNode, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.openFile(fileName)
}

// openFile opens a SiaFile and adds it and all of its parents to the filesystem
// tree.
func (n *DirNode) openFile(fileName string) (*FileNode, error) {
	fn, exists := n.files[fileName]
	if exists && fn.Deleted() {
		return nil, ErrNotExist // don't return a deleted file
	}
	if exists {
		return fn.managedCopy(), nil
	}
	// Load file from disk.
	filePath := filepath.Join(n.absPath(), fileName+modules.SiaFileExtension)
	sf, err := siafile.LoadSiaFile(filePath, n.staticWal)
	if err == siafile.ErrUnknownPath || os.IsNotExist(err) {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, errors.AddContext(err, "failed to load SiaFile from disk")
	}
	fn = &FileNode{
		node:    newNode(n, filePath, fileName, 0, n.staticWal, n.staticLog),
		SiaFile: sf,
	}
	n.files[fileName] = fn
	// Clone the node, give it a new UID and return it.
	return fn.managedCopy(), nil
}

// openDir opens the dir with the specified name within the current dir.
func (n *DirNode) openDir(dirName string) (*DirNode, error) {
	// Check if dir was already loaded. Then just copy it.
	dir, exists := n.directories[dirName]
	if exists {
		return dir.managedCopy(), nil
	}
	// Load the dir.
	dirPath := filepath.Join(n.absPath(), dirName)
	_, err := os.Stat(dirPath)
	if os.IsNotExist(err) {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	dir = &DirNode{
		node:        newNode(n, dirPath, dirName, 0, n.staticWal, n.staticLog),
		directories: make(map[string]*DirNode),
		files:       make(map[string]*FileNode),
		lazySiaDir:  new(*siadir.SiaDir),
	}
	n.directories[*dir.name] = dir
	return dir.managedCopy(), nil
}

// copyDirNode copies the node, adds a new thread to the threads map and returns
// the new instance.
func (n *DirNode) copyDirNode() *DirNode {
	// Copy the dNode and change the uid to a unique one.
	newNode := *n
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = struct{}{}
	return &newNode
}

// managedCopy copies the node, adds a new thread to the threads map and returns the
// new instance.
func (n *DirNode) managedCopy() *DirNode {
	// Copy the dNode and change the uid to a unique one.
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.copyDirNode()
}

// managedOpenDir opens a SiaDir.
func (n *DirNode) managedOpenDir(path string) (*DirNode, error) {
	// Get the name of the next sub directory.
	pathList := strings.Split(path, string(filepath.Separator))
	n.mu.Lock()
	subNode, err := n.openDir(pathList[0])
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// If path is empty we are done.
	pathList = pathList[1:]
	if len(pathList) == 0 {
		return subNode, nil
	}
	// Otherwise open the next dir.
	defer subNode.Close()
	return subNode.managedOpenDir(filepath.Join(pathList...))
}

// managedRemoveDir removes a dir from a dNode.
// NOTE: child.mu needs to be locked
func (n *DirNode) removeDir(child *DirNode) {
	// Remove the child node.
	currentChild, exists := n.directories[*child.name]
	if !exists || child.staticUID != currentChild.staticUID {
		return // nothing to do
	}
	delete(n.directories, *child.name)
}

// removeFile removes a child from a dNode.
// NOTE: child.mu needs to be locked
func (n *DirNode) removeFile(child *FileNode) {
	// Remove the child node.
	currentChild, exists := n.files[*child.name]
	if !exists || child.SiaFile != currentChild.SiaFile {
		return // Nothing to do
	}
	delete(n.files, *child.name)
}

// managedRename renames the fNode's underlying file.
func (n *DirNode) managedRename(newName string, oldParent, newParent *DirNode) error {
	// Iteratively remove oldParent after Rename is done.
	defer oldParent.managedTryRemoveFromParentsIteratively()

	// Lock the parents. If they are the same, only lock one.
	oldParent.mu.Lock()
	defer oldParent.mu.Unlock()
	if oldParent.staticUID != newParent.staticUID {
		newParent.mu.Lock()
		defer newParent.mu.Unlock()
	}
	// Check that newParent doesn't have a dir or file with that name already.
	if exists := newParent.childExists(newName); exists {
		return ErrExists
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	dirsToLock := n.childDirs()
	var dirsToRename []*DirNode
	var filesToRename []*FileNode
	var lockedNodes []*node
	for _, file := range n.childFiles() {
		file.mu.Lock()
		file.Lock()
		lockedNodes = append(lockedNodes, &file.node)
		filesToRename = append(filesToRename, file)
	}
	// Unlock all locked nodes regardless of errors.
	defer func() {
		for _, file := range filesToRename {
			file.Unlock()
		}
		for _, node := range lockedNodes {
			node.mu.Unlock()
		}
	}()
	// Lock dir and all open children. Remember in which order we acquired the
	// locks.
	for len(dirsToLock) > 0 {
		// Get next dir.
		d := dirsToLock[0]
		dirsToLock = dirsToLock[1:]
		// Lock the dir.
		d.mu.Lock()
		lockedNodes = append(lockedNodes, &d.node)
		dirsToRename = append(dirsToRename, d)
		// Lock the open files.
		for _, file := range d.files {
			file.mu.Lock()
			file.Lock()
			lockedNodes = append(lockedNodes, &file.node)
			filesToRename = append(filesToRename, file)
		}
		// Add the open dirs to dirsToLock.
		dirsToLock = append(dirsToLock, d.childDirs()...)
	}
	newBase := filepath.Join(newParent.absPath(), newName)
	// Rename the dir.
	dir, err := n.siaDir()
	if err != nil {
		return err
	}
	err = dir.Rename(newBase)
	if os.IsExist(err) {
		return ErrExists
	}
	if err != nil {
		return err
	}
	// Remove dir from old parent and add it to new parent.
	oldParent.removeDir(n)
	// Update parent and name.
	n.parent = newParent
	*n.name = newName
	*n.path = newBase
	// Add dir to new parent.
	n.parent.directories[*n.name] = n
	// Rename all locked nodes in memory.
	for _, node := range lockedNodes {
		*node.path = filepath.Join(*node.parent.path, *node.name)
	}
	// Rename all files in memory.
	for _, file := range filesToRename {
		*file.path = *file.path + modules.SiaFileExtension
		file.UnmanagedSetSiaFilePath(*file.path)
	}
	// Rename all dirs in memory.
	for _, dir := range dirsToRename {
		if *dir.lazySiaDir == nil {
			continue // dir isn't loaded
		}
		(*dir.lazySiaDir).SetPath(*dir.path)
	}
	return err
}
