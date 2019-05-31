package renter

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// updateSiaDirHealth is a helper method to update the health and the aggregate
// health of a siadir
func (r *Renter) updateSiaDirHealth(siaPath modules.SiaPath, health, aggregateHealth float64) error {
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		return err
	}
	defer siaDir.Close()
	metadata := siaDir.Metadata()
	metadata.Health = health
	metadata.AggregateHealth = aggregateHealth
	err = siaDir.UpdateMetadata(metadata)
	if err != nil {
		return err
	}
	return nil
}

// TestDirectoryHeap probes the directory heap implementation
func TestDirectoryHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Add directories to heap. Using these settings ensures that neither the
	// first of the last element added remains at the top of the health. The
	// heap should look like the following:
	//
	// &{5 1 false {5} {0 0}}
	// &{2 4 true {2} {0 0}}
	// &{3 3 false {3} {0 0}}
	// &{4 2 true {4} {0 0}}
	// &{1 5 false {1} {0 0}}
	// &{6 0 true {6} {0 0}}

	heapLen := 6
	for i := 1; i <= heapLen; i++ {
		siaPath, err := modules.NewSiaPath(fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		d := &directory{
			aggregateHealth: float64(i),
			health:          float64(heapLen - i),
			explored:        i%2 == 0,
			siaPath:         siaPath,
		}
		if !rt.renter.directoryHeap.managedPush(d) {
			t.Fatal("directory not added")
		}
	}

	// Confirm all elements added
	if rt.renter.directoryHeap.managedLen() != heapLen {
		t.Fatalf("heap should have length of %v but was %v", heapLen, rt.renter.directoryHeap.managedLen())
	}

	// Check health of heap
	if rt.renter.directoryHeap.managedPeekHealth() != float64(5) {
		t.Fatalf("Expected health of heap to be the value of the aggregate health of top chunk %v, got %v", 5, rt.renter.directoryHeap.managedPeekHealth())
	}

	// Pop off top element and check against expected values
	d := rt.renter.directoryHeap.managedPop()
	if d.health != float64(1) {
		t.Fatal("Expected Health of 1, got", d.health)
	}
	if d.aggregateHealth != float64(5) {
		t.Fatal("Expected AggregateHealth of 5, got", d.aggregateHealth)
	}
	if d.explored {
		t.Fatal("Expected the directory to be unexplored")
	}

	// Check health of heap
	if rt.renter.directoryHeap.managedPeekHealth() != float64(4) {
		t.Fatalf("Expected health of heap to be the value of the health of top chunk %v, got %v", 4, rt.renter.directoryHeap.managedPeekHealth())
	}

	// Push directory back on, then confirm a second push fails
	if !rt.renter.directoryHeap.managedPush(d) {
		t.Fatal("directory not added")
	}
	if rt.renter.directoryHeap.managedPush(d) {
		t.Fatal("directory should not have been added")
	}

	// Now update directory and confirm it is not the top directory and the top
	// element is as expected
	d.aggregateHealth = 0
	d.health = 0
	d.explored = true
	if !rt.renter.directoryHeap.managedUpdate(d) {
		t.Fatal("directory not updated")
	}
	topDir := rt.renter.directoryHeap.managedPop()
	if topDir.health != float64(4) {
		t.Fatal("Expected Health of 4, got", topDir.health)
	}
	if topDir.aggregateHealth != float64(2) {
		t.Fatal("Expected AggregateHealth of 2, got", topDir.aggregateHealth)
	}
	if !topDir.explored {
		t.Fatal("Expected the directory to be explored")
	}
	// Find Directory in heap and confirm that it was updated
	found := false
	for rt.renter.directoryHeap.managedLen() > 0 {
		topDir = rt.renter.directoryHeap.managedPop()
		if !topDir.siaPath.Equals(d.siaPath) {
			continue
		}
		if found {
			t.Fatal("Duplicate directory in heap")
		}
		found = true
		if topDir.health != d.health {
			t.Fatalf("Expected Health of %v, got %v", d.health, topDir.health)
		}
		if topDir.aggregateHealth != d.aggregateHealth {
			t.Fatalf("Expected AggregateHealth of %v, got %v", d.aggregateHealth, topDir.aggregateHealth)
		}
		if !topDir.explored {
			t.Fatal("Expected the directory to be explored")
		}
	}

	// Reset Direcotry heap
	rt.renter.directoryHeap.managedReset()

	// Confirm that the heap is empty
	if rt.renter.directoryHeap.managedLen() != 0 {
		t.Fatal("heap should empty but has length of", rt.renter.directoryHeap.managedLen())
	}
}

// TestPushSubDirectories probes the methods that add sub directories to the
// heap. This in turn tests the methods for adding unexplored directories
func TestPushSubDirectories(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Create a test directory with the following healths
	//
	// root/ 1
	// root/SubDir1/ 2
	// root/SubDir2/ 3

	// Create directory tree
	siaPath1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath2); err != nil {
		t.Fatal(err)
	}

	// Update metadata
	err = rt.renter.updateSiaDirHealth(siaPath1, float64(2), float64(2))
	if err != nil {
		t.Fatal(err)
	}

	err = rt.renter.updateSiaDirHealth(siaPath2, float64(3), float64(3))
	if err != nil {
		t.Fatal(err)
	}

	// Make sure we are starting with an empty heap
	rt.renter.directoryHeap.managedReset()

	// Add root sub directories
	d := &directory{
		siaPath: modules.RootSiaPath(),
	}
	err = rt.renter.managedPushSubDirectories(d)
	if err != nil {
		t.Fatal(err)
	}

	// Heap should have a length of 2
	if rt.renter.directoryHeap.managedLen() != 2 {
		t.Fatal("Heap should have length of 2 but was", rt.renter.directoryHeap.managedLen())
	}

	// Pop off elements and confirm the are correct
	d = rt.renter.directoryHeap.managedPop()
	if !d.siaPath.Equals(siaPath2) {
		t.Fatalf("Expected directory %v but found %v", siaPath2.String(), d.siaPath.String())
	}
	if d.aggregateHealth != float64(3) {
		t.Fatal("Expected AggregateHealth to be 3 but was", d.aggregateHealth)
	}
	if d.health != float64(3) {
		t.Fatal("Expected Health to be 3 but was", d.health)
	}
	if d.explored {
		t.Fatal("Expected directory to be unexplored")
	}
	d = rt.renter.directoryHeap.managedPop()
	if !d.siaPath.Equals(siaPath1) {
		t.Fatalf("Expected directory %v but found %v", siaPath1.String(), d.siaPath.String())
	}
	if d.aggregateHealth != float64(2) {
		t.Fatal("Expected AggregateHealth to be 2 but was", d.aggregateHealth)
	}
	if d.health != float64(2) {
		t.Fatal("Expected Health to be 2 but was", d.health)
	}
	if d.explored {
		t.Fatal("Expected directory to be unexplored")
	}
}

// TestNextExploredDirectory probes managedNextExploredDirectory to ensure that
// the directory traverses the filesystem as expected
func TestNextExploredDirectory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Create a test directory with the following healths/aggregateHealths
	//
	// root/ 0/3
	// root/SubDir1/ 1/2
	// root/SubDir1/SubDir1/ 1/1
	// root/SubDir1/SubDir2/ 2/2
	// root/SubDir2/ 1/3
	// root/SubDir2/SubDir1/ 1/1
	// root/SubDir2/SubDir2/ 3/3
	//
	// Overall we would expect to see root/SubDir2/SubDir2 popped first followed
	// by root/SubDir1/SubDir2

	// Create SiaPaths
	siaPath1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	siaPath1_1, err := siaPath1.Join("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath1_2, err := siaPath1.Join("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	siaPath2_1, err := siaPath2.Join("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath2_2, err := siaPath2.Join("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	// Create Directories
	if err := rt.renter.CreateDir(siaPath1_1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath1_2); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath2_1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath2_2); err != nil {
		t.Fatal(err)
	}

	// Update metadata
	err = rt.renter.updateSiaDirHealth(modules.RootSiaPath(), float64(0), float64(3))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.updateSiaDirHealth(siaPath1, float64(1), float64(2))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.updateSiaDirHealth(siaPath1_1, float64(1), float64(1))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.updateSiaDirHealth(siaPath1_2, float64(2), float64(2))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.updateSiaDirHealth(siaPath2, float64(1), float64(3))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.updateSiaDirHealth(siaPath2_1, float64(1), float64(1))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.updateSiaDirHealth(siaPath2_2, float64(3), float64(3))
	if err != nil {
		t.Fatal(err)
	}

	// Make sure we are starting with an empty heap, this helps with ndfs and
	// tests proper handling of empty heaps
	rt.renter.directoryHeap.managedReset()
	err = rt.renter.managedPushUnexploredDirectory(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// Pop off next explored directory
	d, err := rt.renter.managedNextExploredDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("No directory popped off heap")
	}

	// Directory should be root/SubDir2/SubDir2
	if !d.siaPath.Equals(siaPath2_2) {
		t.Fatalf("Expected directory %v but found %v", siaPath2_2.String(), d.siaPath.String())
	}
	if d.aggregateHealth != float64(3) {
		t.Fatal("Expected AggregateHealth to be 3 but was", d.aggregateHealth)
	}
	if d.health != float64(3) {
		t.Fatal("Expected Health to be 3 but was", d.health)
	}
	if !d.explored {
		t.Fatal("Expected directory to be explored")
	}

	// Pop off next explored directory
	d, err = rt.renter.managedNextExploredDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if d == nil {
		t.Fatal("No directory popped off heap")
	}

	// Directory should be root/SubDir1/SubDir2
	if !d.siaPath.Equals(siaPath1_2) {
		t.Fatalf("Expected directory %v but found %v", siaPath1_2.String(), d.siaPath.String())
	}
	if d.aggregateHealth != float64(2) {
		t.Fatal("Expected AggregateHealth to be 2 but was", d.aggregateHealth)
	}
	if d.health != float64(2) {
		t.Fatal("Expected Health to be 2 but was", d.health)
	}
	if !d.explored {
		t.Fatal("Expected directory to be explored")
	}
}
