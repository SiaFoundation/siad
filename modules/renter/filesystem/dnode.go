package filesystem

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// dNode is a node which references a SiaDir.
	dNode struct {
		node

		directories map[string]*dNode
		files       map[string]*fNode

		// Since we create dNodes implicitly whenever one of a dNodes children
		// is opened, the SiaDir will be loaded lazily only when a dNode is
		// manually opened by the user. This way we can keep disk i/o to a
		// minimum. The SiaDir is also cleared once a dNode doesn't have any
		// threads accessing it anymore to avoid caching the metadata of a
		// SiaDir any longer than a user has any need for it.
		*siadir.SiaDir
	}
)

// close calls the common close method of all nodes and clears the SiaDir if no
// more threads are accessing.
func (n *dNode) close() {
	// Call common close method.
	n.node._close()

	// If no more threads are accessing this node, clear the SiaDir metadata.
	if len(n.threads) == 0 {
		n.SiaDir = nil
	}
}

// Close calls close on the dNode and also removes the dNode from its parent if
// it's no longer being used and if it doesn't have any children which are
// currently in use.
func (n *dNode) Close() {
	n.mu.Lock()

	// call private close method.
	n.close()

	// Remove node from parent if there are no more children.
	if n.staticParent != nil && len(n.threads)+len(n.directories)+len(n.files) == 0 {
		n.mu.Unlock()
		n.staticParent.managedRemoveDir(n)
		return
	}
	n.mu.Unlock()
}

// Delete recursively deltes a dNode from disk.
func (n *dNode) managedDelete() error {
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
			// Clone the opened dir to force the SiaDir to be loaded.
			dir, err = dir.copy()
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
func (n *dNode) managedDeleteFile(fileName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	// Open the file.
	sf, err := n.openFile(fileName)
	if err != nil {
		return errors.AddContext(err, "failed to open file for deletion")
	}
	defer sf.Close()
	// Delete it.
	return sf.managedDelete()
}

// managedNewSiaFile creates a new SiaFile in the directory.
func (n *dNode) managedNewSiaFile(fileName string, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
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
func (n *dNode) managedOpenFile(fileName string) (*fNode, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.openFile(fileName)
}

// openFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (n *dNode) openFile(fileName string) (*fNode, error) {
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
		fn = &fNode{
			node:    newNode(n, fileName, 0, n.staticWal),
			SiaFile: sf,
		}
		n.files[fileName] = fn
	} else if exists && fn.Deleted() {
		return nil, ErrNotExist // don't return a deleted file
	}
	// lock new node before releasing parent.
	fn.mu.Lock()
	defer fn.mu.Unlock()
	// Clone the node, give it a new UID and return it.
	newNode := *fn
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = newThreadType()
	return &newNode, nil
}

// openDir opens the dir with the specified name within the current dir but
// doesn't clone it. That means it won't increment the number of threads in the
// threads map. That means Close doesn't need to be called on a dNode returned
// by openDir.
func (n *dNode) openDir(dirName string) (*dNode, error) {
	// Check if dir was already loaded.
	dir, exists := n.directories[dirName]
	if exists {
		return dir, nil
	}
	// Check if the dir exists.
	if _, err := os.Stat(filepath.Join(n.staticPath(), dirName)); err != nil {
		return nil, ErrNotExist
	}
	dir = &dNode{
		node: node{
			staticParent: n,
			staticName:   dirName,
			staticWal:    n.staticWal,
			threadUID:    0,
			threads:      make(map[threadUID]threadInfo),
			mu:           new(sync.Mutex),
		},

		directories: make(map[string]*dNode),
		files:       make(map[string]*fNode),
		SiaDir:      nil, // will be lazy-loaded
	}
	n.directories[dir.staticName] = dir
	return dir, nil
}

// copy copies the node, adds a new thread to the threads map and returns the
// new instance.
func (n *dNode) copy() (*dNode, error) {
	// Copy the dNode and change the uid to a unique one.
	newNode := *n
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = newThreadType()
	// If the SiaDir was loaded before, check if it has been deleted in the
	// meantime. If it has, set it to 'nil' to force a reload. If a new dir
	// took its place that one will be loaded instead. Otherwise an error
	// will be returned.
	if newNode.SiaDir != nil && newNode.SiaDir.Deleted() {
		newNode.SiaDir = nil
	}
	// Load the SiaDir if necessary.
	if newNode.SiaDir == nil {
		sd, err := siadir.LoadSiaDir(n.staticPath(), modules.ProdDependencies, n.staticWal)
		if err != nil {
			return nil, err
		}
		newNode.SiaDir = sd
	}
	return &newNode, nil
}

// managedOpenDir opens a SiaDir.
func (n *dNode) managedOpenDir(path string) (*dNode, error) {
	// If path is empty we are done.
	if path == "" {
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.copy()
	}
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
	return subNode.managedOpenDir(path)
}

// managedRemoveDir removes a dir from a dNode. If as a result the dNode ends up
// without children and if the threads map of the dNode is empty, the dNode will
// remove itself from its parent.
func (n *dNode) managedRemoveDir(child *dNode) {
	// Remove the child node.
	n.mu.Lock()
	currentChild, exists := n.directories[child.staticName]
	if !exists || child.SiaDir != currentChild.SiaDir {
		n.mu.Unlock()
		return // Nothing to do
	}
	delete(n.directories, child.staticName)
	removeChild := len(n.threads) == 0 && len(n.files) == 0 && len(n.directories) == 0
	n.mu.Unlock()

	// If there are no more children and the threads map is empty, remove the
	// parent as well. This happens when a directory was added to the tree
	// because one of its children was opened.
	if n.staticParent != nil && removeChild {
		n.staticParent.managedRemoveDir(n)
	}
}

// managedRemoveChild removes a child from a dNode. If as a result the dNode
// ends up without children and if the threads map of the dNode is empty, the
// dNode will remove itself from its parent.
func (n *dNode) managedRemoveFile(child *fNode) {
	// Remove the child node.
	n.mu.Lock()
	currentChild, exists := n.files[child.staticName]
	if !exists || currentChild.SiaFile != child.SiaFile {
		n.mu.Unlock()
		return // Nothing to do
	}
	delete(n.files, child.staticName)
	removeChild := len(n.threads) == 0 && len(n.files) == 0 && len(n.directories) == 0
	n.mu.Unlock()

	// If there are no more children and the threads map is empty, remove the
	// parent as well. This happens when a directory was added to the tree
	// because one of its children was opened.
	if n.staticParent != nil && removeChild {
		n.staticParent.managedRemoveDir(n)
	}
}
