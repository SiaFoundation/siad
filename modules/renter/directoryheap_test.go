package renter

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestDirectoryHeap probes the directory heap implementation
func TestDirectoryHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create renter
	rt, err := newRenterTester(t.Name())
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

	// Reset Direcotry heap
	err = rt.renter.managedResetDirectoryHeap()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the heap has a length of 1
	if rt.renter.directoryHeap.managedLen() != 1 {
		t.Fatal("heap should have a length of 1 but has length of", rt.renter.directoryHeap.managedLen())
	}

	// Pop off top element. It should be an unexplored root
	d = rt.renter.directoryHeap.managedPop()
	if !d.siaPath.Equals(modules.RootSiaPath()) {
		t.Fatalf("Expected siapath to be '%v' but was '%v'", modules.RootSiaPath(), d.siaPath)
	}
	if d.explored {
		t.Fatal("Expected root directory to be unexplored")
	}
}
