package renter

import (
	"context"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// jobReadOffset contains information about a ReadOffset job.
	jobReadOffset struct {
		jobRead

		staticOffset uint64
	}
)

// callExecute executes the jobReadOffset.
func (j *jobReadOffset) callExecute() {
	// Track how long the job takes.
	start := time.Now()
	data, err := j.managedReadOffset()
	jobTime := time.Since(start)

	// Finish the execution.
	j.jobRead.managedFinishExecute(data, err, jobTime)
}

// managedReadOffset returns the sector data for given root.
func (j *jobReadOffset) managedReadOffset() ([]byte, error) {
	// create the program
	w := j.staticQueue.staticWorker()
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt)
	pb.AddReadOffsetInstruction(j.staticLength, j.staticOffset, true)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Read response.
	out, err := j.jobRead.managedRead(w, program, programData, cost)
	if err != nil {
		return nil, errors.AddContext(err, "jobReadOffset: failed to execute managedRead")
	}
	numSectors := int(out.NewSize / modules.SectorSize)

	// Verify proof.
	var ok bool
	secIdx := int(j.staticOffset / modules.SectorSize)
	if j.staticLength == modules.SectorSize {
		root := crypto.MerkleRoot(out.Output)
		ok = crypto.VerifySectorRangeProof([]crypto.Hash{root}, out.Proof, secIdx, secIdx+1, out.NewMerkleRoot)
	} else {
		sectorProofSize := crypto.ProofSize(numSectors, secIdx, secIdx+1)
		if sectorProofSize >= len(out.Proof) {
			return nil, errors.New("returned proof has invalid size")
		}
		sectorProof := out.Proof[:sectorProofSize]
		mixedProof := out.Proof[sectorProofSize:]
		numSegments := out.NewSize / crypto.SegmentSize
		proofStart := int(j.staticOffset) / crypto.SegmentSize
		proofEnd := int(j.staticOffset+j.staticLength) / crypto.SegmentSize
		ok = crypto.VerifyMixedRangeProof(sectorProof, out.Output, mixedProof, out.NewMerkleRoot, numSegments, int(modules.SectorSize), proofStart, proofEnd)
	}
	if !ok {
		return nil, errors.New("verifying proof failed")
	}
	return out.Output, nil
}

// ReadOffset is a helper method to run a ReadOffset job on a worker.
func (w *worker) ReadOffset(ctx context.Context, offset, length uint64) ([]byte, error) {
	readOffsetRespChan := make(chan *jobReadResponse)
	jro := &jobReadOffset{
		jobRead: jobRead{
			staticResponseChan: readOffsetRespChan,
			staticLength:       length,
			jobGeneric: &jobGeneric{
				staticCancelChan: ctx.Done(),

				staticQueue: w.staticJobReadQueue,
			},
		},
		staticOffset: offset,
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
	case resp = <-readOffsetRespChan:
	}
	return resp.staticData, resp.staticErr
}
