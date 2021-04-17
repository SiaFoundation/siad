package renter

// worker.go defines a worker with a work loop. Each worker is connected to a
// single host, and the work loop will listen for jobs and then perform them.
//
// The worker has a set of jobs that it is capable of performing. The standard
// functions for a job are Queue, Kill, and Perform. Queue will add a job to the
// queue of work of that type. Kill will empty the queue and close out any work
// that will not be completed. Perform will grab a job from the queue if one
// exists and complete that piece of work.
//
// The worker has an ephemeral account on the host. It can use this account to
// pay for downloads and uploads. In order to ensure the account's balance does
// not run out, it maintains a balance target by refilling it when necessary.

import (
	"container/list"
	"sync"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/threadgroup"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// minRHP3Version defines the minimum version that supports RHP3. Note that
	// this constant is not used, it is left in for documentation purposes only.
	minRHP3Version = "1.4.10"

	// minRegistryVersion defines the minimum version that is required for a
	// host to support the registry.
	minRegistryVersion = "1.5.1"

	// registryCacheSize is the cache size used by a single worker for the
	// registry cache.
	registryCacheSize = 1 << 20 // 1 MiB
)

const (
	// These variables define the total amount of data that a worker is willing
	// to queue at once when performing async tasks. If the worker has more data
	// queued in its async queue than this, it will stop launching jobs so that
	// the jobs it does launch have more breathing room to complete.
	//
	// The worker may adjust these values dynamically as it starts to run and
	// determines how much stuff it can do simultaneously before its jobs start
	// to have significant latency impact.
	initialConcurrentAsyncReadData  = 10e6
	initialConcurrentAsyncWriteData = 10e6
)

type (
	// A worker listens for work on a certain host.
	//
	// The mutex of the worker only protects the 'unprocessedChunks' and the
	// 'standbyChunks' fields of the worker. The rest of the fields are only
	// interacted with exclusively by the primary worker thread, and only one of
	// those ever exists at a time.
	//
	// The workers have a concept of 'cooldown' for the jobs it performs. If a
	// job fails, the assumption is that future attempts are also likely to
	// fail, because whatever condition resulted in the failure will still be
	// present until some time has passed.
	worker struct {
		// Atomics are used to minimize lock contention on the worker object.
		atomicAccountBalanceCheckRunning uint64         // used for a sanity check
		atomicCache                      unsafe.Pointer // points to a workerCache object
		atomicCacheUpdating              uint64         // ensures only one cache update happens at a time
		atomicPriceTable                 unsafe.Pointer // points to a workerPriceTable object
		atomicPriceTableUpdateRunning    uint64         // used for a sanity check

		// The host pub key also serves as an id for the worker, as there is
		// only one worker per host.
		staticHostPubKey    types.SiaPublicKey
		staticHostPubKeyStr string

		// Job queues for the worker.
		staticJobDownloadSnapshotQueue *jobDownloadSnapshotQueue
		staticJobHasSectorQueue        *jobHasSectorQueue
		staticJobReadQueue             *jobReadQueue
		staticJobLowPrioReadQueue      *jobReadQueue
		staticJobReadRegistryQueue     *jobReadRegistryQueue
		staticJobRenewQueue            *jobRenewQueue
		staticJobUpdateRegistryQueue   *jobUpdateRegistryQueue
		staticJobUploadSnapshotQueue   *jobUploadSnapshotQueue

		// Upload variables.
		unprocessedChunks         *uploadChunks // Yet unprocessed work items.
		uploadConsecutiveFailures int           // How many times in a row uploading has failed.
		uploadRecentFailure       time.Time     // How recent was the last failure?
		uploadRecentFailureErr    error         // What was the reason for the last failure?
		uploadTerminated          bool          // Have we stopped uploading?

		// The staticAccount represent the renter's ephemeral account on the
		// host. It keeps track of the available balance in the account, the
		// worker has a refill mechanism that keeps the account balance filled
		// up until the staticBalanceTarget.
		staticAccount       *account
		staticBalanceTarget types.Currency

		// The loop state contains information about the worker loop. It is
		// mostly atomic variables that the worker uses to ratelimit the
		// launching of async jobs.
		staticLoopState *workerLoopState

		// The maintenance state contains information about the worker's RHP3
		// related state. It is used to determine whether or not the worker's
		// maintenance cooldown can be reset.
		staticMaintenanceState *workerMaintenanceState

		// staticRegistryCache caches information about the worker's host's
		// registry entries.
		staticRegistryCache *registryRevisionCache

		// staticSetInitialEstimates is an object that ensures the initial queue
		// estimates of the HS and RJ queues are only set once.
		staticSetInitialEstimates sync.Once

		// subscription-related fields
		staticSubscriptionInfo *subscriptionInfos

		// Utilities.
		staticTG threadgroup.ThreadGroup
		mu       sync.Mutex
		renter   *Renter
		wakeChan chan struct{} // Worker will check queues if given a wake signal.
	}
)

// downloadChunks is a queue of download chunks.
type downloadChunks struct {
	*list.List
}

// newDownloadChunks initializes a new queue.
func newDownloadChunks() *downloadChunks {
	return &downloadChunks{
		List: list.New(),
	}
}

// Pop removes the first element of the queue.
func (queue *downloadChunks) Pop() *unfinishedDownloadChunk {
	mr := queue.Front()
	if mr == nil {
		return nil
	}
	return queue.List.Remove(mr).(*unfinishedDownloadChunk)
}

// uploadChunks is a queue of upload chunks.
type uploadChunks struct {
	*list.List
}

// newUploadChunks initializes a new queue.
func newUploadChunks() *uploadChunks {
	return &uploadChunks{
		List: list.New(),
	}
}

// Pop removes the first element of the queue.
func (queue *uploadChunks) Pop() *unfinishedUploadChunk {
	mr := queue.Front()
	if mr == nil {
		return nil
	}
	return queue.List.Remove(mr).(*unfinishedUploadChunk)
}

// managedKill will kill the worker.
func (w *worker) managedKill() {
	w.mu.Lock()
	defer w.mu.Unlock()

	err := w.staticTG.Stop()
	if err != nil && !errors.Contains(err, threadgroup.ErrStopped) {
		w.renter.log.Printf("Worker %v: kill failed: %v", w.staticHostPubKeyStr, err)
	}
}

// staticKilled is a convenience function to determine if a worker has been
// killed or not.
func (w *worker) staticKilled() bool {
	select {
	case <-w.staticTG.StopChan():
		return true
	default:
		return false
	}
}

// staticWake will wake the worker from sleeping. This should be called any time
// that a job is queued or a job completes.
func (w *worker) staticWake() {
	select {
	case w.wakeChan <- struct{}{}:
	default:
	}
}

// newWorker will create and return a worker that is ready to receive jobs.
func (r *Renter) newWorker(hostPubKey types.SiaPublicKey) (*worker, error) {
	_, ok, err := r.hostDB.Host(hostPubKey)
	if err != nil {
		return nil, errors.AddContext(err, "could not find host entry")
	}
	if !ok {
		return nil, errors.New("host does not exist")
	}

	// open the account
	account, err := r.staticAccountManager.managedOpenAccount(hostPubKey)
	if err != nil {
		return nil, errors.AddContext(err, "could not open account")
	}

	// set the balance target to 1SC
	//
	// TODO: check that the balance target  makes sense in function of the
	// amount of MDM programs it can run with that amount of money
	balanceTarget := types.SiacoinPrecision
	if r.deps.Disrupt("DisableFunding") {
		balanceTarget = types.ZeroCurrency
	}

	w := &worker{
		staticHostPubKey:    hostPubKey,
		staticHostPubKeyStr: hostPubKey.String(),

		staticAccount:       account,
		staticBalanceTarget: balanceTarget,

		staticRegistryCache: newRegistryCache(registryCacheSize),

		staticSubscriptionInfo: &subscriptionInfos{
			subscriptions:  make(map[modules.RegistryEntryID]*subscription),
			staticWakeChan: make(chan struct{}, 1),
		},

		// Initialize the read and write limits for the async worker tasks.
		// These may be updated in real time as the worker collects metrics
		// about itself.
		staticLoopState: &workerLoopState{
			atomicReadDataLimit:  initialConcurrentAsyncReadData,
			atomicWriteDataLimit: initialConcurrentAsyncWriteData,
		},

		unprocessedChunks: newUploadChunks(),
		wakeChan:          make(chan struct{}, 1),
		renter:            r,
	}
	w.newPriceTable()
	w.newMaintenanceState()
	w.initJobHasSectorQueue()
	w.initJobReadQueue()
	w.initJobLowPrioReadQueue()
	w.initJobRenewQueue()
	w.initJobDownloadSnapshotQueue()
	w.initJobReadRegistryQueue()
	w.initJobUpdateRegistryQueue()
	w.initJobUploadSnapshotQueue()

	// Close the worker when the renter is stopped.
	err = r.tg.OnStop(func() error {
		w.managedKill()
		return nil
	})
	if err != nil {
		return nil, errors.AddContext(err, "failed to register OnStop for worker threadgroup")
	}

	// Get the worker cache set up before returning the worker. This prevents a
	// race condition in some tests.
	w.managedUpdateCache()
	if w.staticCache() == nil {
		return nil, errors.New("unable to build a cache for the worker")
	}
	return w, nil
}
