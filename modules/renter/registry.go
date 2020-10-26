package renter

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/registry"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// DefaultRegistryReadTimeout is the default timeout used when reading from
	// the registry.
	DefaultRegistryReadTimeout = build.Select(build.Var{
		Dev:      30 * time.Second,
		Standard: 5 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// DefaultRegistryUpdateTimeout is the default timeout used when updating
	// the registry.
	DefaultRegistryUpdateTimeout = build.Select(build.Var{
		Dev:      30 * time.Second,
		Standard: 5 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// ErrRegistryEntryNotFound is returned if all workers were unable to fetch
	// the entry.
	ErrRegistryEntryNotFound = errors.New("failed to look up the registry entry")

	// ErrRegistryLookupTimeout is similar to ErrRegistryEntryNotFound but it is
	// returned instead if the lookup timed out before all workers returned.
	ErrRegistryLookupTimeout = errors.New("looking up a registry entry timed out")

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
		Testing:  3,
	}).(int)

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
)

// ReadRegistry starts a registry lookup on all available workers. The
// jobs have 'timeout' amount of time to finish their jobs and return a
// response. Otherwise the response with the highest revision number will be
// used.
func (r *Renter) ReadRegistry(spk types.SiaPublicKey, tweak crypto.Hash, timeout time.Duration) (modules.SignedRegistryValue, error) {
	// Block until there is memory available, and then ensure the memory gets
	// returned.
	// Since registry entries are very small we use a fairly generous multiple.
	if !r.memoryManager.Request(readRegistryMemory, memoryPriorityHigh) {
		return modules.SignedRegistryValue{}, errors.New("renter shut down before memory could be allocated for the project")
	}
	defer r.memoryManager.Return(readRegistryMemory)

	// Create a context. If the timeout is greater than zero, have the context
	// expire when the timeout triggers.
	ctx := r.tg.StopCtx()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.tg.StopCtx(), timeout)
		defer cancel()
	}

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
	// Block until there is memory available, and then ensure the memory gets
	// returned.
	// Since registry entries are very small we use a fairly generous multiple.
	if !r.memoryManager.Request(updateRegistryMemory, memoryPriorityHigh) {
		return errors.New("renter shut down before memory could be allocated for the project")
	}
	defer r.memoryManager.Return(updateRegistryMemory)

	// Create a context. If the timeout is greater than zero, have the context
	// expire when the timeout triggers.
	ctx := r.tg.StopCtx()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.tg.StopCtx(), timeout)
		defer cancel()
	}

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
	// Create a context that dies when the function ends, this will cancel all
	// of the worker jobs that get created by this function.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

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
		// TODO: use PDBR gouging for some basic protection. Should be replaced
		// as part of the gouging overhaul.
		pt := worker.staticPriceTable().staticPriceTable
		err := checkPDBRGouging(pt, cache.staticRenterAllowance)
		if err != nil {
			r.log.Debugf("price gouging detected in worker %v, err: %v\n", worker.staticHostPubKeyStr, err)
			continue
		}

		jrr := worker.newJobReadRegistry(ctx, staticResponseChan, spk, tweak)
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
		return modules.SignedRegistryValue{}, errors.AddContext(modules.ErrNotEnoughWorkersInWorkerPool, "cannot perform ReadRegistry")
	}

	// Prepare a context which will be overwritten by a child context with a timeout
	// when we receive the first response. useHighestRevDefaultTimeout after
	// receiving the first response, this will be closed to abort the search for
	// the highest rev number and return the highest one we have so far.
	useHighestRevCtx := ctx

	var srv modules.SignedRegistryValue
	responses := 0
	successfulResponses := 0

LOOP:
	for responses < len(workers) {
		// Check if we are supposed to stop and use the highest revision
		// response.
		select {
		case <-useHighestRevCtx.Done():
			if successfulResponses > 0 {
				break LOOP
			}
		default:
		}

		// If not, or if we don't have a valid response yet, we wait for one.
		var resp *jobReadRegistryResponse
		select {
		case <-ctx.Done():
			break LOOP // timeout reached
		case resp = <-staticResponseChan:
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

		// Increment successful responses.
		successfulResponses++

		// Remember the response with the highest revision number. We use >=
		// here to also catch the edge case of the initial revision being 0.
		if resp.staticSignedRegistryValue.Revision >= srv.Revision {
			srv = *resp.staticSignedRegistryValue
		}
	}

	// If we don't have a successful response and also not a response for every
	// worker, we timed out.
	if successfulResponses == 0 && responses < len(workers) {
		return modules.SignedRegistryValue{}, ErrRegistryLookupTimeout
	}

	// If we don't have a successful response but received a response from every
	// worker, we were unable to look up the entry.
	if successfulResponses == 0 {
		return modules.SignedRegistryValue{}, ErrRegistryEntryNotFound
	}
	return srv, nil
}

// managedUpdateRegistry updates the registries on all workers with the given
// registry value.
// NOTE: the input ctx only unblocks the call if it fails to hit the threshold
// before the timeout. It doesn't stop the update jobs. That's because we want
// to always make sure we update as many hosts as possble.
func (r *Renter) managedUpdateRegistry(ctx context.Context, spk types.SiaPublicKey, srv modules.SignedRegistryValue) error {
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

	// Filter out hosts that don't support the registry.
	numRegistryWorkers := 0
	for _, worker := range workers {
		cache := worker.staticCache()
		if build.VersionCmp(cache.staticHostVersion, minRegistryVersion) < 0 {
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

		// Create the job. We purposefully use the renter's ctx here instead of
		// the provided one to make sure the jobs can finish in the background
		// instead of being killed when the timeout channel is closed.
		jrr := worker.newJobUpdateRegistry(r.tg.StopCtx(), staticResponseChan, spk, srv)
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

	var additionalErrs error
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

		// Ignore error responses.
		if resp.staticErr != nil {
			// If the error was a ErrLowerRevNum, append it. Only do that once.
			if errors.Contains(resp.staticErr, registry.ErrLowerRevNum) &&
				!errors.Contains(additionalErrs, registry.ErrLowerRevNum) {
				additionalErrs = errors.Compose(additionalErrs, registry.ErrLowerRevNum)
			}
			// If the error was a ErrSameRevNum, append it. Only do that once.
			if errors.Contains(resp.staticErr, registry.ErrSameRevNum) &&
				!errors.Contains(additionalErrs, registry.ErrSameRevNum) {
				additionalErrs = errors.Compose(additionalErrs, registry.ErrSameRevNum)
			}
			continue
		}

		// Increment successful responses.
		successfulResponses++
	}

	// Check if we ran out of workers.
	if successfulResponses == 0 {
		return errors.Compose(ErrRegistryUpdateNoSuccessfulUpdates, additionalErrs)
	}
	if successfulResponses < MinUpdateRegistrySuccesses {
		return errors.Compose(ErrRegistryUpdateInsufficientRedundancy, additionalErrs)
	}
	return nil
}
