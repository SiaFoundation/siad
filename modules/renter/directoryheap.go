package renter

import (
	"container/heap"
	"fmt"
	"math"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// directory is a helper struct that represents a siadir in the
// repairDirectoryHeap
type directory struct {
	// Heap controlled fields
	index int // The index of the item in the heap

	// mu controlled fields
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
	heapDirectories map[modules.SiaPath]*directory

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
func (rdh repairDirectoryHeap) Swap(i, j int) {
	rdh[i], rdh[j] = rdh[j], rdh[i]
	rdh[i].index = i
	rdh[j].index = j
}
func (rdh *repairDirectoryHeap) Push(x interface{}) {
	n := len(*rdh)
	d := x.(*directory)
	d.index = n
	*rdh = append(*rdh, d)
}
func (rdh *repairDirectoryHeap) Pop() interface{} {
	old := *rdh
	n := len(old)
	d := old[n-1]
	d.index = -1 // for safety
	*rdh = old[0 : n-1]
	return d
}

// managedEmpty clears the directory heap by recreating the heap and
// heapDirectories.
func (dh *directoryHeap) managedEmpty() {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	dh.heapDirectories = make(map[modules.SiaPath]*directory)
	dh.heap = repairDirectoryHeap{}
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
		dh.heapDirectories[d.siaPath] = d
		added = true
	}
	return added
}

// managedUpdate will update the directory that is currently in the heap based
// on the directory pasted in.
//
// The worse health will be kept and explored will be prioritized over
// unexplored
func (dh *directoryHeap) managedUpdate(d *directory) bool {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	var updated bool
	heapDir, exists := dh.heapDirectories[d.siaPath]
	if exists {
		// Update the fields of the directory in the heap
		heapDir.mu.Lock()
		heapDir.aggregateHealth = math.Max(heapDir.aggregateHealth, d.aggregateHealth)
		heapDir.health = math.Max(heapDir.health, d.health)
		if d.explored {
			heapDir.explored = d.explored
		}
		heapDir.mu.Unlock()
		heap.Fix(&dh.heap, heapDir.index)
		updated = true
	}

	return updated
}

// managedPushDirectory adds a directory to the directory heap
func (dh *directoryHeap) managedPushDirectory(siaPath modules.SiaPath, aggregateHealth, health float64, explored bool) error {
	d := &directory{
		aggregateHealth: aggregateHealth,
		health:          health,
		explored:        explored,
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
	// Loop until we pop off an explored directory
	for {
		// Pop directory
		d := r.directoryHeap.managedPop()

		// Sanity check that we are still popping off directories
		if d == nil {
			return nil, nil
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
	return r.directoryHeap.managedPushDirectory(siaPath, metadata.AggregateHealth, metadata.Health, false)
}

// managedResetDirectoryHeap resets the directory heap by clearing it and then
// adding an unexplored root directory to the heap.
func (r *Renter) managedResetDirectoryHeap() error {
	// Empty the directory heap
	r.directoryHeap.managedEmpty()
	return r.managedPushUnexploredDirectory(modules.RootSiaPath())
}
