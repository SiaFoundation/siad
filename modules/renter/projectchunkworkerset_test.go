package renter

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestPCWS verifies the functionality of the PCWS.
func TestPCWS(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// create a worker tester
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	t.Run("basic", func(t *testing.T) { testBasic(t, wt) })
	t.Run("multiple", func(t *testing.T) { testMultiple(t, wt) })
	t.Run("newPCWSByRoots", testNewPCWSByRoots)
	t.Run("gouging", testGouging)
}

// testBasic verifies the PCWS using a simple setup with a single host, looking
// for a single sector.
func testBasic(t *testing.T, wt *workerTester) {
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
	ws = pcws.managedWorkerState()

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

// testMultiple verifies the PCWS for a multiple sector lookup on multiple
// hosts.
func testMultiple(t *testing.T, wt *workerTester) {
	// create a helper function that adds a host
	numHosts := 0
	addHost := func() modules.Host {
		testdir := filepath.Join(wt.rt.dir, fmt.Sprintf("host%d", numHosts))
		host, err := wt.rt.addCustomHost(testdir, modules.ProdDependencies)
		if err != nil {
			t.Fatal(err)
		}
		numHosts++
		return host
	}

	// create a helper function that adds a random sector on a given host
	addSector := func(h modules.Host) crypto.Hash {
		// create a random sector
		sectorData := fastrand.Bytes(int(modules.SectorSize))
		sectorRoot := crypto.MerkleRoot(sectorData)

		// add the sector to the host
		err := h.AddSector(sectorRoot, sectorData)
		if err != nil {
			t.Fatal(err)
		}
		return sectorRoot
	}

	// create a helper function that waits for an update
	waitForUpdate := func(ws *pcwsWorkerState) {
		ws.mu.Lock()
		wu := ws.registerForWorkerUpdate()
		ws.mu.Unlock()
		select {
		case <-wu:
		case <-time.After(time.Minute):
			t.Fatal("timed out")
		}
	}

	// create a helper function that compares uint64 slices for equality
	isEqualTo := func(a, b []uint64) bool {
		if len(a) != len(b) {
			return false
		}
		for i, v := range a {
			if v != b[i] {
				return false
			}
		}
		return true
	}

	h1 := addHost()
	h2 := addHost()
	h3 := addHost()

	h1PK := h1.PublicKey().String()
	h2PK := h2.PublicKey().String()
	h3PK := h3.PublicKey().String()

	r1 := addSector(h1)
	r2 := addSector(h1)
	r3 := addSector(h2)
	r4 := addSector(h3)
	r5 := crypto.MerkleRoot(fastrand.Bytes(int(modules.SectorSize)))
	roots := []crypto.Hash{r1, r2, r3, r4, r5}

	// create an EC and a passhtrough cipher key
	ec, err := modules.NewRSCode(1, 4)
	if err != nil {
		t.Fatal(err)
	}
	ptck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// wait until the renter has a worker for all hosts
	err = build.Retry(600, 100*time.Millisecond, func() error {
		ws, err := wt.renter.WorkerPoolStatus()
		if err != nil {
			t.Fatal(err)
		}
		if ws.NumWorkers < 3 {
			_, err = wt.rt.miner.AddBlock()
			if err != nil {
				t.Fatal(err)
			}

			return errors.New("workers not ready yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// wait until we're certain all workers are fit for duty
	err = build.Retry(100, 100*time.Millisecond, func() error {
		ws, err := wt.renter.WorkerPoolStatus()
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range ws.Workers {
			if w.AccountStatus.AvailableBalance.IsZero() ||
				!w.PriceTableStatus.Active ||
				w.MaintenanceOnCooldown {
				return errors.New("worker is not ready yet")
			}
		}
		return nil
	})

	// create PCWS
	pcws, err := wt.renter.newPCWSByRoots(context.Background(), roots, ec, ptck, 0)
	if err != nil {
		t.Fatal(err)
	}
	ws := pcws.managedWorkerState()

	// wait until all workers have resolved
	numWorkers := len(ws.staticRenter.staticWorkerPool.callWorkers())
	for {
		waitForUpdate(ws)
		ws.mu.Lock()
		numResolved := len(ws.resolvedWorkers)
		ws.mu.Unlock()
		if numResolved == numWorkers {
			break
		}
	}

	// fetch piece indices per host
	ws.mu.Lock()
	resolved := ws.resolvedWorkers
	ws.mu.Unlock()
	for _, rw := range resolved {
		var expected []uint64
		var hostname string
		switch rw.worker.staticHostPubKeyStr {
		case h1PK:
			expected = []uint64{0, 1}
			hostname = "host1"
		case h2PK:
			expected = []uint64{2}
			hostname = "host2"
		case h3PK:
			expected = []uint64{3}
			hostname = "host3"
		default:
			hostname = "other"
			continue
		}

		if !isEqualTo(rw.pieceIndices, expected) {
			t.Error("unexpected pieces", hostname, rw.worker.staticHostPubKeyStr[64:], rw.pieceIndices, rw.err)
		}
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

	// verify passing nil for the master key returns an error
	_, err = r.newPCWSByRoots(context.Background(), roots[:1], ptec, nil, 0)
	if err == nil {
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

// TestProjectChunkWorsetSet_managedLaunchWorker probes the
// 'managedLaunchWorker' function on the PCWS.
func TestProjectChunkWorsetSet_managedLaunchWorker(t *testing.T) {
	t.Parallel()

	// create EC + key
	ec := modules.NewPassthroughErasureCoder()
	ck, err := crypto.NewSiaKey(crypto.TypePlain, nil)
	if err != nil {
		t.Fatal(err)
	}

	// create renter
	renter := new(Renter)
	renter.staticWorkerPool = new(workerPool)

	// create PCWS
	pcws := &projectChunkWorkerSet{
		staticChunkIndex:   0,
		staticErasureCoder: ec,
		staticMasterKey:    ck,
		staticPieceRoots:   []crypto.Hash{},

		staticCtx:    context.Background(),
		staticRenter: renter,
	}

	// create PCWS worker state
	ws := &pcwsWorkerState{
		unresolvedWorkers: make(map[string]*pcwsUnresolvedWorker),
		staticRenter:      pcws.staticRenter,
	}

	// mock the worker
	w := new(worker)
	w.newCache()
	w.newPriceTable()
	w.newMaintenanceState()
	w.initJobHasSectorQueue()

	// give it a name and set an initial estimate on the HS queue
	w.staticJobHasSectorQueue.weightedJobTime = float64(123 * time.Second)
	w.staticHostPubKeyStr = "myworker"

	// ensure PT is valid
	w.staticPriceTable().staticExpiryTime = time.Now().Add(time.Hour)

	// launch the worker
	responseChan := make(chan *jobHasSectorResponse, 0)
	err = pcws.managedLaunchWorker(context.Background(), w, responseChan, ws)
	if err != nil {
		t.Fatal(err)
	}

	// verify the worker launched successfully
	uw, exists := ws.unresolvedWorkers["myworker"]
	if !exists {
		t.Log(ws.unresolvedWorkers)
		t.Fatal("unexpected")
	}

	// verify the expected dur matches the initial queue estimate
	expectedDur := time.Until(uw.staticExpectedResolvedTime)
	expectedDurInS := math.Round(expectedDur.Seconds())
	if expectedDurInS != 123 {
		t.Log(expectedDurInS)
		t.Fatal("unexpected")
	}

	// tweak the maintenancestate, putting it on a cooldown
	minuteFromNow := time.Now().Add(time.Minute)
	w.staticMaintenanceState.cooldownUntil = minuteFromNow
	err = pcws.managedLaunchWorker(context.Background(), w, responseChan, ws)
	if err != nil {
		t.Fatal(err)
	}

	// verify the cooldown is being reflected in the estimate
	uw = ws.unresolvedWorkers["myworker"]
	expectedDur = time.Until(uw.staticExpectedResolvedTime)
	expectedDurInS = math.Round(expectedDur.Seconds())
	if expectedDurInS != 123+60 {
		t.Log(expectedDurInS)
		t.Fatal("unexpected")
	}
}
