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

	// Add directories to heap
	for i := 0; i <= 5; i++ {
		siaPath, err := modules.NewSiaPath(fmt.Sprint(i))
		if err != nil {
			t.Fatal(err)
		}
		d := &directory{
			health:  float64(i),
			siaPath: siaPath,
		}
		if !rt.renter.directoryHeap.managedPush(d) {
			t.Fatal("directory not added")
		}
		// For the last element, mark it as explored
		if i == 5 {
			siaPath, err = modules.NewSiaPath(fmt.Sprint(i) + "explored")
			if err != nil {
				t.Fatal(err)
			}
			d = &directory{
				explored: true,
				health:   float64(i),
				siaPath:  siaPath,
			}
			if !rt.renter.directoryHeap.managedPush(d) {
				t.Fatal("directory not added")
			}
		}
	}

	// Confirm all elements added
	if rt.renter.directoryHeap.managedLen() != 7 {
		t.Fatal("heap should have length of 7 but was", rt.renter.directoryHeap.managedLen())
	}

	// Pop off top element, should have a health of 5 and it should be explored
	d := rt.renter.directoryHeap.managedPop()
	if d.health != float64(5) {
		t.Fatal("Expected Health of 5, got", d.health)
	}
	if !d.explored {
		t.Fatal("Expected the directory to be explored")
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
