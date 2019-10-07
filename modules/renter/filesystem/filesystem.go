package filesystem

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		staticWal    *writeaheadlog.WAL
		threads      map[threadUID]threadInfo
		threadUID    threadUID
		mu           *sync.Mutex
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
func New(root string, wal *writeaheadlog.WAL) (*FileSystem, error) {
	if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return nil, errors.AddContext(err, "failed to create root dir")
	}
	return &FileSystem{
		dNode: dNode{
			// The root doesn't require a parent, the name is its absolute path for convenience and it doesn't require a uid.
			node:        newNode(nil, root, 0, wal),
			directories: make(map[string]*dNode),
			files:       make(map[string]*fNode),
		},
	}, nil
}

// newNode is a convenience function to initialize a node.
func newNode(parent *dNode, name string, uid threadUID, wal *writeaheadlog.WAL) node {
	return node{
		staticParent: parent,
		staticName:   name,
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

// NewSiaDir creates the folder for the specified siaPath. This doesn't create
// the folder metadata since that will be created on demand as the individual
// folders are accessed.
func (fs *FileSystem) NewSiaDir(siaPath modules.SiaPath) error {
	dirPath := siaPath.SiaDirSysPath(fs.staticName)
	return errors.AddContext(os.MkdirAll(dirPath, 0700), "NewSiaDir: failed to create folder")
}

// NewSiaFile creates a SiaFile at the specified siaPath.
func (fs *FileSystem) NewSiaFile(siaPath modules.SiaPath, source string, ec modules.ErasureCoder, mk crypto.CipherKey, fileSize uint64, fileMode os.FileMode, disablePartialUpload bool) error {
	// Create SiaDir for file.
	dirSiaPath, err := siaPath.Dir()
	if err != nil {
		return err
	}
	if err := fs.NewSiaDir(dirSiaPath); err != nil {
		return errors.AddContext(err, "failed to create SiaDir for SiaFile")
	}
	// Create SiaFile.
	siaFilePath := siaPath.SiaFileSysPath(fs.staticName)
	_, err = siafile.New(siaFilePath, source, fs.staticWal, ec, mk, fileSize, fileMode, nil, disablePartialUpload)
	return errors.AddContext(err, "NewSiaFile: failed to create file")
}

// OpenSiaDir opens a SiaDir and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) OpenSiaDir(siaPath modules.SiaPath) (*dNode, error) {
	return fs.dNode.managedOpenDir(siaPath.String())
}

// OpenSiaFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) OpenSiaFile(siaPath modules.SiaPath) (*fNode, error) {
	return fs.managedOpenFile(siaPath.String())
}

// managedOpenFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (fs *FileSystem) managedOpenFile(path string) (*fNode, error) {
	// Open the folder that contains the file.
	dirPath, fileName := filepath.Split(path)
	var dir *dNode
	if dirPath == string(filepath.Separator) || dirPath == "." || dirPath == "" {
		dir = &fs.dNode // file is in the root dir
	} else {
		var err error
		dir, err = fs.managedOpenDir(filepath.Dir(path))
		if err != nil {
			return nil, errors.AddContext(err, "failed to open parent dir of file")
		}
		// Close the dir since we are not returning it. The open file keeps it
		// loaded in memory.
		defer dir.close()
	}
	return dir.managedOpenFile(fileName)
}

// managedOpenFile opens a SiaFile and adds it and all of its parents to the
// filesystem tree.
func (n *dNode) managedOpenFile(fileName string) (*fNode, error) {
	fn, exists := n.files[fileName]
	if !exists {
		// Load file from disk.
		filePath := filepath.Join(n.staticPath(), fileName+modules.SiaFileExtension)
		sf, err := siafile.LoadSiaFile(filePath, n.staticWal)
		if err == siafile.ErrUnknownPath {
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
	}
	// Clone the node, give it a new UID and return it.
	newNode := *fn
	newNode.threadUID = newThreadUID()
	newNode.threads[newNode.threadUID] = newThreadType()
	return &newNode, nil
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
	n.staticParent.mu.Lock()
	sd := n.staticParent.directories[n.staticName].SiaDir
	n.staticParent.mu.Unlock()
	if n.SiaDir != sd {
		return
	}

	// Remove node from parent if there are no more children.
	if len(n.threads)+len(n.directories)+len(n.files) == 0 {
		n.staticParent.managedRemoveChild(&n.node)
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
	sf := n.staticParent.files[n.staticName].SiaFile
	n.staticParent.mu.Unlock()
	if n.SiaFile != sf {
		return
	}

	// Remove node from parent.
	if len(n.threads) == 0 {
		n.staticParent.managedRemoveChild(&n.node)
	}
}

// managedRemoveChild removes a child from a dNode. If as a result the dNode
// ends up without children and if the threads map of the dNode is empty, the
// dNode will remove itself from its parent.
func (n *dNode) managedRemoveChild(child *node) {
	// Remove the child node.
	n.mu.Lock()
	_, existsDir := n.directories[child.staticName]
	_, existsFile := n.files[child.staticName]
	if !existsDir && !existsFile {
		build.Critical("removeChild: unknown child")
	}
	delete(n.directories, child.staticName)
	delete(n.files, child.staticName)
	removeChild := len(n.threads) == 0 && len(n.files) == 0 && len(n.directories) == 0
	n.mu.Unlock()

	// If there are no more children and the threads map is empty, remove the
	// parent as well. This happens when a directory was added to the tree
	// because one of its children was opened.
	if n.staticParent != nil && removeChild {
		n.staticParent.managedRemoveChild(&n.node)
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
func (n *dNode) managedOpenDir(path string) (*dNode, error) {
	// If path is empty we are done.
	if path == "" {
		n.mu.Lock()
		defer n.mu.Unlock()
		// Copy the dNode and change the uid to a unique one.
		newNode := *n
		newNode.threadUID = newThreadUID()
		newNode.threads[newNode.threadUID] = newThreadType()
		return &newNode, nil
	}
	// Check if the next element is loaded already.
	subDir, path := filepath.Split(path)
	if subDir == "" && path != "" {
		subDir = path
		path = ""
	}
	subDir = strings.Trim(subDir, string(filepath.Separator))
	n.mu.Lock()
	subNode, exists := n.directories[subDir]
	if exists {
		n.mu.Unlock()
		return subNode.managedOpenDir(path)
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
			threadUID:    0,
			threads:      make(map[threadUID]threadInfo),
			mu:           new(sync.Mutex),
		},

		directories: make(map[string]*dNode),
		files:       make(map[string]*fNode),
		SiaDir:      sd,
	}
	n.directories[subNode.staticName] = subNode
	n.mu.Unlock()
	return subNode.managedOpenDir(path)
}
