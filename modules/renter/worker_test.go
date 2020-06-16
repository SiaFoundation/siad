package renter

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// workerTester is a helper type which contains a renter, host and worker that
// communicates with that host.
type workerTester struct {
	rt   *renterTester
	host modules.Host
	*worker
}

// newWorkerTester creates a new worker for testing.
func newWorkerTester(name string) (*workerTester, error) {
	return newWorkerTesterCustomDependency(name, &modules.ProductionDependencies{})
}

// newWorkerTesterCustomDependency creates a new worker for testing with a
// custom depency.
func newWorkerTesterCustomDependency(name string, deps modules.Dependencies) (*workerTester, error) {
	// Create the renter.
	rt, err := newRenterTesterWithDependency(filepath.Join(name, "renter"), deps)
	if err != nil {
		return nil, err
	}

	// Set an allowance.
	err = rt.renter.hostContractor.SetAllowance(modules.DefaultAllowance)
	if err != nil {
		return nil, err
	}

	// Add a host.
	host, err := rt.addHost(filepath.Join(name, "host"))
	if err != nil {
		return nil, err
	}

	// Wait for worker to show up.
	var w *worker
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, err := rt.miner.AddBlock()
		if err != nil {
			return err
		}
		rt.renter.staticWorkerPool.callUpdate()
		workers := rt.renter.staticWorkerPool.callWorkers()
		if len(workers) != 1 {
			return fmt.Errorf("expected %v workers but got %v", 1, len(workers))
		}
		w = workers[0]
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &workerTester{
		rt:     rt,
		host:   host,
		worker: w,
	}, nil
}

// Close closes the renter and host.
func (wt *workerTester) Close() error {
	err1 := wt.rt.Close()
	err2 := wt.host.Close()
	return errors.Compose(err1, err2)
}

// TestNewWorkerTester creates a new worker
func TestNewWorkerTester(t *testing.T) {
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}
}
