package renter

import (
	"context"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// jobReadSector contains information about a hasSector query.
	jobReadSector struct {
		jobRead

		staticOffset uint64
		staticSector crypto.Hash
	}
)

// callExecute executes the jobReadSector.
func (j *jobReadSector) callExecute() {
	// Track how long the job takes.
	start := time.Now()
	data, err := j.managedReadSector()
	jobTime := time.Since(start)

	// Finish the execution.
	j.jobRead.managedFinishExecute(data, err, jobTime)
}

// managedReadSector returns the sector data for given root.
func (j *jobReadSector) managedReadSector() ([]byte, error) {
	// create the program
	w := j.staticQueue.staticWorker()
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since ReadSector doesn't depend on it.
	pb.AddReadSectorInstruction(j.staticLength, j.staticOffset, j.staticSector, true)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	data, err := j.jobRead.managedRead(w, program, programData, cost)
	return data, errors.AddContext(err, "jobReadSector: failed to execute managedRead")
}

// ReadSector is a helper method to run a ReadSector job on a worker.
func (w *worker) ReadSector(ctx context.Context, root crypto.Hash, offset, length uint64) ([]byte, error) {
	readSectorRespChan := make(chan *jobReadResponse)
	jro := &jobReadSector{
		jobRead: jobRead{
			staticResponseChan: readSectorRespChan,
			staticLength:       length,
			jobGeneric:         newJobGeneric(w.staticJobReadQueue, ctx.Done()),
		},
		staticOffset: offset,
		staticSector: root,
	}

	// Add the job to the queue.
	if !w.staticJobReadQueue.callAdd(jro) {
		return nil, errors.New("worker unavailable")
	}

	// Wait for the response.
	var resp *jobReadResponse
	select {
	case <-ctx.Done():
		return nil, errors.New("Read interrupted")
	case resp = <-readSectorRespChan:
	}
	return resp.staticData, resp.staticErr
}

// readSectorJobExpectedBandwidth is a helper function that returns the expected
// bandwidth consumption of a read sector job. This helper function takes a
// length parameter and is used to get the expected bandwidth without having to
// instantiate a job.
func readSectorJobExpectedBandwidth(length uint64) (ul, dl uint64) {
	ul = 1 << 15                              // 32 KiB
	dl = uint64(float64(length)*1.01) + 1<<14 // (readSize * 1.01 + 16 KiB)
	return
}
