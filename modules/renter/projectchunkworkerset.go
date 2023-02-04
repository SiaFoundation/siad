package renter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrRootNotFound is returned if all workers were unable to recover the
	// root
	ErrRootNotFound = errors.New("workers were unable to recover the data by sector root - all workers failed")

	// ErrProjectTimedOut is returned when the project timed out
	ErrProjectTimedOut = errors.New("project timed out")

	// pcwsWorkerStateResetTime defines the amount of time that the pcws will
	// wait before resetting / refreshing the worker state, meaning that all of
	// the workers will do another round of HasSector queries on the network.
	pcwsWorkerStateResetTime = build.Select(build.Var{
		Dev:      time.Minute * 10,
		Standard: time.Hour * 9,
		Testnet:  time.Hour * 9,
		Testing:  time.Second * 15,
	}).(time.Duration)

	// pcwsHasSectorTimeout defines the amount of time that the pcws will wait
	// before giving up on receiving a HasSector response from a single worker.
	// This value is set as a global timeout because different download queries
	// that have different timeouts will use the same projectChunkWorkerSet.
	pcwsHasSectorTimeout = build.Select(build.Var{
		Dev:      time.Minute * 1,
		Standard: time.Minute * 3,
		Testnet:  time.Minute * 3,
		Testing:  time.Second * 10,
	}).(time.Duration)

	// sectorLookupToDownloadRatio is an arbitrary ratio that resembles the
	// amount of lookups vs downloads. It is used in price gouging checks.
	sectorLookupToDownloadRatio = 16
)

const (
	// pcwsGougingFractionDenom is used to identify what percentage of the
	// allowance is allowed to be spent on HasSector jobs before a worker is
	// flagged for being too expensive.
	//
	// For example, if the denom is 10, that means that if a worker's HasSector
	// cost multiplied by the total expected number of HasSector jobs to be
	// performed in a period exceeds 10% of the allowance, that worker will be
	// flagged for price gouging. If the denom is 100, the worker will be
	// flagged if the HasSector cost reaches 1% of the total cost of the
	// allowance.
	pcwsGougingFractionDenom = 25
)

// pcwsUnreseovledWorker tracks an unresolved worker that is associated with a
// specific projectChunkWorkerSet. The timestamp indicates when the unresolved
// worker is expected to have a resolution, and is an estimate based on historic
// performance from the worker.
type pcwsUnresolvedWorker struct {
	// The expected time that the worker will resolve. A worker is considered
	// resolved if the HasSector job has finished.
	staticExpectedResolvedTime time.Time

	// The worker that is performing the HasSector job.
	staticWorker *worker
}

// pcwsWorkerResponse contains a worker's response to a HasSector query. There
// is a list of piece indices where the worker responded that they had the piece
// at that index. There is also an error field that will be set in the event an
// error occurred while performing the HasSector query.
type pcwsWorkerResponse struct {
	worker       *worker
	pieceIndices []uint64
	err          error
}

// pcwsWorkerState contains the worker state for a single thread that is
// resolving which workers have which pieces. When the projectChunkWorkerSet
// resets, it does so by spinning up a new pcwsWorkerState and then replacing
// the old worker state with the new worker state. The new worker state will
// send out a new round of HasSector queries to the network.
type pcwsWorkerState struct {
	// unresolvedWorkers is the set of workers that are currently running
	// HasSector programs and have not yet finished.
	//
	// A map is used so that workers can be removed from the set in constant
	// time as they complete their HasSector jobs.
	unresolvedWorkers map[string]*pcwsUnresolvedWorker

	// ResolvedWorkers is an array that tracks which workers have responded to
	// HasSector queries and which sectors are available. This array is only
	// appended to as workers come back, meaning that chunk downloads can track
	// internally which elements of the array they have already looked at,
	// saving computational time when updating.
	resolvedWorkers []*pcwsWorkerResponse

	// workerUpdateChans is used by download objects to block until more
	// information about the unresolved workers is available. All of the worker
	// update chans will be closed each time an unresolved worker returns a
	// response (regardless of whether the response is positive or negative).
	// The array will then be cleared.
	//
	// NOTE: Once 'unresolvedWorkers' has a length of zero, any attempt to add a
	// channel to the set of workerUpdateChans should fail, as there will be no
	// more updates. This is specific to this particular worker state, the
	// pcwsWorkerSet as a whole can be reset by replacing the worker state.
	workerUpdateChans []chan struct{}

	// Utilities.
	staticRenter *Renter
	mu           sync.Mutex
}

// projectChunkWorkerSet is an object that contains a set of workers that can be
// used to download a single chunk. The object can be initialized with a siafile
// where the host-root pairs are already known (for traditional renter
// downloads).
//
// If the pcws is initialized with only a set of roots, it will immediately spin
// up a bunch of worker jobs to locate those roots on the network using
// HasSector programs.
//
// Once the pcws has been initialized, it can be used repeatedly to download
// data from the chunk, and it will not need to repeat the network lookups.
// Every few hours (pcwsWorkerStateResetTime), it will re-do the lookups to
// ensure that it is up-to-date on the best way to download the file.
type projectChunkWorkerSet struct {
	// workerState is a pointer to a single pcwsWorkerState, specifically the
	// most recent worker state that has launched. The workerState is
	// responsible for querying the network with HasSector requests and
	// determining which workers are able to download which pieces of the chunk.
	//
	// workerStateLaunchTime indicates when the workerState was launched, which
	// is used to figure out when the worker state should be refreshed.
	//
	// updateInProgress and updateFinishedChan are used to ensure that only one
	// worker state is being refreshed at a time. Before a workerState refresh
	// begins, the projectChunkWorkerSet is locked and the updateInProgress
	// value is set to 'true'. At the same time, a new 'updateFinishedChan' is
	// created. Then the projectChunkWorkerSet is unlocked. New threads that try
	// to launch downloads will see that there is an update in progress and will
	// wait on the 'updateFinishedChan' to close before grabbing the new
	// workerState. When the new workerState is done being initialized, the
	// projectChunkWorkerSet is locked and the updateInProgress field is set to
	// false, the workerState is updated to the new state, and the
	// updateFinishedChan is closed.
	updateInProgress      bool
	updateFinishedChan    chan struct{}
	workerState           *pcwsWorkerState
	workerStateLaunchTime time.Time

	// Decoding and decryption information for the chunk.
	staticChunkIndex   uint64
	staticErasureCoder modules.ErasureCoder
	staticMasterKey    crypto.CipherKey
	staticPieceRoots   []crypto.Hash

	// Utilities
	staticCtx    context.Context
	staticRenter *Renter
	mu           sync.Mutex
}

// chunkFetcher is an interface that exposes a download function, the PCWS
// implements this interface.
type chunkFetcher interface {
	Download(ctx context.Context, pricePerMS types.Currency, offset, length uint64) (chan *downloadResponse, error)
}

// Download will download a range from a chunk.
func (pcws *projectChunkWorkerSet) Download(ctx context.Context, pricePerMS types.Currency, offset, length uint64) (chan *downloadResponse, error) {
	return pcws.managedDownload(ctx, pricePerMS, offset, length)
}

// checkPCWSGouging verifies the cost of grabbing the HasSector information from
// a host is reasonble. The cost of completing the download is not checked.
//
// NOTE: The logic in this function assumes that every pcws results in just one
// download. The reality is that depending on the type of use case, there may be
// significantly less than 1 download per pcws (for single-user nodes that
// frequently open large movies without watching the full movie), or
// significantly more than one download per pcws (for multi-user nodes where
// users most commonly are using the same file over and over).
func checkPCWSGouging(pt modules.RPCPriceTable, allowance modules.Allowance, numWorkers int, numRoots int) error {
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

	// Calculate the cost of a has sector job.
	pb := modules.NewProgramBuilder(&pt, 0)
	for i := 0; i < numRoots; i++ {
		pb.AddHasSectorInstruction(crypto.Hash{})
	}
	programCost, _, _ := pb.Cost(true)
	ulbw, dlbw := hasSectorJobExpectedBandwidth(numRoots)
	bandwidthCost := modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costHasSectorJob := programCost.Add(bandwidthCost)

	// Determine based on the allowance the number of HasSector jobs that would
	// need to be performed under normal conditions to reach the desired amount
	// of total data.
	requiredProjects := allowance.ExpectedDownload / modules.StreamDownloadSize
	requiredHasSectorQueries := requiredProjects * uint64(numWorkers)

	// Determine the total amount that we'd be willing to spend on all of those
	// queries before considering the host complicit in gouging.
	totalCost := costHasSectorJob.Mul64(requiredHasSectorQueries)
	reducedAllowance := allowance.Funds.Div64(pcwsGougingFractionDenom)

	// Check that we do not consider the host complicit in gouging.
	if totalCost.Cmp(reducedAllowance) > 0 {
		errStr := fmt.Sprintf("the cost of performing a HasSector job is too high - price gouging protection enabled")
		return errors.New(errStr)
	}
	return nil
}

// closeUpdateChans will close all of the update chans and clear out the slice.
// This will cause any threads waiting for more results from the unresolved
// workers to unblock.
//
// Typically there will be a small number of channels, often 0 and often just 1.
func (ws *pcwsWorkerState) closeUpdateChans() {
	for _, c := range ws.workerUpdateChans {
		close(c)
	}
	ws.workerUpdateChans = nil
}

// registerForWorkerUpdate will create a channel and append it to the list of
// update chans in the worker state. When there is more information available
// about which worker is the best worker to select, the channel will be closed.
func (ws *pcwsWorkerState) registerForWorkerUpdate() <-chan struct{} {
	// Return a nil channel if there are no more unresolved workers.
	if len(ws.unresolvedWorkers) == 0 {
		return nil
	}

	// Create the channel that will be closed when the set of unresolved workers
	// has been updated.
	c := make(chan struct{})
	ws.workerUpdateChans = append(ws.workerUpdateChans, c)
	return c
}

// managedHandleResponse will handle a HasSector response from a worker,
// updating the workerState accordingly.
//
// The worker's response will be included into the resolvedWorkers even if it is
// emptied or errored because the worker selection algorithms in the downloads
// may wish to be able to view which workers have failed. This is currently
// unused, but certain computational optimizations in the future depend on it.
func (ws *pcwsWorkerState) managedHandleResponse(resp *jobHasSectorResponse) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Defer closing the update chans to signal we've received and processed an
	// HS response.
	defer ws.closeUpdateChans()

	// Delete the worker from the set of unresolved workers.
	w := resp.staticWorker
	if w == nil {
		ws.staticRenter.log.Critical("nil worker provided in resp")
	}
	delete(ws.unresolvedWorkers, w.staticHostPubKeyStr)

	// If the response contained an error, add this worker to the set of
	// resolved workers as supporting no indices.
	if resp.staticErr != nil {
		ws.resolvedWorkers = append(ws.resolvedWorkers, &pcwsWorkerResponse{
			worker: w,
			err:    resp.staticErr,
		})
		return
	}

	// Create the list of pieces that the worker supports and add it to the
	// worker set.
	var indices []uint64
	for i, available := range resp.staticAvailables {
		if available {
			indices = append(indices, uint64(i))
		}
	}
	// Add this worker to the set of resolved workers (even if there are no
	// indices that the worker can fetch).
	ws.resolvedWorkers = append(ws.resolvedWorkers, &pcwsWorkerResponse{
		worker:       w,
		pieceIndices: indices,
	})
}

// managedLaunchWorker will launch a job to determine which sectors of a chunk
// are available through that worker. The resulting unresolved worker is
// returned so it can be added to the pending worker state.
func (pcws *projectChunkWorkerSet) managedLaunchWorker(ctx context.Context, w *worker, responseChan chan *jobHasSectorResponse, ws *pcwsWorkerState) error {
	// Check for gouging.
	cache := w.staticCache()
	pt := w.staticPriceTable().staticPriceTable
	numWorkers := pcws.staticRenter.staticWorkerPool.callNumWorkers()
	err := checkPCWSGouging(pt, cache.staticRenterAllowance, numWorkers, len(pcws.staticPieceRoots))
	if err != nil {
		pcws.staticRenter.log.Debugf("price gouging for chunk worker set detected in worker %v, err %v", w.staticHostPubKeyStr, err)
		return err
	}

	// Check whether the worker is on a cooldown. Because the PCWS is cached, we
	// do not want to exclude this worker if it is on a cooldown, however we do
	// want to take into consideration the cooldown period when we estimate the
	// expected resolve time.
	var coolDownPenalty time.Duration
	if w.managedOnMaintenanceCooldown() {
		wms := w.staticMaintenanceState
		wms.mu.Lock()
		coolDownPenalty = time.Until(wms.cooldownUntil)
		wms.mu.Unlock()
	}

	// Create and launch the job.
	jhs := w.newJobHasSector(ctx, responseChan, pcws.staticPieceRoots...)
	expectedJobTime, err := w.staticJobHasSectorQueue.callAddWithEstimate(jhs)
	if err != nil {
		pcws.staticRenter.log.Debugf("unable to add has sector job to %v, err %v", w.staticHostPubKeyStr, err)
		return err
	}
	expectedResolveTime := expectedJobTime.Add(coolDownPenalty)

	// Create the unresolved worker for this job.
	uw := &pcwsUnresolvedWorker{
		staticWorker:               w,
		staticExpectedResolvedTime: expectedResolveTime,
	}

	// Add the unresolved worker to the worker state. Technically this doesn't
	// need to be wrapped in a lock, but that's not obvious from the function
	// context so we wrap it in a lock anyway. There will be no contention, so
	// there should be minimal performance overhead.
	ws.mu.Lock()
	ws.unresolvedWorkers[w.staticHostPubKeyStr] = uw
	ws.mu.Unlock()
	return nil
}

// threadedFindWorkers will spin up a bunch of jobs to determine which workers
// have what pieces for the pcws, and then update the input worker state with
// the results.
func (pcws *projectChunkWorkerSet) threadedFindWorkers(allWorkersLaunchedChan chan<- struct{}, ws *pcwsWorkerState) {
	err := pcws.staticRenter.tg.Add()
	if err != nil {
		return
	}
	defer pcws.staticRenter.tg.Done()

	// Create a context for finding jobs which has a timeout for waiting on
	// HasSector requests to return.
	ctx, cancel := context.WithTimeout(pcws.staticCtx, pcwsHasSectorTimeout)
	defer cancel()

	// Launch all of the HasSector jobs for each worker. A channel is needed to
	// receive the responses, and the channel needs to be buffered to be equal
	// in size to the number of queries so that none of the workers sending
	// reponses get blocked sending down the channel.
	workers := ws.staticRenter.staticWorkerPool.callWorkers()
	workersLaunched := 0
	responseChan := make(chan *jobHasSectorResponse, len(workers))
	for _, w := range workers {
		err := pcws.managedLaunchWorker(ctx, w, responseChan, ws)
		if err == nil {
			workersLaunched++
		}
	}

	// Signal that all of the workers have launched.
	close(allWorkersLaunchedChan)

	// Because there are timeouts on the HasSector programs, the longest that
	// this loop should be active is a little bit longer than the full timeout
	// for a single HasSector job.
	workersResponded := 0
	for workersResponded < workersLaunched {
		// Block until there is a worker response. Give up if the context times
		// out.
		var resp *jobHasSectorResponse
		select {
		case resp = <-responseChan:
			workersResponded++
		case <-ctx.Done():
			return
		case <-pcws.staticRenter.tg.StopChan():
			return
		}

		// Consistency check - should not be getting nil responses from the
		// workers.
		if resp == nil {
			ws.staticRenter.log.Critical("nil response received")
			continue
		}

		// Parse the response.
		ws.managedHandleResponse(resp)
	}
}

// managedWorkerState returns a pointer to the current worker state object
func (pcws *projectChunkWorkerSet) managedWorkerState() *pcwsWorkerState {
	pcws.mu.Lock()
	defer pcws.mu.Unlock()
	return pcws.workerState
}

// managedTryUpdateWorkerState will check whether the worker state needs to be
// refreshed. If so, it will refresh the worker state.
func (pcws *projectChunkWorkerSet) managedTryUpdateWorkerState() error {
	// The worker state does not need to be refreshed if it is recent or if
	// there is another refresh currently in progress.
	pcws.mu.Lock()
	if pcws.updateInProgress || time.Since(pcws.workerStateLaunchTime) < pcwsWorkerStateResetTime {
		c := pcws.updateFinishedChan
		pcws.mu.Unlock()
		// If there is no update in progress, the channel will already be
		// closed, and therefore listening on the channel will return
		// immediately.
		<-c
		return nil
	}
	// An update is needed. Set the flag that an update is in progress.
	pcws.updateInProgress = true
	pcws.updateFinishedChan = make(chan struct{})
	pcws.mu.Unlock()

	// Create the new worker state and launch the thread that will create worker
	// jobs and collect responses from the workers.
	//
	// The concurrency here is a bit awkward because jobs cannot be launched
	// while the pcws lock is held, the workerState of the pcws cannot be set
	// until all the jobs are launched, and the context for timing out the
	// worker jobs needs to be created in the same thread that listens for the
	// responses. Though there are a lot of concurrency patterns at play here,
	// it was the cleanest thing I could come up with.
	allWorkersLaunchedChan := make(chan struct{})
	ws := &pcwsWorkerState{
		unresolvedWorkers: make(map[string]*pcwsUnresolvedWorker),

		staticRenter: pcws.staticRenter,
	}

	// Launch the thread to find the workers for this launch state.
	err := pcws.staticRenter.tg.Launch(func() {
		pcws.threadedFindWorkers(allWorkersLaunchedChan, ws)
	})
	if err != nil {
		// If there is an error, need to reset the in-progress fields. This will
		// result in the worker set continuing to use the previous worker state.
		pcws.mu.Lock()
		pcws.updateInProgress = false
		pcws.mu.Unlock()
		close(pcws.updateFinishedChan)
		return errors.AddContext(err, "unable to launch worker set")
	}

	// Wait for the thread to indicate that all jobs are launched, the worker
	// state is not ready for use until all jobs have been launched. After that,
	// update the pcws so that the workerState in the pcws is the newest worker
	// state.
	<-allWorkersLaunchedChan
	pcws.mu.Lock()
	pcws.updateInProgress = false
	pcws.workerState = ws
	pcws.workerStateLaunchTime = time.Now()
	pcws.mu.Unlock()
	close(pcws.updateFinishedChan)
	return nil
}

// managedDownload will download a range from a chunk. This call is
// asynchronous. It will return as soon as the initial sector download requests
// have been sent to the workers. This means that it will block until enough
// workers have reported back with HasSector results that the optimal download
// request can be made. Where possible, the projectChunkWorkerSet should be
// created in advance of the download call, so that the HasSector calls have as
// long as possible to complete, reducing the latency of the actual download
// call.
//
// Blocking until all of the piece downloads have been put into job queues
// ensures that the workers will account for the bandwidth overheads associated
// with these jobs before new downloads are requested. Multiple calls to
// 'managedDownload' from the same thread will generally follow the rule that
// the first calls will return first. This rule cannot be enforced if the call
// to managedDownload returns before the download jobs are queued into the
// workers.
//
// pricePerMS is "price per millisecond". This gives the download code a budget
// to spend on faster workers. For example, if a faster set of workers is
// expected to trim 100 milliseconds off of the download time, the download code
// will select those workers only if the additional expense of using those
// workers is less than 100 * pricePerMS.
func (pcws *projectChunkWorkerSet) managedDownload(ctx context.Context, pricePerMS types.Currency, offset, length uint64) (chan *downloadResponse, error) {
	// Potentially force a timeout via a disrupt for testing.
	if pcws.staticRenter.deps.Disrupt("timeoutProjectDownloadByRoot") {
		return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
	}

	// Convenience variables.
	ec := pcws.staticErasureCoder

	// Depending on the encryption type we might have to download the entire
	// entire chunk. For the ciphers we support, this will be the case when the
	// overhead is not zero. This is due to the overhead being a checksum that
	// has to be verified against the entire piece.
	//
	// NOTE: These checks assume that any upload with encryption overhead needs
	// to be downloaded as full sectors. This feels reasonable because smaller
	// sectors were not supported when encryption schemes with overhead were
	// being suggested.
	if pcws.staticMasterKey.Type().Overhead() != 0 && (offset != 0 || length != modules.SectorSize*uint64(ec.MinPieces())) {
		return nil, errors.New("invalid request performed - this chunk has encryption overhead and therefore the full chunk must be downloaded")
	}

	// Refresh the pcws. This will only cause a refresh if one is necessary.
	err := pcws.managedTryUpdateWorkerState()
	if err != nil {
		return nil, errors.AddContext(err, "unable to initiate download")
	}

	// After refresh, grab the worker state.
	ws := pcws.managedWorkerState()

	// Determine the offset and length that needs to be downloaded from the
	// pieces. This is non-trivial because both the network itself and also the
	// erasure coder have required segment sizes.
	pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)

	// If the pricePerMS is zero, initialize it to 1H to avoid division by zero,
	// or multiplication by zero, possibly resulting in unwanted side-effects in
	// the worker selection and/or any other algorithms.
	if pricePerMS.IsZero() {
		pricePerMS = types.NewCurrency64(1)
	}

	// Create the workerResponseChan.
	//
	// The worker response chan is allocated to be quite large. This is because
	// in the worst case, the total number of jobs submitted will be equal to
	// the number of workers multiplied by the number of pieces. We do not want
	// workers blocking when they are trying to send down the channel, so a very
	// large buffered channel is used. Each element in the channel is only 8
	// bytes (it is just a pointer), so allocating a large buffer doesn't
	// actually have too much overhead. Instead of buffering for a full
	// workers*pieces slots, we buffer for pieces*5 slots, under the assumption
	// that the overdrive code is not going to be so aggressive that 5x or more
	// overhead on download will be needed.
	//
	// If this ends up being a problem, we could implement the jobs process to
	// send the result down a channel in goroutine if the first attempt to send
	// the job fails. Then we could probably get away with a smaller buffer,
	// since exceeding the limit currently would cause a worker to stall, where
	// as with the goroutine-on-block method, exceeding the limit merely causes
	// extra goroutines to be spawned.
	workerResponseChan := make(chan *jobReadResponse, ec.NumPieces()*5)

	// Build the full pdc.
	pdc := &projectDownloadChunk{
		offsetInChunk: offset,
		lengthInChunk: length,

		pieceOffset: pieceOffset,
		pieceLength: pieceLength,

		pricePerMS: pricePerMS,

		availablePieces: make([][]*pieceDownload, ec.NumPieces()),
		dataPieces:      make([][]byte, ec.NumPieces()),

		ctx:                  ctx,
		workerResponseChan:   workerResponseChan,
		downloadResponseChan: make(chan *downloadResponse, 1),
		workerSet:            pcws,
		workerState:          ws,
	}

	// Set debug variables on the pdc
	fastrand.Read(pdc.uid[:])
	pdc.launchTime = time.Now()

	// Launch the initial set of workers for the pdc.
	err = pdc.launchInitialWorkers()
	if err != nil {
		return nil, errors.Compose(err, ErrRootNotFound)
	}

	// All initial workers have been launched. The function can return now,
	// unblocking the caller. A background thread will be launched to collect
	// the responses and launch overdrive workers when necessary.
	go pdc.threadedCollectAndOverdrivePieces()
	return pdc.downloadResponseChan, nil
}

// newPCWSByRoots will create a worker set to download a chunk given just the
// set of sector roots associated with the pieces. The hosts that correspond to
// the roots will be determined by scanning the network with a large number of
// HasSector queries. Once opened, the projectChunkWorkerSet can be used to
// initiate many downloads.
func (r *Renter) newPCWSByRoots(ctx context.Context, roots []crypto.Hash, ec modules.ErasureCoder, masterKey crypto.CipherKey, chunkIndex uint64) (*projectChunkWorkerSet, error) {
	// Check that the number of roots provided is consistent with the erasure
	// coder provided.
	//
	// NOTE: There's a legacy special case where 1-of-N only needs 1 root.
	if len(roots) != ec.NumPieces() && !(len(roots) == 1 && ec.MinPieces() == 1) {
		return nil, fmt.Errorf("%v roots provided, but erasure coder specifies %v pieces", len(roots), ec.NumPieces())
	}

	// Check that the given cipher is not nil, if no encryption is required a
	// plain text cipher key should be passed
	if masterKey == nil {
		return nil, errors.New("master key is nil, if no decryption is required pass a plaintext cipher key")
	}

	// Create the worker set.
	pcws := &projectChunkWorkerSet{
		staticChunkIndex:   chunkIndex,
		staticErasureCoder: ec,
		staticMasterKey:    masterKey,
		staticPieceRoots:   roots,

		staticCtx:    ctx,
		staticRenter: r,
	}

	// The worker state is blank, ensure that everything can get started.
	err := pcws.managedTryUpdateWorkerState()
	if err != nil {
		return nil, errors.AddContext(err, "cannot create a new PCWS")
	}

	// Return the worker set.
	return pcws, nil
}
