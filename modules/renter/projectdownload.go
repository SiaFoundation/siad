package renter

import (
	"bytes"
	"context"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

// errNotEnoughPieces is returned when there are not enough pieces found to
// successfully complete the download
var errNotEnoughPieces = errors.New("not enough pieces to complete download")

// pieceDownload tracks a worker downloading a piece, whether that piece has
// returned, and what time the piece is/was expected to return.
//
// NOTE: The actual piece data is stored in the projectDownloadChunk after the
// download completes.
type pieceDownload struct {
	// 'completed', 'launched', and 'failed' are status variables for the piece.
	// If 'launched' is false, it means the piece download has not started yet.
	// Both 'completed' and 'failed' will also be false.
	//
	// If 'launched' is true and neither 'completed' nor 'failed' is true, it
	// means the download is in progress and the result is not known.
	//
	// Only one of 'completed' and 'failed' can be set to true. 'completed'
	// means the download was successful and the piece data was added to the
	// projectDownloadChunk. 'failed' means the download was unsuccessful.
	completed bool
	failed    bool
	launched  bool

	// expectedCompleteTime indicates the time when the download is expected
	// to complete. This is used to determine whether or not a download is late.
	expectedCompleteTime time.Time

	worker *worker
}

// projectDownloadChunk is a bunch of state that helps to orchestrate a download
// from a projectChunkWorkerSet.
//
// The projectDownloadChunk is only ever accessed by a single thread which
// orchestrates the download, which means that it does not need to be thread
// safe.
type projectDownloadChunk struct {
	// Parameters for downloading within the chunk.
	chunkLength uint64
	chunkOffset uint64
	pricePerMS  types.Currency

	// Values derived from the chunk download parameters. The offset and length
	// specify the offset and length that will be sent to the host, which must
	// be segment aligned.
	pieceLength uint64
	pieceOffset uint64

	// availablePieces are pieces where there are one or more workers that have
	// been tasked with fetching the piece.
	//
	// workersConsideredIndex keeps track of what workers were already
	// considered after looking at the pcws. This enables the worker selection
	// code to realize which pieces in the worker set have been resolved since
	// the last check.
	//
	// workersRemaining is the number of unresolved workers at the time the
	// available pieces were last updated. This enables counting the hopeful
	// pieces without introducing a race condition in the finished check.
	availablePieces        [][]pieceDownload
	workersConsideredIndex int
	workersRemaining       int

	// dataPieces is the buffer that is used to place data as it comes back.
	// There is one piece per chunk, and pieces can be nil. To know if the
	// download is complete, the number of non-nil pieces will be counted.
	dataPieces [][]byte

	// The completed data gets sent down the response chan once the full
	// download is done.
	ctx                  context.Context
	downloadResponseChan chan *downloadResponse
	workerResponseChan   chan *jobReadResponse
	workerSet            *projectChunkWorkerSet
	workerState          *pcwsWorkerState
}

// downloadResponse is sent via a channel to the caller of
// 'projectChunkWorkerSet.managedDownload'.
type downloadResponse struct {
	data []byte
	err  error
}

// unresolvedWorkers will return the set of unresolved workers from the worker
// state of the pdc. This operation will also update the set of available pieces
// within the pdc to reflect any previously unresolved workers that are now
// available workers.
//
// A channel will also be returned which will be closed when there are new
// unresolved workers available.
func (pdc *projectDownloadChunk) unresolvedWorkers() ([]*pcwsUnresolvedWorker, <-chan struct{}) {
	ws := pdc.workerState
	ws.mu.Lock()
	defer ws.mu.Unlock()

	var unresolvedWorkers []*pcwsUnresolvedWorker
	for _, uw := range ws.unresolvedWorkers {
		unresolvedWorkers = append(unresolvedWorkers, uw)
	}
	// Add any new resolved workers to the pdc's list of available pieces.
	for i := pdc.workersConsideredIndex; i < len(ws.resolvedWorkers); i++ {
		// Add the returned worker to available pieces for each piece that the
		// resolved worker has.
		resp := ws.resolvedWorkers[i]
		for _, pieceIndex := range resp.pieceIndices {
			pdc.availablePieces[pieceIndex] = append(pdc.availablePieces[pieceIndex], pieceDownload{
				worker: resp.worker,
			})
		}
	}
	pdc.workersConsideredIndex = len(ws.resolvedWorkers)
	pdc.workersRemaining = len(ws.unresolvedWorkers)

	// If there are more unresolved workers, fetch a channel that will be closed
	// when more results from unresolved workers are available.
	return unresolvedWorkers, ws.registerForWorkerUpdate()
}

// handleJobReadResponse will take a jobReadResponse from a worker job
// and integrate it into the set of pieces.
func (pdc *projectDownloadChunk) handleJobReadResponse(jrr *jobReadResponse) {
	// Prevent a production panic.
	if jrr == nil {
		pdc.workerSet.staticRenter.log.Critical("received nil job read response in handleJobReadResponse")
		return
	}

	// Figure out which index this read corresponds to.
	pieceIndex := 0
	for i, root := range pdc.workerSet.staticPieceRoots {
		if jrr.staticSectorRoot == root {
			pieceIndex = i
			break
		}
	}

	// Check whether the job failed.
	if jrr.staticErr != nil {
		// The download failed, update the pdc available pieces to reflect the
		// failure.
		for i := 0; i < len(pdc.availablePieces[pieceIndex]); i++ {
			if pdc.availablePieces[pieceIndex][i].worker.staticHostPubKeyStr == jrr.staticWorker.staticHostPubKeyStr {
				pdc.availablePieces[pieceIndex][i].failed = true
			}
		}
		return
	}

	// Decrypt the piece that has come back.
	//
	// TODO: The input to DecryptBytesInPlace needs to accept a block index, if
	// we aren't decrypting from the beginning of the chunk this will probably
	// fail.
	key := pdc.workerSet.staticMasterKey.Derive(pdc.workerSet.staticChunkIndex, uint64(pieceIndex))
	_, err := key.DecryptBytesInPlace(jrr.staticData, 0)
	if err != nil {
		pdc.workerSet.staticRenter.log.Println("decryption of a piece failed")
		return
	}

	// The download succeeded, add the piece to the appropriate index.
	pdc.dataPieces[pieceIndex] = jrr.staticData
	jrr.staticData = nil // Just in case there's a reference to the job response elsewhere.
	for i := 0; i < len(pdc.availablePieces[pieceIndex]); i++ {
		if pdc.availablePieces[pieceIndex][i].worker.staticHostPubKeyStr == jrr.staticWorker.staticHostPubKeyStr {
			pdc.availablePieces[pieceIndex][i].completed = true
		}
	}
}

// fail will send an error down the download response channel.
func (pdc *projectDownloadChunk) fail(err error) {
	dr := &downloadResponse{
		data: nil,
		err:  err,
	}
	pdc.downloadResponseChan <- dr
}

// finalize will take the completed pieces of the download, decode them,
// and then send the result down the response channel. If there is an error
// during decode, 'pdc.fail()' will be called.
func (pdc *projectDownloadChunk) finalize() {
	// Helper variable.
	ec := pdc.workerSet.staticErasureCoder

	// The chunk download offset and chunk download length are different from
	// the requested offset and length because the chunk download offset and
	// length are required to be a factor of the segment size of the erasure
	// codes.
	//
	// NOTE: This is one of the places where we assume we are using maximum
	// distance separable erasure codes.
	chunkDLOffset := pdc.pieceOffset * uint64(ec.MinPieces())
	chunkDLLength := pdc.pieceLength * uint64(ec.MinPieces())

	// Recover the pieces in to a single byte slice.
	buf := bytes.NewBuffer(nil)
	err := pdc.workerSet.staticErasureCoder.Recover(pdc.dataPieces, chunkDLOffset+chunkDLLength, buf)
	if err != nil {
		pdc.fail(errors.AddContext(err, "unable to complete erasure decode of download"))
		return
	}
	data := buf.Bytes()

	// The full set of data is recovered, truncate it down to just the pieces of
	// data requested by the user and return. We have downloaded a subset of the
	// chunk as the data, and now we must determine which subset of the data was
	// actually requested by the user.
	chunkStartWithinData := pdc.chunkOffset - chunkDLOffset
	chunkEndWithinData := chunkStartWithinData + pdc.chunkLength
	data = data[chunkStartWithinData:chunkEndWithinData]

	// Return the data to the caller.
	dr := &downloadResponse{
		data: data,
		err:  nil,
	}
	pdc.downloadResponseChan <- dr
}

// finished returns true if the download is finished, and returns an error if
// the download is unable to complete.
func (pdc *projectDownloadChunk) finished() (bool, error) {
	// Convenience variables.
	ec := pdc.workerSet.staticErasureCoder

	// Count the number of completed pieces and hopeful pieces in our list of
	// potential downloads.
	completedPieces := 0
	hopefulPieces := 0
	for _, piece := range pdc.availablePieces {
		// Only count one piece as hopeful per set.
		hopeful := false
		for _, pieceDownload := range piece {
			// If this piece is completed, count it both as hopeful and
			// completed, no need to look at other pieces.
			if pieceDownload.completed {
				hopeful = true
				completedPieces++
				break
			}
			// If this piece has not yet failed, it is hopeful. Keep looking
			// through the pieces in case there is a completed piece.
			if !pieceDownload.failed {
				hopeful = true
			}
		}
		if hopeful {
			hopefulPieces++
		}
	}
	if completedPieces >= ec.MinPieces() {
		return true, nil
	}

	// Count the number of workers that haven't completed their results yet.
	hopefulPieces += pdc.workersRemaining

	// Ensure that there are enough pieces that could potentially become
	// completed to finish the download.
	if hopefulPieces < ec.MinPieces() {
		return false, errNotEnoughPieces
	}
	return false, nil
}

// launchWorker will launch a worker and update the corresponding available
// piece.
//
// A time is returned which indicates the expected return time of the worker's
// download. A bool is returned which indicates whether or not the launch was
// successful.
func (pdc *projectDownloadChunk) launchWorker(w *worker, pieceIndex uint64) (time.Time, bool) {
	// Create the read sector job for the worker.
	//
	// TODO: The launch process should minimally have as input the ctx of
	// the pdc, that way if the pdc closes we know to garbage collect the
	// channel and not send down it. Ideally we can even cancel the job if
	// it is in-flight.
	jrs := &jobReadSector{
		jobRead: jobRead{
			staticResponseChan: pdc.workerResponseChan,
			staticLength:       pdc.pieceLength,

			staticSector: pdc.workerSet.staticPieceRoots[pieceIndex],

			jobGeneric: newJobGeneric(pdc.ctx, w.staticJobReadQueue),
		},
		staticOffset: pdc.pieceOffset,
	}
	// Submit the job.
	expectedCompleteTime, added := w.staticJobReadQueue.callAddWithEstimate(jrs)

	// Update the status of the piece that was launched. 'launched' should be
	// set to 'true'. If the launch failed, 'failed' should be set to 'true'. If
	// the launch succeeded, the expected completion time of the job should be
	// set.
	//
	// NOTE: We don't break out of the loop when we find a piece/worker
	// match. If all is going well, each worker should appear at most once
	// in this piece, but for the sake of defensive programming we check all
	// elements anyway.
	for _, pieceDownload := range pdc.availablePieces[pieceIndex] {
		if w.staticHostPubKeyStr == pieceDownload.worker.staticHostPubKeyStr {
			pieceDownload.launched = true
			if added {
				pieceDownload.expectedCompleteTime = expectedCompleteTime
			} else {
				pieceDownload.failed = true
			}
		}
	}
	return expectedCompleteTime, added
}

// threadedCollectAndOverdrivePieces will wait for responses from the workers.
// If workers fail or are late, additional workers will be launched to ensure
// that the download still completes.
func (pdc *projectDownloadChunk) threadedCollectAndOverdrivePieces() {
	// Loop until the download has either failed or completed.
	for {
		// Check whether the download is comlete. An error means that the
		// download has failed and can no longer make progress.
		completed, err := pdc.finished()
		if completed {
			pdc.finalize()
			return
		}
		if err != nil {
			pdc.fail(err)
			return
		}

		// Run the overdrive code. This code needs to be asynchronous so that it
		// does not block receiving on the workerResponseChan. The overdrive
		// code will determine whether launching an overdrive worker is
		// necessary, and will return a channel that will be closed when enough
		// time has elapsed that another overdrive worker should be considered.
		workersUpdatedChan, workersLateChan := pdc.tryOverdrive()

		// Determine when the next overdrive check needs to run.
		select {
		case <-pdc.ctx.Done():
			pdc.fail(errors.New("download failed while waiting for responses"))
			return
		case jrr := <-pdc.workerResponseChan:
			pdc.handleJobReadResponse(jrr)
		case <-workersLateChan:
		case <-workersUpdatedChan:
		}
	}
}

// getPieceOffsetAndLen is a helper function to compute the piece offset and
// length of a chunk download, given the erasure coder for the chunk, the offset
// within the chunk, and the length within the chunk.
func getPieceOffsetAndLen(ec modules.ErasureCoder, offset, length uint64) (pieceOffset, pieceLength uint64) {
	// Fetch the segment size of the ec.
	pieceSegmentSize, partialsSupported := ec.SupportsPartialEncoding()
	if !partialsSupported {
		// If partials are not supported, the full piece needs to be downloaded.
		pieceSegmentSize = modules.SectorSize
	}

	// Consistency check some of the erasure coder values. If the check fails,
	// return that the whole piece must be downloaded.
	if pieceSegmentSize == 0 {
		build.Critical("pcws has a bad erasure coder")
		return 0, modules.SectorSize
	}

	// Determine the download offset within a single piece. We get this by
	// dividing the chunk offset by the number of pieces and then rounding
	// down to the nearest segment size.
	//
	// This is mathematically equivalent to rounding down the chunk size to
	// the nearest chunk segment size and then dividing by the number of
	// pieces.
	pieceOffset = offset / uint64(ec.MinPieces())
	pieceOffset = pieceOffset / pieceSegmentSize
	pieceOffset = pieceOffset * pieceSegmentSize

	// Determine the length that needs to be downloaded. This is done by
	// determining the offset that the download needs to reach, and then
	// subtracting the pieceOffset from the termination offset.
	chunkSegmentSize := pieceSegmentSize * uint64(ec.MinPieces())
	chunkTerminationOffset := offset + length
	overflow := chunkTerminationOffset % chunkSegmentSize
	if overflow != 0 {
		chunkTerminationOffset += chunkSegmentSize - overflow
	}
	pieceTerminationOffset := chunkTerminationOffset / uint64(ec.MinPieces())
	pieceLength = pieceTerminationOffset - pieceOffset
	return pieceOffset, pieceLength
}

// 'managedDownload' will download a range from a chunk. This call is
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
	// Sanity check pricePerMS is greater than zero
	if pricePerMS.IsZero() {
		build.Critical("pricePerMS is expected to be greater than zero")
	}

	// Convenience variables.
	ec := pcws.staticErasureCoder

	// Check encryption type. If the encryption overhead is not zero, the piece
	// offset and length need to download the full chunk. This is due to the
	// overhead being a checksum that has to be verified against the entire
	// piece.
	//
	// NOTE: These checks assume that any upload with encryption overhead needs
	// to be downloaded as full sectors. This feels reasonable because smaller
	// sectors were not supported when encryption schemes with overhead were
	// being suggested.
	if pcws.staticMasterKey.Type().Overhead() != 0 && (offset != 0 || length != modules.SectorSize*uint64(ec.MinPieces())) {
		return nil, errors.New("invalid request performed - this chunk has encryption overhead and therefore the full chunk must be downloaded")
	}

	// Determine the offset and length that needs to be downloaded from the
	// pieces. This is non-trivial because both the network itself and also the
	// erasure coder have required segment sizes.
	pieceOffset, pieceLength := getPieceOffsetAndLen(ec, offset, length)

	// Refresh the pcws. This will only cause a refresh if one is necessary.
	err := pcws.managedTryUpdateWorkerState()
	if err != nil {
		return nil, errors.AddContext(err, "unable to initiate download")
	}
	// After refresh, grab the worker state.
	pcws.mu.Lock()
	ws := pcws.workerState
	pcws.mu.Unlock()

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
	// TODO: If this ends up being a problem, we could implement the jobs
	// process to send the result down a channel in goroutine if the first
	// attempt to send the job fails. Then we could probably get away with a
	// smaller buffer, since exceeding the limit currently would cause a worker
	// to stall, where as with the goroutine-on-block method, exceeding the
	// limit merely causes extra goroutines to be spawned.
	workerResponseChan := make(chan *jobReadResponse, ec.NumPieces()*5)

	// Build the full pdc.
	pdc := &projectDownloadChunk{
		chunkOffset: offset,
		chunkLength: length,
		pricePerMS:  pricePerMS,

		pieceOffset: pieceOffset,
		pieceLength: pieceLength,

		availablePieces: make([][]pieceDownload, ec.NumPieces()),

		dataPieces: make([][]byte, ec.NumPieces()),

		ctx:                  ctx,
		workerResponseChan:   workerResponseChan,
		downloadResponseChan: make(chan *downloadResponse, 1),
		workerSet:            pcws,
		workerState:          ws,
	}

	// TODO: Need to move over any completed items here.

	// Launch the initial set of workers for the pdc.
	err = pdc.launchInitialWorkers()
	if err != nil {
		return nil, errors.AddContext(err, "unable to launch initial set of workers")
	}

	// All initial workers have been launched. The function can return now,
	// unblocking the caller. A background thread will be launched to collect
	// the responses and launch overdrive workers when necessary.
	go pdc.threadedCollectAndOverdrivePieces()
	return pdc.downloadResponseChan, nil
}
