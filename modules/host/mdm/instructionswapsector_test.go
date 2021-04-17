package mdm

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestInstructionSwapSector tests executing a program with a single
// SwapSector instruction.
func TestInstructionSwapSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a storage obligation with some random sectors.
	numSectors := 10
	so := host.newTestStorageObligation(true)
	so.AddRandomSectors(numSectors)

	// Prepare a priceTable and duration.
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5)) // random since it doesn't matter for swap

	// Run basic case.
	t.Run("Basic", func(t *testing.T) {
		testInstructionSwapSectorBasic(t, mdm, uint64(numSectors), pt, duration, so)
	})
	// Run case for both offsets being the same.
	t.Run("SameSector", func(t *testing.T) {
		testInstructionSwapSectorBasic(t, mdm, uint64(numSectors), pt, duration, so)
	})
	// Run case for both offsets being the same.
	t.Run("OutOfBounds", func(t *testing.T) {
		testInstructionSwapSectorOutOfBounds(t, mdm, uint64(numSectors), pt, duration, so)
	})
	// Run case basic case but without requesting a proof.
	t.Run("NoProof", func(t *testing.T) {
		testInstructionSwapSectorNoProof(t, mdm, uint64(numSectors), pt, duration, so)
	})
}

// testInstructionSwapSectorBasic tests swapping 2 random sectors of a
// filecontract which are not the same.
func testInstructionSwapSectorBasic(t *testing.T, mdm *MDM, numSectors uint64, pt *modules.RPCPriceTable, duration types.BlockHeight, so *TestStorageObligation) {
	// Choose 2 random sectors to swap.
	i := fastrand.Uint64n(numSectors)
	j := fastrand.Uint64n(numSectors)
	for i == j {
		j = fastrand.Uint64n(numSectors) // just to be safe
	}

	ics := so.ContractSize()
	imr := so.MerkleRoot()
	oldRoots := append([]crypto.Hash{}, so.sectorRoots...)

	// Use a builder to build the program.
	tb := newTestProgramBuilder(pt, duration)
	tb.AddSwapSectorInstruction(i, j, true)

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, true)
	if err != nil {
		t.Fatal(err)
	}
	output := outputs[0]

	// Compute the expected proof.
	ranges := []crypto.ProofRange{
		{
			Start: i,
			End:   i + 1,
		},
		{
			Start: j,
			End:   j + 1,
		},
	}
	// Sort the ranges.
	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].Start < ranges[j].Start
	})
	expectedProof := crypto.MerkleDiffProof(ranges, uint64(len(oldRoots)), nil, oldRoots)
	oldLeafHashes := []crypto.Hash{oldRoots[ranges[0].Start], oldRoots[ranges[1].Start]}
	expectedOutput := encoding.Marshal(oldLeafHashes)

	// Compute the expected new root.
	newRoots := append([]crypto.Hash{}, oldRoots...)
	newRoots[i], newRoots[j] = newRoots[j], newRoots[i]
	nmr := cachedMerkleRoot(newRoots)

	// Make sure the new merkle root doesn't match the old one.
	if nmr == imr {
		t.Fatal("nmr shouldn't match imr")
	}

	// Assert the output.
	err = outputs[0].assert(ics, nmr, expectedProof, expectedOutput, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Get the leaf hashes.
	var leafHashes []crypto.Hash
	err = encoding.Unmarshal(output.Output, &leafHashes)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the proof, first by verifying the old merkle root.
	ok := crypto.VerifyDiffProof(ranges, uint64(len(oldRoots)), output.Proof, leafHashes, imr)
	if !ok {
		t.Fatal("failed to verify proof")
	}

	// ... then by modifying the leaves and verifying the new merkle root.
	leafHashes[0], leafHashes[1] = leafHashes[1], leafHashes[0]
	ok = crypto.VerifyDiffProof(ranges, uint64(len(oldRoots)), output.Proof, leafHashes, nmr)
	if !ok {
		t.Fatal("failed to verify proof")
	}

	// Make sure the sectors actually got swapped.
	if so.sectorRoots[i] != newRoots[i] {
		t.Fatal("sectors not swapped correctly")
	}
	if so.sectorRoots[j] != newRoots[j] {
		t.Fatal("sectors not swapped correctly")
	}
}

// testInstructionSwapSectorSameIndex tests swapping a random index with itself.
// While not particularly useful, it is supported and will provide a merkle
// proof that verifies the sector is part of the filecontract.
func testInstructionSwapSectorSameIndex(t *testing.T, mdm *MDM, numSectors uint64, pt *modules.RPCPriceTable, duration types.BlockHeight, so *TestStorageObligation) {
	// Choose a random index.
	i := fastrand.Uint64n(numSectors)
	j := i

	ics := so.ContractSize()
	imr := so.MerkleRoot()
	oldRoots := append([]crypto.Hash{}, so.sectorRoots...)

	// Use a builder to build the program.
	tb := newTestProgramBuilder(pt, duration)
	tb.AddSwapSectorInstruction(i, j, true)

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, true)
	if err != nil {
		t.Fatal(err)
	}
	output := outputs[0]

	// Compute the expected proof.
	ranges := []crypto.ProofRange{
		{
			Start: i,
			End:   i + 1,
		},
	}
	expectedProof := crypto.MerkleDiffProof(ranges, uint64(len(oldRoots)), nil, oldRoots)
	oldLeafHashes := []crypto.Hash{oldRoots[ranges[0].Start]}
	expectedOutput := encoding.Marshal(oldLeafHashes)

	// Assert the output.
	err = outputs[0].assert(ics, imr, expectedProof, expectedOutput, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Get the leaf hashes.
	var leafHashes []crypto.Hash
	err = encoding.Unmarshal(output.Output, &leafHashes)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the proof by verifying the old root. The new root is the same as
	// the old one so there is nothing to verify after this.
	ok := crypto.VerifyDiffProof(ranges, uint64(len(oldRoots)), output.Proof, leafHashes, imr)
	if !ok {
		t.Fatal("failed to verify proof")
	}

	// Check that the sector root remained the same.
	if so.sectorRoots[i] != oldRoots[i] {
		t.Fatal("sectors not swapped correctly")
	}
}

// testInstructionSwapSectorOutOfBounds tests that specifying invalid indices
// causes the execution to fail.
func testInstructionSwapSectorOutOfBounds(t *testing.T, mdm *MDM, numSectors uint64, pt *modules.RPCPriceTable, duration types.BlockHeight, so *TestStorageObligation) {
	// i is random and valid but j is out of bounds.
	i := fastrand.Uint64n(numSectors)
	j := numSectors

	// Use a builder to build the program.
	tb := newTestProgramBuilder(pt, duration)
	tb.AddSwapSectorInstruction(i, j, true)

	// Execute it.
	_, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, true)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("idx2 out-of-bounds: %v >= %v", numSectors, numSectors)) {
		t.Fatal("expected execution to fail with out of bounds error", err)
	}

	// j is random and valid but i is out of bounds.
	i = numSectors
	j = fastrand.Uint64n(numSectors)

	// Use a builder to build the program.
	tb = newTestProgramBuilder(pt, duration)
	tb.AddSwapSectorInstruction(i, j, true)

	// Execute it.
	_, err = mdm.ExecuteProgramWithBuilder(tb, so, duration, true)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("idx2 out-of-bounds: %v >= %v", numSectors, numSectors)) {
		t.Fatal("expected execution to fail with out of bounds error", err)
	}

	// both are out of bounds.
	i = numSectors
	j = i

	// Use a builder to build the program.
	tb = newTestProgramBuilder(pt, duration)
	tb.AddSwapSectorInstruction(i, j, true)

	// Execute it. The error message
	_, err = mdm.ExecuteProgramWithBuilder(tb, so, duration, true)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("idx1 out-of-bounds: %v >= %v", numSectors, numSectors)) {
		t.Fatal("expected execution to fail with out of bounds error", err)
	}
}

// testInstructionSwapSectorBasic tests swapping 2 random sectors of a
// filecontract which are not the same.
func testInstructionSwapSectorNoProof(t *testing.T, mdm *MDM, numSectors uint64, pt *modules.RPCPriceTable, duration types.BlockHeight, so *TestStorageObligation) {
	// Choose 2 random sectors to swap.
	i := fastrand.Uint64n(numSectors)
	j := fastrand.Uint64n(numSectors)
	for i == j {
		j = fastrand.Uint64n(numSectors) // just to be safe
	}

	ics := so.ContractSize()
	imr := so.MerkleRoot()
	oldRoots := append([]crypto.Hash{}, so.sectorRoots...)

	// Use a builder to build the program.
	tb := newTestProgramBuilder(pt, duration)
	tb.AddSwapSectorInstruction(i, j, false)

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute the expected new root.
	newRoots := append([]crypto.Hash{}, oldRoots...)
	newRoots[i], newRoots[j] = newRoots[j], newRoots[i]
	nmr := cachedMerkleRoot(newRoots)

	// Make sure the new merkle root doesn't match the old one.
	if nmr == imr {
		t.Fatal("nmr shouldn't match imr")
	}

	// Assert the output.
	err = outputs[0].assert(ics, nmr, []crypto.Hash{}, []byte{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the sectors actually got swapped.
	if so.sectorRoots[i] != newRoots[i] {
		t.Fatal("sectors not swapped correctly")
	}
	if so.sectorRoots[j] != newRoots[j] {
		t.Fatal("sectors not swapped correctly")
	}
}
