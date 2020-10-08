package renter

import (
	"context"
	"encoding/binary"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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
		staticSPK types.SiaPublicKey
		staticRV  modules.SignedRegistryValue

		staticResponseChan chan *jobUpdateRegistryResponse // Channel to send a response down

		*jobGeneric
	}

	// jobUpdateRegistryQueue is a list of UpdateRegistry jobs that have been
	// assigned to the worker.
	jobUpdateRegistryQueue struct {
		// These variables contain an exponential weighted average of the
		// worker's recent performance for jobUpdateRegistryQueue.
		weightedJobTime       float64
		weightedJobsCompleted float64

		*jobGenericQueue
	}

	// jobUpdateRegistryResponse contains the result of a UpdateRegistry query.
	jobUpdateRegistryResponse struct {
		staticErr error
	}
)

// newJobUpdateRegistry is a helper method to create a new UpdateRegistry job.
func (w *worker) newJobUpdateRegistry(ctx context.Context, responseChan chan *jobUpdateRegistryResponse, spk types.SiaPublicKey, rv modules.SignedRegistryValue) *jobUpdateRegistry {
	return &jobUpdateRegistry{
		staticSPK:          spk,
		staticRV:           rv,
		staticResponseChan: responseChan,
		jobGeneric:         newJobGeneric(ctx, w.staticJobUpdateRegistryQueue),
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
		w.renter.log.Print("callDiscard: launch failed", err)
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
			w.renter.log.Println("callExececute: launch failed", err)
		}
	}

	// Check if the host already has the latest entry.
	existingRV, err := lookupRegistry(w, j.staticSPK, j.staticRV.Tweak)
	if err != nil && !strings.Contains(err.Error(), modules.ErrRegistryValueNotExist.Error()) {
		sendResponse(err)
		j.staticQueue.callReportFailure(err)
		return
	}
	found := err == nil

	// if the existing rv doesn't match or if we didn't find any rv we update it.
	if !found || existingRV.Revision != j.staticRV.Revision {
		err = j.managedUpdateRegistry()
		if err != nil {
			sendResponse(err)
			j.staticQueue.callReportFailure(err)
			return
		}
	}

	// Success. We either confirmed the latest revision or updated the host successfully.
	jobTime := time.Since(start)

	// Send the response and report success.
	sendResponse(nil)
	j.staticQueue.callReportSuccess()

	// Update the performance stats on the queue.
	jq := j.staticQueue.(*jobUpdateRegistryQueue)
	jq.mu.Lock()
	jq.weightedJobTime *= jobUpdateRegistryPerformanceDecay
	jq.weightedJobsCompleted *= jobUpdateRegistryPerformanceDecay
	jq.weightedJobTime += float64(jobTime)
	jq.weightedJobsCompleted++
	jq.mu.Unlock()
}

// callExpectedBandwidth returns the bandwidth that is expected to be consumed
// by the job.
//
func (j *jobUpdateRegistry) callExpectedBandwidth() (ul, dl uint64) {
	return updateRegistryJobExpectedBandwidth()
}

// lookupsRegistry looks up a registry on the host and verifies its signature.
func lookupRegistry(w *worker, spk types.SiaPublicKey, tweak crypto.Hash) (modules.SignedRegistryValue, error) {
	// Create the program.
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since UpdateRegistry doesn't depend on it.
	pb.AddReadRegistryInstruction(spk, tweak)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := updateRegistryJobExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth/2, dlBandwidth/2)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
	//
	var responses []programResponse
	responses, _, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, cost)
	if err != nil {
		return modules.SignedRegistryValue{}, errors.AddContext(err, "Unable to execute program")
	}
	for _, resp := range responses {
		if resp.Error != nil {
			return modules.SignedRegistryValue{}, errors.AddContext(resp.Error, "Output error")
		}
		break
	}
	if len(responses) != len(program) {
		return modules.SignedRegistryValue{}, errors.New("received invalid number of responses but no error")
	}
	// Parse response.
	resp := responses[0]
	var sig crypto.Signature
	copy(sig[:], resp.Output[:crypto.SignatureSize])
	rev := binary.LittleEndian.Uint64(resp.Output[crypto.SignatureSize:])
	data := resp.Output[crypto.SignatureSize+8:]
	rv := modules.NewSignedRegistryValue(tweak, data, rev, sig)
	if rv.Verify(spk.ToPublicKey()) != nil {
		return modules.SignedRegistryValue{}, errors.New("failed to verify returned registry value's signature")
	}
	return rv, nil
}

// managedUpdateRegistry updates a registry entry on a host.
func (j *jobUpdateRegistry) managedUpdateRegistry() error {
	w := j.staticQueue.staticWorker()
	// Create the program.
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since UpdateRegistry doesn't depend on it.
	pb.AddUpdateRegistryInstruction(j.staticSPK, j.staticRV)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth/2, dlBandwidth/2)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
	//
	var responses []programResponse
	responses, _, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, cost)
	if err != nil {
		return errors.AddContext(err, "Unable to execute program")
	}
	for _, resp := range responses {
		if resp.Error != nil {
			return errors.AddContext(resp.Error, "Output error")
		}
		break
	}
	if len(responses) != len(program) {
		return errors.New("received invalid number of responses but no error")
	}
	return nil
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
	return 2 * 1500, 2 * 1500 // 2 programs using a single frame each
}
