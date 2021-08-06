package renter

import (
	"context"
	"strings"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// jobUpdateRegistryPerformanceDecay defines how much the average
	// performance is decayed each time a new datapoint is added. The jobs use
	// an exponential weighted average.
	jobUpdateRegistryPerformanceDecay = 0.9
)

// errHostOutdatedProof is returned if the host provides a proof that has a
// valid signature but is still invalid due to its revision number.
var errHostOutdatedProof = errors.New("host returned proof with invalid revision number")

// errHostLowerRevisionThanCache is returned whenever the host claims that the
// latest revision of the registry entry it knows is lower than the one it is
// supposed to have according to the cache.
var errHostLowerRevisionThanCache = errors.New("host claims that the latest revision it knows is lower than the one in the cache")

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
		srv       *modules.SignedRegistryValue // only sent on ErrLowerRevNum and ErrSameRevNum
		staticErr error
	}
)

// newJobUpdateRegistry is a helper method to create a new UpdateRegistry job.
func (w *worker) newJobUpdateRegistry(ctx context.Context, responseChan chan *jobUpdateRegistryResponse, spk types.SiaPublicKey, srv modules.SignedRegistryValue) *jobUpdateRegistry {
	return &jobUpdateRegistry{
		staticSiaPublicKey:        spk,
		staticSignedRegistryValue: srv,
		staticResponseChan:        responseChan,
		jobGeneric:                newJobGeneric(ctx, w.staticJobUpdateRegistryQueue, nil),
	}
}

// callDiscard will discard a job, sending the provided error.
func (j *jobUpdateRegistry) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		response := &jobUpdateRegistryResponse{
			srv:       nil,
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
	sendResponse := func(srv *modules.SignedRegistryValue, err error) {
		errLaunch := w.renter.tg.Launch(func() {
			response := &jobUpdateRegistryResponse{
				srv:       srv,
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
	if modules.IsRegistryEntryExistErr(err) {
		// Report the failure if the host can't provide a signed registry entry
		// with the error.
		if err := rv.Verify(j.staticSiaPublicKey.ToPublicKey()); err != nil {
			sendResponse(nil, err)
			j.staticQueue.callReportFailure(err)
			return
		}
		// If the entry is valid, check if our suggested can actually not be
		// used to update rv.
		shouldUpdate, shouldUpdateErr := rv.ShouldUpdateWith(&j.staticSignedRegistryValue.RegistryValue, w.staticHostPubKey)
		if shouldUpdate {
			sendResponse(nil, errHostOutdatedProof)
			j.staticQueue.callReportFailure(errHostOutdatedProof)
			return
		}
		// If the entry is valid and the revision is also valid, check if we
		// have a higher revision number in the cache than the provided one.
		// TODO: update the cache to store the hash in addition to the revision
		// number for verifying the pow.
		cachedRevision, cached := w.staticRegistryCache.Get(j.staticSiaPublicKey, j.staticSignedRegistryValue.Tweak)
		if cached && cachedRevision > rv.Revision {
			sendResponse(nil, errHostLowerRevisionThanCache)
			j.staticQueue.callReportFailure(errHostLowerRevisionThanCache)
			w.staticRegistryCache.Set(j.staticSiaPublicKey, rv, true) // adjust the cache
			return
		}
		// If the entry is the same as as the one we want to set, consider this
		// a success. Otherwise return the error.
		if !errors.Contains(shouldUpdateErr, modules.ErrSameRevNum) {
			sendResponse(&rv, err)
			return
		}
	} else if err != nil {
		sendResponse(nil, err)
		j.staticQueue.callReportFailure(err)
		return
	}

	// Success. We either confirmed the latest revision or updated the host successfully.
	jobTime := time.Since(start)

	// Update the registry cache.
	w.staticRegistryCache.Set(j.staticSiaPublicKey, j.staticSignedRegistryValue, false)

	// Send the response and report success.
	sendResponse(nil, nil)
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
	version := modules.ReadRegistryVersionNoType
	if build.VersionCmp(w.staticCache().staticHostVersion, "1.5.5") < 0 {
		pb.V154AddUpdateRegistryInstruction(j.staticSiaPublicKey, j.staticSignedRegistryValue)
	} else if build.VersionCmp(w.staticCache().staticHostVersion, "1.5.6") < 0 {
		pb.V156AddUpdateRegistryInstruction(j.staticSiaPublicKey, j.staticSignedRegistryValue)
	} else {
		version = modules.ReadRegistryVersionWithType
		pb.AddUpdateRegistryInstruction(j.staticSiaPublicKey, j.staticSignedRegistryValue)
	}
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Execute the program and parse the responses.
	var responses []programResponse
	responses, _, err := w.managedExecuteProgram(program, programData, types.FileContractID{}, categoryRegistryWrite, cost)
	if err != nil {
		return modules.SignedRegistryValue{}, errors.AddContext(err, "Unable to execute program")
	}
	for _, resp := range responses {
		// If a revision related error was returned, we try to parse the
		// signed registry value from the response.
		err = resp.Error
		// Check for ErrLowerRevNum.
		if err != nil && strings.Contains(err.Error(), modules.ErrLowerRevNum.Error()) {
			err = modules.ErrLowerRevNum
		}
		if err != nil && strings.Contains(err.Error(), modules.ErrSameRevNum.Error()) {
			err = modules.ErrSameRevNum
		}
		if err != nil && strings.Contains(err.Error(), modules.ErrInsufficientWork.Error()) {
			err = modules.ErrInsufficientWork
		}
		if modules.IsRegistryEntryExistErr(err) {
			// Parse the proof.
			_, _, data, revision, sig, entryType, parseErr := parseSignedRegistryValueResponse(resp.Output, false, version)
			rv := modules.NewSignedRegistryValue(j.staticSignedRegistryValue.Tweak, data, revision, sig, entryType)
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
