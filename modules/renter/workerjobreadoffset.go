package renter

import (
	"context"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
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
	bh := w.staticCache().staticBlockHeight
	pt := w.staticPriceTable().staticPriceTable
	pb := modules.NewProgramBuilder(&pt, 0) // 0 duration since Read doesn't depend on it.
	pb.AddRevisionInstruction()
	pb.AddReadOffsetInstruction(j.staticLength, j.staticOffset, true)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	ulBandwidth, dlBandwidth := j.callExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulBandwidth, dlBandwidth)
	cost = cost.Add(bandwidthCost)

	// Read responses.
	responses, err := j.jobRead.managedRead(w, program, programData, cost)
	if err != nil {
		return nil, errors.AddContext(err, "jobReadOffset: failed to execute managedRead")
	}
	revResponse := responses[0]
	downloadResponse := responses[1]

	// Fetch the contract's public key from disk. Due to concurrency we might
	// not know which revision the host used, but since the public key is the
	// same for all revisions, we can at least verify that it's a revision we
	// agreed upon at some point.
	cpk, ok := w.renter.hostContractor.ContractPublicKey(w.staticHostPubKey)
	if !ok {
		return nil, errors.New("jobReadOffset: failed to get public key for contract")
	}

	// Unmarshal the revision response.
	var revResp modules.MDMInstructionRevisionResponse
	err = encoding.Unmarshal(revResponse.Output, &revResp)
	if err != nil {
		return nil, errors.AddContext(err, "jobReadOffset: failed to unmarshal revision")
	}
	// Check that the revision txn contains the right number of signatures
	revisionTxn := revResp.RevisionTxn
	if len(revisionTxn.TransactionSignatures) != 2 {
		return nil, errors.New("jobReadOffset: invalid number of signatures on txn")
	}
	// Check that the revision txn contains the right number of revisions.
	if len(revisionTxn.FileContractRevisions) != 1 {
		return nil, errors.New("jobReadOffset: invalid number of revisions in txn")
	}
	rev := revResp.RevisionTxn.FileContractRevisions[0]
	// Verify the signatures.
	var signature crypto.Signature
	copy(signature[:], revisionTxn.RenterSignature().Signature)
	hash := revisionTxn.SigHash(0, bh) // this should be the start height but this works too
	err = crypto.VerifyHash(hash, cpk, signature)
	if err != nil {
		return nil, errors.AddContext(err, "jobReadOffset: failed to verify signature on revision")
	}
	// Verify proof.
	proofStart := int(j.staticOffset) / crypto.SegmentSize
	proofEnd := int(j.staticOffset+j.staticLength) / crypto.SegmentSize
	ok = crypto.VerifyMixedRangeProof(downloadResponse.Output, downloadResponse.Proof, rev.NewFileMerkleRoot, proofStart, proofEnd)
	if !ok {
		return nil, errors.New("verifying proof failed")
	}
	return downloadResponse.Output, nil
}

// ReadOffset is a helper method to run a ReadOffset job on a worker.
func (w *worker) ReadOffset(ctx context.Context, category spendingCategory, offset, length uint64) ([]byte, error) {
	readOffsetRespChan := make(chan *jobReadResponse)
	jro := &jobReadOffset{
		jobRead: jobRead{
			staticResponseChan: readOffsetRespChan,
			staticLength:       length,

			jobGeneric: newJobGeneric(ctx, w.staticJobReadQueue, jobReadMetadata{
				staticSpendingCategory: category,
				staticWorker:           w,
			}),
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
