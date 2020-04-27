package host

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
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
	pairs := make([]*renterHostPair, 10)
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
		fcLocks[pair.staticFCID] = new(sync.Mutex)
	}

	// setup a 'ReadSector' program for every pair
	readFullSectors := make(map[types.FileContractID]readSectorProgram)
	readPartialSectors := make(map[types.FileContractID]readSectorProgram)
	for _, pair := range pairs {
		fcid := pair.staticFCID
		readFullSectors[fcid] = createRandomReadSectorProgram(pair, true)
		readPartialSectors[fcid] = createRandomReadSectorProgram(pair, false)
	}

	// start the timer
	finished := make(chan struct{})
	time.AfterFunc(timeout, func() {
		close(finished)
	})

	// collect rpc stats
	stats := rpcStats{}

	var wg sync.WaitGroup

	// spin up a goroutine for every pair and perform random RPC calls
	for _, p := range pairs {
		wg.Add(1)
		go func(pair *renterHostPair) {
			defer wg.Done()
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

				// pay by contract 5% of time
				var payByFC bool
				if fastrand.Intn(100) < 5 {
					payByFC = true
				}

				var done bool

				// update price table 10% of the time
				if !done && fastrand.Intn(100) < 10 {
					if payByFC {
						fcLocks[pair.staticFCID].Lock()
						err = pair.updatePriceTable(payByFC)
						fcLocks[pair.staticFCID].Unlock()
					} else {
						err = pair.updatePriceTable(payByFC)
					}
					stats.trackUpdatePT(payByFC)
					done = true
				}

				// fund account 30% of the time
				if !done && fastrand.Intn(100) < 30 {
					curr := pair.PriceTable()
					fcLocks[pair.staticFCID].Lock()
					_, err = pair.fundEphemeralAccount(curr, funding)
					fcLocks[pair.staticFCID].Unlock()
					stats.trackFundEA()
					done = true
				}

				// execute full sector read 30% of the time
				if !done && fastrand.Intn(100) < 30 {
					p := readFullSectors[pair.staticFCID]
					epr := modules.RPCExecuteProgramRequest{
						FileContractID:    pair.staticFCID,
						Program:           p.program,
						ProgramDataLength: uint64(len(p.data)),
					}
					curr := pair.PriceTable()
					budget := p.Cost(curr)
					_, _, err = pair.executeProgram(curr, epr, p.data, budget)
					stats.trackFullRead()
					done = true
				}

				// execute partial sector read the rest of the time
				if !done {
					p := readPartialSectors[pair.staticFCID]
					epr := modules.RPCExecuteProgramRequest{
						FileContractID:    pair.staticFCID,
						Program:           p.program,
						ProgramDataLength: uint64(len(p.data)),
					}
					curr := pair.PriceTable()
					budget := p.Cost(curr)
					_, _, err = pair.executeProgram(curr, epr, p.data, budget)
					stats.trackPartialRead()
					done = true
				}

				// try to recover from expired PT
				if err != nil && (strings.Contains(err.Error(), ErrPriceTableExpired.Error()) || strings.Contains(err.Error(), ErrPriceTableNotFound.Error())) {
					fcLocks[pair.staticFCID].Lock()
					err = pair.updatePriceTable(true)
					fcLocks[pair.staticFCID].Unlock()
					stats.trackUpdatePT(true)
				}

				// try to recover from insufficient balance
				if err != nil && strings.Contains(err.Error(), ErrBalanceInsufficient.Error()) {
					fcLocks[pair.staticFCID].Lock()
					_, err = pair.callFundEphemeralAccount(funding)
					fcLocks[pair.staticFCID].Unlock()
					stats.trackFundEA()
				}

				// ignore max balance exceeded
				if err != nil && strings.Contains(err.Error(), ErrBalanceMaxExceeded.Error()) {
					err = nil
				}

				if err != nil {
					t.Error(err)
					break LOOP
				}
			}
		}(p)
	}
	<-finished
	wg.Wait()

	t.Logf("In %.f seconds, on %d cores, the following RPCs completed successfully: %s\n", timeout.Seconds(), runtime.NumCPU(), stats.String())
}

// readSectorProgram is a helper struct that contains all necessary details to
// execute an MDM program to read a full (or partial) sector from the host
type readSectorProgram struct {
	program modules.Program
	data    []byte
	cost    types.Currency
}

// Cost returns the cost of running this program, calculated using the given rpc
// price table
func (rsp *readSectorProgram) Cost(pt *modules.RPCPriceTable) types.Currency {
	dlcost := pt.DownloadBandwidthCost.Mul64(10220)
	ulcost := pt.UploadBandwidthCost.Mul64(18980)
	return rsp.cost.Add(dlcost).Add(ulcost)
}

// createRandomReadSectorProgram is a helper function that creates a random
// sector on the host and returns a program that reads this sector. It returns a
// program to read a full sector or partial sector depending on the `full`
// parameter. In case `full` is false, we will read a partial sector, which is a
// random length at random offset.
func createRandomReadSectorProgram(pair *renterHostPair, full bool) readSectorProgram {
	sectorRoot, _, err := pair.addRandomSector()
	if err != nil {
		panic(err)
	}

	var offset uint64
	var length uint64
	if full {
		offset = 0
		length = modules.SectorSize
	} else {
		offset = uint64(fastrand.Uint64n((modules.SectorSize/crypto.SegmentSize)-1) * crypto.SegmentSize)
		length = uint64(crypto.SegmentSize) * (fastrand.Uint64n(5) + 1)
	}

	program, data, cost, _, _, _ := newReadSectorProgram(length, offset, sectorRoot, pair.PriceTable())
	return readSectorProgram{
		program: program,
		data:    data,
		cost:    cost,
	}
}

// rpcStats is a helper struct to collect the amount of times an RPC has been
// performed.
type rpcStats struct {
	atomicUpdatePTCalls_FC                  uint64
	atomicUpdatePTCalls_EA                  uint64
	atomicFundAccountCalls_FC               uint64
	atomicExecuteProgramFullReadCalls_EA    uint64
	atomicExecuteProgramPartialReadCalls_EA uint64
}

// trackPartialRead tracks an update price table call
func (rs *rpcStats) trackUpdatePT(payByFC bool) {
	if payByFC {
		atomic.AddUint64(&rs.atomicUpdatePTCalls_FC, 1)
	} else {
		atomic.AddUint64(&rs.atomicUpdatePTCalls_EA, 1)
	}
}

// trackPartialRead tracks a fund ephemeral account call
func (rs *rpcStats) trackFundEA() {
	atomic.AddUint64(&rs.atomicFundAccountCalls_FC, 1)
}

// trackPartialRead tracks a full read
func (rs *rpcStats) trackFullRead() {
	atomic.AddUint64(&rs.atomicExecuteProgramFullReadCalls_EA, 1)
}

// trackPartialRead tracks a partial read
func (rs *rpcStats) trackPartialRead() {
	atomic.AddUint64(&rs.atomicExecuteProgramPartialReadCalls_EA, 1)
}

// String prints a string representation of the RPC statistics
func (rs rpcStats) String() string {
	numUpdatePT_FC := atomic.LoadUint64(&rs.atomicUpdatePTCalls_FC)
	numUpdatePT_EA := atomic.LoadUint64(&rs.atomicUpdatePTCalls_EA)
	numFundAccount_FC := atomic.LoadUint64(&rs.atomicFundAccountCalls_FC)
	numExecProgram_Full_EA := atomic.LoadUint64(&rs.atomicExecuteProgramFullReadCalls_EA)
	numExecProgram_Partial_EA := atomic.LoadUint64(&rs.atomicExecuteProgramPartialReadCalls_EA)

	return fmt.Sprintf(`
	UpdatePriceTableRPC: %d (%d FC %d EA)
	FundEphemeralAccountRPC: %d (%d FC)
	ExecuteMDMProgramRPC (Full Sector Read): %d (%d EA)
	ExecuteMDMProgramRPC (Partial Sector Read): %d (%d EA)
`, numUpdatePT_FC+numUpdatePT_EA, numUpdatePT_FC, numUpdatePT_EA, numFundAccount_FC, numFundAccount_FC, numExecProgram_Full_EA, numExecProgram_Full_EA,
		numExecProgram_Partial_EA, numExecProgram_Partial_EA)
}
