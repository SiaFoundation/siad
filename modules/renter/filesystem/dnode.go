package filesystem

import (
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// DNode is a node which references a SiaDir.
	DNode struct {
		node

		directories map[string]*DNode
		files       map[string]*FNode

		lazySiaDir **siadir.SiaDir
	}
)

// Delete is a wrapper for SiaDir.Delete.
func (n *DNode) Delete() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return err
	}
	return sd.Delete()
}

// DirReader is a wrapper for SiaDir.DirReader.
func (n *DNode) DirReader() (*siadir.DirReader, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return nil, err
	}
	return sd.DirReader()
}

// HealthPercentage returns the health in a more human understandable format out
// of 100%
//
// The percentage is out of 1.25, this is to account for the RepairThreshold of
// 0.25 and assumes that the worst health is 1.5. Since we do not repair until
// the health is worse than the RepairThreshold, a health of 0 - 0.25 is full
// health. Likewise, a health that is greater than 1.25 is essentially 0 health.
func HealthPercentage(health float64) float64 {
	healthPercent := 100 * (1.25 - health)
	if healthPercent > 100 {
		healthPercent = 100
	}
	if healthPercent < 0 {
		healthPercent = 0
	}
	return healthPercent
}

// Metadata is a wrapper for SiaDir.Metadata.
func (n *DNode) Metadata() (siadir.Metadata, error) {
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
func (n *DNode) Path() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return "", err
	}
	return sd.Path(), nil
}

// UpdateMetadata is a wrapper for SiaDir.Path.
func (n *DNode) UpdateMetadata(md siadir.Metadata) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	sd, err := n.siaDir()
	if err != nil {
		return err
	}
	return sd.UpdateMetadata(md)
}

// managedList returns the files and dirs within the SiaDir.
func (n *DNode) managedList(recursive, cached bool, fileLoadChan chan *FNode, dirLoadChan chan *DNode) error {
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
			n.mu.Lock()
			// Open the dir.
			dir, err := n.openDir(info.Name())
			if err != nil {
				n.mu.Unlock()
				return err
			}
			n.mu.Unlock()
			// Hand a copy to the worker. It will handle closing it.
			dirLoadChan <- dir.managedCopy()
			// Call managedList on the child if 'recursive' was specified.
			if recursive {
				err = dir.managedList(recursive, cached, fileLoadChan, dirLoadChan)
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
			// TODO: Add logging
			continue
		}
		fileLoadChan <- file.managedCopy()
		file.Close()
	}
	return nil
}

// close calls the common close method.
func (n *DNode) close() {
	n.node._close()
}

// managedClose calls close while holding the node's lock.
func (n *DNode) managedClose() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.close()
}

// managedNewSiaFileFromReader will read a siafile and its chunks from the given
// reader and add it to the directory. This will always load the file from the
// given reader.
func (n *DNode) managedNewSiaFileFromReader(fileName string, rs io.ReadSeeker) error {
	// Load the file.
	path := filepath.Join(n.managedAbsPath(), fileName+modules.SiaFileExtension)
	sf, chunks, err := siafile.LoadSiaFileFromReaderWithChunks(rs, path, n.staticWal)
	if err != nil {
		return err
	}
	// Check if the path is taken.
	err = ErrExists
	suffix := 0
	currentPath := path
	for {
		fileName := strings.TrimSuffix(filepath.Base(currentPath), modules.SiaFileExtension)
		oldFile, err := n.managedOpenFile(fileName)
		exists := err == nil
		if exists && oldFile.UID() == sf.UID() {
			oldFile.Close()
			return nil // skip file since it already exists
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
	// Either the file doesn't exist yet or we found a filename that doesn't
	// exist. Update the UID for safety and set the correct siafilepath.
	sf.UpdateUniqueID()
	sf.SetSiaFilePath(currentPath)
	// Save the file to disk.
	if err := sf.SaveWithChunks(chunks); err != nil {
		return err
	}
	// Add the node to the dir.
	fn := &FNode{
		node:    newNode(n, currentPath, fileName, 0, n.staticWal),
		SiaFile: sf,
	}
	n.files[fileName] = fn
	return nil
}

// managedNewSiaFileFromLegacyData adds an existing SiaFile to the filesystem
// using the provided siafile.FileData object.
func (n *DNode) managedNewSiaFileFromLegacyData(fileName string, fd siafile.FileData) (*FNode, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Check if the path is taken.
	path := filepath.Join(n.absPath(), fileName+modules.SiaFileExtension)
	if _, err := os.Stat(filepath.Join(path)); !os.IsNotExist(err) {
		return nil, ErrExists
	}
	// Check if the file exists in memory.
	key := strings.TrimSuffix(fileName, modules.SiaFileExtension)
	if _, exists := n.files[key]; exists {
		return nil, ErrExists
	}
	// Otherwise create the file.
	sf, err := siafile.NewFromLegacyData(fd, path, n.staticWal)
	if err != nil {
		return nil, err
	}
	// Add it to the node.
	fn := &FNode{
		node:    newNode(n, path, key, 0, n.staticWal),
		SiaFile: sf,
	}
	n.files[key] = fn
	return fn.managedCopy(), nil
}

// managedSiaDir calls siaDir while holding the node's lock.
func (n *DNode) managedSiaDir() (*siadir.SiaDir, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.siaDir()
}

// siaDir is a wrapper for the lazySiaDir field.
func (n *DNode) siaDir() (*siadir.SiaDir, error) {
	if *n.lazySiaDir != nil {
		return *n.lazySiaDir, nil
	}
	sd, err := siadir.LoadSiaDir(n.absPath(), modules.ProdDependencies, n.staticWal)
	if err == siadir.ErrUnknownPath {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	*n.lazySiaDir = sd
	return sd, nil
}

// Close calls close on the dNode and also removes the dNode from its parent if
// it's no longer being used and if it doesn't have any children which are
// currently in use.
func (n *DNode) Close() {
	// If a parent exists, we need to lock it while closing a child.
	n.mu.Lock()
	parent := n.parent
	n.mu.Unlock()
	if parent != nil {
		parent.mu.Lock()
	}
	n.mu.Lock()
	if n.parent != parent {
		build.Critical("dir parent changed")
	}

	// call private close method.
	n.close()

	// Remove node from parent if there are no more children after this close.
	removeDir := len(n.threads) == 0 && len(n.directories) == 0 && len(n.files) == 0 && true
	if parent != nil && removeDir {
		parent.removeDir(n)
	}

	// Unlock child and parent.
	n.mu.Unlock()
	if parent != nil {
		child := parent
		parent := parent.parent
		child.mu.Unlock() // child is the parent we locked before

		// Iteratively try to remove from parents as long as children got
		// removed.
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
}

// Delete recursively deletes a dNode from disk.
func (n *DNode) managedDelete() error {
	// If there is a parent lock it.
	if n.parent != nil {
		n.parent.mu.Lock()
		defer n.parent.mu.Unlock()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// Get contents of dir.
	dirsToLock := n.childDirs()
	var filesToDelete []*FNode
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
func (n *DNode) managedDeleteFile(fileName string) error {
	n.mu.Lock()
	// Check if the file is open in memory. If it is delete it.
	sf, exists := n.files[fileName]
	if exists {
		n.mu.Unlock()
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
func (n *DNode) managedInfo(siaPath modules.SiaPath) (modules.DirectoryInfo, error) {
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
		AggregateMaxHealthPercentage: HealthPercentage(aggregateMaxHealth),
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
		MaxHealthPercentage: HealthPercentage(maxHealth),
		MinRedundancy:       metadata.MinRedundancy,
		MostRecentModTime:   metadata.ModTime,
		NumFiles:            metadata.NumFiles,
		NumStuckChunks:      metadata.NumStuckChunks,
		NumSubDirs:          metadata.NumSubDirs,
		Size:                metadata.Size,
		StuckHealth:         metadata.StuckHealth,
		SiaPath:             siaPath,
	}, nil
}

// childDirs is a convenience method to return the directories field of a DNode
// as a slice.
func (n *DNode) childDirs() []*DNode {
	dirs := make([]*DNode, 0, len(n.directories))
	for _, dir := range n.directories {
		dirs = append(dirs, dir)
	}
	return dirs
}

// childFiles is a convenience method to return the files field of a DNode as a
// slice.
func (n *DNode) childFiles() []*FNode {
	files := make([]*FNode, 0, len(n.files))
	for _, file := range n.files {
		files = append(files, file)
	}
	return files
}

// managedNewSiaFile creates a new SiaFile in the directory.
func (n *DNode) managedNewSiaFile(fileName string, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Make sure we don't have a copy of that file in memory already.
	if _, exists := n.files[fileName]; exists {
		return ErrExists
	}
	_, err := siafile.New(filepath.Join(n.absPath(), fileName+modules.SiaFileExtension), source, n.staticWal, ec, mk, fileSize, fileMode, nil, disablePartialUpload)
	return errors.AddContext(err, "NewSiaFile: failed to create file")
}

// managedOpenFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (n *DNode) managedOpenFile(fileName string) (*FNode, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.openFile(fileName)
}

// openFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (n *DNode) openFile(fileName string) (*FNode, error) {
	fn, exists := n.files[fileName]
	if !exists {
		// Load file from disk.
		filePath := filepath.Join(n.absPath(), fileName+modules.SiaFileExtension)
		sf, err := siafile.LoadSiaFile(filePath, n.staticWal)
		if err == siafile.ErrUnknownPath || os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		if err != nil {
			return nil, errors.AddContext(err, "failed to load SiaFile from disk")
		}
		fn = &FNode{
			node:    newNode(n, filePath, fileName, 0, n.staticWal),
			SiaFile: sf,
		}
		n.files[fileName] = fn
	} else if exists && fn.Deleted() {
		return nil, ErrNotExist // don't return a deleted file
	}
	// Clone the node, give it a new UID and return it.
	return fn.managedCopy(), nil
}

// openDir opens the dir with the specified name within the current dir.
func (n *DNode) openDir(dirName string) (*DNode, error) {
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
	dir = &DNode{
		node:        newNode(n, dirPath, dirName, 0, n.staticWal),
		directories: make(map[string]*DNode),
		files:       make(map[string]*FNode),
		lazySiaDir:  new(*siadir.SiaDir),
	}
	n.directories[*dir.name] = dir
	return dir.managedCopy(), nil
}

// managedCopy copies the node, adds a new thread to the threads map and returns the
// new instance.
func (n *DNode) managedCopy() *DNode {
	// Copy the dNode and change the uid to a unique one.
	n.mu.Lock()
	defer n.mu.Unlock()
	newNode := *n
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = newThreadType()
	return &newNode
}

// managedOpenDir opens a SiaDir.
func (n *DNode) managedOpenDir(path string) (*DNode, error) {
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
	defer subNode.managedClose()
	return subNode.managedOpenDir(filepath.Join(pathList...))
}

// managedRemoveDir removes a dir from a dNode. If as a result the dNode ends up
// without children and if the threads map of the dNode is empty, the dNode will
// remove itself from its parent.
// NOTE: child.mu needs to be locked
func (n *DNode) removeDir(child *DNode) {
	// Remove the child node.
	currentChild, exists := n.directories[*child.name]
	if !exists || child.staticUID != currentChild.staticUID {
		return // nothing to do
	}
	delete(n.directories, *child.name)
}

// removeFile removes a child from a dNode. If as a result the dNode
// ends up without children and if the threads map of the dNode is empty, the
// dNode will remove itself from its parent.
// NOTE: child.mu needs to be locked
func (n *DNode) removeFile(child *FNode) {
	// Remove the child node.
	currentChild, exists := n.files[*child.name]
	if !exists || child.SiaFile != currentChild.SiaFile {
		return // Nothing to do
	}
	delete(n.files, *child.name)
}

// managedRename renames the fNode's underlying file.
func (n *DNode) managedRename(newName string, oldParent, newParent *DNode) error {
	// Lock the parents. If they are the same, only lock one.
	if oldParent.staticUID == newParent.staticUID {
		oldParent.mu.Lock()
		defer oldParent.mu.Unlock()
	} else {
		oldParent.mu.Lock()
		defer oldParent.mu.Unlock()
		newParent.mu.Lock()
		defer newParent.mu.Unlock()
	}
	// Check that newParent doesn't have a dir with that name already.
	if _, exists := newParent.files[newName]; exists {
		return ErrExists
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	dirsToLock := n.childDirs()
	var dirsToRename []*DNode
	var filesToRename []*FNode
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
	if err == siadir.ErrPathOverload {
		return ErrExists
	}
	if err != nil {
		return err
	}
	// Remove dir from old parent and add it to new parent.
	// TODO: iteratively remove parents like in Close
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
