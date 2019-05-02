package renter

import (
	"container/heap"
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// directory is a helper struct that represents a siadir in the
// repairDirectoryHeap
type directory struct {
	aggregateHealth float64
	health          float64
	explored        bool
	siaPath         modules.SiaPath

	mu sync.Mutex
}

// directoryHeap contains a priority sorted heap of directories that are being
// explored and repaired
type directoryHeap struct {
	heap repairDirectoryHeap

	// heapDirectories is a map containing all the directories currently in the
	// heap
	heapDirectories map[modules.SiaPath]struct{}

	mu sync.Mutex
}

// repairDirectoryHeap is a heap of priority sorted directory elements that need
// to be explored and repaired.
type repairDirectoryHeap []*directory

// Implementation of heap.Interface for repairDirectoryHeap.
func (rdh repairDirectoryHeap) Len() int { return len(rdh) }
func (rdh repairDirectoryHeap) Less(i, j int) bool {
	// Prioritization: If a directory is explored then we should use the Health
	// of the Directory. If a directory is unexplored then we should use the
	// AggregateHealth of the Directory. This will ensure we are following the
	// path of lowest health as well as evaluating each directory on its own
	// merit.
	//
	// Note: we are using the > operator and not >= which means that the element
	// added to the heap first will be prioritized in the event that the healths
	// are equal

	// Determine health of each element to used based on whether or not the
	// element is explored
	var iHealth, jHealth float64
	if rdh[i].explored {
		iHealth = rdh[i].health
	} else {
		iHealth = rdh[i].aggregateHealth
	}
	if rdh[j].explored {
		jHealth = rdh[j].health
	} else {
		jHealth = rdh[j].aggregateHealth
	}

	// Prioritize higher health
	return iHealth > jHealth
}
func (rdh repairDirectoryHeap) Swap(i, j int)       { rdh[i], rdh[j] = rdh[j], rdh[i] }
func (rdh *repairDirectoryHeap) Push(x interface{}) { *rdh = append(*rdh, x.(*directory)) }
func (rdh *repairDirectoryHeap) Pop() interface{} {
	old := *rdh
	n := len(old)
	dir := old[n-1]
	*rdh = old[0 : n-1]
	return dir
}

// managedEmpty clears the directory heap by popping off all the directories and
// ensuring that the map is empty
func (dh *directoryHeap) managedEmpty() {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	for dh.heap.Len() > 0 {
		_ = dh.pop()
	}
	if len(dh.heapDirectories) != 0 {
		build.Critical("heapDirectories map is not empty after emptying the directory heap")
	}
}

// managedLen returns the length of the heap
func (dh *directoryHeap) managedLen() int {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	return dh.heap.Len()
}

// managedPop will return the top directory from the heap
func (dh *directoryHeap) managedPop() (d *directory) {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	if dh.heap.Len() > 0 {
		d = dh.pop()
	}
	return d
}

// managedPush will try to add a directory to the directory heap. If the
// directory is added it will return true, otherwise it will return false.
func (dh *directoryHeap) managedPush(d *directory) bool {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	var added bool
	_, exists := dh.heapDirectories[d.siaPath]
	if !exists {
		heap.Push(&dh.heap, d)
		dh.heapDirectories[d.siaPath] = struct{}{}
		added = true
	}
	return added
}

// managedPushUnexploredDirectory adds an unexplored directory to the directory
// heap
func (dh *directoryHeap) managedPushUnexploredDirectory(siaPath modules.SiaPath, aggregateHealth, health float64) error {
	d := &directory{
		aggregateHealth: aggregateHealth,
		health:          health,
		siaPath:         siaPath,
	}
	if !dh.managedPush(d) {
		return errors.New("failed to push unexplored directory onto heap")
	}
	return nil
}

// pop pulls off the top directory from the heap and deletes it from the map
func (dh *directoryHeap) pop() (d *directory) {
	d = heap.Pop(&dh.heap).(*directory)
	delete(dh.heapDirectories, d.siaPath)
	return d
}

// managedNextExploredDirectory pops directories off of the heap until it
// finds an explored directory. If an unexplored directory is found, any
// subdirectories are added to the heap and the directory is marked as explored
// and pushed back onto the heap.
func (r *Renter) managedNextExploredDirectory() (*directory, error) {
	// Check if heap  is empty
	if r.directoryHeap.managedLen() == 0 {
		err := r.managedPushUnexploredDirectory(modules.RootSiaPath())
		if err != nil {
			return nil, err
		}
	}

	// Loop until we pop off an explored directory
	for {
		// Pop directory
		d := r.directoryHeap.managedPop()

		// Sanity check that we are still popping off directories
		if d == nil {
			build.Critical("no more directories to pop off heap, this should never happen")
			return nil, errors.New("no more directories to pop off heap")
		}

		// Check if explored and mark as explored if unexplored
		d.mu.Lock()
		explored := d.explored
		if !explored {
			d.explored = true
		}
		d.mu.Unlock()
		if explored {
			return d, nil
		}

		// Add Sub directories
		err := r.managedPushSubDirectories(d)
		if err != nil {
			return nil, err
		}

		// Add popped directory back to heap with explored now set to true
		added := r.directoryHeap.managedPush(d)
		if !added {
			return nil, fmt.Errorf("could not push directory %v onto heap", d.siaPath.String())
		}
	}
}

// managedPushSubDirectories adds unexplored directory elements to the heap for
// all of the directory's sub directories
func (r *Renter) managedPushSubDirectories(d *directory) error {
	subDirs, err := r.managedSubDirectories(d.siaPath)
	if err != nil {
		return err
	}
	for _, subDir := range subDirs {
		err = r.managedPushUnexploredDirectory(subDir)
		if err != nil {
			return err
		}
	}
	return nil
}

// managedPushUnexploredDirectory reads the health from the siadir metadata and
// pushes an unexplored directory element onto the heap
func (r *Renter) managedPushUnexploredDirectory(siaPath modules.SiaPath) error {
	// Grab the root siadir metadata
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		return err
	}
	defer siaDir.Close()
	metadata := siaDir.Metadata()

	// Push unexplored directory onto heap
	return r.directoryHeap.managedPushUnexploredDirectory(siaPath, metadata.AggregateHealth, metadata.Health)
}

// managedResetDirectoryHeap resets the directory heap by clearing it and then
// adding an unexplored root directory to the heap.
func (r *Renter) managedResetDirectoryHeap() error {
	// Empty the directory heap
	r.directoryHeap.managedEmpty()
	return r.managedPushUnexploredDirectory(modules.RootSiaPath())
}
