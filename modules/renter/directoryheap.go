package renter

import (
	"container/heap"
	"fmt"
	"math"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/threadgroup"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem/siadir"
)

// directory is a helper struct that represents a siadir in the
// repairDirectoryHeap
type directory struct {
	// Heap controlled fields
	index int // The index of the item in the heap

	staticSiaPath modules.SiaPath

	// mu controlled fields
	aggregateHealth       float64
	aggregateRemoteHealth float64
	explored              bool
	health                float64
	remoteHealth          float64

	mu sync.Mutex
}

// managedHeapHealth returns the health that should be used to prioritize the
// directory in the heap. It also returns a boolean indicating if that health is
// from remote files.
//
// If a directory is explored then we should use the Health of the Directory. If
// a directory is unexplored then we should use the AggregateHealth of the
// Directory. This will ensure we are following the path of lowest health as
// well as evaluating each directory on its own merit.
//
// If either the RemoteHealth or the AggregateRemoteHealth are above the
// RepairThreshold we should use that health in order to prioritize remote files
func (d *directory) managedHeapHealth() (float64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Grab the directory level health values
	remoteHealth := d.remoteHealth
	health := d.health

	// If the directory hasn't been explored yet, grab the aggregate health
	// values
	if !d.explored {
		remoteHealth = d.aggregateRemoteHealth
		health = d.aggregateHealth
	}

	// Use the remoteHealth if it is at or worse than the RepairThreshold
	if modules.NeedsRepair(remoteHealth) {
		return remoteHealth, true
	}
	return health, false
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
	// Get the health of each directory and whether or not they have remote
	// files
	iHealth, iRemote := rdh[i].managedHeapHealth()
	jHealth, jRemote := rdh[j].managedHeapHealth()

	// Prioritize based on Remote first
	if iRemote && !jRemote {
		return true
	}
	if !iRemote && jRemote {
		return false
	}

	// Directories are prioritized based on their heapHealth
	//
	// Note: we are using the > operator and not >= which means that the element
	// added to the heap first will be prioritized in the event that the healths
	// are equal
	return iHealth > jHealth
}
func (rdh repairDirectoryHeap) Swap(i, j int) {
	rdh[i], rdh[j] = rdh[j], rdh[i]
	rdh[i].index, rdh[j].index = i, j
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

// managedLen returns the length of the heap
func (dh *directoryHeap) managedLen() int {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	return dh.heap.Len()
}

// managedPeekHealth returns the current worst health of the directory heap. A
// boolean is returned indicating whether or not the health is based on remote
// health. If the file has poor remote health, this is considered more
// significant than having even poorer local health.
//
// 'Remote' health indicates the health of all chunks that are not available
// locally and therefore need to do remote repairs.
func (dh *directoryHeap) managedPeekHealth() (float64, bool) {
	dh.mu.Lock()
	defer dh.mu.Unlock()

	// If the heap is empty return 0 as that is the max health
	if dh.heap.Len() == 0 {
		return 0, false
	}

	// Pop off and then push back the top directory. We are not using the
	// managed methods here as to avoid removing the directory from the map and
	// having another thread push the directory onto the heap in between locks
	d := heap.Pop(&dh.heap).(*directory)
	defer heap.Push(&dh.heap, d)
	return d.managedHeapHealth()
}

// managedPop will return the top directory from the heap
func (dh *directoryHeap) managedPop() (d *directory) {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	if dh.heap.Len() > 0 {
		d = heap.Pop(&dh.heap).(*directory)
		delete(dh.heapDirectories, d.staticSiaPath)
	}
	return d
}

// managedPush will try to add a directory to the directory heap. If the
// directory already exists, the existing directory will be updated to have the
// worst healths of the pushed directory and the existing directory. And if
// either the existing directory or the pushed directory is marked as
// unexplored, the updated directory will be marked as unexplored.
func (dh *directoryHeap) managedPush(d *directory) {
	dh.mu.Lock()
	defer dh.mu.Unlock()

	// If the directory exists already in the heap, update that directory.
	_, exists := dh.heapDirectories[d.staticSiaPath]
	if exists {
		if !dh.update(d) {
			build.Critical("update should succeed because the directory is known to exist in the heap")
		}
		return
	}

	// If the directory does not exist in the heap, add it to the heap.
	heap.Push(&dh.heap, d)
	dh.heapDirectories[d.staticSiaPath] = d
}

// managedReset clears the directory heap by recreating the heap and
// heapDirectories.
func (dh *directoryHeap) managedReset() {
	dh.mu.Lock()
	defer dh.mu.Unlock()
	dh.heapDirectories = make(map[modules.SiaPath]*directory)
	dh.heap = repairDirectoryHeap{}
}

// update will update the directory that is currently in the heap based on the
// directory pasted in.
//
// The worse health between the pushed dir and the existing dir will be kept to
// ensure that the directory is looked at by the repair heap.
//
// Similarly, if either the new dir or the existing dir are marked as
// unexplored, the new dir will be marked as unexplored to ensure that all
// subdirs of the dir get added to the heap.
func (dh *directoryHeap) update(d *directory) bool {
	heapDir, exists := dh.heapDirectories[d.staticSiaPath]
	if !exists {
		return false
	}
	// Update the health fields of the directory in the heap.
	heapDir.mu.Lock()
	heapDir.aggregateHealth = math.Max(heapDir.aggregateHealth, d.aggregateHealth)
	heapDir.aggregateRemoteHealth = math.Max(heapDir.aggregateRemoteHealth, d.aggregateRemoteHealth)
	heapDir.health = math.Max(heapDir.health, d.health)
	heapDir.remoteHealth = math.Max(heapDir.remoteHealth, d.remoteHealth)
	if !heapDir.explored || !d.explored {
		heapDir.explored = false
	}
	heapDir.mu.Unlock()
	dh.heapDirectories[d.staticSiaPath] = heapDir
	heap.Fix(&dh.heap, heapDir.index)
	return true
}

// managedPushDirectory adds a directory to the directory heap
func (dh *directoryHeap) managedPushDirectory(siaPath modules.SiaPath, metadata siadir.Metadata, explored bool) {
	d := &directory{
		aggregateHealth:       metadata.AggregateHealth,
		aggregateRemoteHealth: metadata.AggregateRemoteHealth,
		explored:              explored,
		health:                metadata.Health,
		remoteHealth:          metadata.RemoteHealth,
		staticSiaPath:         siaPath,
	}
	dh.managedPush(d)
}

// managedNextExploredDirectory pops directories off of the heap until it
// finds an explored directory. If an unexplored directory is found, any
// subdirectories are added to the heap and the directory is marked as explored
// and pushed back onto the heap.
func (r *Renter) managedNextExploredDirectory() (*directory, error) {
	// Loop until we pop off an explored directory
	for {
		select {
		case <-r.tg.StopChan():
			return nil, errors.AddContext(threadgroup.ErrStopped, "renter shutdown before directory could be returned")
		default:
		}

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
			contextStr := fmt.Sprintf("unable to push subdirectories for `%v`", d.staticSiaPath)
			return nil, errors.AddContext(err, contextStr)
		}

		// Add popped directory back to heap with explored now set to true.
		r.directoryHeap.managedPush(d)
	}
}

// managedPushSubDirectories adds unexplored directory elements to the heap for
// all of the directory's sub directories
func (r *Renter) managedPushSubDirectories(d *directory) error {
	subDirs, err := r.managedSubDirectories(d.staticSiaPath)
	if err != nil {
		contextStr := fmt.Sprintf("unable to get subdirectories for `%v`", d.staticSiaPath)
		return errors.AddContext(err, contextStr)
	}
	for _, subDir := range subDirs {
		err = r.managedPushUnexploredDirectory(subDir)
		if err != nil {
			contextStr := fmt.Sprintf("unable to push unexplored directory `%v`", subDir)
			return errors.AddContext(err, contextStr)
		}
	}
	return nil
}

// managedPushUnexploredDirectory reads the health from the siadir metadata and
// pushes an unexplored directory element onto the heap
func (r *Renter) managedPushUnexploredDirectory(siaPath modules.SiaPath) (err error) {
	// Grab the siadir metadata. If it doesn't exist, create it.
	siaDir, err := r.staticFileSystem.OpenSiaDirCustom(siaPath, true)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, siaDir.Close())
	}()
	metadata, err := siaDir.Metadata()
	if err != nil {
		return err
	}

	// Push unexplored directory onto heap.
	r.directoryHeap.managedPushDirectory(siaPath, metadata, false)
	return nil
}
