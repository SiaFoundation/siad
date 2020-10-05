package renter

import (
	"context"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// jobHasSectorPerformanceDecay defines how much the average performance is
	// decayed each time a new datapoint is added. The jobs use an exponential
	// weighted average.
	jobHasSectorPerformanceDecay = 0.9
)

type (
	// jobHasSector contains information about a hasSector query.
	jobHasSector struct {
		staticSectors []crypto.Hash

		staticResponseChan chan *jobHasSectorResponse // Channel to send a response down

		*jobGeneric
	}

	// jobHasSectorQueue is a list of hasSector queries that have been assigned
	// to the worker.
	jobHasSectorQueue struct {
		// These variables contain an exponential weighted average of the
		// worker's recent performance for jobHasSectorQueue.
		weightedJobTime       float64
		weightedJobsCompleted float64

		*jobGenericQueue
	}

	// jobHasSectorResponse contains the result of a hasSector query.
	jobHasSectorResponse struct {
		staticAvailables []bool
		staticErr        error

		// The worker is included in the response so that the caller can listen
		// on one channel for a bunch of workers and still know which worker
		// successfully found the sector root.
		staticWorker *worker
	}
)

// TODO: Gouging

// newJobHasSector is a helper method to create a new HasSector job.
func (w *worker) newJobHasSector(ctx context.Context, responseChan chan *jobHasSectorResponse, roots ...crypto.Hash) *jobHasSector {
	return &jobHasSector{
		staticSectors:      roots,
		staticResponseChan: responseChan,
		jobGeneric:         newJobGeneric(ctx, w.staticJobHasSectorQueue),
	}
}

// callDiscard will discard a job, sending the provided error.
func (j *jobHasSector) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		response := &jobHasSectorResponse{
			staticErr: errors.Extend(err, ErrJobDiscarded),
		}
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
	if errLaunch != nil {
		w.renter.log.Print("callDiscard: launch failed", err)
	}
}

// callExecute will run the has sector job.
func (j *jobHasSector) callExecute() {
	start := time.Now()
	w := j.staticQueue.staticWorker()
	availables, err := j.managedHasSector()
	jobTime := time.Since(start)

	// Send the response.
	response := &jobHasSectorResponse{
		staticAvailables: availables,
		staticErr:        err,

		staticWorker: w,
	}
	err2 := w.renter.tg.Launch(func() {
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
	if err2 != nil {
		w.renter.log.Println("callExececute: launch failed", err)
	}

	// Report success or failure to the queue.
	if err == nil {
		j.staticQueue.callReportSuccess()
	} else {
		j.staticQueue.callReportFailure(err)
		return
	}

	// Job was a success, update the performance stats on the queue.
	jq := j.staticQueue.(*jobHasSectorQueue)
	jq.mu.Lock()
	jq.weightedJobTime *= jobHasSectorPerformanceDecay
	jq.weightedJobsCompleted *= jobHasSectorPerformanceDecay
	jq.weightedJobTime += float64(jobTime)
	jq.weightedJobsCompleted++
	jq.mu.Unlock()
}

// callExpectedBandwidth returns the bandwidth that is expected to be consumed
// by the job.
//
// TODO: These values are overly conservative, once we've got the protocol more
// optimized we can bring these down.
func (j *jobHasSector) callExpectedBandwidth() (ul, dl uint64) {
	return hasSectorJobExpectedBandwidth()
}

// managedHasSector returns whether or not the host has a sector with given root
func (j *jobHasSector) managedHasSector() ([]bool, error) {
	w := j.staticQueue.staticWorker()
	// Create the program.
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since HasSector doesn't depend on it.
	for _, sector := range j.staticSectors {
		pb.AddHasSectorInstruction(sector)
	}
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
	//
	hasSectors := make([]bool, 0, len(program))
	var responses []programResponse
	responses, _, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, cost)
	if err != nil {
		return nil, errors.AddContext(err, "Unable to execute program")
	}
	for _, resp := range responses {
		if resp.Error != nil {
			return nil, errors.AddContext(resp.Error, "Output error")
		}
		hasSectors = append(hasSectors, resp.Output[0] == 1)
		break
	}
	if len(responses) != len(program) {
		return nil, errors.New("received invalid number of responses but no error")
	}
	return hasSectors, nil
}

// callAverageJobTime will return the recent performance of the worker
// attempting to complete has sector jobs.
func (jq *jobHasSectorQueue) callAverageJobTime() time.Duration {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return time.Duration(jq.weightedJobTime / jq.weightedJobsCompleted)
}

// initJobHasSectorQueue will init the queue for the has sector jobs.
func (w *worker) initJobHasSectorQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobHasSectorQueue != nil {
		w.renter.log.Critical("incorret call on initJobHasSectorQueue")
		return
	}

	w.staticJobHasSectorQueue = &jobHasSectorQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}

// hasSectorJobExpectedBandwidth is a helper function that returns the expected
// bandwidth consumption of a has sector job. This helper function enables
// getting at the expected bandwidth without having to instantiate a job.
func hasSectorJobExpectedBandwidth() (ul, dl uint64) {
	ul = 20e3
	dl = 20e3
	return
}
