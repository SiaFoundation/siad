package renter

import (
	"bytes"
	"context"
	"errors"
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

// TestProjectDownloadChunkAdjustedReadDuration is a unit test for the
// 'adjustedReadDuration' function on the pdc.
func TestProjectDownloadChunkAdjustedReadDuration(t *testing.T) {
	t.Parallel()

	// mock a worker, ensure the readqueue returns a non zero time estimate
	worker := new(worker)
	worker.newPriceTable()
	worker.staticPriceTable().staticPriceTable = newDefaultPriceTable()
	worker.initJobReadQueue()
	worker.staticJobReadQueue.weightedJobTime64k = float64(time.Second)
	worker.staticJobReadQueue.weightedJobsCompleted64k = 10
	jrq := worker.staticJobReadQueue

	// fetch the expected job time for a 64kb download job, verify it's not 0
	jobTime := jrq.callExpectedJobTime(1 << 16)
	if jobTime == time.Duration(0) {
		t.Fatal("unexpected")
	}

	// mock a pdc with a 64kb piece length
	pdc := new(projectDownloadChunk)
	pdc.pieceLength = 1 << 16
	pdc.pricePerMS = types.SiacoinPrecision

	// verify the adjusted read duration adds a cost penalty
	duration := pdc.adjustedReadDuration(worker)
	if duration <= jobTime {
		t.Fatal("unexpected", duration, jobTime)
	}
}

// TestProjectDownloadChunkFinalize is a unit test for the 'finalize' function
// on the pdc. It verifies whether the returned data is properly offset to
// include only the pieces requested by the user.
func TestProjectDownloadChunkFinalize(t *testing.T) {
	t.Parallel()

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)

	// create an EC and a passhtrough cipher key
	ec := modules.NewRSSubCodeDefault()
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

	// download a random amount of data at random offset
	length := (fastrand.Uint64n(5) + 1) * crypto.SegmentSize
	offset := fastrand.Uint64n(modules.SectorSize - length)
	pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)

	// create PDC manually
	responseChan := make(chan *downloadResponse, 1)
	pdc := &projectDownloadChunk{
		offsetInChunk: offset,
		lengthInChunk: length,

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
		t.Log(downloadResponse.data, "length:", len(downloadResponse.data))
		t.Log(sectorData[offset:offset+length], "length:", len(sectorData[offset:offset+length]))
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
	pdc.availablePieces = make([][]*pieceDownload, 0)
	pdc.unresolvedWorkersRemaining = 4
	finished, err := pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock one completed piece - still unresolved and hopeful
	pdc.unresolvedWorkersRemaining = 3
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{completed: true}})
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock resolved state - not hopeful and not finished
	pdc.unresolvedWorkersRemaining = 0
	finished, err = pdc.finished()
	if err != errNotEnoughPieces {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock resolves state - add 3 pieces in limbo -> hopeful again
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{}})
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{}})
	pdc.availablePieces = append(pdc.availablePieces, []*pieceDownload{{}})
	finished, err = pdc.finished()
	if err != nil {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// mock two failures -> hope gone again
	pdc.availablePieces[1][0].completed = true
	pdc.availablePieces[1][0].downloadErr = errors.New("failed")
	pdc.availablePieces[2][0].completed = true
	pdc.availablePieces[2][0].downloadErr = errors.New("failed")
	finished, err = pdc.finished()
	if err != errNotEnoughPieces {
		t.Fatal("unexpected error", err)
	}
	if finished {
		t.Fatal("unexpected")
	}

	// undo one failure and add 2 completed -> finished
	pdc.availablePieces[2][0].downloadErr = nil
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

// TestProjectDownloadChunkLaunchWorker is a unit test for the 'launchWorker'
// function on the pdc.
func TestProjectDownloadChunkLaunchWorker(t *testing.T) {
	t.Parallel()

	ec := modules.NewRSCodeDefault()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(crypto.PublicKeySize),
	}

	// mock a worker, ensure the readqueue returns a non zero time estimate
	worker := new(worker)
	worker.initJobReadQueue()
	worker.staticJobReadQueue.weightedJobTime64k = float64(time.Second)
	worker.staticJobReadQueue.weightedJobsCompleted64k = 10
	worker.staticHostPubKeyStr = spk.String()

	// mock a pcws
	pcws := new(projectChunkWorkerSet)
	pcws.staticPieceRoots = make([]crypto.Hash, ec.NumPieces())

	// mock a pdc, ensure available pieces is not nil
	pdc := new(projectDownloadChunk)
	pdc.workerSet = pcws
	pdc.pieceLength = 1 << 16 // 64kb
	pdc.availablePieces = make([][]*pieceDownload, ec.NumPieces())
	for pieceIndex := range pdc.availablePieces {
		pdc.availablePieces[pieceIndex] = append(pdc.availablePieces[pieceIndex], &pieceDownload{
			worker: worker,
		})
	}

	// launch a worker and expect it to have enqueued a job and expect the
	// complete time to be somewhere in the future
	expectedCompleteTime, added := pdc.launchWorker(worker, 0)
	if !added {
		t.Fatal("unexpected")
	}
	if expectedCompleteTime.Before(time.Now()) {
		t.Fatal("unexpected")
	}

	// verify one worker was launched without failure
	numLWF := 0 // launchedWithoutFail
	for _, pieces := range pdc.availablePieces {
		launchedWithoutFail := false
		for _, pieceDownload := range pieces {
			if pieceDownload.launched && pieceDownload.downloadErr == nil {
				launchedWithoutFail = true
			}
		}
		if launchedWithoutFail {
			numLWF++
		}
	}
	if numLWF != 1 {
		t.Fatal("unexpected", numLWF)
	}

	// launch the worker again but kill the queue, expect it to have not added
	// the job to the queue and updated the pieceDownload's status to failed
	worker.staticJobReadQueue.killed = true
	_, added = pdc.launchWorker(worker, 0)
	if added {
		t.Fatal("unexpected")
	}
	numFailed := 0
	for _, pieces := range pdc.availablePieces {
		for _, pieceDownload := range pieces {
			if pieceDownload.downloadErr != nil {
				numFailed++
			}
		}
	}
	if numFailed != 1 {
		t.Fatal("unexpected", numFailed)
	}
}

// TestProjectDownloadChunkOverdriveStatus is a unit test for the
// 'overdriveStatus' function on the pdc.
func TestProjectDownloadChunkOverdriveStatus(t *testing.T) {
	t.Parallel()

	now := time.Now()

	pcws := new(projectChunkWorkerSet)
	pcws.staticErasureCoder = modules.NewRSCodeDefault()

	pdc := new(projectDownloadChunk)
	pdc.workerSet = pcws
	pdc.availablePieces = [][]*pieceDownload{
		{
			{expectedCompleteTime: now.Add(-1 * time.Minute)},
			{expectedCompleteTime: now.Add(-3 * time.Minute)},
		},
		{
			{expectedCompleteTime: now.Add(-2 * time.Minute)},
		},
	}

	// verify we return the correct amount of overdrive workers that need to be
	// launched if no pieces have launched yet, also verify last return time
	toLaunch, returnTime := pdc.overdriveStatus()
	if toLaunch != modules.RenterDefaultDataPieces {
		t.Fatal("unexpected")
	}
	if returnTime != (time.Time{}) {
		t.Fatal("unexpected", returnTime)
	}

	// launch a piece and verify we get 1 worker to launch due to the return
	// time being in the past
	pdc.availablePieces[0][0].launched = true
	toLaunch, returnTime = pdc.overdriveStatus()
	if toLaunch != 1 {
		t.Fatal("unexpected")
	}
	if returnTime != now.Add(-1*time.Minute) {
		t.Fatal("unexpected")
	}

	// add a piecedownload that returns somewhere in the future
	pdc.availablePieces[1] = append(pdc.availablePieces[1], &pieceDownload{
		launched:             true,
		expectedCompleteTime: now.Add(time.Minute),
	})
	toLaunch, returnTime = pdc.overdriveStatus()
	if toLaunch != 0 {
		t.Fatal("unexpected")
	}
	if returnTime != now.Add(time.Minute) {
		t.Fatal("unexpected")
	}
}
