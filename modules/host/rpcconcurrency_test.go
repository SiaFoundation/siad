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
		timeout = 5 * time.Minute
	} else {
		timeout = 30 * time.Second
	}

	// setup the host
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

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
	var stats rpcStats

	var wg sync.WaitGroup
	for _, p := range pairs {
		// spin up a goroutine for every pair that just tries to recover from a
		// set of errors which are expected to happen, if we can recover from
		// them, the test should not be considered as failed
		recoverChan := make(chan error)
		go func(pair *renterHostPair, recoverChan chan error) {
			for err := range recoverChan {
				select {
				case <-finished:
					break
				default:
				}

				var recovered bool

				// try to recover from expired PT
				if !recovered && (strings.Contains(err.Error(), ErrPriceTableExpired.Error()) || strings.Contains(err.Error(), ErrPriceTableNotFound.Error())) {
					err = pair.updatePriceTable(true)
					stats.trackUpdatePT(true)
					recovered = err == nil
				}

				// try to recover from insufficient balance
				if !recovered && strings.Contains(err.Error(), ErrBalanceInsufficient.Error()) {
					his := pair.ht.host.InternalSettings()
					funding := his.MaxEphemeralAccountBalance.Div64(10)
					_, err = pair.callFundEphemeralAccount(funding)
					stats.trackFundEA()
					recovered = err == nil
				}

				// ignore max balance exceeded
				if !recovered && strings.Contains(err.Error(), ErrBalanceMaxExceeded.Error()) {
					err = nil
				}

				if err != nil {
					t.Error(err)
				}
			}
		}(p, recoverChan)

		wg.Add(10)
		for i := 0; i < 10; i++ {
			go func(pair *renterHostPair, recoverChan chan error) {
				defer wg.Done()
			LOOP:
				for {
					select {
					case <-finished:
						break LOOP
					default:
					}

					// execute full sector read 50% of the time
					var err error
					var full = fastrand.Intn(2) == 0

					var p readSectorProgram
					if full {
						p = readFullSectors[pair.staticFCID]
					} else {
						p = readPartialSectors[pair.staticFCID]
					}

					epr := modules.RPCExecuteProgramRequest{
						FileContractID:    pair.staticFCID,
						Program:           p.program,
						ProgramDataLength: uint64(len(p.data)),
					}
					curr := pair.PriceTable()
					_, _, err = pair.executeProgram(curr, epr, p.data, p.Cost(curr))
					if err == nil {
						stats.trackReadSector(full)
					}

					if err != nil {
						recoverChan <- err
					}
				}
			}(p, recoverChan)
		}
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

// trackPartialRead tracks a sector read
func (rs *rpcStats) trackReadSector(full bool) {
	if full {
		atomic.AddUint64(&rs.atomicExecuteProgramFullReadCalls_EA, 1)
	} else {
		atomic.AddUint64(&rs.atomicExecuteProgramPartialReadCalls_EA, 1)
	}
}

// String prints a string representation of the RPC statistics
func (rs *rpcStats) String() string {
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
`, numUpdatePT_FC+numUpdatePT_EA, numUpdatePT_FC, numUpdatePT_EA, numFundAccount_FC, numFundAccount_FC, numExecProgram_Full_EA, numExecProgram_Full_EA, numExecProgram_Partial_EA, numExecProgram_Partial_EA)
}
