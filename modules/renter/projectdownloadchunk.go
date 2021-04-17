package renter

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// errNotEnoughPieces is returned when there are not enough pieces found to
	// successfully complete the download
	errNotEnoughPieces = errors.New("not enough pieces to complete download")
)

type (
	// pieceDownload tracks a worker downloading a piece, whether that piece has
	// returned, and what time the piece is/was expected to return.
	//
	// NOTE: The actual piece data is stored in the projectDownloadChunk after
	// the download completes.
	pieceDownload struct {
		// 'completed', 'launched', and 'downloadErr' are status variables for
		// the piece. If 'launched' is false, it means the piece download has
		// not started yet, 'completed' will also be false.
		//
		// If 'launched' is true and 'completed' is false, it means the download
		// is in progress and the result is not known.
		//
		// If 'completed' is true, the download has been attempted, if it was
		// unsuccessful 'downloadErr' will contain the error with which it
		// failed. If 'downloadErr' is nil however, it means the piece was
		// successfully downloaded.
		completed   bool
		launched    bool
		downloadErr error

		// expectedCompleteTime indicates the time when the download is expected
		// to complete. This is used to determine whether or not a download is late.
		expectedCompleteTime time.Time

		worker *worker
	}

	// projectDownloadChunk is a bunch of state that helps to orchestrate a
	// download from a projectChunkWorkerSet.
	//
	// The projectDownloadChunk is only ever accessed by a single thread which
	// orchestrates the download, which means that it does not need to be thread
	// safe.
	projectDownloadChunk struct {
		// Parameters for downloading a subset of the data within the chunk.
		lengthInChunk uint64
		offsetInChunk uint64

		// Values derived from the chunk download parameters. The offset and
		// length specify the offset and length that will be sent to the host,
		// which must be segment aligned.
		pieceLength uint64
		pieceOffset uint64

		// pricePerMS is the amount of money we are willing to spend on faster
		// workers. If a certain set of workers is 100ms faster, but that
		// exceeds the pricePerMS we are willing to pay for it, we won't use
		// that faster worker set. If it is within the budget however, we will
		// favor the faster and more expensive worker set.
		pricePerMS types.Currency

		// availablePieces are pieces that resolved workers think they can
		// fetch.
		//
		// workersConsideredIndex keeps track of what workers were already
		// considered after looking at the 'resolvedWorkers' array defined on
		// the pcws. This enables the worker selection code to realize which
		// pieces in the worker set have been resolved since the last check.
		//
		// unresolvedWorkersRemaining is the number of unresolved workers at the
		// time the available pieces were last updated. This enables counting
		// the hopeful pieces without introducing a race condition in the
		// finished check.
		availablePieces            [][]*pieceDownload
		workersConsideredIndex     int
		unresolvedWorkersRemaining int

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

		// Debug helpers
		uid             [8]byte
		launchTime      time.Time
		launchedWorkers []*launchedWorkerInfo
	}

	// launchedWorkerInfo tracks information about the worker that has been
	// launched. It is used solely for debugging purposes to enable tracking the
	// chain of events that occurred when a download has timed out or failed.
	launchedWorkerInfo struct {
		// pieceIndex is the index of the piece the worker is set to download,
		// this index corresponds with the index of the `availablePieces` array
		// on the PDC.
		pieceIndex uint64

		// overdriveWorker indicates whether this worker was launched as one of
		// the initial workers, or as an overdrive worker.
		overdriveWorker bool

		launchTime           time.Time
		completeTime         time.Time
		expectedCompleteTime time.Time

		// 'jobDuration' is the total amount of time it took to complete the job
		jobDuration time.Duration

		// 'totalDuration' is the total amount of time it took for the worker to
		// complete the download since it was launched, or the time it took to
		// fail.
		totalDuration time.Duration

		// 'expectedDuration' is the estimated amount of time for this worker to
		// complete the download.
		expectedDuration time.Duration

		// jobErr will contain the error in case it failed.
		jobErr error

		pdc    *projectDownloadChunk
		worker *worker
	}

	// downloadResponse is sent via a channel to the caller of
	// 'projectChunkWorkerSet.managedDownload'.
	downloadResponse struct {
		data []byte
		err  error

		// launchedWorkers contains a list of worker information for the workers
		// that were launched to try and complete this download. This field can
		// be used for debugging purposes should the download time out or error
		// out.
		launchedWorkers []*launchedWorkerInfo
	}
)

// String implements the String interface.
func (lwi *launchedWorkerInfo) String() string {
	pdcId := hex.EncodeToString(lwi.pdc.uid[:])
	hostKey := lwi.worker.staticHostPubKey.ShortString()
	estimate := lwi.expectedDuration.Milliseconds()

	var wDescr string
	if lwi.overdriveWorker {
		wDescr = fmt.Sprintf("overdrive worker %v", hostKey)
	} else {
		wDescr = fmt.Sprintf("initial worker %v", hostKey)
	}

	// if download is not complete yet
	if lwi.completeTime.IsZero() {
		duration := time.Since(lwi.launchTime).Milliseconds()

		return fmt.Sprintf("%v | %v | piece %v | estimated complete %v ms | not responded after %vms", pdcId, wDescr, lwi.pieceIndex, estimate, duration)
	}

	// if download is complete
	var jDescr string
	if lwi.jobErr == nil {
		jDescr = "job completed successfully"
	} else {
		jDescr = fmt.Sprintf("job failed with err: %v", lwi.jobErr.Error())
	}

	totalDur := lwi.totalDuration.Milliseconds()
	jobDur := lwi.jobDuration.Milliseconds()

	return fmt.Sprintf("%v | %v | piece %v | estimated complete %v ms | responded after %vms | read job took %vms | %v", pdcId, wDescr, lwi.pieceIndex, estimate, totalDur, jobDur, jDescr)
}

// successful is a small helper method that returns whether the piece was
// successfully downloaded, this is the case when it completed without error.
func (pd *pieceDownload) successful() bool {
	return pd.completed && pd.downloadErr == nil
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
			pdc.availablePieces[pieceIndex] = append(pdc.availablePieces[pieceIndex], &pieceDownload{
				worker: resp.worker,
			})
		}
	}
	pdc.workersConsideredIndex = len(ws.resolvedWorkers)
	pdc.unresolvedWorkersRemaining = len(ws.unresolvedWorkers)

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

	// Grab the metadata from the response
	metadata := jrr.staticMetadata
	worker := metadata.staticWorker
	pieceIndex := metadata.staticPieceRootIndex
	launchedWorker := pdc.launchedWorkers[metadata.staticLaunchedWorkerIndex]

	// Update the launched worker information, we keep track of these metrics
	// debugging purposes.
	launchedWorker.completeTime = time.Now()
	launchedWorker.jobDuration = jrr.staticJobTime
	launchedWorker.jobErr = jrr.staticErr
	launchedWorker.totalDuration = time.Since(launchedWorker.launchTime)

	// Check whether the job failed.
	if jrr.staticErr != nil {
		// The download failed, update the pdc available pieces to reflect the
		// failure.
		pieceFound := false
		for i := 0; i < len(pdc.availablePieces[pieceIndex]); i++ {
			if pdc.availablePieces[pieceIndex][i].worker.staticHostPubKeyStr == worker.staticHostPubKeyStr {
				if pieceFound {
					build.Critical("The list of available pieces contains duplicates.") // sanity check
				}
				pieceFound = true
				pdc.availablePieces[pieceIndex][i].completed = true
				pdc.availablePieces[pieceIndex][i].downloadErr = jrr.staticErr
			}
		}
		return
	}

	// Decrypt the piece that has come back.
	key := pdc.workerSet.staticMasterKey.Derive(pdc.workerSet.staticChunkIndex, uint64(pieceIndex))
	_, err := key.DecryptBytesInPlace(jrr.staticData, pdc.pieceOffset/crypto.SegmentSize)
	if err != nil {
		pdc.workerSet.staticRenter.log.Println("decryption of a piece failed")
		return
	}

	// The download succeeded, add the piece to the appropriate index.
	pdc.dataPieces[pieceIndex] = jrr.staticData
	jrr.staticData = nil // Just in case there's a reference to the job response elsewhere.

	pieceFound := false
	for i := 0; i < len(pdc.availablePieces[pieceIndex]); i++ {
		if pdc.availablePieces[pieceIndex][i].worker.staticHostPubKeyStr == worker.staticHostPubKeyStr {
			if pieceFound {
				build.Critical("The list of available pieces contains duplicates.") // sanity check
			}
			pieceFound = true
			pdc.availablePieces[pieceIndex][i].completed = true
		}
	}
}

// fail will send an error down the download response channel.
func (pdc *projectDownloadChunk) fail(err error) {
	dr := &downloadResponse{
		data: nil,
		err:  err,

		launchedWorkers: pdc.launchedWorkers,
	}
	pdc.downloadResponseChan <- dr
}

// finalize will take the completed pieces of the download, decode them,
// and then send the result down the response channel. If there is an error
// during decode, 'pdc.fail()' will be called.
func (pdc *projectDownloadChunk) finalize() {
	// Determine the amount of bytes the EC will need to skip from the recovered
	// data when returning the data.
	skipLength := pdc.offsetInChunk % (crypto.SegmentSize * uint64(pdc.workerSet.staticErasureCoder.MinPieces()))

	// Create a skipwriter that ensures we're recovering at the offset
	buf := bytes.NewBuffer(nil)
	skipWriter := &skipWriter{
		writer: buf,
		skip:   int(skipLength),
	}

	// Recover the pieces in to a single byte slice.
	err := pdc.workerSet.staticErasureCoder.Recover(pdc.dataPieces, pdc.lengthInChunk+skipLength, skipWriter)
	if err != nil {
		pdc.fail(errors.AddContext(err, "unable to complete erasure decode of download"))
		return
	}
	data := buf.Bytes()

	// Return the data to the caller.
	dr := &downloadResponse{
		data: data,
		err:  nil,

		launchedWorkers: pdc.launchedWorkers,
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
			if pieceDownload.successful() {
				hopeful = true
				completedPieces++
				break
			}
			// If this piece has not yet failed, it is hopeful. Keep looking
			// through the pieces in case there is a piece that was downloaded
			// successfully.
			if pieceDownload.downloadErr == nil {
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

	// Count the number of workers that haven't resolved yet, and thus
	// (optimistically) might contribute towards downloading a unique piece.
	hopefulPieces += pdc.unresolvedWorkersRemaining

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
func (pdc *projectDownloadChunk) launchWorker(w *worker, pieceIndex uint64, isOverdrive bool) (time.Time, bool) {
	// Sanity check that the pieceOffset and pieceLength are segment aligned.
	if pdc.pieceOffset%crypto.SegmentSize != 0 ||
		pdc.pieceLength%crypto.SegmentSize != 0 {
		build.Critical("pieceOffset or pieceLength is not segment aligned")
	}

	// Create the read sector job for the worker.
	launchedWorkerIndex := uint64(len(pdc.launchedWorkers))
	sectorRoot := pdc.workerSet.staticPieceRoots[pieceIndex]
	jrs := &jobReadSector{
		jobRead: jobRead{
			staticResponseChan: pdc.workerResponseChan,
			staticLength:       pdc.pieceLength,

			jobGeneric: newJobGeneric(pdc.ctx, w.staticJobReadQueue, jobReadMetadata{
				staticWorker:              w,
				staticSectorRoot:          sectorRoot,
				staticSpendingCategory:    categoryDownload,
				staticPieceRootIndex:      pieceIndex,
				staticLaunchedWorkerIndex: launchedWorkerIndex,
			}),
		},
		staticOffset: pdc.pieceOffset,
		staticSector: pdc.workerSet.staticPieceRoots[pieceIndex],
	}

	// Submit the job.
	expectedCompleteTime, added := w.staticJobReadQueue.callAddWithEstimate(jrs)

	// Track the launched worker
	if added {
		pdc.launchedWorkers = append(pdc.launchedWorkers, &launchedWorkerInfo{
			pieceIndex:      pieceIndex,
			overdriveWorker: isOverdrive,

			launchTime:           time.Now(),
			expectedCompleteTime: expectedCompleteTime,
			expectedDuration:     time.Until(expectedCompleteTime),

			pdc:    pdc,
			worker: w,
		})
	}

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
				pieceDownload.completed = true
				pieceDownload.downloadErr = errors.New("unable to add piece to queue")
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
			pdc.fail(errors.New("download timed out"))
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
