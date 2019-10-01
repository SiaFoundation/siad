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
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// threadDepth is how deep the ThreadType will track calling files and
	// calling lines
	threadDepth = 3
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
		root string
		dNode
	}

	node struct {
		parent     *dNode
		staticName string
		threads    map[threadUID]threadInfo
		threadUID  threadUID
		mu         sync.Mutex
	}

	dNode struct {
		node

		directories map[string]*dNode
		files       map[string]*fNode
		*siadir.SiaDir
	}

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
		root: root,
		dNode: dNode{
			node: node{
				parent:     nil,
				staticName: "",
				threads:    make(map[threadUID]threadInfo),
				threadUID:  threadUID(0),
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

func (fs *FileSystem) openDir(path string) (*dNode, error) {
	// Make sure the path is absolute.
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, errors.New("can't get absolute path")
	}
	// Split the path up.
	pathList := filepath.SplitList(path)
	// Recursively call open.
	return fs.dNode.openDir(pathList)
}

func (n *node) close() {
	if _, exists := n.threads[n.threadUID]; !exists {
		build.Critical("threaduid doesn't exist in threads map: ", n.threadUID, len(n.threads))
	}
	delete(n.threads, n.threadUID)

	// The entry that exists in the siafile set may not be the same as the entry
	// that is being closed, this can happen if there was a rename or a delete
	// and then a new/different file was uploaded with the same siapath.
	//
	// If they are not the same entry, there is nothing more to do.
	if n.threadUID != n.parent.directories[n.staticName].node.threadUID &&
		n.threadUID != n.parent.files[n.staticName].node.threadUID {
		return
	}

	// Remove node from parent.
	n.parent.removeChild(n)
}

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
	if n.parent != nil && len(n.threads) == 0 && len(n.files) == 0 && len(n.directories) == 0 {
		n.parent.removeChild(&n.node)
	}
}

func (n *node) path() string {
	path := n.staticName
	for parent := n.parent; parent != nil; parent = parent.parent {
		path = filepath.Join(parent.staticName, path)
	}
	return path
}

func (n *dNode) openDir(pathList []string) (*dNode, error) {
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
		return subNode.openDir(pathList)
	}
	// Otherwise load the dir.
	sd, err := siadir.LoadSiaDir(filepath.Join(n.path(), subDir, siadir.SiaDirExtension))
	if err != nil {
		return nil, err
	}
	subNode = &dNode{
		node: node{
			parent:     n,
			staticName: subDir,
			threadUID:  newThreadUID(),
		},

		directories: make(map[string]*dNode),
		files:       make(map[string]*fNode),
		SiaDir:      sd,
	}
	n.directories[subDir] = subNode
	return subNode.openDir(pathList)
}
