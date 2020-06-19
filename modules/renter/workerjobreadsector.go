package renter

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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

	return j.jobRead.managedRead(w, program, programData, cost)
}
