package renter

import (
	"context"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/registry"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// jobUpdateRegistryPerformanceDecay defines how much the average
	// performance is decayed each time a new datapoint is added. The jobs use
	// an exponential weighted average.
	jobUpdateRegistryPerformanceDecay = 0.9
)

type (
	// jobUpdateRegistry contains information about a UpdateRegistry query.
	jobUpdateRegistry struct {
		staticSiaPublicKey        types.SiaPublicKey
		staticSignedRegistryValue modules.SignedRegistryValue

		staticResponseChan chan *jobUpdateRegistryResponse // Channel to send a response down

		*jobGeneric
	}

	// jobUpdateRegistryQueue is a list of UpdateRegistry jobs that have been
	// assigned to the worker.
	jobUpdateRegistryQueue struct {
		// These variables contain an exponential weighted average of the
		// worker's recent performance for jobUpdateRegistryQueue.
		weightedJobTime float64

		*jobGenericQueue
	}

	// jobUpdateRegistryResponse contains the result of a UpdateRegistry query.
	jobUpdateRegistryResponse struct {
		staticErr error
	}
)

// newJobUpdateRegistry is a helper method to create a new UpdateRegistry job.
func (w *worker) newJobUpdateRegistry(ctx context.Context, responseChan chan *jobUpdateRegistryResponse, spk types.SiaPublicKey, srv modules.SignedRegistryValue) *jobUpdateRegistry {
	return &jobUpdateRegistry{
		staticSiaPublicKey:        spk,
		staticSignedRegistryValue: srv,
		staticResponseChan:        responseChan,
		jobGeneric:                newJobGeneric(ctx, w.staticJobUpdateRegistryQueue),
	}
}

// callDiscard will discard a job, sending the provided error.
func (j *jobUpdateRegistry) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		response := &jobUpdateRegistryResponse{
			staticErr: errors.Extend(err, ErrJobDiscarded),
		}
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
	if errLaunch != nil {
		w.renter.log.Debugln("callDiscard: launch failed", err)
	}
}

// callExecute will run the UpdateRegistry job.
func (j *jobUpdateRegistry) callExecute() {
	start := time.Now()
	w := j.staticQueue.staticWorker()

	// Prepare a method to send a response asynchronously.
	sendResponse := func(err error) {
		errLaunch := w.renter.tg.Launch(func() {
			response := &jobUpdateRegistryResponse{
				staticErr: err,
			}
			select {
			case j.staticResponseChan <- response:
			case <-j.staticCtx.Done():
			case <-w.renter.tg.StopChan():
			}
		})
		if errLaunch != nil {
			w.renter.log.Debugln("callExececute: launch failed", err)
		}
	}

	// update the rv. We ignore ErrSameRevNum and ErrLowerRevNum to not put the
	// host on a cooldown for something that's not necessarily its fault. We
	// might want to add another argument to the job that disables this behavior
	// in the future in case we are certain that a host can't contain those
	// errors.
	rv, err := j.managedUpdateRegistry()
	if err != nil && (strings.Contains(err.Error(), registry.ErrLowerRevNum.Error()) ||
		strings.Contains(err.Error(), registry.ErrSameRevNum.Error())) {
		// Report the failure if it can't provide a valid entry for the error
		if err := rv.Verify(j.staticSiaPublicKey.ToPublicKey()); err != nil {
			sendResponse(err)
			j.staticQueue.callReportFailure(err)
			return
		}
		sendResponse(err)
		return
	} else if err != nil {
		sendResponse(err)
		j.staticQueue.callReportFailure(err)
		return
	}

	// Success. We either confirmed the latest revision or updated the host successfully.
	jobTime := time.Since(start)

	// Send the response and report success.
	sendResponse(err)
	j.staticQueue.callReportSuccess()

	// Update the performance stats on the queue.
	jq := j.staticQueue.(*jobUpdateRegistryQueue)
	jq.mu.Lock()
	jq.weightedJobTime = expMovingAvg(jq.weightedJobTime, float64(jobTime), jobUpdateRegistryPerformanceDecay)
	jq.mu.Unlock()
}

// callExpectedBandwidth returns the bandwidth that is expected to be consumed
// by the job.
func (j *jobUpdateRegistry) callExpectedBandwidth() (ul, dl uint64) {
	return updateRegistryJobExpectedBandwidth()
}

// managedUpdateRegistry updates a registry entry on a host. If the error is
// ErrLowerRevNum or ErrSameRevNum, a signed registry value should be returned
// as proof.
func (j *jobUpdateRegistry) managedUpdateRegistry() (modules.SignedRegistryValue, error) {
	w := j.staticQueue.staticWorker()
	// Create the program.
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since UpdateRegistry doesn't depend on it.
	pb.AddUpdateRegistryInstruction(j.staticSiaPublicKey, j.staticSignedRegistryValue)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
	var responses []programResponse
	responses, _, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, cost)
	if err != nil {
		return modules.SignedRegistryValue{}, errors.AddContext(err, "Unable to execute program")
	}
	for _, resp := range responses {
		// If a revision related error was returned, we try to parse the
		// signed registry value from the response and make sure that it's a
		// valid one with a lower revision number or the same revision number as
		// the one we use for the update.
		err = resp.Error
		if err != nil && (strings.Contains(err.Error(), registry.ErrLowerRevNum.Error()) ||
			strings.Contains(err.Error(), registry.ErrSameRevNum.Error())) {
			rv, parseErr := parseSignedRegistryValueResponse(resp.Output, j.staticSignedRegistryValue.Tweak)
			return rv, errors.Compose(err, parseErr)
		}
		if err != nil {
			return modules.SignedRegistryValue{}, errors.AddContext(resp.Error, "Output error")
		}
		break
	}
	if len(responses) != len(program) {
		return modules.SignedRegistryValue{}, errors.New("received invalid number of responses but no error")
	}
	return modules.SignedRegistryValue{}, nil
}

// initJobUpdateRegistryQueue will init the queue for the UpdateRegistry jobs.
func (w *worker) initJobUpdateRegistryQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobUpdateRegistryQueue != nil {
		w.renter.log.Critical("incorret call on initJobUpdateRegistryQueue")
		return
	}

	w.staticJobUpdateRegistryQueue = &jobUpdateRegistryQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}

// UpdateRegistry is a helper method to run a UpdateRegistry job on a worker.
func (w *worker) UpdateRegistry(ctx context.Context, spk types.SiaPublicKey, rv modules.SignedRegistryValue) error {
	updateRegistryRespChan := make(chan *jobUpdateRegistryResponse)
	jur := w.newJobUpdateRegistry(ctx, updateRegistryRespChan, spk, rv)

	// Add the job to the queue.
	if !w.staticJobUpdateRegistryQueue.callAdd(jur) {
		return errors.New("worker unavailable")
	}

	// Wait for the response.
	var resp *jobUpdateRegistryResponse
	select {
	case <-ctx.Done():
		return errors.New("UpdateRegistry interrupted")
	case resp = <-updateRegistryRespChan:
	}
	return resp.staticErr
}

// updateRegistryUpdateJobExpectedBandwidth is a helper function that returns
// the expected bandwidth consumption of a UpdateRegistry job. This helper
// function enables getting at the expected bandwidth without having to
// instantiate a job.
func updateRegistryJobExpectedBandwidth() (ul, dl uint64) {
	return ethernetMTU, ethernetMTU // a single frame each for upload and download
}
