package renter

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// jobReadPerformanceDecay defines how much decay gets applied to the
	// historic performance of jobRead each time new data comes back.
	// Setting a low value makes the performance more volatile. If the worker
	// tends to have inconsistent performance, having the decay be a low value
	// (0.9 or lower) will be highly detrimental. A higher decay means that the
	// predictor tends to be more accurate over time, but is less responsive to
	// things like network load.
	jobReadPerformanceDecay = 0.9
)

type (
	// jobRead contains information about a Read query.
	jobRead struct {
		staticLength uint64

		staticResponseChan chan *jobReadResponse

		// staticSector can be set by the caller. This field is set in the job
		// response so that upon getting the response the caller knows which job
		// was completed.
		staticSector crypto.Hash
		staticWorker *worker

		*jobGeneric
	}

	// jobReadQueue is a list of Read queries that have been assigned to the
	// worker. The queue also tracks performance metrics, which can then be used
	// by projects to optimize job scheduling between workers.
	jobReadQueue struct {
		// These float64s are converted time.Duration values. They are float64
		// to get better precision on the exponential decay which gets applied
		// with each new data point.
		weightedJobTime64k       float64
		weightedJobTime1m        float64
		weightedJobTime4m        float64
		weightedJobsCompleted64k float64
		weightedJobsCompleted1m  float64
		weightedJobsCompleted4m  float64

		*jobGenericQueue
	}

	// jobReadResponse contains the result of a Read query.
	jobReadResponse struct {
		// The response data.
		staticData []byte
		staticErr  error

		// Metadata related to the job query.
		staticSectorRoot crypto.Hash
		staticWorker     *worker
	}
)

// callDiscard will discard a job, forwarding the error to the caller.
func (j *jobRead) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		response := &jobReadResponse{
			staticErr: errors.Extend(err, ErrJobDiscarded),

			staticSectorRoot: j.staticSector,

			staticWorker: w,
		}
		select {
		case j.staticResponseChan <- response:
		case <-w.renter.tg.StopChan():
		case <-j.staticCtx.Done():
		}
	})
	if errLaunch != nil {
		w.renter.log.Print("callDiscard: launch failed", err)
	}
}

// managedFinishExecute will execute code that is shared by multiple read jobs
// after execution. It updates the performance metrics, records whether the
// execution was successful and returns the response.
func (j *jobRead) managedFinishExecute(readData []byte, readErr error, readJobTime time.Duration) {
	w := j.staticQueue.staticWorker()

	// Send the response in a goroutine so that the worker resources can be
	// released faster. Need to check if the job was canceled so that the
	// goroutine will exit.
	response := &jobReadResponse{
		staticData: readData,
		staticErr:  readErr,

		staticSectorRoot: j.staticSector,

		staticWorker: w,
	}
	err := w.renter.tg.Launch(func() {
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
	if err != nil {
		j.staticQueue.staticWorker().renter.log.Print("managedFinishExecute: launch failed", err)
	}

	// Report success or failure to the queue.
	if readErr != nil {
		j.staticQueue.callReportFailure(readErr)
		return
	}
	j.staticQueue.callReportSuccess()

	// Job succeeded.
	//
	// Update the metrics in the read sector queue based on the amount of
	// time the read took. Stats should only be added if the job did not
	// result in an error. Because there was no failure, the consecutive
	// failures stat can be reset.
	jq := j.staticQueue.(*jobReadQueue)
	jq.mu.Lock()
	if j.staticLength <= 1<<16 {
		jq.weightedJobTime64k *= jobReadPerformanceDecay
		jq.weightedJobsCompleted64k *= jobReadPerformanceDecay
		jq.weightedJobTime64k += float64(readJobTime)
		jq.weightedJobsCompleted64k++
	} else if j.staticLength <= 1<<20 {
		jq.weightedJobTime1m *= jobReadPerformanceDecay
		jq.weightedJobsCompleted1m *= jobReadPerformanceDecay
		jq.weightedJobTime1m += float64(readJobTime)
		jq.weightedJobsCompleted1m++
	} else {
		jq.weightedJobTime4m *= jobReadPerformanceDecay
		jq.weightedJobsCompleted4m *= jobReadPerformanceDecay
		jq.weightedJobTime4m += float64(readJobTime)
		jq.weightedJobsCompleted4m++
	}
	jq.mu.Unlock()
}

// callExpectedBandwidth returns the bandwidth that gets consumed by a
// Read program.
//
// TODO: These values are overly conservative, once we've got the protocol more
// optimized we can bring these down.
func (j *jobRead) callExpectedBandwidth() (ul, dl uint64) {
	ul = 1 << 15                                      // 32 KiB
	dl = uint64(float64(j.staticLength)*1.01) + 1<<14 // (readSize * 1.01 + 16 KiB)
	return
}

// managedRead returns the sector data for the given read program and the merkle
// proof.
func (j *jobRead) managedRead(w *worker, program modules.Program, programData []byte, cost types.Currency) ([]programResponse, error) {
	// execute it
	responses, _, err := w.managedExecuteProgram(program, programData, w.staticCache().staticContractID, cost)
	if err != nil {
		return []programResponse{}, err
	}

	// Sanity check number of responses.
	if len(responses) > len(program) {
		build.Critical("managedExecuteProgram should return at most len(program) instructions")
	}
	if len(responses) == 0 {
		build.Critical("managedExecuteProgram should at least return one instruction when err == nil")
	}
	// If the number of responses doesn't match, the last response should
	// contain an error message.
	if len(responses) != len(program) {
		err := responses[len(responses)-1].Error
		return []programResponse{}, errors.AddContext(err, "managedRead: program execution was interrupted")
	}

	// The last instruction is the actual download.
	response := responses[len(responses)-1]
	if response.Error != nil {
		return []programResponse{}, response.Error
	}
	sectorData := response.Output

	// Check that we received the amount of data that we were expecting.
	if uint64(len(sectorData)) != j.staticLength {
		return []programResponse{}, errors.New("worker returned the wrong amount of data")
	}
	return responses, nil
}

// callAverageJobTime will return the recent performance of the worker
// attempting to complete read jobs. The call distinguishes based on the
// size of the job, breaking the jobs into 3 categories: less than 64kb, less
// than 1mb, and up to a full sector in size.
//
// The breakout is performed because low latency, low throughput workers are
// common, and will have very different performance characteristics across the
// three categories.
func (jq *jobReadQueue) callAverageJobTime(length uint64) time.Duration {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	if length <= 1<<16 {
		return time.Duration(jq.weightedJobTime64k / jq.weightedJobsCompleted64k)
	} else if length <= 1<<20 {
		return time.Duration(jq.weightedJobTime1m / jq.weightedJobsCompleted1m)
	} else {
		return time.Duration(jq.weightedJobTime4m / jq.weightedJobsCompleted4m)
	}
}

// initJobReadQueue will initialize a queue for downloading sectors by
// their root for the worker. This is only meant to be run once at startup.
func (w *worker) initJobReadQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobReadQueue != nil {
		w.renter.log.Critical("incorret call on initJobReadQueue")
	}
	w.staticJobReadQueue = &jobReadQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}
