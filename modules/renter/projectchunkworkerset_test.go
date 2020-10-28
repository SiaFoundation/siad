package renter

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestPCWS verifies the functionality of the PCWS.
func TestPCWS(t *testing.T) {
	t.Run("basic", testBasic)
	t.Run("newPCWSByRoots", testNewPCWSByRoots)
	t.Run("gouging", testGouging)
}

// testBasic verifies the PCWS using a simple setup with a single host, looking
// for a single sector.
func testBasic(t *testing.T) {
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)

	// create a passthrough EC and a passhtrough cipher key
	ptec := modules.NewPassthroughErasureCoder()
	ptck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// define a helper function that waits for an update
	waitForUpdate := func(ws *pcwsWorkerState) {
		ws.mu.Lock()
		wu := ws.registerForWorkerUpdate()
		ws.mu.Unlock()
		select {
		case <-wu:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out")
		}
	}

	// create PCWS
	pcws, err := wt.renter.newPCWSByRoots(context.Background(), []crypto.Hash{sectorRoot}, ptec, ptck, 0)
	if err != nil {
		t.Fatal(err)
	}

	// get the current state update
	pcws.mu.Lock()
	ws := pcws.workerState
	wslt := pcws.workerStateLaunchTime
	pcws.mu.Unlock()

	// verify launch time was set
	unset := time.Time{}
	if wslt == unset {
		t.Fatal("launch time not set")
	}

	// register for worker update and wait
	waitForUpdate(ws)

	// verify resolved and unresolved workers
	ws.mu.Lock()
	resolved := ws.resolvedWorkers
	numResolved := len(ws.resolvedWorkers)
	numUnresolved := len(ws.unresolvedWorkers)
	ws.mu.Unlock()

	if numResolved != 1 || numUnresolved != 0 {
		t.Fatal("unexpected")
	}
	if len(resolved[0].pieceIndices) != 0 {
		t.Fatal("unexpected")
	}

	// add the sector to the host
	err = wt.host.AddSector(sectorRoot, sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// reset the launch time - allowing us to force a state update
	pcws.mu.Lock()
	pcws.workerStateLaunchTime = unset
	pcws.mu.Unlock()
	err = pcws.managedTryUpdateWorkerState()
	if err != nil {
		t.Fatal(err)
	}

	// get the current worker state (!important)
	pcws.mu.Lock()
	ws = pcws.workerState
	pcws.mu.Unlock()

	// register for worker update and wait
	waitForUpdate(ws)

	// verify resolved and unresolved workers
	ws.mu.Lock()
	resolved = ws.resolvedWorkers
	ws.mu.Unlock()

	// expect we found sector at index 0
	if len(resolved) != 1 || len(resolved[0].pieceIndices) != 1 {
		t.Fatal("unexpected")
	}
}

// testNewPCWSByRoots verifies the 'newPCWSByRoots' constructor function and its
// edge cases
func testNewPCWSByRoots(t *testing.T) {
	r := new(Renter)
	r.staticWorkerPool = new(workerPool)

	// create random roots
	var root1 crypto.Hash
	var root2 crypto.Hash
	fastrand.Read(root1[:])
	fastrand.Read(root2[:])
	roots := []crypto.Hash{root1, root2}

	// create a passthrough EC and a passhtrough cipher key
	ptec := modules.NewPassthroughErasureCoder()
	ptck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// verify basic case
	_, err = r.newPCWSByRoots(context.Background(), roots[:1], ptec, ptck, 0)
	if err != nil {
		t.Fatal("unexpected")
	}

	// verify the case where we the amount of roots does not equal num pieces
	// defined in the erasure coder
	_, err = r.newPCWSByRoots(context.Background(), roots, ptec, ptck, 0)
	if err == nil || !strings.Contains(err.Error(), "but erasure coder specifies 1 pieces") {
		t.Fatal(err)
	}

	// verify the legacy case where 1-of-N only needs 1 root
	ec, err := modules.NewRSCode(1, 10)
	if err != nil {
		t.Fatal("unexpected")
	}

	// verify the amount of roots provided **does not** equal num pieces,
	// usually causing an error
	if len(roots[:1]) == ec.NumPieces() {
		t.Fatal("unexpected")
	}
	_, err = r.newPCWSByRoots(context.Background(), roots[:1], ec, ptck, 0)
	if err != nil {
		t.Fatal("unexpected")
	}
}

// testGouging checks that the gouging check is triggering at the right
// times.
func testGouging(t *testing.T) {
	// Create some defaults to get some intuitive ideas for gouging.
	//
	// 100 workers and 1e9 expected download means ~2e6 HasSector queries will
	// be performed.
	pt := modules.RPCPriceTable{
		InitBaseCost:          types.NewCurrency64(1e3),
		DownloadBandwidthCost: types.NewCurrency64(1e3),
		UploadBandwidthCost:   types.NewCurrency64(1e3),
		HasSectorBaseCost:     types.NewCurrency64(1e6),
	}
	allowance := modules.Allowance{
		MaxDownloadBandwidthPrice: types.NewCurrency64(2e3),
		MaxUploadBandwidthPrice:   types.NewCurrency64(2e3),

		Funds: types.NewCurrency64(1e18),

		ExpectedDownload: 1e9, // 1 GiB
	}
	numWorkers := 100
	numRoots := 30

	// Check that the gouging passes for normal values.
	err := checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err != nil {
		t.Error(err)
	}

	// Check with high init base cost.
	pt.InitBaseCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.InitBaseCost = types.NewCurrency64(1e3)

	// Check with high upload bandwidth cost.
	pt.UploadBandwidthCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.UploadBandwidthCost = types.NewCurrency64(1e3)

	// Check with high download bandwidth cost.
	pt.DownloadBandwidthCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.DownloadBandwidthCost = types.NewCurrency64(1e3)

	// Check with high HasSector cost.
	pt.HasSectorBaseCost = types.NewCurrency64(1e12)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	pt.HasSectorBaseCost = types.NewCurrency64(1e6)

	// Check with low MaxDownloadBandwidthPrice.
	allowance.MaxDownloadBandwidthPrice = types.NewCurrency64(100)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.MaxDownloadBandwidthPrice = types.NewCurrency64(2e3)

	// Check with low MaxUploadBandwidthPrice.
	allowance.MaxUploadBandwidthPrice = types.NewCurrency64(100)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.MaxUploadBandwidthPrice = types.NewCurrency64(2e3)

	// Check with reduced funds.
	allowance.Funds = types.NewCurrency64(1e15)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.Funds = types.NewCurrency64(1e18)

	// Check with increased expected download.
	allowance.ExpectedDownload = 1e12
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err == nil {
		t.Error("bad")
	}
	allowance.ExpectedDownload = 1e9

	// Check that the base allowanace still passes. (ensures values have been
	// reset correctly)
	err = checkPCWSGouging(pt, allowance, numWorkers, numRoots)
	if err != nil {
		t.Error(err)
	}
}
