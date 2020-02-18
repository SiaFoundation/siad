package renter

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// uniqueRefreshPaths is a helper struct for determining the minimum number of
// directories that will need to have callThreadedBubbleMetadata called on in
// order to properly update the affected directory tree. Since bubble calls
// itself on the parent directory when it finishes with a directory, only a call
// to the lowest level child directory is needed to properly update the entire
// directory tree.
type uniqueRefreshPaths struct {
	childDirs  map[modules.SiaPath]struct{}
	parentDirs map[modules.SiaPath]struct{}

	r  *Renter
	mu sync.Mutex
}

// newUniqueRefreshPaths returns an initialized uniqueRefreshPaths struct
func (r *Renter) newUniqueRefreshPaths() *uniqueRefreshPaths {
	return &uniqueRefreshPaths{
		childDirs:  make(map[modules.SiaPath]struct{}),
		parentDirs: make(map[modules.SiaPath]struct{}),

		r: r,
	}
}

// callAdd adds a path to uniqueRefreshPaths.
func (ufp *uniqueRefreshPaths) callAdd(path modules.SiaPath) error {
	ufp.mu.Lock()
	defer ufp.mu.Unlock()

	// Check if the path is in the parent directory map
	if _, ok := ufp.parentDirs[path]; ok {
		return nil
	}

	// Check if the path is in the child directory map
	if _, ok := ufp.childDirs[path]; ok {
		return nil
	}

	// Add path to the childDir map
	ufp.childDirs[path] = struct{}{}

	// Check all path elements to make sure any parent directories are removed
	// from the child directory map and added to the parent directory map
	for !path.IsRoot() {
		// Get the parentDir of the path
		parentDir, err := path.Dir()
		if err != nil {
			contextStr := fmt.Sprintf("unable to get parent directory of %v", path)
			return errors.AddContext(err, contextStr)
		}
		// Check if the parentDir is in the childDirs map
		if _, ok := ufp.childDirs[parentDir]; ok {
			// Remove from childDir map and add to parentDir map
			delete(ufp.childDirs, parentDir)
			ufp.parentDirs[parentDir] = struct{}{}
		}
		// Set path equal to the parentDir
		path = parentDir
	}
	return nil
}

// callRefreshAll uses the uniqueRefreshPaths's Renter to call
// callThreadedBubbleMetadata on all the directories in the childDir map
func (ufp *uniqueRefreshPaths) callRefreshAll() {
	ufp.mu.Lock()
	defer ufp.mu.Unlock()
	for sp := range ufp.childDirs {
		go ufp.r.callThreadedBubbleMetadata(sp)
	}
}
