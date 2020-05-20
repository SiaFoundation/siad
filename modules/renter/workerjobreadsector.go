package renter

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// jobReadSectorPerformanceDecay defines how much decay gets applied to the
	// historic performance of jobReadSector each time new data comes back.
	// Setting a low value makes the performance more volatile. If the worker
	// tends to have inconsistent performance, having the decay be a low value
	// (0.9 or lower) will be highly detrimental. A higher decay means that the
	// predictor tends to be more accurate over time, but is less responsive to
	// things like network load.
	jobReadSectorPerformanceDecay = 0.9
)

type (
	// jobReadSector contains information about a hasSector query.
	jobReadSector struct {
		staticCanceledChan chan struct{}               // Can signal that the job has been canceled
		staticResponseChan chan *jobReadSectorResponse // Channel to send a response down

		staticLength uint64
		staticOffset uint64
		staticSector crypto.Hash
	}

	// jobReadSectorQueue is a list of hasSector queries that have been assigned
	// to the worker. The queue also tracks performance metrics, which can then
	// be used by projects to optimize job scheduling between workers.
	jobReadSectorQueue struct {
		killed bool
		jobs   []jobReadSector

		// Cooldown variables.
		cooldownUntil       time.Time
		consecutiveFailures uint64

		// These float64s are converted time.Duration values. They are float64
		// to get better precision on the exponential decay which gets applied
		// with each new data point.
		weightedJobTime64k       float64
		weightedJobTime1m        float64
		weightedJobTime4m        float64
		weightedJobsCompleted64k float64
		weightedJobsCompleted1m  float64
		weightedJobsCompleted4m  float64

		staticWorker *worker
		mu           sync.Mutex
	}

	// jobReadSectorResponse contains the result of a hasSector query.
	jobReadSectorResponse struct {
		staticData []byte
		staticErr  error
	}
)

// programReadSectorBandwidth returns the bandwidth that gets consumed by a
// ReadSector program.
//
// TODO: These values are overly conservative, once we've got the protocol more
// optimized we can bring these down.
func programReadSectorBandwidth(offset, length uint64) (ulBandwidth, dlBandwidth uint64) {
	ulBandwidth = 1 << 15                              // 32 KiB
	dlBandwidth = uint64(float64(length)*1.01) + 1<<14 // (readSize * 1.01 + 16 KiB)
	return
}

// newJobReadSectorQueue will initialize a queue for downloading sectors by
// their root for the worker. This is only meant to be run once at startup.
func (w *worker) newJobReadSectorQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobReadSectorQueue != nil {
		w.renter.log.Critical("incorret call on newJobReadSectorQueue")
	}
	w.staticJobReadSectorQueue = &jobReadSectorQueue{
		staticWorker: w,
	}
}

// staticCanceled is a convenience function to check whether a job has been
// canceled.
func (j *jobReadSector) staticCanceled() bool {
	select {
	case <-j.staticCanceledChan:
		return true
	default:
		return false
	}
}

// callAdd will add a job to the queue. False will be returned if the job cannot
// be queued because the worker has been killed.
func (jq *jobReadSectorQueue) callAdd(job jobReadSector) bool {
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

// callAverageJobTime will return the recent perforamcne of the worker
// attempting to complete read sector jobs. The call distinguishes based on the
// size of the job, breaking the jobs into 3 categories: less than 64kb, less
// than 1mb, and up to a full sector in size.
//
// The breakout is performed because low latency, low throughput workers are
// common, and will have very different performance characteristics across the
// three categories.
func (jq *jobReadSectorQueue) callAverageJobTime(length uint64) time.Duration {
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

// callNext will provide the next jobReadSector from the set of jobs.
func (jq *jobReadSectorQueue) callNext() (func(), uint64, uint64) {
	var job jobReadSector
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
		// Track how long the job takes.
		start := time.Now()
		data, err := jq.staticWorker.managedReadSector(job.staticSector, job.staticOffset, job.staticLength)
		jobTime := time.Since(start)
		response := &jobReadSectorResponse{
			staticData: data,
			staticErr:  err,
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

		// If the job fails, go on cooldown.
		if err != nil {
			jq.mu.Lock()
			jq.cooldownUntil = cooldownUntil(jq.consecutiveFailures)
			jq.consecutiveFailures++
			jq.discardJobsReadSector()
			jq.mu.Unlock()
			return
		}

		// Update the metrics in the read sector queue based on the amount of
		// time the read took. Stats should only be added if the job did not
		// result in an error. Because there was no failure, the consecutive
		// failures stat can be reset.
		jq.mu.Lock()
		jq.consecutiveFailures = 0
		if job.staticLength <= 1<<16 {
			jq.weightedJobTime64k *= jobReadSectorPerformanceDecay
			jq.weightedJobsCompleted64k *= jobReadSectorPerformanceDecay
			jq.weightedJobTime64k += float64(jobTime)
			jq.weightedJobsCompleted64k++
		} else if job.staticLength <= 1<<20 {
			jq.weightedJobTime1m *= jobReadSectorPerformanceDecay
			jq.weightedJobsCompleted1m *= jobReadSectorPerformanceDecay
			jq.weightedJobTime1m += float64(jobTime)
			jq.weightedJobsCompleted1m++
		} else {
			jq.weightedJobTime4m *= jobReadSectorPerformanceDecay
			jq.weightedJobsCompleted4m *= jobReadSectorPerformanceDecay
			jq.weightedJobTime4m += float64(jobTime)
			jq.weightedJobsCompleted4m++
		}
		jq.mu.Unlock()
	}

	// Return the job along with the bandwidth estimates for completing the job.
	ulBandwidth, dlBandwidth := programReadSectorBandwidth(job.staticOffset, job.staticLength)
	return jobFn, ulBandwidth, dlBandwidth
}

// managedReadSector returns the sector data for given root
func (w *worker) managedReadSector(sectorRoot crypto.Hash, offset, length uint64) ([]byte, error) {
	// create the program
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt)
	pb.AddReadSectorInstruction(length, offset, sectorRoot, true)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := programReadSectorBandwidth(offset, length)
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// exeucte it
	//
	// TODO: for this program we don't actually need the file contract - v149
	// only.
	responses, err := w.managedExecuteProgram(program, programData, w.staticCache().staticContractID, cost)
	if err != nil {
		return nil, err
	}

	// return the response
	var sectorData []byte
	for _, resp := range responses {
		if resp.Error != nil {
			return nil, resp.Error
		}
		sectorData = resp.Output
		break
	}
	return sectorData, nil
}

// discardJobsReadSector will drop all of the read sector jobs for this worker.
func (jq *jobReadSectorQueue) discardJobsReadSector() {
	for _, job := range jq.jobs {
		// Send the response in a goroutine so that the worker resources can be
		// released faster.
		j := job
		jq.staticWorker.renter.tg.Launch(func() {
			response := &jobReadSectorResponse{
				staticErr: errors.New("worker is dumping all read sector jobs"),
			}
			select {
			case j.staticResponseChan <- response:
			case <-j.staticCanceledChan:
			}
		})
	}
	jq.jobs = nil
}

// managedDiscardJobsReadSector will release all remaining ReadSector jobs as
// failed.
func (w *worker) managedDiscardJobsReadSector() {
	jq := w.staticJobReadSectorQueue // Convenience variable
	jq.mu.Lock()
	jq.discardJobsReadSector()
	jq.mu.Unlock()
}

// managedKillJobsReadSector will release all remaining ReadSector jobs as failed.
func (w *worker) managedKillJobsReadSector() {
	jq := w.staticJobReadSectorQueue // Convenience variable
	jq.mu.Lock()
	jq.discardJobsReadSector()
	jq.killed = true
	jq.mu.Unlock()
}
