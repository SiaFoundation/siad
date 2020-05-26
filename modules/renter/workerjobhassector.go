package renter

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"

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
		staticCanceledChan chan struct{}              // Can signal that the job has been canceled
		staticResponseChan chan *jobHasSectorResponse // Channel to send a response down

		staticSector crypto.Hash
	}

	// jobHasSectorQueue is a list of hasSector queries that have been assigned
	// to the worker.
	jobHasSectorQueue struct {
		killed bool
		jobs   []jobHasSector

		// Cooldown variables.
		cooldownUntil       time.Time
		consecutiveFailures uint64

		// These variables contain an exponential weighted average of the
		// worker's recent performance for jobHasSectorQueue.
		weightedJobTime       float64
		weightedJobsCompleted float64

		staticWorker *worker
		mu           sync.Mutex
	}

	// jobHasSectorResponse contains the result of a hasSector query.
	jobHasSectorResponse struct {
		staticAvailable bool
		staticErr       error

		// The worker is included in the response so that the caller can listen
		// on one channel for a bunch of workers and still know which worker
		// successfully found the sector root.
		staticWorker *worker
	}
)

// programHasSectorBandwidth returns the bandwidth that gets consumed by a
// HasSector program.
//
// TODO: These values are overly conservative, once we've got the protocol more
// optimized we can bring these down.
func programHasSectorBandwidth() (ulBandwidth, dlBandwidth uint64) {
	ulBandwidth = 20e3
	dlBandwidth = 20e3
	return
}

// newJobHasSectorQueue will initialize a has sector job queue for the worker.
// This is only meant to be run once at startup.
func (w *worker) newJobHasSectorQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobHasSectorQueue != nil {
		w.renter.log.Critical("incorret call on newJobHasSectorQueue")
	}
	w.staticJobHasSectorQueue = &jobHasSectorQueue{
		staticWorker: w,
	}
}

// staticCanceled is a convenience function to check whether a job has been
// canceled.
func (j *jobHasSector) staticCanceled() bool {
	select {
	case <-j.staticCanceledChan:
		return true
	default:
		return false
	}
}

// callAdd will add a job to the queue. False will be returned if the job cannot
// be queued because the worker has been killed.
func (jq *jobHasSectorQueue) callAdd(job jobHasSector) bool {
	jq.mu.Lock()
	defer jq.mu.Unlock()

	// Check if the queue has been killed.
	if jq.killed {
		return false
	}
	// Check if the queue is on cooldown.
	if time.Now().Before(jq.cooldownUntil) {
		return false
	}

	jq.jobs = append(jq.jobs, job)
	jq.staticWorker.staticWake()
	return true
}

// callAverageJobTime will return the recent performance of the worker
// attempting to complete has sector jobs.
func (jq *jobHasSectorQueue) callAverageJobTime() time.Duration {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return time.Duration(jq.weightedJobTime / jq.weightedJobsCompleted)
}

// callNext will provide the next jobHasSector from the set of jobs.
func (jq *jobHasSectorQueue) callNext() (func(), uint64, uint64) {
	var job jobHasSector
	jq.mu.Lock()
	for {
		if len(jq.jobs) == 0 {
			jq.mu.Unlock()
			return nil, 0, 0
		}

		// Grab the next job.
		job = jq.jobs[0]
		jq.jobs = jq.jobs[1:]

		// Grab the next job if this one has been canceled.
		if job.staticCanceled() {
			continue
		}
		// We have a job, can break out of the job search.
		break
	}
	jq.mu.Unlock()

	// Create the actual job that will be run by the async job launcher.
	jobFn := func() {
		start := time.Now()
		available, err := jq.staticWorker.managedHasSector(job.staticSector)
		jobTime := time.Since(start)
		response := &jobHasSectorResponse{
			staticAvailable: available,
			staticErr:       err,

			staticWorker: jq.staticWorker,
		}

		// Send the response in a goroutine so that the worker resources can be
		// released faster. Need to check if the job was canceled so that the
		// goroutine will exit.
		jq.staticWorker.renter.tg.Launch(func() {
			// We don't listen on the tg stopChan because it is assumed that the
			// project which issued the job will close job.canceled when the tg
			// stops.
			select {
			case job.staticResponseChan <- response:
			case <-job.staticCanceledChan:
			}
		})

		// If the job fails, go on cooldown. Skip updating the performance
		// metrics.
		if err != nil {
			jq.mu.Lock()
			jq.cooldownUntil = cooldownUntil(jq.consecutiveFailures)
			jq.consecutiveFailures++
			jq.discardJobsHasSector()
			jq.mu.Unlock()
			return
		}

		// Update the performance stats. There was no error, so the consecutive
		// failures value can be reset.
		jq.mu.Lock()
		jq.consecutiveFailures = 0
		jq.weightedJobTime *= jobHasSectorPerformanceDecay
		jq.weightedJobsCompleted *= jobHasSectorPerformanceDecay
		jq.weightedJobTime += float64(jobTime)
		jq.weightedJobsCompleted++
		jq.mu.Unlock()
	}

	// Return the job along with the bandwidth estimates for completing the job.
	ulBandwidth, dlBandwidth := programHasSectorBandwidth()
	return jobFn, ulBandwidth, dlBandwidth
}

// managedHasSector returns whether or not the host has a sector with given root
func (w *worker) managedHasSector(sectorRoot crypto.Hash) (bool, error) {
	// Create the program.
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt)
	pb.AddHasSectorInstruction(sectorRoot)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := programHasSectorBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
	//
	// TODO: Are we expecting more than one response? Should we check that there
	// was only one response?
	//
	// TODO: for this program we don't actually need the file contract - v149
	// only.
	var hasSector bool
	var responses []programResponse
	responses, err := w.managedExecuteProgram(program, programData, w.staticCache().staticContractID, cost)
	if err != nil {
		return false, errors.AddContext(err, "Unable to execute program")
	}
	for _, resp := range responses {
		if resp.Error != nil {
			return false, errors.AddContext(resp.Error, "Output error")
		}
		hasSector = resp.Output[0] == 1
		break
	}
	return hasSector, nil
}

// discardJobsHasSector will release any has sector jobs in the queue.
func (jq *jobHasSectorQueue) discardJobsHasSector() {
	for _, job := range jq.jobs {
		// Send the response in a goroutine so that the worker resources can be
		// released faster.
		j := job
		jq.staticWorker.renter.tg.Launch(func() {
			response := &jobHasSectorResponse{
				staticErr: errors.New("worker is dumping all has sector jobs"),
			}
			select {
			case j.staticResponseChan <- response:
			case <-j.staticCanceledChan:
			}
		})
	}
	jq.jobs = nil
}

// managedDiscardJobsHasSector will release all remaining HasSector jobs as
// failed.
func (w *worker) managedDiscardJobsHasSector() {
	jq := w.staticJobHasSectorQueue // Convenience variable
	jq.mu.Lock()
	jq.discardJobsHasSector()
	jq.mu.Unlock()
}

// managedKillJobsHasSector will release all remaining HasSector jobs as failed.
func (w *worker) managedKillJobsHasSector() {
	jq := w.staticJobHasSectorQueue // Convenience variable
	jq.mu.Lock()
	jq.discardJobsHasSector()
	jq.killed = true
	jq.mu.Unlock()
}
