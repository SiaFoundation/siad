package host

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestRPCConcurrentCalls makes a whole set of concurrent RPC calls to the host
// from multiple renters and verifies all of them succeed without error
func TestRPCConcurrentCalls(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// determine a reasonable timeout
	var timeout time.Duration
	if build.VLONG {
		timeout = time.Minute
	} else {
		timeout = 10 * time.Second
	}

	// setup the host
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	his := ht.host.InternalSettings()
	funding := his.MaxEphemeralAccountBalance.Div64(10)

	// create an arbitrary amount of renters
	pairs := make([]*renterHostPair, 16)
	for i := range pairs {
		pair, err := newRenterHostPairCustomHostTester(ht)
		if err != nil {
			t.Fatal(err)
		}
		pairs[i] = pair
	}

	// setup a lock guarding the filecontracts seeing as we are concurrently
	// accessing them and generating revisions for them
	fcLocks := make(map[types.FileContractID]*sync.Mutex)
	for _, pair := range pairs {
		fcLocks[pair.fcid] = new(sync.Mutex)
	}

	// setup a 'ReadSector' program for every pair
	type ReadSectorProgram struct {
		program modules.Program
		data    []byte
		cost    types.Currency
	}
	readSectorPrograms := make(map[types.FileContractID]ReadSectorProgram)
	for _, pair := range pairs {
		sectorRoot, _, err := pair.addRandomSector()
		if err != nil {
			t.Fatal(err)
		}
		program, data, cost, _, _, _ := newReadSectorProgram(modules.SectorSize, 0, sectorRoot, pair.PriceTable())
		readSectorPrograms[pair.fcid] = ReadSectorProgram{
			program: program,
			data:    data,
			cost:    cost,
		}
	}

	// start the timer
	finished := make(chan struct{})
	time.AfterFunc(timeout, func() {
		close(finished)
	})

	var atomicUpdatePTCalls_FC uint64
	var atomicUpdatePTCalls_EA uint64
	var atomicFundAccountCalls_FC uint64
	var atomicExecuteProgramCalls_EA uint64

	// spin up a large amount of threads that use the renter-host pairs in
	// parallel
	totalThreads := 10 * runtime.NumCPU()
	for thread := 0; thread < totalThreads; thread++ {
		go func() {
			// create two streams
			rs, hs := NewTestStreams()
			defer rs.Close()
			defer hs.Close()

		LOOP:
			for {
				select {
				case <-finished:
					break LOOP
				default:
				}

				var err error

				// pick a random pair
				pair := pairs[fastrand.Intn(len(pairs))]

				// pay by contract 5% of time
				var payByFC bool
				if fastrand.Intn(100) < 5 {
					payByFC = true
				}

				var done bool

				// update price table 10% of the time
				if !done && fastrand.Intn(100) < 10 {
					if payByFC {
						fcLocks[pair.fcid].Lock()
						err = pair.updatePriceTable(payByFC)
						fcLocks[pair.fcid].Unlock()
						atomic.AddUint64(&atomicUpdatePTCalls_FC, 1)
					} else {
						err = pair.updatePriceTable(payByFC)
						atomic.AddUint64(&atomicUpdatePTCalls_EA, 1)
					}
					done = true
				}

				// fund account 30% of the time
				if !done && fastrand.Intn(100) < 30 {
					curr := pair.PriceTable()
					fcLocks[pair.fcid].Lock()
					_, err = pair.fundEphemeralAccount(curr.FundAccountCost.Add(funding))
					fcLocks[pair.fcid].Unlock()
					atomic.AddUint64(&atomicFundAccountCalls_FC, 1)
					done = true
				}

				// execute read full sector program 30% of the time
				if !done && fastrand.Intn(100) < 30 {
					p := readSectorPrograms[pair.fcid]
					epr := modules.RPCExecuteProgramRequest{
						FileContractID:    pair.fcid,
						Program:           p.program,
						ProgramDataLength: uint64(len(p.data)),
					}
					curr := pair.PriceTable()
					dlcost := curr.DownloadBandwidthCost.Mul64(10220)
					ulcost := curr.UploadBandwidthCost.Mul64(18980)
					budget := p.cost.Add(dlcost).Add(ulcost)
					_, _, err = pair.executeProgram(epr, p.data, budget)
					atomic.AddUint64(&atomicExecuteProgramCalls_EA, 1)
					done = true
				}

				// try to recover from expired PT
				if err != nil && (strings.Contains(err.Error(), ErrPriceTableExpired.Error()) || strings.Contains(err.Error(), ErrPriceTableNotFound.Error())) {
					fcLocks[pair.fcid].Lock()
					err = pair.updatePriceTable(true)
					fcLocks[pair.fcid].Unlock()
					if err != nil {
						t.Log("TRY RECOVER FAILED", err)
					}
				}

				// try to recover from insufficient balance
				if err != nil && strings.Contains(err.Error(), ErrBalanceInsufficient.Error()) {
					curr := pair.PriceTable()
					fcLocks[pair.fcid].Lock()
					_, err = pair.fundEphemeralAccount(curr.FundAccountCost.Add(funding))
					fcLocks[pair.fcid].Unlock()
					if err != nil {
						t.Log("TRY RECOVER FAILED TWICE", err)
					}
				}

				if err != nil {
					t.Error(err)
					break LOOP
				}
			}
		}()
	}
	<-finished

	numUpdatePT_FC := atomic.LoadUint64(&atomicUpdatePTCalls_FC)
	numUpdatePT_EA := atomic.LoadUint64(&atomicUpdatePTCalls_EA)
	numFundAccount_FC := atomic.LoadUint64(&atomicFundAccountCalls_FC)
	numExecuteProgram_EA := atomic.LoadUint64(&atomicExecuteProgramCalls_EA)

	t.Logf(`
	In %.f seconds, on %d cores, the following RPCs completed successfully:
	UpdatePriceTableRPC: 		%d (%d FC %d EA)
	FundEphemeralAccountRPC: 	%d (%d FC)
	ExecuteMDMProgramRPC: 		%d (%d EA)
	`, timeout.Seconds(), runtime.NumCPU(), numUpdatePT_FC+numUpdatePT_EA, numUpdatePT_FC, numUpdatePT_EA, numFundAccount_FC, numFundAccount_FC, numExecuteProgram_EA, numExecuteProgram_EA)
}
