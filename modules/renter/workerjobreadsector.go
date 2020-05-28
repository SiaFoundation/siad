package renter

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

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
		staticLength uint64
		staticOffset uint64
		staticSector crypto.Hash

		staticResponseChan chan *jobReadSectorResponse // Channel to send a response down

		*jobGeneric
	}

	// jobReadSectorQueue is a list of hasSector queries that have been assigned
	// to the worker. The queue also tracks performance metrics, which can then
	// be used by projects to optimize job scheduling between workers.
	jobReadSectorQueue struct {
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

	// jobReadSectorResponse contains the result of a hasSector query.
	jobReadSectorResponse struct {
		staticData []byte
		staticErr  error
	}
)

// TODO: Gouging

// callDiscard will discard a job, forwarding the error to the caller.
func (j *jobReadSector) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	w.renter.tg.Launch(func() {
		response := &jobReadSectorResponse{
			staticErr: errors.Extend(err, ErrJobDiscarded),
		}
		select {
		case j.staticResponseChan <- response:
		case <-w.renter.tg.StopChan():
		case <-j.staticCancelChan:
		}
	})
}

// callExecute will execute the job.
func (j *jobReadSector) callExecute() {
	// Track how long the job takes.
	start := time.Now()
	data, err := j.managedReadSector()
	jobTime := time.Since(start)

	// Send the response in a goroutine so that the worker resources can be
	// released faster. Need to check if the job was canceled so that the
	// goroutine will exit.
	response := &jobReadSectorResponse{
		staticData: data,
		staticErr:  err,
	}
	w := j.staticQueue.staticWorker()
	w.renter.tg.Launch(func() {
		// We don't listen on the tg stopChan because it is assumed that the
		// project which issued the job will close job.canceled when the tg
		// stops.
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCancelChan:
		case <-w.renter.tg.StopChan():
		}
	})

	// Report success or failure to the queue.
	if err == nil {
		j.staticQueue.callReportSuccess()
	} else {
		j.staticQueue.callReportFailure(err)
		return
	}

	// Job succeeded.
	//
	// Update the metrics in the read sector queue based on the amount of
	// time the read took. Stats should only be added if the job did not
	// result in an error. Because there was no failure, the consecutive
	// failures stat can be reset.
	jq := j.staticQueue.(*jobReadSectorQueue)
	jq.mu.Lock()
	if j.staticLength <= 1<<16 {
		jq.weightedJobTime64k *= jobReadSectorPerformanceDecay
		jq.weightedJobsCompleted64k *= jobReadSectorPerformanceDecay
		jq.weightedJobTime64k += float64(jobTime)
		jq.weightedJobsCompleted64k++
	} else if j.staticLength <= 1<<20 {
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

// callExpectedBandwidth returns the bandwidth that gets consumed by a
// ReadSector program.
//
// TODO: These values are overly conservative, once we've got the protocol more
// optimized we can bring these down.
func (j *jobReadSector) callExpectedBandwidth() (ul, dl uint64) {
	ul = 1 << 15                                      // 32 KiB
	dl = uint64(float64(j.staticLength)*1.01) + 1<<14 // (readSize * 1.01 + 16 KiB)
	return
}

// managedReadSector returns the sector data for given root.
func (j *jobReadSector) managedReadSector() ([]byte, error) {
	// create the program
	w := j.staticQueue.staticWorker()
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt)
	pb.AddReadSectorInstruction(j.staticLength, j.staticOffset, j.staticSector, true)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// exeucte it
	responses, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, cost)
	if err != nil {
		return nil, err
	}

	// Pull the sector data from the response.
	var sectorData []byte
	for _, resp := range responses {
		if resp.Error != nil {
			return nil, resp.Error
		}
		sectorData = resp.Output
		break
	}
	// Check that we received the amount of data that we were expecting.
	if uint64(len(sectorData)) != j.staticLength {
		return nil, errors.New("worker returned the wrong amount of data")
	}
	return sectorData, nil
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

// initJobReadSectorQueue will initialize a queue for downloading sectors by
// their root for the worker. This is only meant to be run once at startup.
func (w *worker) initJobReadSectorQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobReadSectorQueue != nil {
		w.renter.log.Critical("incorret call on initJobReadSectorQueue")
	}
	w.staticJobReadSectorQueue = &jobReadSectorQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}
