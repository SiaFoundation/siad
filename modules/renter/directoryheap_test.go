package renter

import (
	"os"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
)

// updateSiaDirHealth is a helper method to update the health and the aggregate
// health of a siadir
func (r *Renter) updateSiaDirHealth(siaPath modules.SiaPath, health, aggregateHealth float64) (err error) {
	siaDir, err := r.staticFileSystem.OpenSiaDir(siaPath)
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
	metadata.Health = health
	metadata.AggregateHealth = aggregateHealth
	err = siaDir.UpdateMetadata(metadata)
	if err != nil {
		return err
	}
	return nil
}

// addDirectoriesToHeap is a helper function for adding directories to the
// Renter's directory heap
func addDirectoriesToHeap(r *Renter, numDirs int, explored, remote bool) {
	// If the directory is remote, the remote health should be worse than the
	// non remote healths to ensure the remote prioritization overrides the
	// health comparison
	//
	// If the directory is explored, the aggregateHealth should be worse to
	// ensure that the directories health is used and prioritized

	for i := 0; i < numDirs; i++ {
		// Initialize values
		var remoteHealth, aggregateRemoteHealth float64
		health := float64(fastrand.Intn(100)) + 0.25
		aggregateHealth := float64(fastrand.Intn(100)) + 0.25

		// If remote then set the remote healths to be non zero and make sure
		// that the coresponding healths are worse
		if remote {
			remoteHealth = float64(fastrand.Intn(100)) + 0.25
			aggregateRemoteHealth = float64(fastrand.Intn(100)) + 0.25
			health = remoteHealth + 1
			aggregateHealth = aggregateRemoteHealth + 1
		}

		// If explored, set the aggregate values to be half the RepairThreshold
		// higher than the non aggregate values. Using half the RepairThreshold
		// so that non remote directories are still considered non remote.
		if explored {
			aggregateHealth = health + modules.RepairThreshold/2
			aggregateRemoteHealth = remoteHealth + modules.RepairThreshold/2
		}

		// Create the directory and push it on to the heap
		d := &directory{
			aggregateHealth:       aggregateHealth,
			aggregateRemoteHealth: aggregateRemoteHealth,
			explored:              explored,
			health:                health,
			remoteHealth:          remoteHealth,
			staticSiaPath:         modules.RandomSiaPath(),
		}
		r.directoryHeap.managedPush(d)
	}
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Check that the heap was initialized properly
	if rt.renter.directoryHeap.managedLen() != 0 {
		t.Fatal("directory heap should have length of 0 but has length of", rt.renter.directoryHeap.managedLen())
	}

	// Add directories of each type to the heap
	addDirectoriesToHeap(rt.renter, 1, true, true)
	addDirectoriesToHeap(rt.renter, 1, true, false)
	addDirectoriesToHeap(rt.renter, 1, false, true)
	addDirectoriesToHeap(rt.renter, 1, false, false)

	// Confirm all elements added.
	if rt.renter.directoryHeap.managedLen() != 4 {
		t.Fatalf("heap should have length of %v but was %v", 4, rt.renter.directoryHeap.managedLen())
	}

	// Check that the heapHealth is remote
	_, remote := rt.renter.directoryHeap.managedPeekHealth()
	if !remote {
		t.Error("Heap should have a remote health at the top")
	}

	// Pop the directories and validate their position
	d1 := rt.renter.directoryHeap.managedPop()
	d2 := rt.renter.directoryHeap.managedPop()
	d1Health, d1Remote := d1.managedHeapHealth()
	d2Health, d2Remote := d2.managedHeapHealth()

	// Both Directories should be remote
	if !d1Remote || !d2Remote {
		t.Errorf("Expected both directories to be remote but got %v and %v", d1Remote, d2Remote)
	}
	// The top directory should have the worst health
	if d1Health < d2Health {
		t.Errorf("Expected top directory to have worse health but got %v >= %v", d1Health, d2Health)
	}

	d3 := rt.renter.directoryHeap.managedPop()
	d4 := rt.renter.directoryHeap.managedPop()
	d3Health, d3Remote := d3.managedHeapHealth()
	d4Health, d4Remote := d4.managedHeapHealth()
	// Both Directories should not be remote
	if d3Remote || d4Remote {
		t.Errorf("Expected both directories to not be remote but got %v and %v", d3Remote, d4Remote)
	}
	// The top directory should have the worst health
	if d3Health < d4Health {
		t.Errorf("Expected top directory to have worse health but got %v >= %v", d3Health, d4Health)
	}

	// Push directories part on to the heap
	rt.renter.directoryHeap.managedPush(d1)
	rt.renter.directoryHeap.managedPush(d2)
	rt.renter.directoryHeap.managedPush(d3)
	rt.renter.directoryHeap.managedPush(d4)

	// Modifying d4 and re-push it to update it's position in the heap
	d4.explored = true
	d4.aggregateHealth = 0
	d4.aggregateRemoteHealth = d1Health + 1
	d4.health = 0
	d4.remoteHealth = 0
	rt.renter.directoryHeap.managedPush(d4)

	// Now, even though d4 has a worse aggregate remote health than d1's
	// heapHealth, it should not be on the top of the heap because it is
	// explored and therefore its heapHealth will be using the non aggregate
	// fields
	d := rt.renter.directoryHeap.managedPop()
	if reflect.DeepEqual(d, d4) {
		t.Log(d)
		t.Log(d4)
		t.Error("Expected top directory to not be directory 4")
	}
	if !reflect.DeepEqual(d, d1) {
		t.Log(d)
		t.Log(d1)
		t.Error("Expected top directory to still be directory 1")
	}

	// Push top directory back onto heap
	rt.renter.directoryHeap.managedPush(d)

	// No set d4 to not be explored, this should be enough to force it to the
	// top of the heap
	d4.explored = false
	rt.renter.directoryHeap.managedPush(d4)

	// Check that top directory is directory 4
	d = rt.renter.directoryHeap.managedPop()
	if !reflect.DeepEqual(d, d4) {
		t.Log(d)
		t.Log(d4)
		t.Error("Expected top directory to be directory 4")
	}

	// Reset Directory heap
	rt.renter.directoryHeap.managedReset()

	// Confirm that the heap is empty
	if rt.renter.directoryHeap.managedLen() != 0 {
		t.Fatal("heap should empty but has length of", rt.renter.directoryHeap.managedLen())
	}

	// Test pushing an unexplored directory
	err = rt.renter.managedPushUnexploredDirectory(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if rt.renter.directoryHeap.managedLen() != 1 {
		t.Fatal("directory heap should have length of 1 but has length of", rt.renter.directoryHeap.managedLen())
	}
	d = rt.renter.directoryHeap.managedPop()
	if d.explored {
		t.Fatal("directory should be unexplored root")
	}
	if !d.staticSiaPath.Equals(modules.RootSiaPath()) {
		t.Fatal("Directory should be root directory but is", d.staticSiaPath)
	}

	// Make sure pushing an unexplored dir that doesn't exist works.
	randomSP, err := modules.RootSiaPath().Join(modules.RandomSiaPath().String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.managedPushUnexploredDirectory(randomSP)
	if err != nil {
		t.Fatal(err)
	}
	// Try again but this time just remove the .siadir file.
	err = os.Remove(randomSP.SiaDirMetadataSysPath(rt.renter.staticFileSystem.Root()))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.managedPushUnexploredDirectory(randomSP)
	if err != nil {
		t.Fatal(err)
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a test directory with the following healths
	//
	// / 1
	// /SubDir1/ 2
	// /SubDir2/ 3

	// Create directory tree
	siaPath1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath2, modules.DefaultDirPerm); err != nil {
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

	// Add siafiles sub directories
	d := &directory{
		staticSiaPath: modules.RootSiaPath(),
	}
	err = rt.renter.managedPushSubDirectories(d)
	if err != nil {
		t.Fatal(err)
	}

	// Heap should have a length of 4
	if rt.renter.directoryHeap.managedLen() != 4 {
		t.Fatal("Heap should have length of 4 but was", rt.renter.directoryHeap.managedLen())
	}

	// Pop off elements and confirm the are correct
	d = rt.renter.directoryHeap.managedPop()
	if !d.staticSiaPath.Equals(siaPath2) {
		t.Fatalf("Expected directory %v but found %v", siaPath2.String(), d.staticSiaPath.String())
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
	if !d.staticSiaPath.Equals(siaPath1) {
		t.Fatalf("Expected directory %v but found %v", siaPath1.String(), d.staticSiaPath.String())
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

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
	if err := rt.renter.CreateDir(siaPath1_1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath1_2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath2_1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(siaPath2_2, modules.DefaultDirPerm); err != nil {
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

	// Directory should be root/home/siafiles/SubDir2/SubDir2
	if !d.staticSiaPath.Equals(siaPath2_2) {
		t.Fatalf("Expected directory %v but found %v", siaPath2_2.String(), d.staticSiaPath.String())
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

	// Directory should be root/homes/siafiles/SubDir1/SubDir2
	if !d.staticSiaPath.Equals(siaPath1_2) {
		t.Fatalf("Expected directory %v but found %v", siaPath1_2.String(), d.staticSiaPath.String())
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

// TestDirectoryHeapHealth probes the directory managedHeapHealth method
func TestDirectoryHeapHealth(t *testing.T) {
	// Initiate directory struct
	aggregateRemoteHealth := float64(fastrand.Intn(100)) + 0.25
	aggregateHealth := aggregateRemoteHealth + 1
	remoteHealth := aggregateHealth + 1
	health := remoteHealth + 1
	d := directory{
		aggregateHealth:       aggregateHealth,
		aggregateRemoteHealth: aggregateRemoteHealth,
		health:                health,
		remoteHealth:          remoteHealth,
	}

	// Explored is false so aggregate values should return even though the
	// directory health's are worse. Even though the aggregateHealth is worse
	// the aggregateRemoteHealth should be returned as it is above the
	// RepairThreshold
	heapHealth, remote := d.managedHeapHealth()
	if !remote {
		t.Fatal("directory should be considered remote")
	}
	if heapHealth != d.aggregateRemoteHealth {
		t.Errorf("Expected heapHealth to be %v but was %v", d.aggregateRemoteHealth, heapHealth)
	}

	// Setting the aggregateRemoteHealth to 0 should make the aggregateHealth
	// value be returned
	d.aggregateRemoteHealth = 0
	heapHealth, remote = d.managedHeapHealth()
	if remote {
		t.Fatal("directory should not be considered remote")
	}
	if heapHealth != d.aggregateHealth {
		t.Errorf("Expected heapHealth to be %v but was %v", d.aggregateHealth, heapHealth)
	}

	// Setting the explored value to true should recreate the above to checks
	// but for the non aggregate values
	d.explored = true
	heapHealth, remote = d.managedHeapHealth()
	if !remote {
		t.Fatal("directory should be considered remote")
	}
	if heapHealth != d.remoteHealth {
		t.Errorf("Expected heapHealth to be %v but was %v", d.remoteHealth, heapHealth)
	}
	d.remoteHealth = 0
	heapHealth, remote = d.managedHeapHealth()
	if remote {
		t.Fatal("directory should not be considered remote")
	}
	if heapHealth != d.health {
		t.Errorf("Expected heapHealth to be %v but was %v", d.health, heapHealth)
	}
}
