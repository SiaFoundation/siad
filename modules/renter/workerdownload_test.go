package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSegmentsForRecovery tests the segmentsForRecovery helper function.
func TestSegmentsForRecovery(t *testing.T) {
	// Test the legacy erasure coder first.
	rscOld, err := siafile.NewRSCode(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	offset := fastrand.Intn(100)
	length := fastrand.Intn(100)
	startSeg, numSeg := segmentsForRecovery(uint64(offset), uint64(length), rscOld)
	if startSeg != 0 || numSeg != modules.SectorSize/crypto.SegmentSize {
		t.Fatal("segmentsForRecovery failed for legacy erasure coder")
	}

	// Get a new erasure coder and decoded segment size.
	rsc, err := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		t.Fatal(err)
	}

	// Define a function for easier testing.
	assert := func(offset, length, expectedStartSeg, expectedNumSeg uint64) {
		startSeg, numSeg := segmentsForRecovery(offset, length, rsc)
		if startSeg != expectedStartSeg {
			t.Fatalf("wrong startSeg: expected %v but was %v", expectedStartSeg, startSeg)
		}
		if numSeg != expectedNumSeg {
			t.Fatalf("wrong numSeg: expected %v but was %v", expectedNumSeg, numSeg)
		}
	}

	// Test edge cases within the first segment.
	assert(0, 640, 0, 1)
	assert(1, 639, 0, 1)
	assert(639, 1, 0, 1)
	assert(1, 639, 0, 1)

	// Same lengths but different offset.
	assert(640, 640, 1, 1)
	assert(641, 639, 1, 1)
	assert(1279, 1, 1, 1)
	assert(641, 639, 1, 1)

	// Test fetching 2 segments.
	assert(0, 641, 0, 2)
	assert(1, 640, 0, 2)
	assert(640, 641, 1, 2)
	assert(641, 640, 1, 2)

	// Test fetching 3 segments.
	assert(0, 1281, 0, 3)
	assert(1, 1280, 0, 3)
	assert(1, 1281, 0, 3)
	assert(640, 1281, 1, 3)
	assert(641, 1280, 1, 3)
}

// TestSectorOffsetAndLength tests the sectorOffsetAndLength helper function.
func TestSectorOffsetAndLength(t *testing.T) {
	// Test the legacy erasure coder first.
	rscOld, err := siafile.NewRSCode(10, 20)
	if err != nil {
		t.Fatal(err)
	}
	offset := fastrand.Intn(100)
	length := fastrand.Intn(100)
	startSeg, numSeg := sectorOffsetAndLength(uint64(offset), uint64(length), rscOld)
	if startSeg != 0 || numSeg != modules.SectorSize {
		t.Fatal("sectorOffsetAndLength failed for legacy erasure coder")
	}

	// Get a new erasure coder and decoded segment size.
	rsc, err := siafile.NewRSSubCode(10, 20, crypto.SegmentSize)
	if err != nil {
		t.Fatal(err)
	}

	// Define a function for easier testing.
	assert := func(offset, length, expectedOffset, expectedLength uint64) {
		o, l := sectorOffsetAndLength(offset, length, rsc)
		if o != expectedOffset {
			t.Fatalf("wrong offset: expected %v but was %v", expectedOffset, o)
		}
		if l != expectedLength {
			t.Fatalf("wrong length: expected %v but was %v", expectedLength, l)
		}
	}

	// Test edge cases within the first segment.
	assert(0, 640, 0, 64)
	assert(1, 639, 0, 64)
	assert(639, 1, 0, 64)
	assert(1, 639, 0, 64)

	// Same lengths but different offset.
	assert(640, 640, 64, 64)
	assert(641, 639, 64, 64)
	assert(1279, 1, 64, 64)
	assert(641, 639, 64, 64)

	// Test fetching 2 segments.
	assert(0, 641, 0, 128)
	assert(1, 640, 0, 128)
	assert(640, 641, 64, 128)
	assert(641, 640, 64, 128)

	// Test fetching 3 segments.
	assert(0, 1281, 0, 192)
	assert(1, 1280, 0, 192)
	assert(1, 1281, 0, 192)
	assert(640, 1281, 64, 192)
	assert(641, 1280, 64, 192)
}
