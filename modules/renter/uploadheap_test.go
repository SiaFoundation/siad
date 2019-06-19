package renter

import (
	"math"
	"os"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
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
	defer rt.Close()

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
		rt.renter.staticWorkerPool.workers[string(i)] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Call managedBuildUnfinishedChunks as not stuck loop, all un stuck chunks
	// should be returned
	uucs := rt.renter.managedBuildUnfinishedChunks(f, hosts, targetUnstuckChunks, offline, goodForRenew)
	if len(uucs) != int(f.NumChunks())-1 {
		t.Fatalf("Incorrect number of chunks returned, expected %v got %v", int(f.NumChunks())-1, len(uucs))
	}
	for _, c := range uucs {
		if c.stuck {
			t.Fatal("Found stuck chunk when expecting only unstuck chunks")
		}
	}

	// Call managedBuildUnfinishedChunks as stuck loop, all stuck chunks should
	// be returned
	uucs = rt.renter.managedBuildUnfinishedChunks(f, hosts, targetStuckChunks, offline, goodForRenew)
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

	// Call managedBuildUnfinishedChunks as not stuck loop, since the file is
	// now not repairable it should return no chunks
	uucs = rt.renter.managedBuildUnfinishedChunks(f, hosts, targetUnstuckChunks, offline, goodForRenew)
	if len(uucs) != 0 {
		t.Fatalf("Incorrect number of chunks returned, expected 0 got %v", len(uucs))
	}

	// Call managedBuildUnfinishedChunks as stuck loop, all chunks should be
	// returned because they should have been marked as stuck by the previous
	// call and stuck chunks should still be returned if the file is not
	// repairable
	uucs = rt.renter.managedBuildUnfinishedChunks(f, hosts, targetStuckChunks, offline, goodForRenew)
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
	defer rt.Close()

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
		rt.renter.staticWorkerPool.workers[string(i)] = &worker{
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
	t.Parallel()

	// Create renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

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

// TestAddChunksToHeap probes the managedAddChunksToHeap method to ensure it is
// functioning as intended
func TestAddChunksToHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create File params
	_, rsc := testingFileParams()
	source, err := rt.createZeroByteFileOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	up := modules.FileUploadParams{
		Source:      source,
		ErasureCode: rsc,
	}

	// Create files in multiple directories
	var numChunks uint64
	var dirSiaPaths []modules.SiaPath
	names := []string{"rootFile", "subdir/File", "subdir2/file"}
	for _, name := range names {
		siaPath, err := modules.NewSiaPath(name)
		if err != nil {
			t.Fatal(err)
		}
		up.SiaPath = siaPath
		f, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), modules.SectorSize, 0777)
		if err != nil {
			t.Fatal(err)
		}
		// Track number of chunks
		numChunks += f.NumChunks()
		dirSiaPath, err := siaPath.Dir()
		if err != nil {
			t.Fatal(err)
		}
		// Make sure directories are created
		err = rt.renter.CreateDir(dirSiaPath)
		if err != nil && err != siadir.ErrPathOverload {
			t.Fatal(err)
		}
		dirSiaPaths = append(dirSiaPaths, dirSiaPath)
	}

	// Call bubbled to ensure directory metadata is updated
	for _, siaPath := range dirSiaPaths {
		err := rt.renter.managedBubbleMetadata(siaPath)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Manually add workers to worker pool and create host map
	hosts := make(map[string]struct{})
	for i := 0; i < rsc.MinPieces(); i++ {
		rt.renter.staticWorkerPool.workers[string(i)] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Make sure directory Heap is ready
	err = rt.renter.managedPushUnexploredDirectory(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// call managedAddChunksTo Heap
	siaPaths, err := rt.renter.managedAddChunksToHeap(hosts)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that all chunks from all the directories were added since there
	// are not enough chunks in only one directory to fill the heap
	if len(siaPaths) != 3 {
		t.Fatal("Expected 3 siaPaths to be returned, got", siaPaths)
	}
	if rt.renter.uploadHeap.managedLen() != int(numChunks) {
		t.Fatalf("Expected uploadHeap to have %v chunks but it has %v chunks", numChunks, rt.renter.uploadHeap.managedLen())
	}
}

// TestAddDirectoryBackToHeap ensures that when not all the chunks in a
// directory are added to the uploadHeap that the directory is added back to the
// directoryHeap with an updated Health
func TestAddDirectoryBackToHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter with interrupt dependency
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create file
	rsc, _ := siafile.NewRSCode(1, 1)
	siaPath, err := modules.NewSiaPath("test")
	if err != nil {
		t.Fatal(err)
	}
	source, err := rt.createZeroByteFileOnDisk()
	if err != nil {
		t.Fatal(err)
	}
	up := modules.FileUploadParams{
		Source:      source,
		SiaPath:     siaPath,
		ErasureCode: rsc,
	}
	f, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), modules.SectorSize, 0777)
	if err != nil {
		t.Fatal(err)
	}

	// Create maps for method inputs
	hosts := make(map[string]struct{})
	offline := make(map[string]bool)
	goodForRenew := make(map[string]bool)

	// Manually add workers to worker pool
	for i := 0; i < int(f.NumChunks()); i++ {
		rt.renter.staticWorkerPool.workers[string(i)] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Confirm we are starting with an empty upload and directory heap
	if rt.renter.uploadHeap.managedLen() != 0 {
		t.Fatal("Expected upload heap to be empty but has length of", rt.renter.uploadHeap.managedLen())
	}
	// "Empty" -> gets initialized with the root dir, therefore should have one
	// directory in it.
	if rt.renter.directoryHeap.managedLen() != 1 {
		t.Fatal("Expected directory heap to be empty but has length of", rt.renter.directoryHeap.managedLen())
	}
	// Reset the dir heap to clear the root dir out, rest of test wants an empty
	// heap.
	rt.renter.directoryHeap.managedReset()

	// Add chunks from file to uploadHeap
	rt.renter.managedBuildAndPushChunks([]*siafile.SiaFileSetEntry{f}, hosts, targetUnstuckChunks, offline, goodForRenew)

	// Upload heap should now have NumChunks chunks and directory heap should still be empty
	if rt.renter.uploadHeap.managedLen() != int(f.NumChunks()) {
		t.Fatalf("Expected upload heap to be of size %v but was %v", f.NumChunks(), rt.renter.uploadHeap.managedLen())
	}
	if rt.renter.directoryHeap.managedLen() != 0 {
		t.Fatal("Expected directory heap to be empty but has length of", rt.renter.directoryHeap.managedLen())
	}

	// Empty uploadHeap
	rt.renter.uploadHeap.managedReset()

	// Fill upload heap with chunks that are a worse health than the chunks in
	// the file
	var i uint64
	for rt.renter.uploadHeap.managedLen() < maxUploadHeapChunks {
		chunk := &unfinishedUploadChunk{
			id: uploadChunkID{
				fileUID: "chunk",
				index:   i,
			},
			stuck:           false,
			piecesCompleted: -1,
			piecesNeeded:    1,
		}
		if !rt.renter.uploadHeap.managedPush(chunk) {
			t.Fatal("Chunk should have been added to heap")
		}
		i++
	}

	// Record length of upload heap
	uploadHeapLen := rt.renter.uploadHeap.managedLen()

	// Try and add chunks to upload heap again
	rt.renter.managedBuildAndPushChunks([]*siafile.SiaFileSetEntry{f}, hosts, targetUnstuckChunks, offline, goodForRenew)

	// No chunks should have been added to the upload heap
	if rt.renter.uploadHeap.managedLen() != uploadHeapLen {
		t.Fatalf("Expected upload heap to be of size %v but was %v", uploadHeapLen, rt.renter.uploadHeap.managedLen())
	}
	// There should be one directory in the directory heap now
	if rt.renter.directoryHeap.managedLen() != 1 {
		t.Fatal("Expected directory heap to have 1 element but has length of", rt.renter.directoryHeap.managedLen())
	}
	// The directory should be marked as explored
	d := rt.renter.directoryHeap.managedPop()
	if !d.explored {
		t.Fatal("Directory should be explored")
	}
	// The directory should be the root directory as that is where we created
	// the test file
	if !d.siaPath.Equals(modules.RootSiaPath()) {
		t.Fatal("Expected Directory siapath to be the root siaPath but was", d.siaPath.String())
	}
	// The directory health should be that of the file since none of the chunks
	// were added
	health, _, _ := f.Health(offline, goodForRenew)
	if d.health != health {
		t.Fatalf("Expected directory health to be %v but was %v", health, d.health)
	}
}
