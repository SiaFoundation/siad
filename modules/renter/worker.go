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
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// workerCacheUpdateFrequency specifies how much time must pass before the
	// worker updates its cache.
	workerCacheUpdateFrequency = build.Select(build.Var{
		Dev:      time.Second * 5,
		Standard: time.Minute,
		Testing:  time.Second,
	}).(time.Duration)
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
	staticHostPubKey     types.SiaPublicKey
	staticHostPubKeyStr  string
	staticHostMuxAddress string

	// Cached value for the contract utility, updated infrequently.
	cachedContractUtility modules.ContractUtility

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

	// Job queues for the worker.
	staticFetchBackupsJobQueue   fetchBackupsJobQueue
	staticJobQueueDownloadByRoot jobQueueDownloadByRoot

	// Upload variables.
	unprocessedChunks         []*unfinishedUploadChunk // Yet unprocessed work items.
	uploadConsecutiveFailures int                      // How many times in a row uploading has failed.
	uploadRecentFailure       time.Time                // How recent was the last failure?
	uploadRecentFailureErr    error                    // What was the reason for the last failure?
	uploadTerminated          bool                     // Have we stopped uploading?

	// The staticAccount represent the renter's ephemeral account on the host.
	// It keeps track of the available balance in the account, the worker has a
	// refill mechanism that keeps the account balance filled up until the
	// staticBalanceTarget.
	staticAccount       *account
	staticBalanceTarget types.Currency

	// The current block height is cached on the client and gets updated by the
	// renter when the consensus changes. This to avoid fetching the block
	// height from the renter on every RPC call.
	cachedBlockHeight types.BlockHeight

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

// managedUpdateCache will check how recently each of the cached values of the
// worker has been updated and update anything that is not recent enough.
//
// 'false' will be returned if the cache cannot be updated, signaling that the
// worker should exit.
func (w *worker) managedUpdateCache() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	utility, exists := w.renter.hostContractor.ContractUtility(w.staticHostPubKey)
	if !exists {
		return false
	}
	w.cachedContractUtility = utility
	return true
}

// staticKilled is a convenience function to determine if a worker has been
// killed or not.
func (w *worker) staticKilled() bool {
	select {
	case <-w.killChan:
		return true
	default:
		return false
	}
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
	defer w.managedKillJobsDownloadByRoot()

	// Fetch the cache for the worker before doing any work.
	if !w.managedUpdateCache() {
		w.renter.log.Println("Worker is being insta-killed because the cache update could not locate utility for the worker")
		return
	}
	lastCacheUpdate := time.Now()

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

		// Check if the cache needs to be updated.
		if time.Since(lastCacheUpdate) > workerCacheUpdateFrequency {
			if !w.managedUpdateCache() {
				w.renter.log.Debugln("worker is being killed because the cache could not be updated")
				return
			}
			lastCacheUpdate = time.Now()
		}

		// Check if the account needs to be refilled.
		w.scheduleRefillAccount()

		// Perform any job to fetch the list of backups from the host.
		workAttempted := w.managedPerformFetchBackupsJob()
		if workAttempted {
			continue
		}
		// Perform any job to fetch data by its sector root. This is given
		// priority because it is only used by viewnodes, which are service
		// operators that need to have good performance for their customers.
		workAttempted = w.managedLaunchJobDownloadByRoot()
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

		// Create a timer and a drain function for the timer.
		cacheUpdateTimer := time.NewTimer(workerCacheUpdateFrequency)
		drainCacheTimer := func() {
			if !cacheUpdateTimer.Stop() {
				<-cacheUpdateTimer.C
			}
		}

		// Block until:
		//    + New work has been submitted
		//    + The cache timer fires
		//    + The worker is killed
		//    + The renter is stopped
		select {
		case <-w.wakeChan:
			drainCacheTimer()
			continue
		case <-cacheUpdateTimer.C:
			continue
		case <-w.killChan:
			drainCacheTimer()
			return
		case <-w.renter.tg.StopChan():
			drainCacheTimer()
			return
		}
	}
}

// scheduleRefillAccount will check if the account needs to be refilled,
// and will schedule a fund account job if so. This is called every time the
// worker spends from the account.
func (w *worker) scheduleRefillAccount() {
	// Calculate the threshold, if the account's available balance is below this
	// threshold, we want to trigger a refill. We only refill if we drop below a
	// threshold because we want to avoid refilling every time we drop 1 hasting
	// below the target.
	threshold := w.staticBalanceTarget.Div64(2)

	// Fetch the account's available balance and skip if it's above the
	// threshold
	balance := w.staticAccount.AvailableBalance()
	if balance.Cmp(threshold) >= 0 {
		return
	}

	// TODO: manually perform the refill
	// TODO: handle result chan
}

// newWorker will create and return a worker that is ready to receive jobs.
func (r *Renter) newWorker(hostPubKey types.SiaPublicKey, blockHeight types.BlockHeight) (*worker, error) {
	host, ok, err := r.hostDB.Host(hostPubKey)
	if err != nil {
		return nil, errors.AddContext(err, "could not find host entry")
	}
	if !ok {
		return nil, errors.New("host does not exist")
	}

	// TODO: set the balance target to 1SC and a check that verifies it makes
	// sense in function of the amount of MDM programs it can run with that
	// amount of money

	// TODO: enable the account refiller by setting a balance target greater
	// than the zero currency. It is important this remains zero for as long as
	// the FundEphemeralAccountRPC is not merged. Before it is enabled we also
	// need a cooldown in case the RPC fails, to ensure we don't keep enqueueing
	// refill jobs

	hostMuxAddress := fmt.Sprintf("%s:%s", host.NetAddress.Host(), host.HostExternalSettings.SiaMuxPort)

	account := newAccount(hostPubKey)

	return &worker{
		staticHostPubKey:     hostPubKey,
		staticHostPubKeyStr:  hostPubKey.String(),
		staticHostMuxAddress: hostMuxAddress,

		staticAccount:       account,
		staticBalanceTarget: types.ZeroCurrency,

		cachedBlockHeight: blockHeight,

		killChan: make(chan struct{}),
		wakeChan: make(chan struct{}, 1),
		renter:   r,
	}, nil
}

// threadedUpdateBlockheightOnWorkers is called on consensus change and updates
// the (cached) blockheight on every individual worker.
func (r *Renter) threadedUpdateBlockheightOnWorkers() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	// grab the current block height and have all workers cache it
	blockHeight := r.cs.Height()
	for _, worker := range r.staticWorkerPool.managedWorkers() {
		worker.managedUpdateBlockHeight(blockHeight)
	}
}

// programResponse is a helper struct that wraps the RPCExecuteProgramResponse
// alongside the data output
type programResponse struct {
	modules.RPCExecuteProgramResponse
	Output []byte
}

// managedExecuteProgram performs the ExecuteProgramRPC on the host
func (w *worker) managedExecuteProgram(pp modules.PaymentProvider, stream siamux.Stream, pt *modules.RPCPriceTable, p modules.Program, data []byte, fcid types.FileContractID, cost types.Currency) (responses []programResponse, err error) {
	// grab some variables from the worker
	w.mu.Lock()
	hostKey := w.staticHostPubKey
	refundAccountID := w.staticAccount.staticID
	blockHeight := w.cachedBlockHeight
	w.mu.Unlock()

	// close the stream
	defer func() {
		cErr := stream.Close()
		if cErr != nil {
			err = errors.Compose(err, cErr)
			responses = nil
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return
	}

	// send price table uid
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return
	}

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    fcid,
		Program:           p,
		ProgramDataLength: uint64(len(data)),
	}

	// provide payment
	err = pp.ProvidePayment(stream, hostKey, modules.RPCUpdatePriceTable, cost, refundAccountID, blockHeight)
	if err != nil {
		return
	}

	// send the execute program request.
	err = modules.RPCWrite(stream, epr)
	if err != nil {
		return
	}

	// send the programData.
	_, err = stream.Write(data)
	if err != nil {
		return
	}

	// read the responses.
	responses = make([]programResponse, len(epr.Program))
	for i := range responses {
		err = modules.RPCRead(stream, &responses[i])
		if err != nil {
			return
		}

		// Read the output data.
		outputLen := responses[i].OutputLength
		responses[i].Output = make([]byte, outputLen, outputLen)
		_, err = io.ReadFull(stream, responses[i].Output)
		if err != nil {
		}

		// If the response contains an error we are done.
		if responses[i].Error != nil {
			return
		}
	}
	return
}

// managedFundAccount will call the fundAccountRPC on the host and if successful
// will deposit the given amount into the specified ephemeral account.
func (w *worker) managedFundAccount(pp modules.PaymentProvider, stream siamux.Stream, pt modules.RPCPriceTable, id modules.AccountID, amount types.Currency) (resp modules.FundAccountResponse, err error) {
	// grab some variables from the worker
	w.mu.Lock()
	hostKey := w.staticHostPubKey
	blockHeight := w.cachedBlockHeight
	w.mu.Unlock()

	// close the stream
	defer func() {
		cErr := stream.Close()
		if cErr != nil {
			err = errors.Compose(err, cErr)
			resp = modules.FundAccountResponse{}
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		return
	}

	// send price table uid
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return
	}

	// send fund account request
	err = modules.RPCWrite(stream, modules.FundAccountRequest{Account: id})
	if err != nil {
		return
	}

	// provide payment
	err = pp.ProvidePayment(stream, hostKey, modules.RPCFundAccount, amount.Add(pt.FundAccountCost), modules.ZeroAccountID, blockHeight)
	if err != nil {
		return
	}

	// receive FundAccountResponse
	err = modules.RPCRead(stream, &resp)
	return
}

// managedUpdateBlockHeight is called by the renter when it processes a
// consensus change. The worker forwards this to the RPC client to ensure it has
// the latest block height. This eliminates lock contention on the renter.
func (w *worker) managedUpdateBlockHeight(blockHeight types.BlockHeight) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cachedBlockHeight = blockHeight
}

// managedUpdatePriceTable performs the UpdatePriceTableRPC on the host.
func (w *worker) managedUpdatePriceTable(pp modules.PaymentProvider, stream siamux.Stream) (pt modules.RPCPriceTable, err error) {
	// grab some variables from the worker
	w.mu.Lock()
	hostKey := w.staticHostPubKey
	refundAccountID := w.staticAccount.staticID
	blockHeight := w.cachedBlockHeight
	w.mu.Unlock()

	// close the stream
	defer func() {
		cErr := stream.Close()
		if cErr != nil {
			err = errors.Compose(err, cErr)
			pt = modules.RPCPriceTable{}
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		return
	}

	// receive the price table
	var uptr modules.RPCUpdatePriceTableResponse
	err = modules.RPCRead(stream, &uptr)
	if err != nil {
		return
	}

	// decode the JSON
	err = json.Unmarshal(uptr.PriceTableJSON, &pt)
	if err != nil {
		return
	}

	// TODO: perform gouging check

	// TODO: (follow-up) this should negatively affect the host's score

	// provide payment
	err = pp.ProvidePayment(stream, hostKey, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, refundAccountID, blockHeight)
	return
}
