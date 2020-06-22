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
	pb := modules.NewProgramBuilder(&pt)
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
			jobGeneric: &jobGeneric{
				staticCancelChan: ctx.Done(),

				staticQueue: w.staticJobReadQueue,
			},
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
