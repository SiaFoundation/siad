package renter

import (
	"container/heap"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// directory is a helper struct that represents a siadir in the
// repairDirectoryHeap
type directory struct {
	explored bool
	health   float64
	siaPath  modules.SiaPath

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
	// Since a higher health is worse we use the > operator
	return rdh[i].health > rdh[j].health
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

// managedPushUnexploredRoot adds an unexplored root to the directory heap
func (dh *directoryHeap) managedPushUnexploredRoot(health float64) error {
	d := &directory{
		health:  health,
		siaPath: modules.RootSiaPath(),
	}
	if !dh.managedPush(d) {
		return errors.New("failed to push unexplored root directory onto heap")
	}
	return nil
}

// pop pulls off the top directory from the heap and deletes it from the map
func (dh *directoryHeap) pop() (d *directory) {
	d = heap.Pop(&dh.heap).(*directory)
	delete(dh.heapDirectories, d.siaPath)
	return d
}

// managedResetDirectoryHeap resets the directory heap by clearing it and then
// adding an unexplored root directory to the heap.
func (r *Renter) managedResetDirectoryHeap() error {
	// Empty the directory heap
	r.directoryHeap.managedEmpty()

	// Grab the root siadir metadata
	siaDir, err := r.staticDirSet.Open(modules.RootSiaPath())
	if err != nil {
		return err
	}
	defer siaDir.Close()
	metadata := siaDir.Metadata()

	return r.directoryHeap.managedPushUnexploredRoot(metadata.Health)
}
