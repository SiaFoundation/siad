package renter

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestSegmentsForRecovery tests the segmentsForRecovery helper function.
func TestSegmentsForRecovery(t *testing.T) {
	// Test the legacy erasure coder first.
	rscOld, err := modules.NewRSCode(10, 20)
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
	rsc, err := modules.NewRSSubCode(10, 20, 64)
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
	rscOld, err := modules.NewRSCode(10, 20)
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
	rsc, err := modules.NewRSSubCode(10, 20, 64)
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

// TestCheckDownloadGouging checks that the fetch backups price gouging
// checker is correctly detecting price gouging from a host.
func TestCheckDownloadGouging(t *testing.T) {
	oneCurrency := types.NewCurrency64(1)

	// minAllowance contains only the fields necessary to test the price gouging
	// function. The min allowance doesn't include any of the max prices,
	// because the function will ignore them if they are not set.
	minAllowance := modules.Allowance{
		// Funds is set such that the tests come out to an easy, round number.
		// One siacoin is multiplied by the number of elements that are checked
		// for gouging, and then divided by the gounging denominator.
		Funds: types.SiacoinPrecision.Mul64(3).Div64(downloadGougingFractionDenom).Sub(oneCurrency),

		ExpectedDownload: modules.StreamDownloadSize, // 1 stream download operation.
	}
	// minHostSettings contains only the fields necessary to test the price
	// gouging function.
	//
	// The cost is set to be exactly equal to the price gouging limit, such that
	// slightly decreasing any of the values evades the price gouging detector.
	minPriceTable := &modules.RPCPriceTable{
		ReadBaseCost:          types.SiacoinPrecision,
		ReadLengthCost:        types.SiacoinPrecision.Div64(modules.StreamDownloadSize),
		DownloadBandwidthCost: types.SiacoinPrecision.Div64(modules.StreamDownloadSize),
	}

	err := checkDownloadGouging(minAllowance, minPriceTable)
	if err == nil {
		t.Fatal("expecting price gouging check to fail:", err)
	}

	// Drop the host prices one field at a time.
	newPriceTable := minPriceTable
	newPriceTable.ReadBaseCost = minPriceTable.ReadBaseCost.Mul64(100).Div64(101)
	err = checkDownloadGouging(minAllowance, newPriceTable)
	if err != nil {
		t.Fatal(err)
	}
	newPriceTable = minPriceTable
	newPriceTable.DownloadBandwidthCost = minPriceTable.DownloadBandwidthCost.Mul64(100).Div64(101)
	err = checkDownloadGouging(minAllowance, newPriceTable)
	if err != nil {
		t.Fatal(err)
	}
	newPriceTable = minPriceTable
	newPriceTable.ReadLengthCost = minPriceTable.ReadLengthCost.Mul64(100).Div64(101)
	err = checkDownloadGouging(minAllowance, newPriceTable)
	if err != nil {
		t.Fatal(err)
	}

	// Set min settings on the allowance that are just below what should be
	// acceptable.
	maxAllowance := minAllowance
	maxAllowance.Funds = maxAllowance.Funds.Add(oneCurrency)
	maxAllowance.MaxRPCPrice = modules.MDMReadCost(minPriceTable, modules.StreamDownloadSize).Add(oneCurrency)
	maxAllowance.MaxContractPrice = oneCurrency
	maxAllowance.MaxDownloadBandwidthPrice = minPriceTable.DownloadBandwidthCost.Add(oneCurrency)
	maxAllowance.MaxStoragePrice = oneCurrency
	maxAllowance.MaxUploadBandwidthPrice = oneCurrency

	// The max allowance should have no issues with price gouging.
	err = checkDownloadGouging(maxAllowance, minPriceTable)
	if err != nil {
		t.Fatal(err)
	}

	// Should fail if the MaxRPCPrice is dropped.
	failAllowance := maxAllowance
	failAllowance.MaxRPCPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkDownloadGouging(failAllowance, minPriceTable)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}

	// Should fail if the MaxDownloadBandwidthPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxDownloadBandwidthPrice = minPriceTable.DownloadBandwidthCost.Sub(oneCurrency)
	err = checkDownloadGouging(failAllowance, minPriceTable)
	if err == nil {
		t.Fatal("expecting price gouging check to fail")
	}
}

// TestProcessDownloadChunk is a unit test for managedProcessDownloadChunk.
func TestProcessDownloadChunk(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a method to create a minimal valid chunk.
	rc, err := modules.NewRSSubCode(1, 1, crypto.SegmentSize)
	if err != nil {
		t.Fatal(err)
	}
	chunk := func() *unfinishedDownloadChunk {
		return &unfinishedDownloadChunk{
			erasureCode:      rc,
			piecesCompleted:  0,                            // no pieces completed
			failed:           false,                        // hasn't failed
			workersRemaining: rc.MinPieces(),               // one worker for each data piece remains
			completedPieces:  make([]bool, rc.NumPieces()), // no piece is completed
			pieceUsage:       make([]bool, rc.NumPieces()), // no piece in use
			staticChunkMap: map[string]downloadPieceInfo{ // worker has a piece
				wt.staticHostPubKey.String(): {},
			},
			download: &download{
				completeChan: make(chan struct{}),
			},
			staticMemoryManager: wt.renter.repairMemoryManager,
		}
	}

	// Valid and needed chunk.
	udc := chunk()
	c := wt.managedProcessDownloadChunk(udc)
	if c == nil {
		t.Fatal("c shouldn't be nil")
	}

	// Valid chunk but not needed.
	//
	// pieceTaken
	udc = chunk()
	pieceIndex := udc.staticChunkMap[wt.staticHostPubKey.String()].index
	udc.pieceUsage[pieceIndex] = true
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if len(udc.workersStandby) != 1 {
		t.Fatalf("expected 1 standby worker but got %v", len(udc.workersStandby))
	}
	// enough pieces in progress
	udc = chunk()
	udc.piecesRegistered = rc.MinPieces()
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if len(udc.workersStandby) != 1 {
		t.Fatalf("expected 1 standby worker but got %v", len(udc.workersStandby))
	}

	// helper to add jobs to the queue.
	addBlankJobs := func(n int) {
		for i := 0; i < n; i++ {
			j := wt.newJobReadSector(context.Background(), wt.staticJobLowPrioReadQueue, make(chan *jobReadResponse), categoryDownload, crypto.Hash{}, 0, 0)
			wt.staticJobLowPrioReadQueue.mu.Lock()
			wt.staticJobLowPrioReadQueue.jobs.PushBack(j)
			wt.staticJobLowPrioReadQueue.mu.Unlock()
		}
	}

	// Invalid chunk, not on cooldown.
	//
	// download complete
	queue := wt.staticJobLowPrioReadQueue
	udc = chunk()
	addBlankJobs(3)
	close(udc.download.completeChan)
	udc.download.err = errors.New("test error to prevent critical")
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 3 {
		t.Fatalf("expected 3 download chunks but got %v", queue.callLen())
	}
	// min pieces completed
	udc = chunk()
	udc.piecesCompleted = rc.MinPieces()
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 3 {
		t.Fatalf("expected 3 download chunk but got %v", queue.callLen())
	}
	// udc failed
	udc = chunk()
	udc.failed = true
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 3 {
		t.Fatalf("expected 3 download chunk but got %v", queue.callLen())
	}
	// insufficient number of workers remaining
	udc = chunk()
	udc.workersRemaining = udc.erasureCode.MinPieces() - 1
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != -1 {
		t.Fatalf("expected %v remaining workers but got %v", -1, udc.workersRemaining)
	}
	if queue.callLen() != 3 {
		t.Fatalf("expected 3 download chunk but got %v", queue.callLen())
	}
	// worker has no piece
	udc = chunk()
	udc.staticChunkMap = make(map[string]downloadPieceInfo)
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 3 {
		t.Fatalf("expected 3 download chunk but got %v", queue.callLen())
	}
	// piece is completed
	udc = chunk()
	udc.completedPieces[pieceIndex] = true
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 3 {
		t.Fatalf("expected 3 download chunk but got %v", queue.callLen())
	}
	// Invalid chunk, on cooldown.
	// download complete
	addBlankJobs(3)
	queue.mu.Lock()
	queue.consecutiveFailures = 100
	queue.cooldownUntil = cooldownUntil(queue.consecutiveFailures)
	queue.mu.Unlock()
	udc = chunk()
	close(udc.download.completeChan)
	udc.download.err = errors.New("test error to prevent critical")
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if queue.callLen() != 0 {
		t.Fatalf("expected 0 download chunk but got %v", queue.callLen())
	}
	// min pieces completed
	addBlankJobs(3)
	udc = chunk()
	udc.piecesCompleted = rc.MinPieces()
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 0 {
		t.Fatalf("expected 0 download chunk but got %v", queue.callLen())
	}
	// udc failed
	addBlankJobs(3)
	udc = chunk()
	udc.failed = true
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 0 {
		t.Fatalf("expected 0 download chunk but got %v", queue.callLen())
	}
	// insufficient number of workers remaining
	addBlankJobs(3)
	udc = chunk()
	udc.workersRemaining = udc.erasureCode.MinPieces() - 1
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != -1 {
		t.Fatalf("expected %v remaining workers but got %v", -1, udc.workersRemaining)
	}
	if queue.callLen() != 0 {
		t.Fatalf("expected 0 download chunk but got %v", queue.callLen())
	}
	// worker has no piece
	addBlankJobs(3)
	udc = chunk()
	udc.staticChunkMap = make(map[string]downloadPieceInfo)
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 0 {
		t.Fatalf("expected 0 download chunk but got %v", queue.callLen())
	}
	// piece is completed
	addBlankJobs(3)
	udc = chunk()
	udc.completedPieces[pieceIndex] = true
	c = wt.managedProcessDownloadChunk(udc)
	if c != nil {
		t.Fatal("c should be nil")
	}
	if udc.workersRemaining != udc.erasureCode.MinPieces()-1 {
		t.Fatalf("expected %v remaining workers but got %v", udc.erasureCode.MinPieces()-1, udc.workersRemaining)
	}
	if queue.callLen() != 0 {
		t.Fatalf("expected 0 download chunk but got %v", queue.callLen())
	}
}
