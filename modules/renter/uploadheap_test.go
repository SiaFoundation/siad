package renter

import (
	"encoding/hex"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestBuildUnfinishedChunks probes buildUnfinishedChunks to make sure that the
// correct chunks are being added to the heap
func TestBuildUnfinishedChunks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create file with more than 1 chunk and mark the first chunk at stuck
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     "stuckFile",
		ErasureCode: rsc,
	}
	f, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 10e3, 0777)
	if err != nil {
		t.Fatal(err)
	}
	if f.NumChunks() <= 1 {
		t.Fatalf("File created with not enough chunks for test, have %v need at least 2", f.NumChunks())
	}
	if err = f.SetStuck(uint64(0), true); err != nil {
		t.Fatal(err)
	}

	// Manually add workers to worker pool and create host map
	hosts := make(map[string]struct{})
	for i := 0; i < int(f.NumChunks()); i++ {
		rt.renter.workerPool[types.FileContractID{byte(i)}] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Call buildUnfinishedChunks as not stuck loop, all un stuck chunks should be returned
	uucs := rt.renter.buildUnfinishedChunks(f.CopyEntry(int(f.NumChunks())), hosts, targetUnstuckChunks)
	if len(uucs) != int(f.NumChunks())-1 {
		t.Fatalf("Incorrect number of chunks returned, expected %v got %v", int(f.NumChunks())-1, len(uucs))
	}
	for _, c := range uucs {
		if c.stuck {
			t.Fatal("Found stuck chunk when expecting only unstuck chunks")
		}
	}

	// Call buildUnfinishedChunks as stuck loop, all stuck chunks should be returned
	uucs = rt.renter.buildUnfinishedChunks(f.CopyEntry(int(f.NumChunks())), hosts, targetStuckChunks)
	if len(uucs) != 1 {
		t.Fatalf("Incorrect number of chunks returned, expected 1 got %v", len(uucs))
	}
	for _, c := range uucs {
		if !c.stuck {
			t.Fatal("Found unstuck chunk when expecting only stuck chunks")
		}
	}
}

// TestBuildChunkHeap probes managedBuildChunkHeap to make sure that the correct
// chunks are being added to the heap
func TestBuildChunkHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create 2 files
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     "testfile-" + hex.EncodeToString(fastrand.Bytes(8)),
		ErasureCode: rsc,
	}
	f1, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 10e3, 0777)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath = "testfile-" + hex.EncodeToString(fastrand.Bytes(8))
	f2, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 10e3, 0777)
	if err != nil {
		t.Fatal(err)
	}

	// Manually add workers to worker pool and create host map
	hosts := make(map[string]struct{})
	for i := 0; i < int(f1.NumChunks()+f2.NumChunks()); i++ {
		rt.renter.workerPool[types.FileContractID{byte(i)}] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Call managedBuildChunkHeap as not stuck loop, the heap should have a
	// length equal to the number of chunks in both of the files
	rt.renter.managedBuildChunkHeap("", hosts, targetUnstuckChunks)
	if rt.renter.uploadHeap.managedLen() != int(f1.NumChunks()+f2.NumChunks()) {
		t.Fatalf("Expected heap length of %v but got %v", int(f1.NumChunks()+f2.NumChunks()), rt.renter.uploadHeap.managedLen())
	}

	// Pop all chunks off and confirm they are not stuck and not marked as
	// stuckRepair
	chunk := rt.renter.uploadHeap.managedPop()
	for chunk != nil {
		if chunk.stuck || chunk.stuckRepair {
			t.Log("Stuck:", chunk.stuck)
			t.Log("StuckRepair:", chunk.stuckRepair)
			t.Fatal("Chunk has incorrect stuck fields")
		}
		chunk = rt.renter.uploadHeap.managedPop()
	}

	// Reset upload heap
	rt.renter.uploadHeap.heapChunks = make(map[uploadChunkID]struct{})
	rt.renter.uploadHeap.heap = uploadChunkHeap{}

	// Set the first file's RecentRepairTime to now
	if err := f1.UpdateRecentRepairTime(); err != nil {
		t.Fatal(err)
	}

	// Call managedBuildChunkHeap as not stuck loop, the heap should have a
	// length equal to the number of chunks in only the second file
	rt.renter.managedBuildChunkHeap("", hosts, targetUnstuckChunks)
	if rt.renter.uploadHeap.managedLen() != int(f2.NumChunks()) {
		t.Fatalf("Expected heap length of %v but got %v", int(f2.NumChunks()), rt.renter.uploadHeap.managedLen())
	}

	// Pop all chunks off and confirm they are not stuck and not marked as
	// stuckRepair
	chunk = rt.renter.uploadHeap.managedPop()
	for chunk != nil {
		if chunk.stuck || chunk.stuckRepair {
			t.Log("Stuck:", chunk.stuck)
			t.Log("StuckRepair:", chunk.stuckRepair)
			t.Fatal("Chunk has incorrect stuck fields")
		}
		chunk = rt.renter.uploadHeap.managedPop()
	}

	// Reset upload heap
	rt.renter.uploadHeap.heapChunks = make(map[uploadChunkID]struct{})
	rt.renter.uploadHeap.heap = uploadChunkHeap{}

	// Mark both files as stuck
	if err := f1.MarkAllChunksAsStuck(); err != nil {
		t.Fatal(err)
	}
	if err := f2.MarkAllChunksAsStuck(); err != nil {
		t.Fatal(err)
	}

	// Call managedBuildChunkHeap as stuck loop, the heap should have a length
	// of 1 because only 1 chunk should be added to the heap from the stuck loop
	rt.renter.managedBuildChunkHeap("", hosts, targetStuckChunks)
	if rt.renter.uploadHeap.managedLen() != 1 {
		t.Fatalf("Expected heap length of %v but got %v", 1, rt.renter.uploadHeap.managedLen())
	}

	// Pop all chunks off and confirm they are stuck and marked as stuckRepair
	chunk = rt.renter.uploadHeap.managedPop()
	for chunk != nil {
		if !chunk.stuck || !chunk.stuckRepair {
			t.Log("Stuck:", chunk.stuck)
			t.Log("StuckRepair:", chunk.stuckRepair)
			t.Fatal("Chunk has incorrect stuck fields")
		}
		chunk = rt.renter.uploadHeap.managedPop()
	}
}
