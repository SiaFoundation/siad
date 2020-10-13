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
	// ethernetMTU is the minimum transferable size for ethernet networks. It's
	// used as the estimated upper limit for the frame size of the SiaMux.
	ethernetMTU = 1500

	// jobReadRegistryPerformanceDecay defines how much the average
	// performance is decayed each time a new datapoint is added. The jobs use
	// an exponential weighted average.
	jobReadRegistryPerformanceDecay = 0.9
)

type (
	// jobReadRegistry contains information about a ReadRegistry query.
	jobReadRegistry struct {
		staticSiaPublicKey types.SiaPublicKey
		staticTweak        crypto.Hash

		staticResponseChan chan *jobReadRegistryResponse // Channel to send a response down

		*jobGeneric
	}

	// jobReadRegistryQueue is a list of ReadRegistry jobs that have been
	// assigned to the worker.
	jobReadRegistryQueue struct {
		// These variables contain an exponential weighted average of the
		// worker's recent performance for jobReadRegistryQueue.
		weightedJobTime float64

		*jobGenericQueue
	}

	// jobReadRegistryResponse contains the result of a ReadRegistry query.
	jobReadRegistryResponse struct {
		staticSignedRegistryValue modules.SignedRegistryValue
		staticErr                 error
	}
)

// lookupsRegistry looks up a registry on the host and verifies its signature.
func lookupRegistry(w *worker, spk types.SiaPublicKey, tweak crypto.Hash) (modules.SignedRegistryValue, error) {
	// Create the program.
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since ReadRegistry doesn't depend on it.
	pb.AddReadRegistryInstruction(spk, tweak)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := readRegistryJobExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
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

	// Verify tweak.
	if rv.Tweak != tweak {
		return modules.SignedRegistryValue{}, errors.New("host returned a registry value for the wrong tweak")
	}

	// Verify signature.
	if rv.Verify(spk.ToPublicKey()) != nil {
		return modules.SignedRegistryValue{}, errors.New("failed to verify returned registry value's signature")
	}
	return rv, nil
}

// newJobReadRegistry is a helper method to create a new ReadRegistry job.
func (w *worker) newJobReadRegistry(ctx context.Context, responseChan chan *jobReadRegistryResponse, spk types.SiaPublicKey, tweak crypto.Hash) *jobReadRegistry {
	return &jobReadRegistry{
		staticSiaPublicKey: spk,
		staticTweak:        tweak,
		staticResponseChan: responseChan,
		jobGeneric:         newJobGeneric(ctx, w.staticJobReadRegistryQueue),
	}
}

// callDiscard will discard a job, sending the provided error.
func (j *jobReadRegistry) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		response := &jobReadRegistryResponse{
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

// callExecute will run the ReadRegistry job.
func (j *jobReadRegistry) callExecute() {
	start := time.Now()
	w := j.staticQueue.staticWorker()

	// Prepare a method to send a response asynchronously.
	sendResponse := func(srv modules.SignedRegistryValue, err error) {
		errLaunch := w.renter.tg.Launch(func() {
			response := &jobReadRegistryResponse{
				staticSignedRegistryValue: srv,
				staticErr:                 err,
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

	// read the value. We ignore ErrRegistryValueNotExist to not put the host on
	// a cooldown for something that's not necessarily its fault. In the future
	// we might want to implement a flag to disable this behavior in case we
	// know that a host must have the entry.
	srv, err := lookupRegistry(w, j.staticSiaPublicKey, j.staticTweak)
	if err != nil && !strings.Contains(err.Error(), modules.ErrRegistryValueNotExist.Error()) {
		j.staticQueue.callReportFailure(err)
		return
	}

	// Success.
	jobTime := time.Since(start)

	// Send the response and report success.
	sendResponse(srv, err)
	j.staticQueue.callReportSuccess()

	// Update the performance stats on the queue.
	jq := j.staticQueue.(*jobReadRegistryQueue)
	jq.mu.Lock()
	jq.weightedJobTime = expMovingAvg(jq.weightedJobTime, float64(jobTime), jobReadRegistryPerformanceDecay)
	jq.mu.Unlock()
}

// callExpectedBandwidth returns the bandwidth that is expected to be consumed
// by the job.
func (j *jobReadRegistry) callExpectedBandwidth() (ul, dl uint64) {
	return readRegistryJobExpectedBandwidth()
}

// initJobReadRegistryQueue will init the queue for the ReadRegistry jobs.
func (w *worker) initJobReadRegistryQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobReadRegistryQueue != nil {
		w.renter.log.Critical("incorret call on initJobReadRegistryQueue")
		return
	}

	w.staticJobReadRegistryQueue = &jobReadRegistryQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}

// ReadRegistry is a helper method to run a ReadRegistry job on a worker.
func (w *worker) ReadRegistry(ctx context.Context, spk types.SiaPublicKey, tweak crypto.Hash) (modules.SignedRegistryValue, error) {
	readRegistryRespChan := make(chan *jobReadRegistryResponse)
	jur := w.newJobReadRegistry(ctx, readRegistryRespChan, spk, tweak)

	// Add the job to the queue.
	if !w.staticJobReadRegistryQueue.callAdd(jur) {
		return modules.SignedRegistryValue{}, errors.New("worker unavailable")
	}

	// Wait for the response.
	var resp *jobReadRegistryResponse
	select {
	case <-ctx.Done():
		return modules.SignedRegistryValue{}, errors.New("ReadRegistry interrupted")
	case resp = <-readRegistryRespChan:
	}
	return resp.staticSignedRegistryValue, resp.staticErr
}

// readRegistryJobExpectedBandwidth is a helper function that returns the
// expected bandwidth consumption of a ReadRegistry job. This helper function
// enables getting at the expected bandwidth without having to instantiate a
// job.
func readRegistryJobExpectedBandwidth() (ul, dl uint64) {
	return ethernetMTU, ethernetMTU // a single frame each for upload and download
}
