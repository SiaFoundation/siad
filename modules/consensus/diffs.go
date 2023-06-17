package consensus

import (
	"errors"

	"gitlab.com/NebulousLabs/bolt"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	errApplySiafundPoolDiffMismatch  = errors.New("committing a siafund pool diff with an invalid 'previous' field")
	errDiffsNotGenerated             = errors.New("applying diff set before generating errors")
	errInvalidSuccessor              = errors.New("generating diffs for a block that's an invalid successsor to the current block")
	errNegativePoolAdjustment        = errors.New("committing a siafund pool diff with a negative adjustment")
	errNonApplySiafundPoolDiff       = errors.New("committing a siafund pool diff that doesn't have the 'apply' direction")
	errRevertSiafundPoolDiffMismatch = errors.New("committing a siafund pool diff with an invalid 'adjusted' field")
	errWrongAppliedDiffSet           = errors.New("applying a diff set that isn't the current block")
	errWrongRevertDiffSet            = errors.New("reverting a diff set that isn't the current block")
)

// commitDiffSetSanity performs a series of sanity checks before committing a
// diff set.
func (cs *ConsensusSet) commitDiffSetSanity(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	// This function is purely sanity checks.
	if !build.DEBUG {
		return
	}

	// Diffs should have already been generated for this node.
	if !pb.DiffsGenerated {
		panic(errDiffsNotGenerated)
	}

	// Current node must be the input node's parent if applying, and
	// current node must be the input node if reverting.
	if dir == modules.DiffApply {
		parent, err := cs.getBlockMap(tx, pb.Block.ParentID)
		if build.DEBUG && err != nil {
			panic(err)
		}
		if cs.blockID(parent.Block) != currentBlockID(tx) {
			panic(errWrongAppliedDiffSet)
		}
	} else {
		if cs.blockID(pb.Block) != currentBlockID(tx) {
			panic(errWrongRevertDiffSet)
		}
	}
}

// commitSiacoinOutputDiff applies or reverts a SiacoinOutputDiff.
func (cs *ConsensusSet) commitSiacoinOutputDiff(tx *bolt.Tx, scod modules.SiacoinOutputDiff, dir modules.DiffDirection) {
	if scod.Direction == dir {
		cs.addSiacoinOutput(tx, scod.ID, scod.SiacoinOutput)
	} else {
		cs.removeSiacoinOutput(tx, scod.ID)
	}
}

// commitFileContractDiff applies or reverts a FileContractDiff.
func (cs *ConsensusSet) commitFileContractDiff(tx *bolt.Tx, fcd modules.FileContractDiff, dir modules.DiffDirection) {
	if fcd.Direction == dir {
		cs.addFileContract(tx, fcd.ID, fcd.FileContract)
	} else {
		cs.removeFileContract(tx, fcd.ID)
	}
}

// commitSiafundOutputDiff applies or reverts a Siafund output diff.
func commitSiafundOutputDiff(tx *bolt.Tx, sfod modules.SiafundOutputDiff, dir modules.DiffDirection) {
	if sfod.Direction == dir {
		addSiafundOutput(tx, sfod.ID, sfod.SiafundOutput)
	} else {
		removeSiafundOutput(tx, sfod.ID)
	}
}

// commitDelayedSiacoinOutputDiff applies or reverts a delayedSiacoinOutputDiff.
func commitDelayedSiacoinOutputDiff(tx *bolt.Tx, dscod modules.DelayedSiacoinOutputDiff, dir modules.DiffDirection) {
	if dscod.Direction == dir {
		addDSCO(tx, dscod.MaturityHeight, dscod.ID, dscod.SiacoinOutput)
	} else {
		removeDSCO(tx, dscod.MaturityHeight, dscod.ID)
	}
}

// commitSiafundPoolDiff applies or reverts a SiafundPoolDiff.
func commitSiafundPoolDiff(tx *bolt.Tx, sfpd modules.SiafundPoolDiff, dir modules.DiffDirection) {
	// Sanity check - siafund pool should only ever increase.
	if build.DEBUG {
		if sfpd.Adjusted.Cmp(sfpd.Previous) < 0 {
			panic(errNegativePoolAdjustment)
		}
		if sfpd.Direction != modules.DiffApply {
			panic(errNonApplySiafundPoolDiff)
		}
	}

	if dir == modules.DiffApply {
		// Sanity check - sfpd.Previous should equal the current siafund pool.
		if build.DEBUG && !getSiafundPool(tx).Equals(sfpd.Previous) {
			panic(errApplySiafundPoolDiffMismatch)
		}
		setSiafundPool(tx, sfpd.Adjusted)
	} else {
		// Sanity check - sfpd.Adjusted should equal the current siafund pool.
		if build.DEBUG && !getSiafundPool(tx).Equals(sfpd.Adjusted) {
			panic(errRevertSiafundPoolDiffMismatch)
		}
		setSiafundPool(tx, sfpd.Previous)
	}
}

// createUpcomingDelayeOutputdMaps creates the delayed siacoin output maps that
// will be used when applying delayed siacoin outputs in the diff set.
func createUpcomingDelayedOutputMaps(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	if dir == modules.DiffApply {
		createDSCOBucket(tx, pb.Height+types.MaturityDelay)
	} else if pb.Height >= types.MaturityDelay {
		createDSCOBucket(tx, pb.Height)
	}
}

// commitNodeDiffs commits all of the diffs in a block node.
func (cs *ConsensusSet) commitNodeDiffs(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	if dir == modules.DiffApply {
		for _, scod := range pb.SiacoinOutputDiffs {
			cs.commitSiacoinOutputDiff(tx, scod, dir)
		}
		for _, fcd := range pb.FileContractDiffs {
			cs.commitFileContractDiff(tx, fcd, dir)
		}
		for _, sfod := range pb.SiafundOutputDiffs {
			commitSiafundOutputDiff(tx, sfod, dir)
		}
		for _, dscod := range pb.DelayedSiacoinOutputDiffs {
			commitDelayedSiacoinOutputDiff(tx, dscod, dir)
		}
		for _, sfpd := range pb.SiafundPoolDiffs {
			commitSiafundPoolDiff(tx, sfpd, dir)
		}
	} else {
		for i := len(pb.SiacoinOutputDiffs) - 1; i >= 0; i-- {
			cs.commitSiacoinOutputDiff(tx, pb.SiacoinOutputDiffs[i], dir)
		}
		for i := len(pb.FileContractDiffs) - 1; i >= 0; i-- {
			cs.commitFileContractDiff(tx, pb.FileContractDiffs[i], dir)
		}
		for i := len(pb.SiafundOutputDiffs) - 1; i >= 0; i-- {
			commitSiafundOutputDiff(tx, pb.SiafundOutputDiffs[i], dir)
		}
		for i := len(pb.DelayedSiacoinOutputDiffs) - 1; i >= 0; i-- {
			commitDelayedSiacoinOutputDiff(tx, pb.DelayedSiacoinOutputDiffs[i], dir)
		}
		for i := len(pb.SiafundPoolDiffs) - 1; i >= 0; i-- {
			commitSiafundPoolDiff(tx, pb.SiafundPoolDiffs[i], dir)
		}
	}
}

// deleteObsoleteDelayedOutputMaps deletes the delayed siacoin output maps that
// are no longer in use.
func deleteObsoleteDelayedOutputMaps(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	// There are no outputs that mature in the first MaturityDelay blocks.
	if dir == modules.DiffApply && pb.Height >= types.MaturityDelay {
		deleteDSCOBucket(tx, pb.Height)
	} else if dir == modules.DiffRevert {
		deleteDSCOBucket(tx, pb.Height+types.MaturityDelay)
	}
}

// updateCurrentPath updates the current path after applying a diff set.
func (cs *ConsensusSet) updateCurrentPath(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	// Update the current path.
	if dir == modules.DiffApply {
		pushPath(tx, cs.blockID(pb.Block))
	} else {
		popPath(tx)
	}
}

// commitFoundationUpdate updates the current Foundation unlock hashes in
// accordance with the specified block and direction.
//
// Because these updates do not have associated diffs, we cannot apply multiple
// updates per block. Instead, we apply the first update and ignore the rest.
func (cs *ConsensusSet) commitFoundationUpdate(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	if dir == modules.DiffApply {
		for i := range pb.Block.Transactions {
			cs.applyArbitraryData(tx, pb, pb.Block.Transactions[i])
		}
	} else {
		// Look for a set of prior unlock hashes for this height.
		primary, failsafe, exists := getPriorFoundationUnlockHashes(tx, pb.Height)
		if exists {
			setFoundationUnlockHashes(tx, primary, failsafe)
			deletePriorFoundationUnlockHashes(tx, pb.Height)
			cs.transferFoundationOutputs(tx, pb.Height, primary)
		}
	}
}

// commitDiffSet applies or reverts the diffs in a blockNode.
func (cs *ConsensusSet) commitDiffSet(tx *bolt.Tx, pb *processedBlock, dir modules.DiffDirection) {
	// Sanity checks - there are a few so they were moved to another function.
	if build.DEBUG {
		cs.commitDiffSetSanity(tx, pb, dir)
	}

	createUpcomingDelayedOutputMaps(tx, pb, dir)
	cs.commitNodeDiffs(tx, pb, dir)
	deleteObsoleteDelayedOutputMaps(tx, pb, dir)
	cs.commitFoundationUpdate(tx, pb, dir)
	cs.updateCurrentPath(tx, pb, dir)
}

// generateAndApplyDiff will verify the block and then integrate it into the
// consensus state. These two actions must happen at the same time because
// transactions are allowed to depend on each other. We can't be sure that a
// transaction is valid unless we have applied all of the previous transactions
// in the block, which means we need to apply while we verify.
func (cs *ConsensusSet) generateAndApplyDiff(tx *bolt.Tx, pb *processedBlock) error {
	// Sanity check - the block being applied should have the current block as
	// a parent.
	if build.DEBUG && pb.Block.ParentID != currentBlockID(tx) {
		panic(errInvalidSuccessor)
	}

	// Create the bucket to hold all of the delayed siacoin outputs created by
	// transactions this block. Needs to happen before any transactions are
	// applied.
	createDSCOBucket(tx, pb.Height+types.MaturityDelay)

	// Validate and apply each transaction in the block. They cannot be
	// validated all at once because some transactions may not be valid until
	// previous transactions have been applied.
	for _, txn := range pb.Block.Transactions {
		err := cs.validTransaction(tx, txn)
		if err != nil {
			return err
		}
		cs.applyTransaction(tx, pb, txn)
	}

	// After all of the transactions have been applied, 'maintenance' is
	// applied on the block. This includes adding any outputs that have reached
	// maturity, applying any contracts with missed storage proofs, and adding
	// the miner payouts and Foundation subsidy to the list of delayed outputs.
	cs.applyMaintenance(tx, pb)

	// DiffsGenerated are only set to true after the block has been fully
	// validated and integrated. This is required to prevent later blocks from
	// being accepted on top of an invalid block - if the consensus set ever
	// forks over an invalid block, 'DiffsGenerated' will be set to 'false',
	// requiring validation to occur again. when 'DiffsGenerated' is set to
	// true, validation is skipped, therefore the flag should only be set to
	// true on fully validated blocks.
	pb.DiffsGenerated = true

	// Add the block to the current path and block map.
	bid := cs.blockID(pb.Block)
	blockMap := tx.Bucket(BlockMap)
	cs.updateCurrentPath(tx, pb, modules.DiffApply)

	// Sanity check preparation - set the consensus hash at this height so that
	// during reverting a check can be performed to assure consistency when
	// adding and removing blocks. Must happen after the block is added to the
	// path.
	if build.DEBUG {
		pb.ConsensusChecksum = consensusChecksum(tx)
	}

	return blockMap.Put(bid[:], encoding.Marshal(*pb))
}
