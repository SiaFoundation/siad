package renter

import (
	"testing"
	"time"
)

// TestEstimateTimeUntilComplete is a unit test that probes
// 'estimateTimeUntilComplete'
func TestEstimateTimeUntilComplete(t *testing.T) {
	t.Parallel()

	// took 100ms for the chunk to become available, using default Skynet EC
	// params
	timeUntilAvail := time.Duration(100 * time.Millisecond)
	minPieces := 1
	numPieces := 10

	timeUntilComplete := estimateTimeUntilComplete(timeUntilAvail, minPieces, numPieces)
	if timeUntilComplete.Milliseconds() != 990 {
		t.Fatal("unexpected", timeUntilComplete)
	}

	// took 200ms for the chunk to become available, using default Renter EC
	// params
	timeUntilAvail = time.Duration(200 * time.Millisecond)
	minPieces = 10
	numPieces = 30

	timeUntilComplete = estimateTimeUntilComplete(timeUntilAvail, minPieces, numPieces)
	if timeUntilComplete.Milliseconds() != 440 {
		t.Fatal("unexpected", timeUntilComplete)
	}

	// took 200ms for the chunk to become available, using custom Renter EC
	// params
	timeUntilAvail = time.Duration(200 * time.Millisecond)
	minPieces = 64
	numPieces = 96
	timeUntilComplete = estimateTimeUntilComplete(timeUntilAvail, minPieces, numPieces)
	if timeUntilComplete.Milliseconds() != 110 {
		t.Fatal("unexpected", timeUntilComplete)
	}
}
