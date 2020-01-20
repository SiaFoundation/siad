package renter

// worker.go defines a worker with a work loop. Each worker is connected to a
// single host, and the work loop will listen for jobs and then perform them.
//
// The worker has a set of jobs that it is capable of performing. The standard
// functions for a job are Queue, Kill, and Perform. Queue will add a job to the
// queue of work of that type. Kill will empty the queue and close out any work
// that will not be completed. Perform will grab a job from the queue if one
// exists and complete that piece of work. See workerfetchbackups.go for a clean
// example.
//
// The worker has an ephemeral account on the host. It can use this account to
// pay for downloads and uploads. In order to ensure the account's balance does
// not run out, it maintains a balance target by refilling it when necessary.
//
// TODO: A single session should be added to the worker that gets maintained
// within the work loop. All jobs performed by the worker will use the worker's
// single session.
//
// TODO: The upload and download code needs to be moved into properly separated
// subsystems.
//
// TODO: Need to write testing around the kill functions in the worker, to clean
// up any queued jobs after a worker has been killed.

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// A worker listens for work on a certain host.
//
// The mutex of the worker only protects the 'unprocessedChunks' and the
// 'standbyChunks' fields of the worker. The rest of the fields are only
// interacted with exclusively by the primary worker thread, and only one of
// those ever exists at a time.
//
// The workers have a concept of 'cooldown' for uploads and downloads. If a
// download or upload operation fails, the assumption is that future attempts
// are also likely to fail, because whatever condition resulted in the failure
// will still be present until some time has passed. Without any cooldowns,
// uploading and downloading with flaky hosts in the worker sets has
// substantially reduced overall performance and throughput.
type worker struct {
	// The host pub key also serves as an id for the worker, as there is only
	// one worker per host.
	staticHostPubKey types.SiaPublicKey

	// Download variables that are not protected by a mutex, but also do not
	// need to be protected by a mutex, as they are only accessed by the master
	// thread for the worker.
	//
	// The 'owned' prefix here indicates that only the master thread for the
	// object (in this case, 'threadedWorkLoop') is allowed to access these
	// variables. Because only that thread is allowed to access the variables,
	// that thread is able to access these variables without a mutex.
	ownedDownloadConsecutiveFailures int       // How many failures in a row?
	ownedDownloadRecentFailure       time.Time // How recent was the last failure?

	// Download variables related to queuing work. They have a separate mutex to
	// minimize lock contention.
	downloadChunks     []*unfinishedDownloadChunk // Yet unprocessed work items.
	downloadMu         sync.Mutex
	downloadTerminated bool // Has downloading been terminated for this worker?

	// Fetch backups queue for the worker.
	staticFetchBackupsJobQueue fetchBackupsJobQueue

	// Upload variables.
	unprocessedChunks         []*unfinishedUploadChunk // Yet unprocessed work items.
	uploadConsecutiveFailures int                      // How many times in a row uploading has failed.
	uploadRecentFailure       time.Time                // How recent was the last failure?
	uploadRecentFailureErr    error                    // What was the reason for the last failure?
	uploadTerminated          bool                     // Have we stopped uploading?

	// The staticFundAccountJobQueue holds the fund account jobs
	staticFundAccountJobQueue fundAccountJobQueue

	// Account represents the account on the host. The worker will maintain a
	// certain account balance defined by the max and target balance.
	staticBalanceTarget types.Currency
	account             *account

	// Utilities.
	//
	// The mutex is only needed when interacting with 'downloadChunks' and
	// 'unprocessedChunks', as everything else is only accessed from the single
	// master thread.
	killChan chan struct{} // Worker will shut down if a signal is sent down this channel.
	mu       sync.Mutex
	renter   *Renter
	wakeChan chan struct{} // Worker will check queues if given a wake signal.
}

// managedBlockUntilReady will block until the worker has internet connectivity.
// 'false' will be returned if a kill signal is received or if the renter is
// shut down before internet connectivity is restored. 'true' will be returned
// if internet connectivity is successfully restored.
func (w *worker) managedBlockUntilReady() bool {
	// Check if the worker has received a kill signal, or if the renter has
	// received a stop signal.
	select {
	case <-w.renter.tg.StopChan():
		return false
	case <-w.killChan:
		return false
	default:
	}

	// Check internet connectivity. If the worker does not have internet
	// connectivity, block until connectivity is restored.
	for !w.renter.g.Online() {
		select {
		case <-w.renter.tg.StopChan():
			return false
		case <-w.killChan:
			return false
		case <-time.After(offlineCheckFrequency):
		}
	}
	return true
}

// staticWake needs to be called any time that a job queued.
func (w *worker) staticWake() {
	select {
	case w.wakeChan <- struct{}{}:
	default:
	}
}

// threadedWorkLoop continually checks if work has been issued to a worker. The
// work loop checks for different types of work in a specific order, forming a
// priority queue for the various types of work. It is possible for continuous
// requests for one type of work to drown out a worker's ability to perform
// other types of work.
//
// If no work is found, the worker will sleep until woken up. Because each
// iteration is stateless, it may be possible to reduce the goroutine count in
// Sia by spinning down the worker / expiring the thread when there is no work,
// and then checking if the thread exists and creating a new one if not when
// alerting / waking the worker. This will not interrupt any connections that
// the worker has because the worker object will be kept in memory via the
// worker map.
func (w *worker) threadedWorkLoop() {
	// Ensure that all queued jobs are gracefully cleaned up when the worker is
	// shut down.
	//
	// TODO: Need to write testing around these kill functions and ensure they
	// are executing correctly.
	defer w.managedKillUploading()
	defer w.managedKillDownloading()
	defer w.managedKillFetchBackupsJobs()
	defer w.managedKillFundAccountJobs()

	// Primary work loop. There are several types of jobs that the worker can
	// perform, and they are attempted with a specific priority. If any type of
	// work is attempted, the loop resets to check for higher priority work
	// again. This means that a stream of higher priority tasks can starve a
	// building set of lower priority tasks.
	//
	// 'workAttempted' indicates that there was a job to perform, and that a
	// nontrivial amount of time was spent attempting to perform the job. The
	// job may or may not have been successful, that is irrelevant.
	for {
		// There are certain conditions under which the worker should either
		// block or exit. This function will block until those conditions are
		// met, returning 'true' when the worker can proceed and 'false' if the
		// worker should exit.
		if !w.managedBlockUntilReady() {
			return
		}

		// Check if the account needs to be refilled in a background thread.
		go w.threadedScheduleRefillAccount()

		// Perform any fund account jobs in a background thread.
		go w.threadedPerformFundAcountJob()

		// Perform any job to fetch the list of backups from the host.
		var workAttempted bool
		workAttempted = w.managedPerformFetchBackupsJob()
		if workAttempted {
			continue
		}
		// Perform any job to help download a chunk.
		workAttempted = w.managedPerformDownloadChunkJob()
		if workAttempted {
			continue
		}
		// Perform any job to help upload a chunk.
		workAttempted = w.managedPerformUploadChunkJob()
		if workAttempted {
			continue
		}

		// Block until new work is received via the upload or download channels,
		// or until a kill or stop signal is received.
		select {
		case <-w.wakeChan:
			continue
		case <-w.killChan:
			return
		case <-w.renter.tg.StopChan():
			return
		}
	}
}

// threadedScheduleRefillAccount will check if the account needs to be refilled,
// and will schedule a fund account job if so. This is called every time the
// worker spends from the account.
func (w *worker) threadedScheduleRefillAccount() {
	if err := w.renter.tg.Add(); err != nil {
		return
	}
	defer w.renter.tg.Done()

	// Calculate the threshold, if the account's available balance is below this
	// threshold, we want to trigger a refill.We only refill if we drop below a
	// threshold because we want to avoid refilling every time we drop 1 hasting
	// below the target.
	threshold := w.staticBalanceTarget.Mul64(8).Div64(10)

	// Fetch the account's available balance and skip if it's above the
	// threshold
	balance := w.account.AvailableBalance()
	if balance.Cmp(threshold) >= 0 {
		return
	}

	// If it's below the threshold, calculate the refill amount and enqueue a
	// new fund account job
	refill := w.staticBalanceTarget.Sub(balance)
	w.callQueueFundAccount(refill)
}

// newWorker will create and return a worker that is ready to receive jobs.
func (r *Renter) newWorker(hostPubKey types.SiaPublicKey, a *account) (*worker, error) {
	// TODO: we'll need the host to figure out a balance target
	_, ok, err := r.hostDB.Host(hostPubKey)
	if err != nil {
		return nil, errors.AddContext(err, "could not find host entry")
	}
	if !ok {
		return nil, errors.New("host does not exist")
	}

	return &worker{
		staticHostPubKey: hostPubKey,
		killChan:         make(chan struct{}),
		wakeChan:         make(chan struct{}, 1),
		renter:           r,

		// TODO: the balance target is currently hardcoded and does not take
		// into account the max ephemeral account balance (configured by the
		// host). The target balance should be calculated based off of that and
		// probably also a max configurable in the renter. For now the target is
		// temporarily set to half the default ephemeral account max balance
		staticBalanceTarget: types.SiacoinPrecision.Div64(2),
		account:             a,
	}, nil
}
