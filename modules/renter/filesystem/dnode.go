package filesystem

import (
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strings"

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

		// Since we create dNodes implicitly whenever one of a DNodes children
		// is opened, the SiaDir will be loaded lazily only when a dNode is
		// manually opened by the user. This way we can keep disk i/o to a
		// minimum. The SiaDir is also cleared once a dNode doesn't have any
		// threads accessing it anymore to avoid caching the metadata of a
		// SiaDir any longer than a user has any need for it.
		*siadir.SiaDir
	}
)

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

func (n *DNode) managedList(recursive, cached bool, fileLoadChan chan *FNode, dirLoadChan chan *DNode) error {
	fis, err := ioutil.ReadDir(n.staticPath())
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
		}
		// Handle file.
		n.mu.Lock()
		file, err := n.openFile(strings.TrimSuffix(info.Name(), modules.SiaFileExtension))
		if err != nil {
			n.mu.Unlock()
			return err
		}
		fileLoadChan <- file.managedCopy()
		file.Close()
	}
	return nil
}

// close calls the common close method of all nodes and clears the SiaDir if no
// more threads are accessing.
func (n *DNode) close() {
	// Call common close method.
	n.node._close()
}

func (n *DNode) managedClose() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.close()
}

// Close calls close on the dNode and also removes the dNode from its parent if
// it's no longer being used and if it doesn't have any children which are
// currently in use.
func (n *DNode) Close() {
	// If a parent exists, we need to lock it while closing a child.
	parent := n.staticParent
	if parent != nil {
		parent.mu.Lock()
	}
	n.mu.Lock()

	// Remove node from parent if there are no more children after this close.
	removeDir := len(n.threads) == 1 && len(n.directories) == 0 && len(n.files) == 0
	if parent != nil && removeDir {
		parent.removeDir(n)
	}

	// call private close method.
	n.close()

	// Unlock child and parent.
	n.mu.Unlock()
	if parent != nil {
		parent.mu.Unlock()

		// Iteratively try to remove parents as long as children got removed.
		for child, parent := parent, parent.staticParent; removeDir && parent != nil; child, parent = parent, parent.staticParent {
			parent.mu.Lock()
			child.mu.Lock()
			removeDir = len(child.threads)+len(child.directories)+len(child.files) == 0
			parent.removeDir(child)
			child.mu.Unlock()
			parent.mu.Unlock()
		}
	}
}

// Delete recursively deltes a dNode from disk.
func (n *DNode) managedDelete() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Get contents of dir.
	fis, err := ioutil.ReadDir(n.staticPath())
	if err != nil {
		return err
	}
	for _, fi := range fis {
		// Delete subdir.
		if fi.IsDir() {
			dir, err := n.openDir(fi.Name())
			if err != nil {
				return err
			}
			if err := dir.Delete(); err != nil {
				dir.close()
				return err
			}
			dir.close()
			continue
		}
		// Delete file.
		if filepath.Ext(fi.Name()) == modules.SiaFileExtension {
			file, err := n.openFile(fi.Name())
			if err != nil {
				return err
			}
			if err := file.managedDelete(); err != nil {
				return err
			}
		}
	}
	return nil
}

// managedDeleteFile deletes the file with the given name from the directory.
func (n *DNode) managedDeleteFile(fileName string) error {
	n.mu.Lock()
	// Open the file.
	sf, err := n.openFile(fileName)
	if err != nil {
		n.mu.Unlock()
		return errors.AddContext(err, "failed to open file for deletion")
	}
	// Delete it.
	if err := sf.managedDelete(); err != nil {
		n.mu.Unlock()
		sf.Close()
		return err
	}
	n.mu.Unlock()
	sf.Close()
	return nil
}

// staticInfo builds and returns the DirectoryInfo of a SiaDir.
func (n *DNode) staticInfo(siaPath modules.SiaPath) modules.DirectoryInfo {
	// Grab the siadir metadata
	metadata := n.Metadata()
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
	}
}

// managedNewSiaFile creates a new SiaFile in the directory.
func (n *DNode) managedNewSiaFile(fileName string, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Make sure we don't have a copy of that file in memory already.
	if _, exists := n.files[fileName]; exists {
		return ErrExists
	}
	_, err := siafile.New(filepath.Join(n.staticPath(), fileName+modules.SiaFileExtension), source, n.staticWal, ec, mk, fileSize, fileMode, nil, disablePartialUpload)
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
		filePath := filepath.Join(n.staticPath(), fileName+modules.SiaFileExtension)
		sf, err := siafile.LoadSiaFile(filePath, n.staticWal)
		if err == siafile.ErrUnknownPath || os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		if err != nil {
			return nil, errors.AddContext(err, "failed to load SiaFile from disk")
		}
		fn = &FNode{
			node:    newNode(n, fileName, 0, n.staticWal),
			SiaFile: sf,
		}
		n.files[fileName] = fn
	} else if exists && fn.Deleted() {
		return nil, ErrNotExist // don't return a deleted file
	}
	// Clone the node, give it a new UID and return it.
	return fn.managedCopy(), nil
}

// openDir opens the dir with the specified name within the current dir but
// doesn't clone it. That means it won't increment the number of threads in the
// threads map. That means Close doesn't need to be called on a dNode returned
// by openDir.
func (n *DNode) openDir(dirName string) (*DNode, error) {
	// Check if dir was already loaded. Then just copy it.
	dir, exists := n.directories[dirName]
	if exists {
		return dir.managedCopy(), nil
	}
	// Load the dir.
	siaDir, err := siadir.LoadSiaDir(filepath.Join(n.staticPath(), dirName), modules.ProdDependencies, n.staticWal)
	if err == siadir.ErrUnknownPath {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	dir = &DNode{
		node:        newNode(n, dirName, 0, n.staticWal),
		directories: make(map[string]*DNode),
		files:       make(map[string]*FNode),
		SiaDir:      siaDir,
	}
	n.directories[dir.staticName] = dir
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
	subDir, path := filepath.Split(path)
	if subDir == "" {
		subDir = path
		path = ""
	}
	subDir = strings.Trim(subDir, string(filepath.Separator))
	n.mu.Lock()
	subNode, err := n.openDir(subDir)
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	// If path is empty we are done.
	if path == "" {
		return subNode, nil
	}
	// Otherwise open the next dir.
	defer subNode.managedClose()
	return subNode.managedOpenDir(path)
}

// managedRemoveDir removes a dir from a dNode. If as a result the dNode ends up
// without children and if the threads map of the dNode is empty, the dNode will
// remove itself from its parent.
func (n *DNode) removeDir(child *DNode) {
	// Remove the child node.
	currentChild, exists := n.directories[child.staticName]
	if !exists || child.SiaDir != currentChild.SiaDir {
		return // nothing to do
	}
	delete(n.directories, child.staticName)
}

// removeFile removes a child from a dNode. If as a result the dNode
// ends up without children and if the threads map of the dNode is empty, the
// dNode will remove itself from its parent.
func (n *DNode) removeFile(child *FNode) {
	// Remove the child node.
	currentChild, exists := n.files[child.staticName]
	if !exists || child.SiaFile != currentChild.SiaFile {
		return // Nothing to do
	}
	delete(n.files, child.staticName)
}
