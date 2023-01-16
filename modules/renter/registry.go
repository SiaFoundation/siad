package renter

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	// MaxRegistryReadTimeout is the default timeout used when reading from
	// the registry.
	MaxRegistryReadTimeout = build.Select(build.Var{
		Dev:      30 * time.Second,
		Standard: 5 * time.Minute,
		Testnet:  5 * time.Minute,
		Testing:  10 * time.Second,
	}).(time.Duration)

	// DefaultRegistryUpdateTimeout is the default timeout used when updating
	// the registry.
	DefaultRegistryUpdateTimeout = build.Select(build.Var{
		Dev:      30 * time.Second,
		Standard: 5 * time.Minute,
		Testnet:  5 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// ErrRegistryEntryNotFound is returned if all workers were unable to fetch
	// the entry.
	ErrRegistryEntryNotFound = errors.New("registry entry not found")

	// ErrRegistryLookupTimeout is similar to ErrRegistryEntryNotFound but it is
	// returned instead if the lookup timed out before all workers returned.
	ErrRegistryLookupTimeout = errors.New("registry entry not found within given time")

	// ErrRegistryUpdateInsufficientRedundancy is returned if updating the
	// registry failed due to running out of workers before reaching
	// MinUpdateRegistrySuccess successful updates.
	ErrRegistryUpdateInsufficientRedundancy = errors.New("registry update failed due reach sufficient redundancy")

	// ErrRegistryUpdateNoSuccessfulUpdates is returned if not a single update
	// was successful.
	ErrRegistryUpdateNoSuccessfulUpdates = errors.New("all registry updates failed")

	// ErrRegistryUpdateTimeout is returned when updating the registry was
	// aborted before reaching MinUpdateRegistrySucesses.
	ErrRegistryUpdateTimeout = errors.New("registry update timed out before reaching the minimum amount of updated hosts")

	// MinUpdateRegistrySuccesses is the minimum amount of success responses we
	// require from UpdateRegistry to be valid.
	MinUpdateRegistrySuccesses = build.Select(build.Var{
		Dev:      3,
		Standard: 3,
		Testnet:  3,
		Testing:  3,
	}).(int)

	// ReadRegistryBackgroundTimeout is the amount of time a read registry job
	// can stay active in the background before being cancelled.
	ReadRegistryBackgroundTimeout = build.Select(build.Var{
		Dev:      time.Minute,
		Standard: 2 * time.Minute,
		Testnet:  2 * time.Minute,
		Testing:  5 * time.Second,
	}).(time.Duration)

	// updateRegistryMemory is the amount of registry that UpdateRegistry will
	// request from the memory manager.
	updateRegistryMemory = uint64(20 * (1 << 10)) // 20kib

	// readRegistryMemory is the amount of registry that ReadRegistry will
	// request from the memory manager.
	readRegistryMemory = uint64(20 * (1 << 10)) // 20kib

	// useHighestRevDefaultTimeout is the amount of time before ReadRegistry
	// will stop waiting for additional responses from hosts and accept the
	// response with the highest rev number. The timer starts when we get the
	// first response and doesn't reset afterwards.
	useHighestRevDefaultTimeout = 100 * time.Millisecond

	// updateRegistryBackgroundTimeout is the time an update registry job on a
	// worker stays active in the background after managedUpdateRegistry returns
	// successfully.
	updateRegistryBackgroundTimeout = time.Minute

	// readRegistryStatsInterval is the granularity with which read registry
	// stats are collected. The smaller the number the faster updating the stats
	// is but the less accurate the estimate.
	readRegistryStatsInterval = 20 * time.Millisecond

	// readRegistryStatsDecay is the decay applied to the registry stats.
	readRegistryStatsDecay = 0.995

	// readRegistryStatsPercentile is the percentile returned by the read
	// registry stats Estimate method.
	readRegistryStatsPercentile = 0.99

	// readRegistrySeed is the first duration added to the registry stats after
	// creating it.
	// NOTE: This needs to be <= readRegistryBackgroundTimeout
	readRegistryStatsSeed = build.Select(build.Var{
		Dev:      30 * time.Second,
		Standard: 2 * time.Second,
		Testnet:  2 * time.Second,
		Testing:  5 * time.Second,
	}).(time.Duration)
)

// readResponseSet is a helper type which allows for returning a set of ongoing
// ReadRegistry responses.
type readResponseSet struct {
	c    <-chan *jobReadRegistryResponse
	left int

	readResps []*jobReadRegistryResponse
}

// newReadResponseSet creates a new set from a response chan and number of
// workers which are expected to write to that chan.
func newReadResponseSet(responseChan <-chan *jobReadRegistryResponse, numWorkers int) *readResponseSet {
	return &readResponseSet{
		c:         responseChan,
		left:      numWorkers,
		readResps: make([]*jobReadRegistryResponse, 0, numWorkers),
	}
}

// collect will collect all responses. It will block until it has received all
// of them or until the provided context is closed.
func (rrs *readResponseSet) collect(ctx context.Context) []*jobReadRegistryResponse {
	for rrs.responsesLeft() > 0 {
		resp := rrs.next(ctx)
		if resp == nil {
			break
		}
	}
	return rrs.readResps
}

// next returns the next available response. It will block until the response is
// received or the provided context is closed.
func (rrs *readResponseSet) next(ctx context.Context) *jobReadRegistryResponse {
	select {
	case <-ctx.Done():
		return nil
	case resp := <-rrs.c:
		rrs.readResps = append(rrs.readResps, resp)
		rrs.left--
		return resp
	}
}

// responsesLeft returns the number of responses that can still be fetched with
// Next.
func (rrs *readResponseSet) responsesLeft() int {
	return rrs.left
}

// threadedAddResponseSet adds a response set to the stats. This includes
// waiting for all responses to arrive and then updating the stats using the
// fastest success response with the highest rev number.
func (rs *readRegistryStats) threadedAddResponseSet(ctx context.Context, startTime time.Time, rrs *readResponseSet) {
	// Get all responses.
	resps := rrs.collect(ctx)
	if resps == nil {
		return // nothing to do
	}

	// Check for shutdown since collect might have blocked for a while.
	select {
	case <-ctx.Done():
		return // shutdown
	default:
	}

	// Find the fastest timing with the highest revision number.
	var best *jobReadRegistryResponse
	for _, resp := range resps {
		if resp.staticErr != nil {
			continue
		}

		// If there is no best yet, always set it.
		if best == nil {
			best = resp
			continue
		}
		// If there is no rv yet, always set it.
		bestRV := best.staticSignedRegistryValue
		respRV := resp.staticSignedRegistryValue
		if bestRV == nil && respRV != nil {
			best = resp
			continue
		}
		// If there is an rv but the new response doesn't have one, ignore it.
		if bestRV != nil && respRV == nil {
			continue
		}
		// The one with the higher revision gets priority if both have an rv.
		// TODO: Add code to check for scenarios related to rapidly updating
		// entries.
		if bestRV != nil && respRV != nil && respRV.Revision > bestRV.Revision {
			best = resp
			continue
		}
		// Otherwise the faster one wins.
		if resp.staticCompleteTime.Before(best.staticCompleteTime) {
			best = resp
			continue
		}
	}

	// No successful responses. We can't update the stats.
	if best == nil {
		return
	}

	// Add the duration to the estimate.
	d := best.staticCompleteTime.Sub(startTime)

	// The error is ignored since it only returns an error if the measurement is
	// outside of the 5 minute bounds the stats were created with.
	_ = rs.AddDatum(d)
}

// ReadRegistry starts a registry lookup on all available workers. The
// jobs have 'timeout' amount of time to finish their jobs and return a
// response. Otherwise the response with the highest revision number will be
// used.
func (r *Renter) ReadRegistry(spk types.SiaPublicKey, tweak crypto.Hash, timeout time.Duration) (modules.SignedRegistryValue, error) {
	// Create a context. If the timeout is greater than zero, have the context
	// expire when the timeout triggers.
	ctx := r.tg.StopCtx()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.tg.StopCtx(), timeout)
		defer cancel()
	}

	// Block until there is memory available, and then ensure the memory gets
	// returned.
	// Since registry entries are very small we use a fairly generous multiple.
	if !r.registryMemoryManager.Request(ctx, readRegistryMemory, memoryPriorityHigh) {
		return modules.SignedRegistryValue{}, errors.New("timeout while waiting in job queue - server is busy")
	}
	defer r.registryMemoryManager.Return(readRegistryMemory)

	// Start the ReadRegistry jobs.
	srv, err := r.managedReadRegistry(ctx, spk, tweak)
	if errors.Contains(err, ErrRegistryLookupTimeout) {
		err = errors.AddContext(err, fmt.Sprintf("timed out after %vs", timeout.Seconds()))
	}
	return srv, err
}

// UpdateRegistry updates the registries on all workers with the given
// registry value.
func (r *Renter) UpdateRegistry(spk types.SiaPublicKey, srv modules.SignedRegistryValue, timeout time.Duration) error {
	// Create a context. If the timeout is greater than zero, have the context
	// expire when the timeout triggers.
	ctx := r.tg.StopCtx()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.tg.StopCtx(), timeout)
		defer cancel()
	}

	// Block until there is memory available, and then ensure the memory gets
	// returned.
	// Since registry entries are very small we use a fairly generous multiple.
	if !r.registryMemoryManager.Request(ctx, updateRegistryMemory, memoryPriorityHigh) {
		return errors.New("timeout while waiting in job queue - server is busy")
	}
	defer r.registryMemoryManager.Return(updateRegistryMemory)

	// Start the UpdateRegistry jobs.
	err := r.managedUpdateRegistry(ctx, spk, srv)
	if errors.Contains(err, ErrRegistryUpdateTimeout) {
		err = errors.AddContext(err, fmt.Sprintf("timed out after %vs", timeout.Seconds()))
	}
	return err
}

// managedReadRegistry starts a registry lookup on all available workers. The
// jobs have 'timeout' amount of time to finish their jobs and return a
// response. Otherwise the response with the highest revision number will be
// used.
func (r *Renter) managedReadRegistry(ctx context.Context, spk types.SiaPublicKey, tweak crypto.Hash) (modules.SignedRegistryValue, error) {
	// Specify a sane timeout for jobs that is independent of the user specified
	// timeout. It is the maximum time that we let a job execute in the
	// background before cancelling it.
	backgroundCtx, backgroundCancel := context.WithTimeout(r.tg.StopCtx(), ReadRegistryBackgroundTimeout)

	// Get the full list of workers and create a channel to receive all of the
	// results from the workers. The channel is buffered with one slot per
	// worker, so that the workers do not have to block when returning the
	// result of the job, even if this thread is not listening.
	workers := r.staticWorkerPool.callWorkers()
	staticResponseChan := make(chan *jobReadRegistryResponse, len(workers))

	// Filter out hosts that don't support the registry.
	numRegistryWorkers := 0
	for _, worker := range workers {
		cache := worker.staticCache()
		if build.VersionCmp(cache.staticHostVersion, minRegistryVersion) < 0 {
			continue
		}

		// check for price gouging
		//
		// TODO: use 'checkProjectDownloadGouging' gouging for some basic
		// protection. Should be replaced as part of the gouging overhaul.
		pt := worker.staticPriceTable().staticPriceTable
		err := checkProjectDownloadGouging(pt, cache.staticRenterAllowance)
		if err != nil {
			r.log.Debugf("price gouging detected in worker %v, err: %v\n", worker.staticHostPubKeyStr, err)
			continue
		}

		jrr := worker.newJobReadRegistry(backgroundCtx, staticResponseChan, spk, tweak)
		if !worker.staticJobReadRegistryQueue.callAdd(jrr) {
			// This will filter out any workers that are on cooldown or
			// otherwise can't participate in the project.
			continue
		}
		workers[numRegistryWorkers] = worker
		numRegistryWorkers++
	}
	workers = workers[:numRegistryWorkers]
	// If there are no workers remaining, fail early.
	if len(workers) == 0 {
		backgroundCancel()
		return modules.SignedRegistryValue{}, errors.AddContext(modules.ErrNotEnoughWorkersInWorkerPool, "cannot perform ReadRegistry")
	}
	numWorkers := len(workers)

	// If specified, increment numWorkers. This will cause the loop to never
	// exit without any of the context being closed since the response set won't
	// be able to read the last response.
	if r.deps.Disrupt("ReadRegistryBlocking") {
		numWorkers++
	}

	// Create the response set.
	responseSet := newReadResponseSet(staticResponseChan, numWorkers)

	// Add the response set to the stats after this method is done.
	startTime := time.Now()
	defer func() {
		_ = r.tg.Launch(func() {
			r.staticRRS.threadedAddResponseSet(backgroundCtx, startTime, responseSet)
			backgroundCancel()
		})
	}()

	// Further restrict the input timeout using historical data.
	ctx, cancel := context.WithTimeout(ctx, r.staticRRS.Estimate())
	defer cancel()

	// Prepare a context which will be overwritten by a child context with a timeout
	// when we receive the first response. useHighestRevDefaultTimeout after
	// receiving the first response, this will be closed to abort the search for
	// the highest rev number and return the highest one we have so far.
	var useHighestRevCtx context.Context

	var srv *modules.SignedRegistryValue
	responses := 0
	for responseSet.responsesLeft() > 0 {
		// Check cancel condition and block for more responses.
		var resp *jobReadRegistryResponse
		if srv != nil {
			// If we have a successful response already, we wait on the highest
			// rev ctx.
			resp = responseSet.next(useHighestRevCtx)
		} else {
			// Otherwise we don't wait on the usehighestRevCtx since we need a
			// successful response to abort.
			resp = responseSet.next(ctx)
		}
		if resp == nil {
			break // context triggered
		}

		// When we get the first response, we initialize the highest rev
		// timeout.
		if responses == 0 {
			c, cancel := context.WithTimeout(ctx, useHighestRevDefaultTimeout)
			defer cancel()
			useHighestRevCtx = c
		}

		// Increment responses.
		responses++

		// Ignore error responses and responses that returned no entry.
		if resp.staticErr != nil || resp.staticSignedRegistryValue == nil {
			continue
		}

		// Remember the response with the highest revision number. We use >=
		// here to also catch the edge case of the initial revision being 0.
		revHigher := srv != nil && resp.staticSignedRegistryValue.RegistryValue.Revision > srv.Revision
		revSame := srv != nil && resp.staticSignedRegistryValue.RegistryValue.Revision == srv.Revision
		moreWork := srv != nil && resp.staticSignedRegistryValue.HasMoreWork(srv.RegistryValue)
		if srv == nil || revHigher || (revSame && moreWork) {
			srv = resp.staticSignedRegistryValue
		}
	}

	// If we don't have a successful response and also not a response for every
	// worker, we timed out.
	if srv == nil && responses < len(workers) {
		return modules.SignedRegistryValue{}, ErrRegistryLookupTimeout
	}

	// If we don't have a successful response but received a response from every
	// worker, we were unable to look up the entry.
	if srv == nil {
		return modules.SignedRegistryValue{}, ErrRegistryEntryNotFound
	}
	return *srv, nil
}

// managedUpdateRegistry updates the registries on all workers with the given
// registry value.
// NOTE: the input ctx only unblocks the call if it fails to hit the threshold
// before the timeout. It doesn't stop the update jobs. That's because we want
// to always make sure we update as many hosts as possble.
func (r *Renter) managedUpdateRegistry(ctx context.Context, spk types.SiaPublicKey, srv modules.SignedRegistryValue) (err error) {
	// Verify the signature before updating the hosts.
	if err := srv.Verify(spk.ToPublicKey()); err != nil {
		return errors.AddContext(err, "managedUpdateRegistry: failed to verify signature of entry")
	}
	// Get the full list of workers and create a channel to receive all of the
	// results from the workers. The channel is buffered with one slot per
	// worker, so that the workers do not have to block when returning the
	// result of the job, even if this thread is not listening.
	workers := r.staticWorkerPool.callWorkers()
	staticResponseChan := make(chan *jobUpdateRegistryResponse, len(workers))

	// Create a context to continue updating registry values in the background.
	updateTimeoutCtx, updateTimeoutCancel := context.WithTimeout(r.tg.StopCtx(), updateRegistryBackgroundTimeout)
	defer func() {
		if err != nil {
			// If managedUpdateRegistry fails the caller is going to assume that
			// updating the value failed. Don't let any jobs linger in that
			// case.
			updateTimeoutCancel()
		}
	}()

	// Filter out hosts that don't support the registry.
	numRegistryWorkers := 0
	for _, worker := range workers {
		cache := worker.staticCache()
		if build.VersionCmp(cache.staticHostVersion, minRegistryVersion) < 0 {
			continue
		}

		// Skip !goodForUpload workers.
		if !cache.staticContractUtility.GoodForUpload {
			continue
		}

		// check for price gouging
		// TODO: use upload gouging for some basic protection. Should be
		// replaced as part of the gouging overhaul.
		host, ok, err := r.hostDB.Host(worker.staticHostPubKey)
		if !ok || err != nil {
			continue
		}
		err = checkUploadGouging(cache.staticRenterAllowance, host.HostExternalSettings)
		if err != nil {
			r.log.Debugf("price gouging detected in worker %v, err: %v\n", worker.staticHostPubKeyStr, err)
			continue
		}

		// Create the job.
		jrr := worker.newJobUpdateRegistry(updateTimeoutCtx, staticResponseChan, spk, srv)
		if !worker.staticJobUpdateRegistryQueue.callAdd(jrr) {
			// This will filter out any workers that are on cooldown or
			// otherwise can't participate in the project.
			continue
		}
		workers[numRegistryWorkers] = worker
		numRegistryWorkers++
	}
	workers = workers[:numRegistryWorkers]
	// If there are no workers remaining, fail early.
	if len(workers) < MinUpdateRegistrySuccesses {
		return errors.AddContext(modules.ErrNotEnoughWorkersInWorkerPool, "cannot performa UpdateRegistry")
	}

	workersLeft := len(workers)
	responses := 0
	successfulResponses := 0
	highestInvalidRevNum := uint64(0)
	invalidRevNum := false

	var respErrs error
	for successfulResponses < MinUpdateRegistrySuccesses && workersLeft+successfulResponses >= MinUpdateRegistrySuccesses {
		// Check deadline.
		var resp *jobUpdateRegistryResponse
		select {
		case <-ctx.Done():
			// Timeout reached.
			return ErrRegistryUpdateTimeout
		case resp = <-staticResponseChan:
		}

		// Decrement the number of workers.
		workersLeft--

		// Increment number of responses.
		responses++

		// Ignore error responses except for invalid revision errors.
		if resp.staticErr != nil {
			// If we receive ErrLowerRevNum or ErrSameRevNum, remember the revision number
			// that was presented as proof. In the end we return the highest one to be able
			// to determine the next revision number that is save to use.
			if (errors.Contains(resp.staticErr, modules.ErrLowerRevNum) || errors.Contains(resp.staticErr, modules.ErrSameRevNum)) &&
				resp.srv.Revision > highestInvalidRevNum {
				highestInvalidRevNum = resp.srv.Revision
				invalidRevNum = true
			}
			respErrs = errors.Compose(respErrs, resp.staticErr)
			continue
		}

		// Increment successful responses.
		successfulResponses++
	}

	// Check for an invalid revision error and return the right error according
	// to the highest invalid revision we remembered.
	if invalidRevNum {
		if highestInvalidRevNum == srv.Revision {
			err = modules.ErrSameRevNum
		} else {
			err = modules.ErrLowerRevNum
		}
	}

	// Check if we ran out of workers.
	if successfulResponses == 0 {
		r.log.Print("RegistryUpdate failed with 0 successful responses: ", err)
		return errors.Compose(err, ErrRegistryUpdateNoSuccessfulUpdates)
	}
	if successfulResponses < MinUpdateRegistrySuccesses {
		r.log.Printf("RegistryUpdate failed with %v < %v successful responses: %v", successfulResponses, MinUpdateRegistrySuccesses, err)
		return errors.Compose(err, ErrRegistryUpdateInsufficientRedundancy)
	}
	return nil
}
