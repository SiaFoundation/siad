package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/errors"
)

// TestHasSectorCallExecuteCanceledJob tests if executing a already cancelled
// job works as expected.
func TestHasSectorCallExecuteCancelledJob(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a worker.
	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableWorker{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a cancelled job.
	cancel := make(chan struct{})
	close(cancel)
	responseChan := make(chan *jobHasSectorResponse)
	jhs := wt.worker.newJobHasSector(cancel, responseChan, crypto.Hash{})

	// Add the job to the queue.
	if !wt.worker.staticJobHasSectorQueue.callAdd(jhs) {
		t.Fatal("failed to add job")
	}

	// Execute jobs until we get a response.
	wt.worker.externTryLaunchAsyncJob()

	// Get the response.
	resp := <-responseChan
	if !errors.Contains(resp.staticErr, ErrJobDiscarded) {
		t.Fatal("wrong error", resp.staticErr)
	}
}
