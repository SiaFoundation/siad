package renter

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
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
func (urp *uniqueRefreshPaths) callAdd(path modules.SiaPath) error {
	urp.mu.Lock()
	defer urp.mu.Unlock()

	// Check if the path is in the parent directory map
	if _, ok := urp.parentDirs[path]; ok {
		return nil
	}

	// Check if the path is in the child directory map
	if _, ok := urp.childDirs[path]; ok {
		return nil
	}

	// Add path to the childDir map
	urp.childDirs[path] = struct{}{}

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
		if _, ok := urp.childDirs[parentDir]; ok {
			// Remove from childDir map and add to parentDir map
			delete(urp.childDirs, parentDir)
		}
		// Make sure the parentDir is in the parentDirs map
		urp.parentDirs[parentDir] = struct{}{}
		// Set path equal to the parentDir
		path = parentDir
	}
	return nil
}

// callNumChildDirs returns the number of child directories currently being
// tracked.
func (urp *uniqueRefreshPaths) callNumChildDirs() int {
	urp.mu.Lock()
	defer urp.mu.Unlock()
	return len(urp.childDirs)
}

// callNumParentDirs returns the number of parent directories currently being
// tracked.
func (urp *uniqueRefreshPaths) callNumParentDirs() int {
	urp.mu.Lock()
	defer urp.mu.Unlock()
	return len(urp.parentDirs)
}

// callRefreshAll will update the directories in the childDir map by calling
// refreshAll in a go routine.
func (urp *uniqueRefreshPaths) callRefreshAll() error {
	urp.mu.Lock()
	defer urp.mu.Unlock()
	return urp.r.tg.Launch(func() {
		urp.refreshAll()
	})
}

// callRefreshAllBlocking will update the directories in the childDir map by
// calling refreshAll.
func (urp *uniqueRefreshPaths) callRefreshAllBlocking() {
	urp.mu.Lock()
	defer urp.mu.Unlock()
	urp.refreshAll()
}

// refreshAll calls the urp's Renter's managedBubbleMetadata method on all the
// directories in the childDir map
func (urp *uniqueRefreshPaths) refreshAll() {
	// Create a siaPath channel with numBubbleWorkerThreads spaces
	siaPathChan := make(chan modules.SiaPath, numBubbleWorkerThreads)

	// Launch worker groups
	var wg sync.WaitGroup
	for i := 0; i < numBubbleWorkerThreads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for siaPath := range siaPathChan {
				complete := urp.r.staticBubbleScheduler.callQueueBubble(siaPath)
				select {
				case <-complete:
				case <-urp.r.tg.StopChan():
					return
				}
			}
		}()
	}

	// Add all child dir siaPaths to the siaPathChan
	for sp := range urp.childDirs {
		select {
		case siaPathChan <- sp:
		case <-urp.r.tg.StopChan():
			// Renter has shutdown, close the channel and return.
			close(siaPathChan)
			wg.Wait()
			return
		}
	}

	// Close siaPathChan and wait for worker groups to complete
	close(siaPathChan)
	wg.Wait()

	return
}
