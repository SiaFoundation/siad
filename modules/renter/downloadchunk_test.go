package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// TestRecoveredDataOffset tests the recoveredDataOffset helper function.
func TestRecoveredDataOffset(t *testing.T) {
	// Test the legacy erasure coder first.
	rscOld, err := siafile.NewRSCode(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	offset := fastrand.Intn(100)
	rdo := recoveredDataOffset(uint64(offset), rscOld)
	if rdo != uint64(offset) {
		t.Fatal("recoveredDataOffset failed for legacy erasure coder")
	}

	// Get a new erasure coder and decoded segment size.
	rsc, err := siafile.NewRSSubCode(10, 20, 64)
	if err != nil {
		t.Fatal(err)
	}

	// Define a function for easier testing.
	assert := func(offset, length, expectedOffset uint64) {
		o := recoveredDataOffset(offset, rsc)
		if o != expectedOffset {
			t.Fatalf("wrong offset: expected %v but was %v", expectedOffset, o)
		}
	}

	// Test edge cases within the first segment.
	assert(0, 640, 0)
	assert(1, 639, 1)
	assert(639, 1, 639)
	assert(1, 639, 1)

	// Same lengths but different offset.
	assert(640, 640, 0)
	assert(641, 639, 1)
	assert(1279, 1, 639)
	assert(641, 639, 1)

	// Test fetching 2 segments.
	assert(0, 641, 0)
	assert(1, 640, 1)
	assert(640, 641, 0)
	assert(641, 640, 1)

	// Test fetching 3 segments.
	assert(0, 1281, 0)
	assert(1, 1280, 1)
	assert(1, 1281, 1)
	assert(640, 1281, 0)
	assert(641, 1280, 1)
}

// TestBytesToRecover tests the bytesToRecover helper function.
func TestBytesToRecover(t *testing.T) {
	// Test the legacy erasure coder first.
	rscOld, err := siafile.NewRSCode(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	offset := fastrand.Intn(100)
	length := fastrand.Intn(100)
	chunkSize := fastrand.Intn(100)
	btr := bytesToRecover(uint64(offset), uint64(length), uint64(chunkSize), rscOld)
	if btr != uint64(chunkSize) {
		t.Fatal("bytesToRecover failed for legacy erasure coder")
	}

	// Get a new erasure coder and decoded segment size.
	rsc, err := siafile.NewRSSubCode(10, 20, 64)
	if err != nil {
		t.Fatal(err)
	}

	// Define a function for easier testing.
	assert := func(offset, length, expectedNumBytes uint64) {
		numBytes := bytesToRecover(offset, length, uint64(rsc.MinPieces())*modules.SectorSize, rsc)
		if numBytes != expectedNumBytes {
			t.Fatalf("wrong numBytes: expected %v but was %v", expectedNumBytes, numBytes)
		}
	}

	// Test edge cases within the first segment.
	assert(0, 640, 640)
	assert(1, 639, 640)
	assert(639, 1, 640)
	assert(1, 639, 640)

	// Same lengths but different offset.
	assert(640, 640, 640)
	assert(641, 639, 640)
	assert(1279, 1, 640)
	assert(641, 639, 640)

	// Test fetching 2 segments.
	assert(0, 641, 1280)
	assert(1, 640, 1280)
	assert(640, 641, 1280)
	assert(641, 640, 1280)

	// Test fetching 3 segments.
	assert(0, 1281, 1920)
	assert(1, 1280, 1920)
	assert(1, 1281, 1920)
	assert(640, 1281, 1920)
	assert(641, 1280, 1920)
}
