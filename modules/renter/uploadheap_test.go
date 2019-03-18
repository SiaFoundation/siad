package renter

import (
	"encoding/hex"
	"fmt"
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

	// Add 1 piece to each chunk
	hosts := make(map[string]struct{})
	offline := make(map[string]bool)
	goodForRenew := make(map[string]bool)
	for i := uint64(0); i < f.NumChunks(); i++ {
		host := fmt.Sprintln("host", i)
		spk := types.SiaPublicKey{}
		spk.LoadString(host)
		hosts[spk.String()] = struct{}{}
		goodForRenew[spk.String()] = true
		offline[spk.String()] = false
		if err := f.AddPiece(spk, i, 0, crypto.Hash{}); err != nil {
			t.Fatal(err)
		}
	}

	// Manually add workers to worker pool
	for i := 0; i < int(f.NumChunks()); i++ {
		rt.renter.workerPool[types.FileContractID{byte(i)}] = &worker{
			downloadChan: make(chan struct{}, 1),
			killChan:     make(chan struct{}),
			uploadChan:   make(chan struct{}, 1),
		}
	}

	// Call buildUnfinishedChunks as not stuck loop, all un stuck chunks should be returned
	uucs := rt.renter.buildUnfinishedChunks(f, hosts, targetUnstuckChunks, offline, goodForRenew)
	if len(uucs) != int(f.NumChunks())-1 {
		t.Fatalf("Incorrect number of chunks returned, expected %v got %v", int(f.NumChunks())-1, len(uucs))
	}
	for _, c := range uucs {
		if c.stuck {
			t.Fatal("Found stuck chunk when expecting only unstuck chunks")
		}
	}

	// Call buildUnfinishedChunks as stuck loop, all stuck chunks should be returned
	uucs = rt.renter.buildUnfinishedChunks(f, hosts, targetStuckChunks, offline, goodForRenew)
	if len(uucs) != 1 {
		t.Fatalf("Incorrect number of chunks returned, expected 1 got %v", len(uucs))
	}
	for _, c := range uucs {
		if !c.stuck {
			t.Fatal("Found unstuck chunk when expecting only stuck chunks")
		}
	}

	// Reset offline and goodForRenewMaps to make chunks seem not downloadable
	goodForRenew = make(map[string]bool)
	offline = make(map[string]bool)

	// Call buildUnfinishedChunks as not stuck loop, since the file is now not
	// downloadable it should return no chunks
	uucs = rt.renter.buildUnfinishedChunks(f, hosts, targetUnstuckChunks, offline, goodForRenew)
	if len(uucs) != 0 {
		t.Fatalf("Incorrect number of chunks returned, expected 0 got %v", len(uucs))
	}

	// Call buildUnfinishedChunks as stuck loop, all chunks should be returned
	// because they should have been marked as stuck by the previous call and
	// stuck chunks should still be returned if the file is not downloadable
	uucs = rt.renter.buildUnfinishedChunks(f, hosts, targetStuckChunks, offline, goodForRenew)
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

	// Call managedBuildChunkHeap as stuck loop, since there are no stuck chunks
	// there should be no chunks in the upload heap
	rt.renter.managedBuildChunkHeap("", hosts, targetStuckChunks)
	if rt.renter.uploadHeap.managedLen() != 0 {
		t.Fatalf("Expected heap length of %v but got %v", 0, rt.renter.uploadHeap.managedLen())
	}

	// Call managedBuildChunkHeap as not stuck loop, since we didn't upload the
	// files we created nor do we have contracts, all the chunks will be viewed
	// as not downloadable because they have a health of >1. Therefore we
	// shouldn't see any chunks in the heap
	rt.renter.managedBuildChunkHeap("", hosts, targetUnstuckChunks)
	if rt.renter.uploadHeap.managedLen() != 0 {
		t.Fatalf("Expected heap length of %v but got %v", 0, rt.renter.uploadHeap.managedLen())
	}

	// Call managedBuildChunkHeap again as the stuck loop, since the previous
	// call saw all the chunks as not downloadable it will have marked them as
	// stuck so we should now see one chunk in the heap
	rt.renter.managedBuildChunkHeap("", hosts, targetStuckChunks)
	if rt.renter.uploadHeap.managedLen() != 1 {
		t.Fatalf("Expected heap length of %v but got %v", 1, rt.renter.uploadHeap.managedLen())
	}

	// Pop all chunks off and confirm they are not stuck and not marked as
	// stuckRepair
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
