package typesutil

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// MinimumTransactionSet takes two transaction sets as input and returns a
// combined transaction set. The first input is the set of required
// transactions, which the caller is indicating must all be a part of the final
// set.The second input is a set of related transactions that the caller
// believes may contain parent transactions of the required transactions.
// MinimumCombinedSet will scan through the related transactions and pull in any
// which are required parents of the required transactions, returning the final
// result.
//
// The final transaction set which gets returned will contain all of the
// required transactions, and will contain any of the related transactions which
// are necessary for the required transactions to be confirmed.
//
// NOTE: Both of the inputs are proper transaction sets. A proper transaction
// set is already sorted so that no parent comes after a child in the array.
func MinimumTransactionSet(requiredTxns []types.Transaction, relatedTxns []types.Transaction) []types.Transaction {
	// objectID is used internally to the MinimumCombinedSet function to
	// identify which transactions create outputs for eachother.
	type objectID [32]byte

	// Track which transactions have already been scanned and added to the final
	// set of required transactions.
	includedTxns := make(map[types.TransactionID]struct{})

	// Determine what the required inputs are for the provided transaction.
	requiredInputs := make(map[objectID]struct{})
	for _, txn := range requiredTxns {
		for _, sci := range txn.SiacoinInputs {
			oid := objectID(sci.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		for _, fcr := range txn.FileContractRevisions {
			oid := objectID(fcr.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		for _, sp := range txn.StorageProofs {
			oid := objectID(sp.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		for _, sfi := range txn.SiafundInputs {
			oid := objectID(sfi.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		includedTxns[txn.ID()] = struct{}{}
	}

	// Create a list of which related transactions create which outputs.
	potentialSources := make(map[objectID]*types.Transaction)
	for i := 0; i < len(relatedTxns); i++ {
		for j := range relatedTxns[i].SiacoinOutputs {
			potentialSources[objectID(relatedTxns[i].SiacoinOutputID(uint64(j)))] = &relatedTxns[i]
		}
		for j := range relatedTxns[i].FileContracts {
			potentialSources[objectID(relatedTxns[i].FileContractID(uint64(j)))] = &relatedTxns[i]
		}
		for j := range relatedTxns[i].SiafundOutputs {
			potentialSources[objectID(relatedTxns[i].SiafundOutputID(uint64(j)))] = &relatedTxns[i]
		}
	}

	// Cycle through all of the required inputs and find the transactions that
	// contain required inputs to the provided transaction. Do so in a loop that
	// will keep checking for more required inputs
	visitedInputs := make(map[objectID]struct{})
	var requiredParents []types.Transaction
	for len(requiredInputs) > 0 {
		newRequiredInputs := make(map[objectID]struct{})
		for ri := range requiredInputs {
			// First check whether we've scanned this input for required parents
			// before. If so, there is no need to scan again. This clause will
			// guarantee eventual termination.
			_, exists := visitedInputs[ri]
			if exists {
				continue
			}
			visitedInputs[ri] = struct{}{}

			// Check if this input is available at all in the potential sources.
			// If not, that means this input may already be confirmed on the
			// blockchain.
			txn, exists := potentialSources[ri]
			if !exists {
				continue
			}

			// Check if this transaction has already been scanned and added as a
			// requirement.
			_, exists = includedTxns[txn.ID()]
			if exists {
				continue
			}

			// If the input does have a source in the list of related
			// transactions, the source also needs to have its inputs checked
			// for any requirements.
			requiredParents = append(requiredParents, *txn)
			for _, sci := range txn.SiacoinInputs {
				oid := objectID(sci.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
			for _, fcr := range txn.FileContractRevisions {
				oid := objectID(fcr.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
			for _, sp := range txn.StorageProofs {
				oid := objectID(sp.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
			for _, sfi := range txn.SiafundInputs {
				oid := objectID(sfi.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
		}

		// All previously required inputs have been visited, but new required
		// inputs may have been picked up. Now need to scan those new required
		// inputs.
		requiredInputs = newRequiredInputs
	}

	// Build the final set. The requiredTxns are already sorted to be in the
	// correct order (per the input requirements) but the required parents were
	// constructed in reverse order, and therefore need to be reversed as they
	// are appended.
	var minSet []types.Transaction
	for i := len(requiredParents) - 1; i >= 0; i-- {
		minSet = append(minSet, requiredParents[i])
	}
	minSet = append(minSet, requiredTxns...)
	return minSet
}
