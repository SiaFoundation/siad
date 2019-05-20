package renter

import (
	"math"
	"os"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestBuildUnfinishedChunks probes buildUnfinishedChunks to make sure that the
// correct chunks are being added to the heap
func TestBuildUnfinishedChunks(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Create file on disk
	path, err := rt.createZeroByteFileOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	// Create file with more than 1 chunk and mark the first chunk at stuck
	rsc, _ := siafile.NewRSCode(1, 1)
	siaPath, err := modules.NewSiaPath("stuckFile")
	if err != nil {
		t.Fatal(err)
	}
	up := modules.FileUploadParams{
		Source:      path,
		SiaPath:     siaPath,
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

	// Create maps to pass into methods
	hosts := make(map[string]struct{})
	offline := make(map[string]bool)
	goodForRenew := make(map[string]bool)

	// Manually add workers to worker pool
	for i := 0; i < int(f.NumChunks()); i++ {
		rt.renter.workerPool[types.FileContractID{byte(i)}] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Call buildUnfinishedChunks as not stuck loop, all un stuck chunks should be returned
	id := rt.renter.mu.Lock()
	uucs := rt.renter.buildUnfinishedChunks(f, hosts, targetUnstuckChunks, offline, goodForRenew)
	rt.renter.mu.Unlock(id)
	if len(uucs) != int(f.NumChunks())-1 {
		t.Fatalf("Incorrect number of chunks returned, expected %v got %v", int(f.NumChunks())-1, len(uucs))
	}
	for _, c := range uucs {
		if c.stuck {
			t.Fatal("Found stuck chunk when expecting only unstuck chunks")
		}
	}

	// Call buildUnfinishedChunks as stuck loop, all stuck chunks should be returned
	id = rt.renter.mu.Lock()
	uucs = rt.renter.buildUnfinishedChunks(f, hosts, targetStuckChunks, offline, goodForRenew)
	rt.renter.mu.Unlock(id)
	if len(uucs) != 1 {
		t.Fatalf("Incorrect number of chunks returned, expected 1 got %v", len(uucs))
	}
	for _, c := range uucs {
		if !c.stuck {
			t.Fatal("Found unstuck chunk when expecting only stuck chunks")
		}
	}

	// Remove file on disk to make file not repairable
	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}

	// Call buildUnfinishedChunks as not stuck loop, since the file is now not
	// repairable it should return no chunks
	id = rt.renter.mu.Lock()
	uucs = rt.renter.buildUnfinishedChunks(f, hosts, targetUnstuckChunks, offline, goodForRenew)
	rt.renter.mu.Unlock(id)
	if len(uucs) != 0 {
		t.Fatalf("Incorrect number of chunks returned, expected 0 got %v", len(uucs))
	}

	// Call buildUnfinishedChunks as stuck loop, all chunks should be returned
	// because they should have been marked as stuck by the previous call and
	// stuck chunks should still be returned if the file is not repairable
	id = rt.renter.mu.Lock()
	uucs = rt.renter.buildUnfinishedChunks(f, hosts, targetStuckChunks, offline, goodForRenew)
	rt.renter.mu.Unlock(id)
	if len(uucs) != int(f.NumChunks()) {
		t.Fatalf("Incorrect number of chunks returned, expected %v got %v", f.NumChunks(), len(uucs))
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
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// Create 2 files
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	f1, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 10e3, 0777)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath = modules.RandomSiaPath()
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

	// Call managedBuildChunkHeap as stuck loop, since there are no stuck chunks
	// there should be no chunks in the upload heap
	rt.renter.managedBuildChunkHeap(modules.RootSiaPath(), hosts, targetStuckChunks)
	if rt.renter.uploadHeap.managedLen() != 0 {
		t.Fatalf("Expected heap length of %v but got %v", 0, rt.renter.uploadHeap.managedLen())
	}

	// Call managedBuildChunkHeap as not stuck loop, since we didn't upload the
	// files we created nor do we have contracts, all the chunks will be viewed
	// as not downloadable because they have a health of >1. Therefore we
	// shouldn't see any chunks in the heap
	rt.renter.managedBuildChunkHeap(modules.RootSiaPath(), hosts, targetUnstuckChunks)
	if rt.renter.uploadHeap.managedLen() != 0 {
		t.Fatalf("Expected heap length of %v but got %v", 0, rt.renter.uploadHeap.managedLen())
	}

	// Call managedBuildChunkHeap again as the stuck loop, since the previous
	// call saw all the chunks as not downloadable it will have marked them as
	// stuck.
	//
	// For the stuck loop managedBuildChunkHeap will randomly grab one chunk
	// from maxChunksInHeap files to add to the heap. There are two files
	// created in the test so we would expect 2 or maxStuckChunksInHeap,
	// whichever is less, chunks to be added to the heap
	rt.renter.managedBuildChunkHeap(modules.RootSiaPath(), hosts, targetStuckChunks)
	expectedChunks := math.Min(2, float64(maxStuckChunksInHeap))
	if rt.renter.uploadHeap.managedLen() != int(expectedChunks) {
		t.Fatalf("Expected heap length of %v but got %v", expectedChunks, rt.renter.uploadHeap.managedLen())
	}

	// Pop all chunks off and confirm they are stuck and marked as stuckRepair
	chunk := rt.renter.uploadHeap.managedPop()
	for chunk != nil {
		if !chunk.stuck || !chunk.stuckRepair {
			t.Log("Stuck:", chunk.stuck)
			t.Log("StuckRepair:", chunk.stuckRepair)
			t.Fatal("Chunk has incorrect stuck fields")
		}
		chunk = rt.renter.uploadHeap.managedPop()
	}
}

// TestUploadHeap probes the upload heap to make sure chunks are sorted
// correctly
func TestUploadHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Add chunks to heap. Chunks are prioritize by stuck status first and then
	// by piecesComplete/piecesNeeded
	//
	// Adding 2 stuck chunks then 2 unstuck chunks, each set has a chunk with 1
	// piece completed and 2 pieces completed. If the heap doesn't sort itself
	// then this would put an unstuck chunk with the highest completion at the
	// top of the heap which would be wrong
	chunk := &unfinishedUploadChunk{
		id: uploadChunkID{
			fileUID: "stuck",
			index:   1,
		},
		stuck:           true,
		piecesCompleted: 1,
		piecesNeeded:    1,
	}
	if !rt.renter.uploadHeap.managedPush(chunk) {
		t.Fatal("unable to push chunk", chunk)
	}
	chunk = &unfinishedUploadChunk{
		id: uploadChunkID{
			fileUID: "stuck",
			index:   2,
		},
		stuck:           true,
		piecesCompleted: 2,
		piecesNeeded:    1,
	}
	if !rt.renter.uploadHeap.managedPush(chunk) {
		t.Fatal("unable to push chunk", chunk)
	}
	chunk = &unfinishedUploadChunk{
		id: uploadChunkID{
			fileUID: "unstuck",
			index:   1,
		},
		stuck:           true,
		piecesCompleted: 1,
		piecesNeeded:    1,
	}
	if !rt.renter.uploadHeap.managedPush(chunk) {
		t.Fatal("unable to push chunk", chunk)
	}
	chunk = &unfinishedUploadChunk{
		id: uploadChunkID{
			fileUID: "unstuck",
			index:   2,
		},
		stuck:           true,
		piecesCompleted: 2,
		piecesNeeded:    1,
	}
	if !rt.renter.uploadHeap.managedPush(chunk) {
		t.Fatal("unable to push chunk", chunk)
	}

	chunk = rt.renter.uploadHeap.managedPop()
	if !chunk.stuck {
		t.Fatal("top chunk should be stuck")
	}
	if chunk.piecesCompleted != 1 {
		t.Fatal("top chunk should have the less amount of completed chunks")
	}
}
