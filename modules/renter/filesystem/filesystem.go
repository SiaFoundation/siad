package filesystem

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// ErrNotExist is returned when a file or folder can't be found on disk.
	ErrNotExist = errors.New("path does not exist")
)

type (
	// FileSystem implements a thread-safe filesystem for Sia for loading
	// SiaFiles, SiaDirs and potentially other supported Sia types in the
	// future.
	FileSystem struct {
		dNode
	}

	// node is a struct that contains the commmon fields of every node.
	node struct {
		staticParent *dNode
		staticName   string
		threads      map[threadUID]threadInfo
		threadUID    threadUID
		mu           sync.Mutex
	}

	// dNode is a node which references a SiaDir.
	dNode struct {
		node

		directories map[string]*dNode
		files       map[string]*fNode
		*siadir.SiaDir
	}

	// fNode is a node which references a SiaFile.
	fNode struct {
		node

		*siafile.SiaFile
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

// New creates a new FileSystem at the specified root path. The folder will be
// created if it doesn't exist already.
func New(root string) (*FileSystem, error) {
	if err := os.Mkdir(root, 0600); err != nil && !os.IsExist(err) {
		return nil, errors.AddContext(err, "failed to create root dir")
	}
	return &FileSystem{
		dNode: dNode{
			node: node{
				staticParent: nil,  // the root doesn't have a parent
				staticName:   root, // the root doesn't have a name
				threads:      make(map[threadUID]threadInfo),
				threadUID:    threadUID(0), // the root doesn't require a uid
			},
			directories: make(map[string]*dNode),
			files:       make(map[string]*fNode),
		},
	}, nil
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

// NewSiaDir creates the folder for the specified siaPath. This doesn't create
// the folder metadata since that will be created on demand as the individual
// folders are accessed.
func (fs *FileSystem) NewSiaDir(siaPath modules.SiaPath) error {
	dirPath := siaPath.SiaDirSysPath(fs.staticName)
	return errors.AddContext(os.MkdirAll(dirPath, 0600), "NewSiaDir: failed to create folder")
}

// managedOpenDir opens a directory within the filesystem and all of its
// parents.
func (fs *FileSystem) managedOpenDir(path string) (*dNode, error) {
	// Make sure the path is absolute.
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("can't get absolute path")
	}
	// Split the path up.
	pathList := filepath.SplitList(path)
	// Recursively call open.
	return fs.dNode.managedOpenDir(pathList)
}

// close removes a thread from the node's threads map. This should only be
// called from within other 'close' methods.
func (n *node) close() {
	if _, exists := n.threads[n.threadUID]; !exists {
		build.Critical("threaduid doesn't exist in threads map: ", n.threadUID, len(n.threads))
	}
	delete(n.threads, n.threadUID)
}

// close calls close on the underlying node and also removes the dNode from its
// parent if it's no longer being used and if it doesn't have any children which
// are currently in use.
func (n *dNode) close() {
	// Call common close method.
	n.node.close()

	// The entry that exists in the parent may not be the same as the entry
	// that is being closed, this can happen if there was a rename or a delete
	// and then a new/different file was uploaded with the same path.
	//
	// If they are not the same node, there is nothing more to do.
	if n.threadUID != n.staticParent.directories[n.staticName].node.threadUID {
		return
	}

	// Remove node from parent if there are no more children.
	if len(n.directories)+len(n.files) == 0 {
		n.staticParent.removeChild(&n.node)
	}
}

// close calls close on the underlying node and also removes the fNode from its
// parent.
func (n *fNode) close() {
	// Call common close method.
	n.node.close()

	// The entry that exists in the parent may not be the same as the entry
	// that is being closed, this can happen if there was a rename or a delete
	// and then a new/different file was uploaded with the same path.
	//
	// If they are not the same node, there is nothing more to do.
	n.staticParent.mu.Lock()
	currentUID := n.staticParent.files[n.staticName].node.threadUID
	n.staticParent.mu.Unlock()
	if n.threadUID != currentUID {
		return
	}

	// Remove node from parent.
	n.staticParent.removeChild(&n.node)
}

// removeChild removes a child from a dNode. If as a result the dNode ends up
// without children and if the threads map of the dNode is empty, the dNode will
// remove itself from its parent.
func (n *dNode) removeChild(child *node) {
	// Remove the child node.
	_, existsDir := n.directories[child.staticName]
	_, existsFile := n.directories[child.staticName]
	if !existsDir && !existsFile {
		build.Critical("removeChild: unknown child")
	}
	delete(n.directories, child.staticName)
	delete(n.files, child.staticName)

	// If there are no more children and the threads map is empty, remove the
	// parent as well. This happens when a directory was added to the tree
	// because one of its children was opened.
	if n.staticParent != nil && len(n.threads) == 0 && len(n.files) == 0 && len(n.directories) == 0 {
		n.staticParent.removeChild(&n.node)
	}
}

// staticPath returns the absolute path of the node on disk.
func (n *node) staticPath() string {
	path := n.staticName
	for parent := n.staticParent; parent != nil; parent = parent.staticParent {
		path = filepath.Join(parent.staticName, path)
	}
	return path
}

// openDir opens a SiaDir.
func (n *dNode) managedOpenDir(pathList []string) (*dNode, error) {
	// If pathList is empty we are done.
	if len(pathList) == 0 {
		n.mu.Lock()
		defer n.mu.Unlock()
		if existing, exists := n.threads[n.threadUID]; exists {
			err := fmt.Errorf("node with uid %v was already registered: %v", n.threadUID, existing)
			build.Critical(err)
			return nil, err
		}
		n.threads[n.threadUID] = newThreadType()
		return n, nil
	}
	// Check if the next element is loaded already.
	subDir, pathList := pathList[0], pathList[1:]
	n.mu.Lock()
	subNode, exists := n.directories[subDir]
	if exists {
		n.mu.Unlock()
		return subNode.managedOpenDir(pathList)
	}
	// Otherwise load the dir.
	sd, err := siadir.LoadSiaDir(filepath.Join(n.staticPath(), subDir, siadir.SiaDirExtension))
	if err != nil {
		n.mu.Unlock()
		return nil, err
	}
	subNode = &dNode{
		node: node{
			staticParent: n,
			staticName:   subDir,
			threadUID:    newThreadUID(),
		},

		directories: make(map[string]*dNode),
		files:       make(map[string]*fNode),
		SiaDir:      sd,
	}
	n.directories[subDir] = subNode
	n.mu.Unlock()
	return subNode.managedOpenDir(pathList)
}
