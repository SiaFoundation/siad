package renter

import (
	"strconv"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestStuckStack probes the implementation of the stuck stack
func TestStuckStack(t *testing.T) {
	stack := stuckStack{
		stack:    make([]modules.SiaPath, 0, maxSuccessfulStuckRepairFiles),
		siaPaths: make(map[modules.SiaPath]struct{}),
	}

	// Check stack initialized as expected
	if stack.managedLen() != 0 {
		t.Fatal("Expected length of 0 got", stack.managedLen())
	}

	// Create some SiaPaths to add to the stack
	sp1, _ := modules.NewSiaPath("siaPath1")
	sp2, _ := modules.NewSiaPath("siaPath2")

	// Test pushing 1 siapath onto stack
	stack.managedPush(sp1)
	if stack.managedLen() != 1 {
		t.Fatal("Expected length of 1 got", stack.managedLen())
	}
	siaPath := stack.managedPop()
	if !siaPath.Equals(sp1) {
		t.Log("siaPath:", siaPath)
		t.Log("sp1:", sp1)
		t.Fatal("SiaPaths not equal")
	}
	if stack.managedLen() != 0 {
		t.Fatal("Expected length of 0 got", stack.managedLen())
	}

	// Test adding multiple siaPaths to stack
	stack.managedPush(sp1)
	stack.managedPush(sp2)
	if stack.managedLen() != 2 {
		t.Fatal("Expected length of 2 got", stack.managedLen())
	}
	// Last siapath added should be returned
	siaPath = stack.managedPop()
	if !siaPath.Equals(sp2) {
		t.Log("siaPath:", siaPath)
		t.Log("sp2:", sp2)
		t.Fatal("SiaPaths not equal")
	}

	// Pushing first siapath again should result in moving it to the top
	stack.managedPush(sp2)
	stack.managedPush(sp1)
	if stack.managedLen() != 2 {
		t.Fatal("Expected length of 2 got", stack.managedLen())
	}
	siaPath = stack.managedPop()
	if !siaPath.Equals(sp1) {
		t.Log("siaPath:", siaPath)
		t.Log("sp1:", sp1)
		t.Fatal("SiaPaths not equal")
	}

	// Length should never exceed maxSuccessfulStuckRepairFiles
	for i := 0; i < 2*maxSuccessfulStuckRepairFiles; i++ {
		sp, _ := modules.NewSiaPath(strconv.Itoa(i))
		stack.managedPush(sp)
		if stack.managedLen() > maxSuccessfulStuckRepairFiles {
			t.Fatalf("Length exceeded %v, %v", maxSuccessfulStuckRepairFiles, stack.managedLen())
		}
	}
}
