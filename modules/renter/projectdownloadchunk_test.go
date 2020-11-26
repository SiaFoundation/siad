package renter

import (
	"bytes"
	"context"
	"math"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAddCostPenalty is a unit test that covers the `addCostPenalty` helper
// function.
func TestAddCostPenalty(t *testing.T) {
	// verify overflow
	jt := time.Duration(1)
	jc := types.NewCurrency64(math.MaxUint64).Mul64(10)
	pricePerMS := types.NewCurrency64(2)
	jt = addCostPenalty(jt, jc, pricePerMS)
	if jt != time.Duration(math.MaxInt64) {
		t.Error("Expected job time to be adjusted to MaxInt64 on overflow")
	}

	// verify penalty higher than MaxInt64
	jt = time.Duration(1)
	jc = types.NewCurrency64(math.MaxInt64).Add64(1)
	pricePerMS = types.NewCurrency64(1)
	jt = addCostPenalty(jt, jc, pricePerMS)
	if jt != time.Duration(math.MaxInt64) {
		t.Error("Expected job time to be adjusted to MaxInt64 when penalty exceeds MaxInt64")
	}

	// verify high job time overflowing after adding penalty
	jc = types.NewCurrency64(10)
	pricePerMS = types.NewCurrency64(1)   // penalty is 10
	jt = time.Duration(math.MaxInt64 - 5) // job time + penalty exceeds MaxInt64
	jt = addCostPenalty(jt, jc, pricePerMS)
	if jt != time.Duration(math.MaxInt64) {
		t.Error("Expected job time to be adjusted to MaxInt64 when job time + penalty exceeds MaxInt64")
	}

	// verify happy case
	jt = time.Duration(fastrand.Intn(10) + 1)
	jc = types.NewCurrency64(fastrand.Uint64n(100) + 1)
	pricePerMS = types.NewCurrency64(fastrand.Uint64n(10) + 1)
	adjusted := addCostPenalty(jt, jc, pricePerMS)
	if adjusted <= jt {
		t.Error("unexpected")
	}

	// verify we assert pricePerMS to be higher than zero
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected a panic when pricePerMS is zero")
		}
	}()
	addCostPenalty(jt, jc, types.ZeroCurrency)
}

// TestProjectDownloadChunkFinalize is a unit test for the 'finalize' function
// on the pdc. It verifies whether the returned data is properly offset to
// include only the pieces requested by the user.
func TestProjectDownloadChunkFinalize(t *testing.T) {
	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)

	// create an EC and a passhtrough cipher key
	ec := modules.NewRSCodeDefault()
	ck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// RS encode the data
	pieces, err := ec.Encode(sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// create PCWS manually
	pcws := &projectChunkWorkerSet{
		staticChunkIndex:   0,
		staticErasureCoder: ec,
		staticMasterKey:    ck,
		staticPieceRoots:   []crypto.Hash{sectorRoot},

		staticCtx:    context.Background(),
		staticRenter: new(Renter),
	}

	// select a random number of segments to read at random offset
	numSegments := fastrand.Uint64n(5) + 1
	totalSegments := modules.SectorSize / crypto.SegmentSize
	offset := fastrand.Uint64n(totalSegments-numSegments+1) * crypto.SegmentSize
	length := numSegments * crypto.SegmentSize
	pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)

	// create PDC manually
	responseChan := make(chan *downloadResponse, 1)
	pdc := &projectDownloadChunk{
		chunkOffset: offset,
		chunkLength: length,

		pieceOffset: pieceOffset,
		pieceLength: pieceLength,

		dataPieces: pieces,

		downloadResponseChan: responseChan,
		workerSet:            pcws,
	}

	// call finalize
	pdc.finalize()

	// verify the download
	downloadResponse := <-responseChan
	if downloadResponse.err != nil {
		t.Fatal("unexpected error", downloadResponse.err)
	}
	if !bytes.Equal(downloadResponse.data, sectorData[offset:offset+length]) {
		t.Log(downloadResponse.data)
		t.Log(sectorData[offset : offset+length])
		t.Fatal("unexpected data")
	}
}

// TestProjectDownloadChunkFinished is a unit test for the 'finished' function
// on the pdc. It verifies whether the hopeful and completed pieces are properly
// counted and whether the return values are correct.
func TestProjectDownloadChunkFinished(t *testing.T) {
	// create an EC
	ec, err := modules.NewRSCode(3, 9)
	if err != nil {
		t.Fatal(err)
	}

	// create a passhtrough cipher key
	ck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// create PCWS manually
	pcws := &projectChunkWorkerSet{
		staticChunkIndex:   0,
		staticErasureCoder: ec,
		staticMasterKey:    ck,
		staticPieceRoots:   []crypto.Hash{},

		staticCtx:    context.Background(),
		staticRenter: new(Renter),
	}

	// create PDC manually - only the essentials
	pdc := &projectDownloadChunk{workerSet: pcws}

	// mock unresolved state with hope of successful download
	pdc.availablePieces = make([][]pieceDownload, 0)
	pdc.workersRemaining = 4
	finished, err := pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock one completed piece - still unresolved and hopeful
	pdc.workersRemaining = 3
	pdc.availablePieces = append(pdc.availablePieces, []pieceDownload{{completed: true}})
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock resolved state - not hopeful and not finished
	pdc.workersRemaining = 0
	finished, err = pdc.finished()
	if err != errNotEnoughPieces {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock resolves state - add 3 pieces in limbo -> hopeful again
	pdc.availablePieces = append(pdc.availablePieces, []pieceDownload{{}})
	pdc.availablePieces = append(pdc.availablePieces, []pieceDownload{{}})
	pdc.availablePieces = append(pdc.availablePieces, []pieceDownload{{}})
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock two failures -> hope gone again
	pdc.availablePieces[1][0].failed = true
	pdc.availablePieces[2][0].failed = true
	finished, err = pdc.finished()
	if err != errNotEnoughPieces {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// undo one failure and add 2 completed -> finished
	pdc.availablePieces[2][0].failed = false
	pdc.availablePieces[2][0].completed = true
	pdc.availablePieces[3][0].completed = true
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if !finished {
		t.Fatal("unexpected")
	}
}
