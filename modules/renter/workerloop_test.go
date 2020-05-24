package renter

import (
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// dependencyTestJobSerialzation is a special dependency to change the behavior
// of 'externTryLaunchSerialJob' to check that it does a good job of only having
// a single serial job run at a time.
type dependencyTestJobSerialization struct {
	modules.ProductionDependencies

	// Making this a time.Duration means we don't have to typecast it when
	// comparing the number of jobs to the amount of time it took to complete
	// them.
	jobsCompleted time.Duration

	mu sync.Mutex
}

// Disrupt will check for two specific disrupts and respond accordingly.
func (d *dependencyTestJobSerialization) Disrupt(s string) bool {
	if s == "TestJobSerialization" {
		return true
	}
	if s == "TestJobSerializationCompleted" {
		d.mu.Lock()
		d.jobsCompleted++
		d.mu.Unlock()
	}
	return false
}

// TestJobSerialization checks that only one serial job for the worker is
// allowed to run at once.
func TestJobSerialization(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a sub worker.
	d := &dependencyTestJobSerialization{}
	w := new(worker)
	w.renter = new(Renter)
	w.renter.deps = d

	// Launch a bunch of serial jobs in the worker. Each job that succeedsd
	// should take about 100ms to complete, we launch jobs 25ms apart for this
	// reason. To minimize code clutter, there is no shared state between the
	// job that runs and what happens here, safety is instead checked using
	// sanity checks within the job that runs.
	start := time.Now()
	for i := 0; i < 100; i++ {
		w.externTryLaunchSerialJob()
		time.Sleep(time.Millisecond * 25)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.jobsCompleted > 30 || d.jobsCompleted < 20 {
		t.Error("job serializer seems to be running the wrong number of jobs", d.jobsCompleted)
	}
	if time.Since(start) < d.jobsCompleted*100*time.Millisecond {
		t.Error("job serializer should be ensuring that at most one job completes per 100ms")
	}
}
