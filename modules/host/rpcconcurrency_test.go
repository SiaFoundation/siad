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
	defer func() {
		err := ht.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	// prepare an amount with which we'll refund the EAs
	his := ht.host.InternalSettings()
	funding := his.MaxEphemeralAccountBalance.Div64(1e5)

	// create 10 renter host pairs
	pairs := make([]*renterHostPair, 10)
	for i := range pairs {
		pair, err := newRenterHostPairCustomHostTester(ht)
		if err != nil {
			t.Fatal(err)
		}
		// prefund the EAs
		_, err = pair.callFundEphemeralAccount(funding)
		if err != nil {
			t.Fatal(err)
		}
		pairs[i] = pair
	}

	// for every pair precreate MDM programs
	readFullPrograms := make(map[types.FileContractID]mdmProgram)
	readPartPrograms := make(map[types.FileContractID]mdmProgram)
	hasSectorPrograms := make(map[types.FileContractID]mdmProgram)
	for _, pair := range pairs {
		root, _, err := pair.addRandomSector()
		if err != nil {
			t.Fatal(err)
		}
		pt := pair.PriceTable()
		fcid := pair.staticFCID
		readFullPrograms[fcid] = createRandomReadSectorProgram(pt, root, true)
		readPartPrograms[fcid] = createRandomReadSectorProgram(pt, root, false)
		hasSectorPrograms[fcid] = createRandomHasSectorProgram(pt, root)
	}

	// randomScenario is a helper function that randomly selects which program
	// to execute, returns its cost and a function that tracks a successful call
	randomScenario := func(pair *renterHostPair, stats *rpcStats) (program mdmProgram, cost types.Currency, track func()) {
		pt := pair.PriceTable()
		scenario := fastrand.Intn(3)
		switch scenario {
		case 0:
			program = readFullPrograms[pair.staticFCID]
			dlcost := pt.DownloadBandwidthCost.Mul64(10220)
			ulcost := pt.UploadBandwidthCost.Mul64(18980)
			cost = program.cost.Add(dlcost).Add(ulcost)
			track = func() {
				stats.trackReadSector(true)
			}
		case 1:
			program = readPartPrograms[pair.staticFCID]
			dlcost := pt.DownloadBandwidthCost.Mul64(10220)
			ulcost := pt.UploadBandwidthCost.Mul64(18980)
			cost = program.cost.Add(dlcost).Add(ulcost)
			track = func() {
				stats.trackReadSector(false)
			}
		case 2:
			program = hasSectorPrograms[pair.staticFCID]
			dlcost := pt.DownloadBandwidthCost.Mul64(7300)
			ulcost := pt.UploadBandwidthCost.Mul64(18980)
			cost = program.cost.Add(dlcost).Add(ulcost)
			track = func() {
				stats.trackHasSector()
			}
		}
		return
	}

	// start the timer
	finished := make(chan struct{})
	time.AfterFunc(timeout, func() {
		close(finished)
	})

	// collect rpc stats
	stats := &rpcStats{}

	numThreads := 10
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
					continue
				default:
				}

				// try to recover from insufficient balance
				var recovered bool
				if !recovered && strings.Contains(err.Error(), ErrBalanceInsufficient.Error()) {
					_, err = pair.fundEphemeralAccount(pair.PriceTable(), funding)
					stats.trackFundEA()
					recovered = err == nil
				}

				// try to recover from expired PT
				if !recovered && (strings.Contains(err.Error(), ErrPriceTableExpired.Error()) || strings.Contains(err.Error(), ErrPriceTableNotFound.Error())) {
					var payByFC bool
					err = pair.updatePriceTable(false) // try using an EA
					if err != nil {
						pair.updatePriceTable(true)
						payByFC = true
					}
					stats.trackUpdatePT(payByFC)
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

		wg.Add(numThreads)
		for i := 0; i < numThreads; i++ {
			go func(pair *renterHostPair, recoverChan chan error) {
				defer wg.Done()
			LOOP:
				for {
					select {
					case <-finished:
						break LOOP
					default:
					}

					// get a random program to execute
					p, cost, trackFn := randomScenario(pair, stats)
					epr := modules.RPCExecuteProgramRequest{
						FileContractID:    pair.staticFCID,
						Program:           p.program,
						ProgramDataLength: uint64(len(p.data)),
					}

					// execute it and handle the error
					pt := pair.PriceTable()
					_, _, err := pair.executeProgram(pt, epr, p.data, cost)
					if err != nil {
						recoverChan <- err
					} else {
						trackFn()
					}
				}
			}(p, recoverChan)
		}
	}
	<-finished
	wg.Wait()

	t.Logf("In %.f seconds, on %d cores across %d threads, the following RPCs completed: %s\n", timeout.Seconds(), runtime.NumCPU(), numThreads, stats.String())
}

// mdmProgram is a helper struct that contains all necessary details to execute
// an MDM program to read a full (or partial) sector from the host
type mdmProgram struct {
	program modules.Program
	data    []byte
	cost    types.Currency
}

// createRandomReadSectorProgram is a helper function that creates a program to
// read data from the host. If full is set to true, the program will perform a
// full sector read, if it is false we return a program that reads a random
// couple of segments at random offset.
func createRandomReadSectorProgram(pt *modules.RPCPriceTable, root crypto.Hash, full bool) mdmProgram {
	var offset uint64
	var length uint64
	if full {
		offset = 0
		length = modules.SectorSize
	} else {
		offset = uint64(fastrand.Uint64n((modules.SectorSize/crypto.SegmentSize)-1) * crypto.SegmentSize)
		length = uint64(crypto.SegmentSize) * (fastrand.Uint64n(5) + 1)
	}
	p, data, cost, _, _, _ := newReadSectorProgram(length, offset, root, pt)
	return mdmProgram{
		program: p,
		data:    data,
		cost:    cost,
	}
}

// createRandomHasSectorProgram is a helper function that creates a random
// sector on the host and returns a program that returns whether or not the host
// has this sector.
func createRandomHasSectorProgram(pt *modules.RPCPriceTable, root crypto.Hash) mdmProgram {
	p, data, cost, _, _, _ := newHasSectorProgram(root, pt)
	return mdmProgram{
		program: p,
		data:    data,
		cost:    cost,
	}
}

// rpcStats is a helper struct to collect the amount of times an RPC has been
// performed.
type rpcStats struct {
	atomicUpdatePTCallsFC                uint64
	atomicUpdatePTCallsEA                uint64
	atomicFundAccountCalls               uint64
	atomicExecuteProgramFullReadCalls    uint64
	atomicExecuteProgramPartialReadCalls uint64
	atomicExecuteHasSectorCalls          uint64
}

// trackPartialRead tracks an update price table call
func (rs *rpcStats) trackUpdatePT(payByFC bool) {
	if payByFC {
		atomic.AddUint64(&rs.atomicUpdatePTCallsFC, 1)
	} else {
		atomic.AddUint64(&rs.atomicUpdatePTCallsEA, 1)
	}
}

// trackPartialRead tracks a fund ephemeral account call
func (rs *rpcStats) trackFundEA() {
	atomic.AddUint64(&rs.atomicFundAccountCalls, 1)
}

// trackPartialRead tracks a sector read
func (rs *rpcStats) trackReadSector(full bool) {
	if full {
		atomic.AddUint64(&rs.atomicExecuteProgramFullReadCalls, 1)
	} else {
		atomic.AddUint64(&rs.atomicExecuteProgramPartialReadCalls, 1)
	}
}

// trackHasSector tracks a sector lookup
func (rs *rpcStats) trackHasSector() {
	atomic.AddUint64(&rs.atomicExecuteHasSectorCalls, 1)
}

// String prints a string representation of the RPC statistics
func (rs *rpcStats) String() string {
	numPTFC := atomic.LoadUint64(&rs.atomicUpdatePTCallsFC)
	numPTEA := atomic.LoadUint64(&rs.atomicUpdatePTCallsEA)
	numPT := numPTFC + numPTEA

	numEA := atomic.LoadUint64(&rs.atomicFundAccountCalls)
	numFS := atomic.LoadUint64(&rs.atomicExecuteProgramFullReadCalls)
	numPS := atomic.LoadUint64(&rs.atomicExecuteProgramPartialReadCalls)
	numHS := atomic.LoadUint64(&rs.atomicExecuteHasSectorCalls)
	total := numPT + numEA + numFS + numPS + numHS

	return fmt.Sprintf(`
	Total RPC Calls: %d
 
	UpdatePriceTableRPC: %d (%d by FC)
	FundEphemeralAccountRPC: %d
	ExecuteMDMProgramRPC (Full Sector Read): %d
	ExecuteMDMProgramRPC (Partial Sector Read): %d
	ExecuteMDMProgramRPC (Has Sector): %d
`, total, numPT, numPTFC, numEA, numFS, numPS, numHS)
}
