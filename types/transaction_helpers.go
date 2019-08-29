package types

import (
	"errors"
)

// TransactionGraphEdge defines an edge in a TransactionGraph, containing a
// source transaction, a destination transaction, a value, and a miner fee.
type TransactionGraphEdge struct {
	Dest   int
	Fee    Currency
	Source int
	Value  Currency
}

// TransactionGraph will return a set of valid transactions that all spend
// outputs according to the input graph. Each [source, dest] pair defines an
// edge of the graph. The graph must be fully connected and the granparent of
// the graph must be the sourceOutput. '0' refers to an edge from the source
// output. Each edge also specifies a value for the output, and an amount of
// fees. If the fees are zero, no fees will be added for that edge. 'sources'
// must be sorted.
//
// Example of acceptable input:
//
// sourceOutput: // a valid siacoin output spending to UnlockConditions{}.UnlockHash()
//
// Sources: [0, 0, 1, 2, 3, 3, 3, 4]
// Dests:   [1, 2, 3, 3, 4, 4, 5, 6]
//
// Resulting Graph:
//
//    o
//   / \
//  o   o
//   \ /
//    o
//   /|\
//   \| \
//    o  x // 'x' transactions are symbolic, not actually created
//    |
//    x
//
func TransactionGraph(sourceOutput SiacoinOutputID, edges []TransactionGraphEdge) ([]Transaction, error) {
	// Basic input validation.
	if len(edges) < 1 {
		return nil, errors.New("no graph specificed")
	}

	// Check that the first value of 'sources' is zero, and that the rest of the
	// array is sorted.
	if edges[0].Source != 0 {
		return nil, errors.New("first edge must speficy node 0 as the parent")
	}
	if edges[0].Dest != 1 {
		return nil, errors.New("first edge must speficy node 1 as the child")
	}
	latest := edges[0].Source
	for _, edge := range edges {
		if edge.Source < latest {
			return nil, errors.New("'sources' input is not sorted")
		}
		latest = edge.Source
	}

	// Create the set of output ids, and fill out the input ids for the source
	// transaction.
	biggest := 0
	for _, edge := range edges {
		if edge.Dest > biggest {
			biggest = edge.Dest
		}
	}
	txnInputs := make([][]SiacoinOutputID, biggest+1)
	txnInputs[0] = []SiacoinOutputID{sourceOutput}

	// Go through the nodes bit by bit and create outputs.
	// Fill out the outputs for the source.
	i, j := 0, 0
	ts := make([]Transaction, edges[len(edges)-1].Source+1)
	for i < len(edges) {
		var t Transaction

		// Grab the inputs for this transaction.
		for _, outputID := range txnInputs[j] {
			t.SiacoinInputs = append(t.SiacoinInputs, SiacoinInput{
				ParentID: outputID,
			})
		}

		// Grab the outputs for this transaction.
		startingPoint := i
		current := edges[i].Source
		for i < len(edges) && edges[i].Source == current {
			t.SiacoinOutputs = append(t.SiacoinOutputs, SiacoinOutput{
				Value:      edges[i].Value,
				UnlockHash: UnlockConditions{}.UnlockHash(),
			})
			if !edges[i].Fee.IsZero() {
				t.MinerFees = append(t.MinerFees, edges[i].Fee)
			}
			i++
		}

		// Record the inputs for the next transactions.
		for k := startingPoint; k < i; k++ {
			txnInputs[edges[k].Dest] = append(txnInputs[edges[k].Dest], t.SiacoinOutputID(uint64(k-startingPoint)))
		}
		ts[j] = t
		j++
	}

	return ts, nil
}

type objectID [32]byte

// MinimumCombinedSet takes two transaction sets as input and returns a combined
// transaction set. The first input is the set of required transactions, which
// the caller is indicating must all be a part of the final set. The second
// input is a set of related transactions that the caller believes may contain
// parent transactions of the required transactions. MinimumCombinedSet will
// scan through the related transactions and pull in any which are required
// parents of the required transactions, returning the final result.
//
// The final transaction set which gets returned will contain all of the
// required transactions, and will contain any of the related transactions which
// are necessary for the required transactions to be confirmed.
//
// NOTE: Both of the inputs are proper transaction sets. A proper transaction
// set is already sorted so that no parent comes after a child in the array.
func MinimumCombinedSet(requiredTxns []Transaction, relatedTxns []Transaction) []Transaction {
	// Track which transactions have already been scanned and added to the final
	// set of required transactions.
	includedTxns := make(map[TransactionID]struct{})

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
	potentialSources := make(map[objectID]*Transaction)
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
	var requiredParents []Transaction
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

			// Check if this transcation has already been scanned and added as a
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
	for i := len(requiredParents) - 1; i >= 0; i-- {
		requiredTxns = append(requiredTxns, requiredParents[i])
	}
	return requiredTxns
}
