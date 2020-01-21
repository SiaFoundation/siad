package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// fundAccountJobQueue is the primary structure for managing fund ephemeral
// account jobs from the worker.
type fundAccountJobQueue struct {
	queue []*fundAccountJob
	mu    sync.Mutex
}

// fundAccountJob contains the details of which ephemeral account to fund and
// with how much.
type fundAccountJob struct {
	amount     types.Currency
	resultChan chan fundAccountJobResult
}

// fundAccountJobResult contains the result from funding an ephemeral account
// on the host.
type fundAccountJobResult struct {
	funded types.Currency
	err    error
}

// callQueueFundAccountJob will add a fund account job to the worker's
// queue. A channel will be returned, this channel will have the result of the
// job returned down it when the job is completed.
func (w *worker) callQueueFundAccount(amount types.Currency) chan fundAccountJobResult {
	resultChan := make(chan fundAccountJobResult)
	w.staticFundAccountJobQueue.mu.Lock()
	w.staticFundAccountJobQueue.queue = append(w.staticFundAccountJobQueue.queue, &fundAccountJob{
		amount:     amount,
		resultChan: resultChan,
	})
	w.staticFundAccountJobQueue.mu.Unlock()
	w.staticWake()

	return resultChan
}

// managedKillFundAccountJobs will throw an error for all queued fund account
// jobs, as they will not complete due to the worker being shut down.
func (w *worker) managedKillFundAccountJobs() {
	w.staticFundAccountJobQueue.mu.Lock()
	for _, job := range w.staticFundAccountJobQueue.queue {
		result := fundAccountJobResult{
			funded: types.ZeroCurrency,
			err:    errors.New("worker killed before account could be funded"),
		}
		job.resultChan <- result
	}
	w.staticFundAccountJobQueue.mu.Unlock()
}

// threadedPerformFundAcountJob will try and execute a fund account job if there
// is one in the queue.
func (w *worker) threadedPerformFundAcountJob() {
	// Register ourselves with the threadgroup
	if err := w.renter.tg.Add(); err != nil {
		return
	}
	defer w.renter.tg.Done()

	// Try to dequeue a job, return if there's no work to be performed
	w.staticFundAccountJobQueue.mu.Lock()
	if len(w.staticFundAccountJobQueue.queue) == 0 {
		w.staticFundAccountJobQueue.mu.Unlock()
		return
	}
	job := w.staticFundAccountJobQueue.queue[0]
	w.staticFundAccountJobQueue.queue = w.staticFundAccountJobQueue.queue[1:]
	w.staticFundAccountJobQueue.mu.Unlock()

	client, err := w.renter.managedRPCClient(w.staticHostPubKey)
	if err != nil {
		job.resultChan <- fundAccountJobResult{err: err}
		return
	}

	err = client.FundEphemeralAccount(w.account.staticID, job.amount)
	if err != nil {
		job.resultChan <- fundAccountJobResult{err: err}
		return
	}

	job.resultChan <- fundAccountJobResult{funded: job.amount}
}
