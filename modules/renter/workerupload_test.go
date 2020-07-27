package renter

import (
	"math"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCheckUploadGouging checks that the upload price gouging checker is
// correctly detecting price gouging from a host.
func TestCheckUploadGouging(t *testing.T) {
	oneCurrency := types.NewCurrency64(1)

	// minAllowance contains only the fields necessary to test the price gouging
	// function. The min allowance doesn't include any of the max prices,
	// because the function will ignore them if they are not set.
	minAllowance := modules.Allowance{
		// Funds is set such that the tests come out to an easy, round number.
		// One siacoin is multiplied by the number of elements that are checked
		// for gouging, and then divided by the gounging denominator.
		Funds:  types.SiacoinPrecision.Mul64(4).Div64(uploadGougingFractionDenom).Sub(oneCurrency),
		Period: 1, // 1 block.

		ExpectedStorage: modules.StreamUploadSize, // 1 stream upload operation.
	}
	// minHostSettings contains only the fields necessary to test the price
	// gouging function.
	//
	// The cost is set to be exactly equal to the price gouging limit, such that
	// slightly decreasing any of the values evades the price gouging detector.
	minHostSettings := modules.HostExternalSettings{
		BaseRPCPrice:         types.SiacoinPrecision,
		SectorAccessPrice:    types.SiacoinPrecision,
		UploadBandwidthPrice: types.SiacoinPrecision.Div64(modules.StreamUploadSize),
		StoragePrice:         types.SiacoinPrecision.Div64(modules.StreamUploadSize),
	}

	err := checkUploadGouging(minAllowance, minHostSettings)
	if err == nil {
		t.Fatal("expecting price gouging check to fail:", err)
	}

	// Drop the host prices one field at a time.
	newHostSettings := minHostSettings
	newHostSettings.BaseRPCPrice = minHostSettings.BaseRPCPrice.Mul64(100).Div64(101)
	err = checkUploadGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.SectorAccessPrice = minHostSettings.SectorAccessPrice.Mul64(100).Div64(101)
	err = checkUploadGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.UploadBandwidthPrice = minHostSettings.UploadBandwidthPrice.Mul64(100).Div64(101)
	err = checkUploadGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}
	newHostSettings = minHostSettings
	newHostSettings.StoragePrice = minHostSettings.StoragePrice.Mul64(100).Div64(101)
	err = checkUploadGouging(minAllowance, newHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Set min settings on the allowance that are just below that should be
	// acceptable.
	maxAllowance := minAllowance
	maxAllowance.Funds = maxAllowance.Funds.Add(oneCurrency)
	maxAllowance.MaxRPCPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxContractPrice = oneCurrency
	maxAllowance.MaxDownloadBandwidthPrice = oneCurrency
	maxAllowance.MaxSectorAccessPrice = types.SiacoinPrecision.Add(oneCurrency)
	maxAllowance.MaxStoragePrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Add(oneCurrency)
	maxAllowance.MaxUploadBandwidthPrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Add(oneCurrency)

	// The max allowance should have no issues with price gouging.
	err = checkUploadGouging(maxAllowance, minHostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// Should fail if the MaxRPCPrice is dropped.
	failAllowance := maxAllowance
	failAllowance.MaxRPCPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkUploadGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Error("expecting price gouging check to fail")
	}

	// Should fail if the MaxSectorAccessPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxSectorAccessPrice = types.SiacoinPrecision.Sub(oneCurrency)
	err = checkUploadGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Error("expecting price gouging check to fail")
	}

	// Should fail if the MaxStoragePrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxStoragePrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Sub(oneCurrency)
	err = checkUploadGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Error("expecting price gouging check to fail")
	}

	// Should fail if the MaxUploadBandwidthPrice is dropped.
	failAllowance = maxAllowance
	failAllowance.MaxUploadBandwidthPrice = types.SiacoinPrecision.Div64(modules.StreamUploadSize).Sub(oneCurrency)
	err = checkUploadGouging(failAllowance, minHostSettings)
	if err == nil {
		t.Error("expecting price gouging check to fail")
	}
}

// TestProcessUploadChunk is a unit test for managedProcessUploadChunk.
func TestProcessUploadChunk(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// create worker.
	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableWorkerLoop{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close()

	// some vars for the test.
	pieces := 10

	// helper method to create a valid upload chunk.
	chunk := func() *unfinishedUploadChunk {
		return &unfinishedUploadChunk{
			unusedHosts: map[string]struct{}{
				wt.staticHostPubKey.String(): {},
			},
			piecesNeeded:      pieces,
			piecesCompleted:   0,
			piecesRegistered:  0,
			pieceUsage:        make([]bool, pieces),
			released:          true,
			workersRemaining:  1,
			physicalChunkData: make([][]byte, pieces),
			logicalChunkData:  make([][]byte, pieces),
			availableChan:     make(chan struct{}),
			memoryNeeded:      uint64(pieces) * modules.SectorSize,
		}
	}

	// valid chunk
	uuc := chunk()
	wt.mu.Lock()
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()
	uuc.mu.Lock()
	uuc.pieceUsage[0] = true // mark first piece as used
	uuc.mu.Unlock()
	nc, pieceIndex := wt.managedProcessUploadChunk(uuc)
	if nc == nil {
		t.Error("next chunk shouldn't be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 1 {
		t.Error("expected pieceIndex to be 1 since piece 0 is marked as used", pieceIndex)
	}
	if uuc.piecesRegistered != 1 {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 1)
	}
	if uuc.workersRemaining != 0 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 0 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 0)
	}
	if !uuc.pieceUsage[1] {
		t.Errorf("expected pieceUsage[1] to be true")
	}
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 3 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 3)
	}
	wt.mu.Unlock()

	// valid - no help needed
	uuc = chunk()
	wt.mu.Lock()
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()
	uuc.mu.Lock()
	uuc.pieceUsage[0] = true // mark first piece as used
	uuc.piecesRegistered = uuc.piecesNeeded
	uuc.mu.Unlock()
	_ = wt.renter.memoryManager.Request(modules.SectorSize*uint64(pieces-1), true)
	nc, pieceIndex = wt.managedProcessUploadChunk(uuc)
	if nc != nil {
		t.Error("next chunk should be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 0 {
		t.Error("expected pieceIndex to be 0", pieceIndex)
	}
	if uuc.piecesRegistered != uuc.piecesNeeded {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 1)
	}
	if uuc.workersRemaining != 0 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 1 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 1)
	}
	for _, pu := range uuc.pieceUsage {
		// managedCleanUpUploadChunk sets all elements to true
		if !pu {
			t.Errorf("expected pu to be true")
		}
	}
	// Standby workers are woken.
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 3 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 3)
	}
	wt.mu.Unlock()

	// invalid - not a candidate
	uuc = chunk()
	wt.mu.Lock()
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()
	uuc.mu.Lock()
	uuc.unusedHosts = make(map[string]struct{})
	uuc.mu.Unlock()
	_ = wt.renter.memoryManager.Request(modules.SectorSize*uint64(pieces), true)
	nc, pieceIndex = wt.managedProcessUploadChunk(uuc)
	if nc != nil {
		t.Error("next chunk should be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 0 {
		t.Error("expected pieceIndex to be 0", pieceIndex)
	}
	if uuc.piecesRegistered != 0 {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 0)
	}
	if uuc.workersRemaining != 0 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 0 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 0)
	}
	for _, pu := range uuc.pieceUsage {
		// managedCleanUpUploadChunk sets all elements to true
		if !pu {
			t.Errorf("expected pu to be true")
		}
	}
	// Standby workers are woken.
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 3 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 3)
	}
	wt.mu.Unlock()

	// invalid - complete
	uuc = chunk()
	wt.mu.Lock()
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()
	uuc.mu.Lock()
	uuc.piecesCompleted = uuc.piecesNeeded
	uuc.mu.Unlock()
	_ = wt.renter.memoryManager.Request(modules.SectorSize*uint64(pieces), true)
	nc, pieceIndex = wt.managedProcessUploadChunk(uuc)
	if nc != nil {
		t.Error("next chunk should be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 0 {
		t.Error("expected pieceIndex to be 0", pieceIndex)
	}
	if uuc.piecesRegistered != 0 {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 0)
	}
	if uuc.workersRemaining != 0 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 1 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 1)
	}
	for _, pu := range uuc.pieceUsage {
		// managedCleanUpUploadChunk sets all elements to true
		if !pu {
			t.Errorf("expected pu to be true")
		}
	}
	// Standby workers are woken.
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 3 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 3)
	}
	wt.mu.Unlock()

	// set upload recent failure for cooldown cases.
	wt.mu.Lock()
	wt.uploadRecentFailure = time.Now()
	wt.mu.Unlock()

	// invalid - not a candidate - oncooldown
	uuc = chunk()
	wt.mu.Lock()
	wt.uploadConsecutiveFailures = math.MaxInt32
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()
	uuc.mu.Lock()
	uuc.unusedHosts = make(map[string]struct{})
	uuc.mu.Unlock()
	_ = wt.renter.memoryManager.Request(modules.SectorSize*uint64(pieces), true)
	nc, pieceIndex = wt.managedProcessUploadChunk(uuc)
	if nc != nil {
		t.Error("next chunk should be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 0 {
		t.Error("expected pieceIndex to be 0", pieceIndex)
	}
	if uuc.piecesRegistered != 0 {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 0)
	}
	if uuc.workersRemaining != -3 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 0 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 0)
	}
	for _, pu := range uuc.pieceUsage {
		// managedCleanUpUploadChunk sets all elements to true
		if !pu {
			t.Errorf("expected pu to be true")
		}
	}
	// Standby workers are woken.
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 0 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 0)
	}
	wt.mu.Unlock()

	// invalid - completed, oncooldown
	uuc = chunk()
	wt.mu.Lock()
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()
	uuc.mu.Lock()
	uuc.piecesCompleted = uuc.piecesNeeded
	uuc.mu.Unlock()
	_ = wt.renter.memoryManager.Request(modules.SectorSize*uint64(pieces), true)
	nc, pieceIndex = wt.managedProcessUploadChunk(uuc)
	if nc != nil {
		t.Error("next chunk should be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 0 {
		t.Error("expected pieceIndex to be 0", pieceIndex)
	}
	if uuc.piecesRegistered != 0 {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 0)
	}
	if uuc.workersRemaining != -3 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 1 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 1)
	}
	for _, pu := range uuc.pieceUsage {
		// managedCleanUpUploadChunk sets all elements to true
		if !pu {
			t.Errorf("expected pu to be true")
		}
	}
	// Standby workers are woken.
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 0 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 0)
	}
	wt.mu.Unlock()

	// invalid - !goodForUpload
	uuc = chunk()
	wt.mu.Lock()
	wt.unprocessedChunks = []*unfinishedUploadChunk{uuc, uuc, uuc}
	wt.mu.Unlock()

	// mark contract as bad
	err = wt.renter.CancelContract(wt.staticCache().staticContractID)
	if err != nil {
		t.Fatal(err)
	}
	wt.managedUpdateCache()
	_ = wt.renter.memoryManager.Request(modules.SectorSize*uint64(pieces), true)
	nc, pieceIndex = wt.managedProcessUploadChunk(uuc)
	if nc != nil {
		t.Error("next chunk should be nil")
	}
	uuc.mu.Lock()
	if pieceIndex != 0 {
		t.Error("expected pieceIndex to be 0", pieceIndex)
	}
	if uuc.piecesRegistered != 0 {
		t.Errorf("piecesRegistered %v != %v", uuc.piecesRegistered, 0)
	}
	if uuc.workersRemaining != -3 {
		t.Errorf("workersRemaining %v != %v", uuc.workersRemaining, 0)
	}
	if len(uuc.unusedHosts) != 1 {
		t.Errorf("unusedHosts %v != %v", len(uuc.unusedHosts), 1)
	}
	for _, pu := range uuc.pieceUsage {
		// managedCleanUpUploadChunk sets all elements to true
		if !pu {
			t.Errorf("expected pu to be true")
		}
	}
	// Standby workers are woken.
	if len(uuc.workersStandby) != 0 {
		t.Errorf("expected %v standby workers got %v", 0, len(uuc.workersStandby))
	}
	uuc.mu.Unlock()
	wt.mu.Lock()
	if len(wt.unprocessedChunks) != 0 {
		t.Errorf("unprocessedChunks %v != %v", len(wt.unprocessedChunks), 0)
	}
	wt.mu.Unlock()
}
