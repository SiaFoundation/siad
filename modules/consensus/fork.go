package consensus

import (
	"errors"

	"gitlab.com/NebulousLabs/bolt"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
)

var (
	errExternalRevert = errors.New("cannot revert to block outside of current path")
)

// backtrackToCurrentPath traces backwards from 'pb' until it reaches a block
// in the ConsensusSet's current path (the "common parent"). It returns the
// (inclusive) set of blocks between the common parent and 'pb', starting from
// the former.
func backtrackToCurrentPath(tx *bolt.Tx, pb *processedBlock) []*processedBlock {
	path := []*processedBlock{pb}
	for {
		// Error is not checked in production code - an error can only indicate
		// that pb.Height > blockHeight(tx).
		currentPathID, err := getPath(tx, pb.Height)
		if currentPathID == pb.Block.ID() {
			break
		}
		// Sanity check - an error should only indicate that pb.Height >
		// blockHeight(tx).
		if build.DEBUG && err != nil && pb.Height <= blockHeight(tx) {
			panic(err)
		}

		// Prepend the next block to the list of blocks leading from the
		// current path to the input block.
		pb, err = getBlockMap(tx, pb.Block.ParentID)
		if build.DEBUG && err != nil {
			panic(err)
		}
		path = append([]*processedBlock{pb}, path...)
	}
	return path
}

// revertToBlock will revert blocks from the ConsensusSet's current path until
// 'pb' is the current block. Blocks are returned in the order that they were
// reverted.  'pb' is not reverted.
func (cs *ConsensusSet) revertToBlock(tx *bolt.Tx, pb *processedBlock) (revertedBlocks []*processedBlock) {
	// Sanity check - make sure that pb is in the current path.
	currentPathID, err := getPath(tx, pb.Height)
	if err != nil || currentPathID != pb.Block.ID() {
		if build.DEBUG {
			panic(errExternalRevert) // needs to be panic for TestRevertToNode
		} else {
			build.Critical(errExternalRevert)
		}
	}

	// Rewind blocks until 'pb' is the current block.
	for currentBlockID(tx) != pb.Block.ID() {
		block := currentProcessedBlock(tx)
		commitDiffSet(tx, block, modules.DiffRevert)
		revertedBlocks = append(revertedBlocks, block)

		// Sanity check - after removing a block, check that the consensus set
		// has maintained consistency.
		if build.Release == "testing" {
			cs.checkConsistency(tx)
		} else {
			cs.maybeCheckConsistency(tx)
		}
	}
	return revertedBlocks
}

// applyUntilBlock will successively apply the blocks between the consensus
// set's current path and 'pb'.
func (cs *ConsensusSet) applyUntilBlock(tx *bolt.Tx, pb *processedBlock) (appliedBlocks []*processedBlock, err error) {
	// Backtrack to the common parent of 'bn' and current path and then apply the new blocks.
	newPath := backtrackToCurrentPath(tx, pb)
	for _, block := range newPath[1:] {
		// If the diffs for this block have already been generated, apply diffs
		// directly instead of generating them. This is much faster.
		if block.DiffsGenerated {
			commitDiffSet(tx, block, modules.DiffApply)
		} else {
			// compute core state+diff first, since generateAndApplyDiff mutates the db
			coreVC := coreValidationContext(tx)
			coreBlock := coreConvertBlock(pb.Block)
			coreErr := coreVC.ValidateBlock(coreBlock)
			coreState, coreDiff := coreApplyBlock(coreVC, coreBlock)

			err := generateAndApplyDiff(tx, block)

			if (err == nil) != (coreErr == nil) {
				if err == nil {
					cs.log.Println("WARN: block passed in siad but failed in core:", coreErr)
				} else {
					cs.log.Println("WARN: block failed in siad but passed in core:", err)
				}
			} else {
				siadState := coreValidationContext(tx).State
				siadDiff := computeConsensusChangeDiffs(block, true)
				if coreState != siadState {
					cs.log.Println("WARN: state mismatch")
				}
				if !coreEqualDiff(coreConvertDiff(coreDiff, pb.Height), siadDiff) {
					cs.log.Println("WARN: block diff mismatch in siad and core")
				}
			}

			if err != nil {
				// Mark the block as invalid.
				cs.dosBlocks[block.Block.ID()] = struct{}{}
				return nil, err
			}
		}
		appliedBlocks = append(appliedBlocks, block)

		// Sanity check - after applying a block, check that the consensus set
		// has maintained consistency.
		if build.Release == "testing" {
			cs.checkConsistency(tx)
		} else {
			cs.maybeCheckConsistency(tx)
		}
	}
	return appliedBlocks, nil
}

// forkBlockchain will move the consensus set onto the 'newBlock' fork. An
// error will be returned if any of the blocks applied in the transition are
// found to be invalid. forkBlockchain is atomic; the ConsensusSet is only
// updated if the function returns nil.
func (cs *ConsensusSet) forkBlockchain(tx *bolt.Tx, newBlock *processedBlock) (revertedBlocks, appliedBlocks []*processedBlock, err error) {
	commonParent := backtrackToCurrentPath(tx, newBlock)[0]
	revertedBlocks = cs.revertToBlock(tx, commonParent)
	appliedBlocks, err = cs.applyUntilBlock(tx, newBlock)
	if err != nil {
		return nil, nil, err
	}
	return revertedBlocks, appliedBlocks, nil
}
