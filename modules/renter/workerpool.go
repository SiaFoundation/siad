package renter

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// workerPool is the collection of workers that the renter can use for
// uploading, downloading, and other tasks related to communicating with the
// host. There is one worker per host that the renter has a contract with. This
// includes hosts that have been disabled or otherwise been marked as
// !GoodForRenew or !GoodForUpload. We keep all of these workers so that they
// can be used in emergencies in the event that there seems to be no other way
// to recover data.
//
// TODO: Currently the repair loop does a lot of fetching and passing of host
// maps and offline maps and goodforrenew maps. All of those objects should be
// cached in the worker pool, which will both improve performance and reduce the
// calling complexity of the functions that currently need to pass this
// information around.
type workerPool struct {
	workers map[string]*worker // The string is the host's public key.
	mu      sync.RWMutex
	renter  *Renter
}

// callWorker will return the worker associated with the provided public key.
// If no worker is found, an error will be returned.
func (wp *workerPool) callWorker(hostPubKey types.SiaPublicKey) (*worker, error) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	worker, exists := wp.workers[hostPubKey.String()]
	if !exists {
		return nil, errors.New("worker is not available in the worker pool")
	}
	return worker, nil
}

// callUpdate will grab the set of contracts from the contractor and update the
// worker pool to match, creating new workers and killing existing workers as
// necessary.
func (wp *workerPool) callUpdate() {
	contractSlice := wp.renter.hostContractor.Contracts()
	contractMap := make(map[string]modules.RenterContract, len(contractSlice))
	for _, contract := range contractSlice {
		contractMap[contract.HostPublicKey.String()] = contract
	}

	// Lock the worker pool for the duration of updating its fields.
	wp.mu.Lock()
	defer wp.mu.Unlock()

	// Add a worker for any contract that does not already have a worker.
	for id, contract := range contractMap {
		_, exists := wp.workers[id]
		if !exists {
			w, err := wp.renter.newWorker(contract.HostPublicKey)
			if err != nil {
				wp.renter.log.Println((errors.AddContext(err, fmt.Sprintf("could not create a new worker for host %v", contract.HostPublicKey))))
				continue
			}
			wp.workers[id] = w
			if err := wp.renter.tg.Add(); err != nil {
				// Renter shutdown is happening, abort the loop to create more
				// workers.
				break
			}
			go func() {
				defer wp.renter.tg.Done()
				w.threadedWorkLoop()
			}()
		}
	}

	// Remove a worker for any worker that is not in the set of new contracts.
	totalCoolDown := 0
	for id, worker := range wp.workers {
		select {
		case <-wp.renter.tg.StopChan():
			// Release the lock and return to prevent error of trying to close
			// the worker channel after a shutdown
			return
		default:
		}
		contract, exists := contractMap[id]
		if !exists {
			delete(wp.workers, id)
			close(worker.killChan)
		}

		// A lock is grabbed on the worker to fetch some info for a debugging
		// statement. build.DEBUG is used so that worker lock contention is not
		// introduced needlessly.
		if build.DEBUG {
			worker.mu.Lock()
			onCoolDown, coolDownTime := worker.onUploadCooldown()
			if onCoolDown {
				totalCoolDown++
			}
			wp.renter.log.Debugf("Worker %v is GoodForUpload %v for contract %v and is on uploadCooldown %v for %v because of %v", worker.staticHostPubKey, contract.Utility.GoodForUpload, contract.ID, onCoolDown, coolDownTime, worker.uploadRecentFailureErr)
			worker.mu.Unlock()
		}
	}
	wp.renter.log.Debugf("worker pool has %v workers, %v are on cooldown", len(wp.workers), totalCoolDown)
}

// newWorkerPool will initialize and return a worker pool.
func (r *Renter) newWorkerPool() *workerPool {
	wp := &workerPool{
		workers: make(map[string]*worker),
		renter:  r,
	}
	wp.renter.tg.OnStop(func() error {
		wp.mu.RLock()
		for _, w := range wp.workers {
			close(w.killChan)
		}
		wp.mu.RUnlock()
		return nil
	})
	wp.callUpdate()
	return wp
}
