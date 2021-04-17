package renter

import (
	"container/heap"
	"fmt"
	"math/big"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// projectdownloadinit.go implements an algorithm to select the best set of
// initial workers for completing a download. This algorithm is balancing
// between two different criteria. The first is the amount of time that the
// download will take to complete - the algorithm tries to minimize this - and
// the second is cost.
//
// The download is created with an input of 'pricePerMS', which means that the
// download algorithm should pick a slower set of workers so long as the amount
// of money saved is greater than the price per millisecond multiplied by the
// number of milliseconds of slowdown that is incurred by switching to cheaper
// workers.
//
// The algorithm used is fairly involved, but achieves an okay runtime. First,
// the complete set of workers are placed into a heap sorted by their expected
// return time. The fastest workers are sorted to be popped out of the heap
// first.
//
// Because of parallelism, the expected return time of the project is equal to
// the expected return time of the slowest worker. Because of the 'pricePerMS'
// value, we can convert a duration into a price. The total adjusted cost of a
// set of workers is therefore the monetary cost of each worker plus the
// 'pricePerMS' multiplied by the duration until the slowest worker would
// finish.
//
// To get a baseline, we pop off 'MinPieces' workers, tally up the financial
// cost, and track the duration of the slowest worker. We then compute the total
// adjusted cost of using the fastest possible set of workers. We save a copy of
// this set as the 'bestSet', retaining the initial construction as the
// 'workingSet'.
//
// Then we iterate by popping a new worker off of the heap. Two things are at
// play. The first is that this worker may be cheaper than one of our existing
// workers. And the second is that this worker is slower (due to the time sorted
// heap), so therefore may drive the total cost up despite being cheaper. We do
// not know at this time if the optimal set includes an even slower worker. We
// update the working set by replacing the most expensive worker with the new
// worker, assuming that the new worker is cheaper. (if the new worker is not
// cheaper, the new worker is ignored). After the update, we check whether the
// working set's new cost is cheaper than the best set's cost. If so, we
// overwrite the best set with the current working set. If not, we continue
// popping off new workers in pursuit of a cheaper adjusted set of workers.
//
// We know that if the optimal set of workers contains a slower worker than the
// current worker, and the current worker is cheaper than an existing worker,
// then there is no time penalty to swapping out an existing worker for the
// current cheaper worker. Keeping a best set and a working set allows us to
// build towards the optimal set even if there are suboptimal increments along
// the way.
//
// There are two complications. The first complication is that not every worker
// can fetch every piece. The second complication is that some workers can fetch
// multiple pieces.
//
// For workers that cannot fetch every piece, we will only consider the pieces
// that they can fetch. If they can fetch a piece that has no worker in it, that
// worker will be added to the workingSet and the most expensive worker will be
// evicted. If a new worker only has pieces that overlap with workers already in
// the workingSet, the new worker will evict the most expensive worker that it
// is capable of replacing. If it cannot replace anyone because everyone it
// could replace is already cheaper, the new worker will be ignored.
//
// When an existing worker is evicted, it will go back into the heap so that
// there can be an attempt to add it back to the set. The worker that got
// evicted may be able to replace some other worker in the working set.
//
// For workers that can fetch multiple pieces, the worker will be added back
// into the heap after it is inserted into the working set. To account for the
// extra load that is put onto the worker for having to fetch multiple pieces,
// the 'readDuration' of the worker will be added to the 'competeTime' each
// additional time that the worker is put back into the heap. This is overly
// pessimistic, but guarantees that we do not overload a particular worker and
// slow the entire download down.

// maxWaitUnresolvedWorkerUpdate defines the amount of time we want to wait for
// unresolved workers to become resolved when trying to create the initial
// worker set.
const maxWaitUnresolvedWorkerUpdate = 10 * time.Millisecond

// errNotEnoughWorkers is returned if the working set does not have enough
// workers to successfully complete the download
var errNotEnoughWorkers = errors.New("not enough workers to complete download")

// pdcInitialWorker tracks information about a worker that is useful for
// building the optimal set of launch workers.
type pdcInitialWorker struct {
	// The completeTime is the time at which we estimate the worker will have
	// completed the download. It is based on the expected completion time of
	// the  has sector job, plus the readDuration.
	//
	// The cost is the amount of money will be spent on fetching a single piece
	// for this pdc.
	//
	// The readDuration tracks the amount of time the worker is expected to take
	// to execute a read job. The readDuration gets added to the duration each
	// time the worker is added back into the heap to potentially be used an
	// additional time. Technically, the worker is able to fetch in parallel and
	// so assuming an additional full 'readDuration' per read is overly
	// pessimistic, at the same time we prefer to spread our downloads over
	// multiple workers so the pessimism is not too bad.
	completeTime time.Time
	cost         types.Currency
	readDuration time.Duration

	// The list of pieces indicates which pieces the worker is capable of
	// fetching. If 'unresolved' is set to true, the worker will be treated as
	// though it can fetch the first 'MinPieces' pieces.
	pieces     []uint64
	unresolved bool
	worker     *worker
}

// A heap of pdcInitialWorkers that is sorted by 'completeTime'. Workers that
// have a sooner/earlier complete time will be popped off of the heap first.
type pdcWorkerHeap []*pdcInitialWorker

func (wh *pdcWorkerHeap) Len() int { return len(*wh) }
func (wh *pdcWorkerHeap) Less(i, j int) bool {
	return (*wh)[i].completeTime.Before((*wh)[j].completeTime)
}
func (wh *pdcWorkerHeap) Swap(i, j int)      { (*wh)[i], (*wh)[j] = (*wh)[j], (*wh)[i] }
func (wh *pdcWorkerHeap) Push(x interface{}) { *wh = append(*wh, x.(*pdcInitialWorker)) }
func (wh *pdcWorkerHeap) Pop() interface{} {
	old := *wh
	n := len(old)
	x := old[n-1]
	*wh = old[:n-1]
	return x
}

// initialWorkerHeap will create a heap with all of the potential workers for
// this piece. It will include all of the unresolved workers, and it will
// attempt to exclude any workers that are known to be non-viable - for example
// workers with no pieces that can be resolved or workers that are currently on
// cooldown for the read job. The worker heap optimizes for speed, not cost.
// Cost is taken into account at a later point where the initial worker set is
// built.
func (pdc *projectDownloadChunk) initialWorkerHeap(unresolvedWorkers []*pcwsUnresolvedWorker, unresolvedWorkerTimePenalty time.Duration) pdcWorkerHeap {
	// Add all of the unresolved workers to the heap.
	var workerHeap pdcWorkerHeap
	for _, uw := range unresolvedWorkers {
		// Ignore workers that are on a maintenance cooldown. Good performing
		// workers are generally never on maintenance cooldown, so by skipping
		// them here we avoid ever waiting for them to resolve.
		if uw.staticWorker.managedOnMaintenanceCooldown() {
			continue
		}

		// Verify whether the read queue is on a cooldown, if so skip this
		// worker.
		jrq := uw.staticWorker.staticJobReadQueue
		if jrq.callOnCooldown() {
			continue
		}

		// Fetch the resolveTime, which is the time until the HS job is expected
		// to resolve. If that time is in the past, set it to a time in the
		// future, equal to the amount that it's late.
		resolveTime := uw.staticExpectedResolvedTime
		if resolveTime.Before(time.Now()) {
			resolveTime = time.Now().Add(time.Since(resolveTime))
		}

		// Determine the expected readDuration and cost for this worker. Add the
		// readDuration to the hasSectorTime to get the full
		// complete time for the download
		cost := jrq.callExpectedJobCost(pdc.pieceLength)
		readDuration := jrq.callExpectedJobTime(pdc.pieceLength)
		if readDuration == 0 {
			continue
		}

		completeTime := resolveTime.Add(readDuration).Add(unresolvedWorkerTimePenalty)

		// Create the pieces for the unresolved worker. Because the unresolved
		// worker could be potentially used to fetch any piece (we won't know
		// until the resolution is complete), we add a set of pieces as though
		// the worker could single-handedly complete all of the pieces.
		pieces := make([]uint64, pdc.workerSet.staticErasureCoder.MinPieces())
		for i := 0; i < len(pieces); i++ {
			pieces[i] = uint64(i)
		}

		// Push the element into the heap.
		heap.Push(&workerHeap, &pdcInitialWorker{
			completeTime: completeTime,
			cost:         cost,
			readDuration: readDuration,

			pieces:     pieces,
			unresolved: true,
			worker:     uw.staticWorker,
		})
	}

	// Add the resolved workers to the heap. In the worker state, the resolved
	// workers are organized as a series of available pieces, because that is
	// what made the overdrive code the easiest.
	resolvedWorkersMap := make(map[string]*pdcInitialWorker)
	for i, piece := range pdc.availablePieces {
		for _, pieceDownload := range piece {
			w := pieceDownload.worker
			pt := w.staticPriceTable().staticPriceTable
			allowance := w.staticCache().staticRenterAllowance

			// Ignore this worker if its host is considered to be price gouging.
			err := checkProjectDownloadGouging(pt, allowance)
			if err != nil {
				continue
			}

			// Ignore this worker if the worker is not currently equipped to
			// perform async work, or if the read queue is on a cooldown.
			jrq := w.staticJobReadQueue
			if !w.managedAsyncReady() || w.staticJobReadQueue.callOnCooldown() {
				continue
			}

			// If the worker is already in the resolved workers map, add this
			// piece to the set of pieces the worker can complete. Otherwise,
			// create a new element for this worker.
			elem, exists := resolvedWorkersMap[w.staticHostPubKeyStr]
			if exists {
				// Elem is a pointer, so the map does not need to be updated.
				elem.pieces = append(elem.pieces, uint64(i))
			} else {
				cost := jrq.callExpectedJobCost(pdc.pieceLength)
				readDuration := jrq.callExpectedJobTime(pdc.pieceLength)
				resolvedWorkersMap[w.staticHostPubKeyStr] = &pdcInitialWorker{
					completeTime: time.Now().Add(readDuration),
					cost:         cost,
					readDuration: readDuration,

					pieces:     []uint64{uint64(i)},
					unresolved: false,
					worker:     w,
				}
			}
		}
	}

	// Push a pdcInitialWorker into the heap for each worker in the resolved
	// workers map.
	for _, rw := range resolvedWorkersMap {
		heap.Push(&workerHeap, rw)
	}
	return workerHeap
}

// createInitialWorkerSet will go through the current set of workers and
// determine the best set of workers to use when attempting to download a piece.
// Note that we only return this best set if all workers from the worker set are
// resolved, if that is not the case we simply return nil.
func (pdc *projectDownloadChunk) createInitialWorkerSet(workerHeap pdcWorkerHeap) ([]*pdcInitialWorker, error) {
	// Convenience variable.
	ec := pdc.workerSet.staticErasureCoder
	gs := types.NewCurrency(new(big.Int).Exp(big.NewInt(10), big.NewInt(33), nil)) // 1GS

	// Keep track of the current best set, and the amount of time it will take
	// the best set to return. And keep track of the current working set, and
	// the amount of time it will take the current working set to return.
	//
	// The total adjusted cost of a set is the cost of launching each of its
	// individual workers, plus a single adjustment for the duration of the set.
	// The duration of the set is the longest of any duration of its individual
	// workers.
	//
	// The algorithm for finding the best set is to start by adding all of the
	// fastest workers, and putting them into the best set. Then, we copy the
	// best set into the working set. We add slower workers to the working set
	// one at a time. Each time we add a worker, we replace any of the faster
	// workers that is more expensive than the slower worker. When we are done,
	// we look at the new total adjusted cost of the working set. If it is less
	// than the best set, we replace the best set with the current working set
	// and continue building out the working set. If it is not better than the
	// best set, we just keep building out the working set. This is guaranteed
	// to find the optimal best set while only using a linear amount of total
	// computation.
	bestSet := make([]*pdcInitialWorker, ec.NumPieces())
	workingSet := make([]*pdcInitialWorker, ec.NumPieces())

	bestSetCost := gs
	var workingSetCost types.Currency
	var workingSetDuration time.Duration

	// Build the best set that we can. Each iteration will attempt to improve
	// the working set by adding a new worker. This may or may not succeed,
	// depending on how cheap the worker is and how slow the worker is. Each
	// time that the working set is better than the best set, overwrite the best
	// set with the new working set.
	for len(workerHeap) > 0 {
		// Grab the next worker from the heap.
		nextWorker := heap.Pop(&workerHeap).(*pdcInitialWorker)
		if nextWorker == nil {
			build.Critical("wasn't expecting to pop a nil worker")
			break
		}

		// Iterate through the working set and determine the cost and index of
		// the most expensive worker. If the new worker is not cheaper, the
		// working set cannot be updated.
		highestCost := types.ZeroCurrency
		highestCostIndex := 0
		totalWorkers := 0
		for i := 0; i < len(workingSet); i++ {
			if workingSet[i] == nil {
				continue
			}
			if workingSet[i].cost.Cmp(highestCost) > 0 {
				highestCost = workingSet[i].cost
				highestCostIndex = i
			}
			totalWorkers++
		}

		// Consistency check: we should never have more than MinPieces workers
		// assigned.
		if totalWorkers > ec.MinPieces() {
			pdc.workerSet.staticRenter.log.Critical("total workers mistake in download code", totalWorkers, ec.MinPieces())
		}
		enoughWorkers := totalWorkers == ec.MinPieces()

		// If the time cost of this worker is strictly higher than the full cost
		// of the best set, there can be no more improvements to the best set,
		// and the loop can exit.
		workerTimeCost := pdc.pricePerMS.Mul64(uint64(nextWorker.readDuration.Milliseconds()))
		if workerTimeCost.Cmp(bestSetCost) > 0 && enoughWorkers {
			break
		}

		// If all workers in the working set are already cheaper than this
		// worker, skip this worker.
		if highestCost.Cmp(nextWorker.cost) <= 0 && enoughWorkers {
			continue
		}

		// Find a spot for this new worker. The new worker only gets a spot if
		// it can fit into an empty spot, or if it can evict an existing worker
		// and have a better cost. If there are multiple spots where an eviction
		// could happen, the most expensive should be evicted. Going into an
		// empty spot is best, because that means we can evict the most
		// expensive worker in the whole working set.
		workerUseful := false
		bestSpotEmpty := false
		bestSpotCost := nextWorker.cost // this will cause the loop to ignore workers that are already better than nextWorker
		bestSpotIndex := uint64(0)
		bestSpotPiecePos := 0
		for i, index := range nextWorker.pieces {
			if workingSet[index] == nil {
				bestSpotEmpty = true
				bestSpotIndex = index
				bestSpotPiecePos = i
				break
			}
			if workingSet[index].cost.Cmp(bestSpotCost) > 0 {
				workerUseful = true
				bestSpotCost = workingSet[index].cost
				bestSpotIndex = index
				bestSpotPiecePos = i
			}
		}

		// Check whether the worker is useful at all. It may not be useful if
		// the only pieces it has are already available via cheaper workers.
		if !bestSpotEmpty && !workerUseful {
			continue
		}

		// We know for certain now that the current worker is useful. Update the
		// duration of the working set to be the speed of the nextWorker if the
		// nextWorker is slower.
		//
		// nextWorker may not be slower if it was re-added to the heap in a
		// previous interation due to being evicted from its spot. If it was
		// evicted and re-added, that means there is hope that this worker was
		// useful in a different place.
		if nextWorker.readDuration > workingSetDuration {
			workingSetDuration = nextWorker.readDuration
		}

		// Perform the actual replacement. Remember to update the total cost of
		// the working set. In the event of an in-place eviction, the evicted
		// worker is put back into the heap so that we can check whether there
		// is another more suitable slot for the evicted worker.
		newWorker := false // helps determine whether the best set should be made.
		if bestSpotEmpty {
			workingSetCost = workingSetCost.Add(nextWorker.cost)
			workingSet[bestSpotIndex] = nextWorker

			// Only do the eviction if we already have enough workers.
			if enoughWorkers {
				workingSetCost = workingSetCost.Sub(highestCost)
				heap.Push(&workerHeap, workingSet[highestCostIndex])
				workingSet[highestCostIndex] = nil
			} else {
				newWorker = true
			}
		} else {
			workingSetCost = workingSetCost.Add(nextWorker.cost)
			workingSetCost = workingSetCost.Sub(workingSet[bestSpotIndex].cost)
			heap.Push(&workerHeap, workingSet[bestSpotIndex])
			workingSet[bestSpotIndex] = nextWorker
		}

		// Determine whether the working set is now cheaper than the best set.
		// Adding in the new worker has made the working set cheaper in terms of
		// raw cost, but the new worker is slower, so the time penalty has gone
		// up.
		workingSetTimeCost := pdc.pricePerMS.Mul64(uint64(workingSetDuration.Milliseconds()))
		workingSetTotalCost := workingSetCost.Add(workingSetTimeCost)
		if newWorker || workingSetTotalCost.Cmp(bestSetCost) < 0 {
			bestSetCost = workingSetTotalCost
			// Do a copy operation. Can't set one equal to the other because
			// then changes to the working set will update the best set.
			copy(bestSet, workingSet)
		}

		// Create a new entry for 'nextWorker' and push that entry back into the
		// heap. This is in case 'nextWorker' is able to fetch multiple pieces.
		// The duration of the next worker will be increased by the
		// 'readDuration' as a worst case estmiation of what the performance hit
		// will be for using the same worker multiple times.
		if len(nextWorker.pieces) > 1 {
			copyWorker := *nextWorker

			// ensure we're not in an infite loop here by removing the piece
			// we're using this worker for from the copy worker's list of pieces
			piecesLen := len(copyWorker.pieces)
			copyWorker.pieces[bestSpotPiecePos] = copyWorker.pieces[piecesLen-1]
			copyWorker.pieces = copyWorker.pieces[:piecesLen-1]

			copyWorker.completeTime = nextWorker.completeTime.Add(nextWorker.readDuration)
			heap.Push(&workerHeap, &copyWorker)
		}
	}

	// We now have the best set. If the best set does not have enough workers to
	// complete the download, return an error. If the best set has enough
	// workers to complete the download but some of the workers in the best set
	// are yet unresolved, return the updateChan and everything else is nil, if
	// the best set is done and all of the workers in the best set are resolved,
	// return the best set and everything else is nil.
	totalWorkers := 0
	isUnresolved := false
	for _, worker := range bestSet {
		if worker == nil {
			continue
		}
		totalWorkers++
		isUnresolved = isUnresolved || worker.unresolved
	}

	if totalWorkers < ec.MinPieces() {
		return nil, errors.AddContext(errNotEnoughWorkers, fmt.Sprintf("%v < %v", totalWorkers, ec.MinPieces()))
	}

	if isUnresolved {
		return nil, nil
	}

	return bestSet, nil
}

// launchInitialWorkers will pick the initial set of workers that needs to be
// launched and then launch them. This is a non-blocking function that returns
// once jobs have been scheduled for MinPieces workers.
func (pdc *projectDownloadChunk) launchInitialWorkers() error {
	start := time.Now()

	for {
		// Get the list of unresolved workers. This will also grab an update, so
		// any workers that have resolved recently will be reflected in the
		// newly returned set of values.
		unresolvedWorkers, updateChan := pdc.unresolvedWorkers()

		// Create a list of usable workers, sorted by the amount of time they
		// are expected to take to return. We pass in the time since we've
		// initially tried to launch the initial set of workers, this time is
		// being used as a time penalty which we'll attribute to unresolved
		// workers. Ensuring resolved workers are being selected if we're
		// waiting too long for unresolved workers to resolve.
		unresolvedWorkerPenalty := time.Since(start)
		workerHeap := pdc.initialWorkerHeap(unresolvedWorkers, unresolvedWorkerPenalty)

		// Create an initial worker set
		finalWorkers, err := pdc.createInitialWorkerSet(workerHeap)
		if err != nil {
			return errors.AddContext(err, "unable to build initial set of workers")
		}

		// If the function returned an actual set of workers, we are good to
		// launch.
		if finalWorkers != nil {
			for i, fw := range finalWorkers {
				if fw == nil {
					continue
				}
				pdc.launchWorker(fw.worker, uint64(i), false)
			}
			return nil
		}

		select {
		case <-updateChan:
		case <-time.After(maxWaitUnresolvedWorkerUpdate):
			// We want to limit the amount of time spent waiting for unresolved
			// workers to become resolved. This is because we assign a penalty
			// to unresolved workers, and on every iteration this penalty might
			// have caused an already resolved worker to be favoured over the
			// unresolved worker in the set.
		case <-pdc.ctx.Done():
			return errors.New("timed out while trying to build initial set of workers")
		}
	}
}

// checkProjectDownloadGouging verifies the cost of executing the jobs performed
// by the project download are reasonable in relation to the user's allowance
// and the amount of data they intend to download
func checkProjectDownloadGouging(pt modules.RPCPriceTable, allowance modules.Allowance) error {
	// Check whether the download bandwidth price is too high.
	if !allowance.MaxDownloadBandwidthPrice.IsZero() && allowance.MaxDownloadBandwidthPrice.Cmp(pt.DownloadBandwidthCost) < 0 {
		return fmt.Errorf("download bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v - price gouging protection enabled", pt.DownloadBandwidthCost, allowance.MaxDownloadBandwidthPrice)
	}

	// Check whether the upload bandwidth price is too high.
	if !allowance.MaxUploadBandwidthPrice.IsZero() && allowance.MaxUploadBandwidthPrice.Cmp(pt.UploadBandwidthCost) < 0 {
		return fmt.Errorf("upload bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v - price gouging protection enabled", pt.UploadBandwidthCost, allowance.MaxUploadBandwidthPrice)
	}

	// If there is no allowance, price gouging checks have to be disabled,
	// because there is no baseline for understanding what might count as price
	// gouging.
	if allowance.Funds.IsZero() {
		return nil
	}

	// In order to decide whether or not the cost of performing a PDBR is too
	// expensive, we make some assumptions with regards to lookup vs download
	// job ratio and avg download size. The total cost is then compared in
	// relation to the allowance, where we verify that a fraction of the cost
	// (which we'll call reduced cost) to download the amount of data the user
	// intends to download does not exceed its allowance.

	// Calculate the cost of a has sector job
	pb := modules.NewProgramBuilder(&pt, 0)
	pb.AddHasSectorInstruction(crypto.Hash{})
	programCost, _, _ := pb.Cost(true)

	ulbw, dlbw := hasSectorJobExpectedBandwidth(1)
	bandwidthCost := modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costHasSectorJob := programCost.Add(bandwidthCost)

	// Calculate the cost of a read sector job, we use StreamDownloadSize as an
	// average download size here which is 64 KiB.
	pb = modules.NewProgramBuilder(&pt, 0)
	pb.AddReadSectorInstruction(modules.StreamDownloadSize, 0, crypto.Hash{}, true)
	programCost, _, _ = pb.Cost(true)

	ulbw, dlbw = readSectorJobExpectedBandwidth(modules.StreamDownloadSize)
	bandwidthCost = modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costReadSectorJob := programCost.Add(bandwidthCost)

	// Calculate the cost of a project
	costProject := costReadSectorJob.Add(costHasSectorJob.Mul64(uint64(sectorLookupToDownloadRatio)))

	// Now that we have the cost of each job, and we estimate a sector lookup to
	// download ratio of 16, all we need to do is calculate the number of
	// projects necessary to download the expected download amount.
	numProjects := allowance.ExpectedDownload / modules.StreamDownloadSize

	// The cost of downloading is considered too expensive if the allowance is
	// insufficient to cover a fraction of the expense to download the amount of
	// data the user intends to download
	totalCost := costProject.Mul64(numProjects)
	reducedCost := totalCost.Div64(downloadGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		return fmt.Errorf("combined PDBR pricing of host yields %v, which is more than the renter is willing to pay for downloads: %v - price gouging protection enabled", reducedCost, allowance.Funds)
	}

	return nil
}
