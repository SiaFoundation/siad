package renter

import (
	"context"
	"fmt"
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

		staticResponseChan chan *jobHasSectorResponse

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

// newJobHasSector is a helper method to create a new HasSector job.
func (w *worker) newJobHasSector(ctx context.Context, responseChan chan *jobHasSectorResponse, roots ...crypto.Hash) *jobHasSector {
	return &jobHasSector{
		staticSectors:      roots,
		staticResponseChan: responseChan,
		jobGeneric:         newJobGeneric(ctx, w.staticJobHasSectorQueue, nil),
	}
}

// callDiscard will discard a job, sending the provided error.
func (j *jobHasSector) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		response := &jobHasSectorResponse{
			staticErr: errors.Extend(err, ErrJobDiscarded),

			staticWorker: w,
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
	if err != nil {
		j.staticQueue.callReportFailure(err)
		return
	}
	j.staticQueue.callReportSuccess()

	if w.staticHostPubKeyStr == "ed25519:80123282887700bef150939d5f4196cf9b8145882e5ba850391431e183968449" {
		fmt.Println("HS took", jobTime.Milliseconds())
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
func (j *jobHasSector) callExpectedBandwidth() (ul, dl uint64) {
	return hasSectorJobExpectedBandwidth(len(j.staticSectors))
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
	hasSectors := make([]bool, 0, len(program))
	var responses []programResponse
	responses, _, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, cost)
	if err != nil {
		return nil, errors.AddContext(err, "unable to execute program for has sector job")
	}
	for _, resp := range responses {
		if resp.Error != nil {
			return nil, errors.AddContext(resp.Error, "Output error")
		}
		hasSectors = append(hasSectors, resp.Output[0] == 1)
	}
	if len(responses) != len(program) {
		return nil, errors.New("received invalid number of responses but no error")
	}
	return hasSectors, nil
}

// callAddWithEstimate will add a job to the queue and return a timestamp for
// when the job is estimated to complete. An error will be returned if the job
// is not successfully queued.
func (jq *jobHasSectorQueue) callAddWithEstimate(j *jobHasSector) (time.Time, error) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	now := time.Now()
	estimate := jq.expectedJobTime(uint64(len(j.staticSectors)))
	j.externJobStartTime = now
	j.externEstimatedJobDuration = estimate
	if !jq.add(j) {
		return time.Time{}, errors.New("unable to add job to queue")
	}
	return now.Add(estimate), nil
}

// expectedJobTime will return the amount of time that a job is expected to
// take, given the current conditions of the queue.
func (jq *jobHasSectorQueue) expectedJobTime(numSectors uint64) time.Duration {
	// if we don't have any historic data yet, return a sane (slightly
	// pessimistic) default of 100ms
	if jq.weightedJobsCompleted == 0 {
		return 100 * time.Millisecond
	}

	return time.Duration(jq.weightedJobTime / jq.weightedJobsCompleted)
}

// callExpectedJobTime returns the expected amount of time that this job will
// take to complete.
func (jq *jobHasSectorQueue) callExpectedJobTime(numSectors uint64) time.Duration {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return jq.expectedJobTime(numSectors)
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
func hasSectorJobExpectedBandwidth(numRoots int) (ul, dl uint64) {
	// Roughly 40 roots can fit into a single frame. To be conservative, we use
	// a value of 30.
	//
	// Roughly 150 responses can fit into a single frame. To be conservative, we
	// use a value of 100.
	uploadMult := numRoots / 30
	downloadMult := numRoots / 100
	// A base of 1500 is used for the packet size. On ipv4, it is technically
	// smaller, but siamux is general and the packet size is the Ethernet MTU
	// (1500 bytes) minus any protocol overheads. It's possible if the renter is
	// connected directly over an interface to a host that there is no overhead,
	// which means siamux could use the full 1500 bytes. So we use the most
	// conservative value here as well.
	ul = uint64(1500 * (1 + uploadMult))
	dl = uint64(1500 * (1 + downloadMult))
	return
}
