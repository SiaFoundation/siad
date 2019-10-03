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

// TransactionGraph will take a set of edges as input and use those edges to
// create a transaction graph. The code interprets the edges as going from a
// source node to a sink node. For all nodes, all of the inputs will be part of
// the same transaction. And for all nodes, all of the outputs will be part of
// the same transaction. For nodes that have only outputs (the source node, node
// 0), these nodes will appear in only one transaction. And for nodes that have
// only inputs (the sink nodes), these nodes will also only appear in one
// transaction.
//
// Two nodes will only appear in the same transaction if required to satisfy the
// above requirements, otherwise the nodes will be created using distinct
// transactions.
//
// Node zero will use the 'sourceOutput' as its input, and is the only node that
// is allowed to have no inputs specified.
//
// Example Input:
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
//    o  o
//    |
//    o
//
// This graph will result in 4 transactions:
//
//    t1: [0->1],[0->2]
//    t2: [1->3],[2->3]
//    t3: [3->4],[3->4],[3->5]
//    t4: [4->6]
//
// NOTE: the edges must be specified so that the inputs and outputs of the
// resulting transaction add up correctly.
func TransactionGraph(sourceOutput SiacoinOutputID, edges []TransactionGraphEdge) ([]Transaction, error) {
	// Generating the transaction graph based on a set of edges is non-trivial.
	//
	// Step 1: Generate a map of nodes. Each node records which nodes use it for
	// input, and which nodes use it for output. The map goes from node index to
	// node data.
	//
	// Step 2: Create a list of outputs that need to be added to a transaction.
	// The first element of this list will be node 0, which uses the source
	// output as its input.
	//
	// Step 3: For each node in the list, check whether that node has already
	// been added to a transaction for its outputs. If so, skip that node.
	//
	// Step 4: For the nodes whose outputs do not yet appear in a transaction,
	// create a transaction to house that node. Then follow each output of the
	// node to the inputs of the destination nodes.
	//
	// Step 5: For each input in a destination node, follow that input back to
	// the node that created the output. If that output already appears in a
	// transaction, the graph is invalid and an error must be returned. If that
	// node's outputs do not appear in a transaction yet, that node's inputs
	// need to be checked. If that node's inputs do not appear in a transaction
	// yet, the current transaction has to be put on hold and the transaction
	// for those inputs needs to be created by following the inputs back to
	// their corresponding outputs and starting back at step 2.
	//
	// Step 6: As the transactions are searched, any outputs created by the
	// transaction will need to be added to the list of outputs that haven't had
	// their transactions finished yet to be searched later.
	//
	// Step 7: Once all transaction diagrams are complete, translate into
	// transactions.
	//
	// In short, the algorithm we use is essentially a recursive
	// depth-first-search that builds the correct transaction graph, and then
	// the transactions are processed in an order that allows us to create all
	// of their IDs.

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
