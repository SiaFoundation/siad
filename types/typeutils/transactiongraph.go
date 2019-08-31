package typesutil

import (
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// EmptyUnlockHash is the unlock hash of unlock conditions that are
	// trivially spendable.
	EmptyUnlockHash types.UnlockHash = types.UnlockConditions{}.UnlockHash()
)

// TransactionGraph is a helper tool to allow a user to easily construct
// elaborate transaction graphs. The transaction tool will handle creating valid
// transactions, providing the user with a clean interface for building
// transactions.
type TransactionGraph struct {
	siacoinInputs      []types.SiacoinInput
	siacoinInputsUsed  map[int]struct{}
	siacoinInputsValue []types.Currency

	transactions []types.Transaction
}

// SimpleTransaction specifies what outputs it spends, and what outputs it
// creates, by index. When passed in TransactionGraph, it will be automatically
// transformed into a valid transaction.
//
// Currently, there is only support for SiacoinInputs, SiacoinOutputs, and
// MinerFees, however the code has been structured so that support for Siafunds
// and FileContracts can be easily added in the future.
type SimpleTransaction struct {
	SiacoinInputs  []int            // Which inputs to use, by index.
	SiacoinOutputs []types.Currency // The values of each output.

	/*
		SiafundInputs []int // Which inputs to use, by index.
		SiafundOutputs []types.Currency // The values of each output.

		FileContracts int // The number of file contracts to create.
		FileContractRevisions []int // Which file contracts to revise.
		StorageProofs []int // Which file contracts to create proofs for.
	*/

	MinerFees []types.Currency // The fees used.

	/*
		ArbitraryData [][]byte
	*/
}

// AddSiacoinSource will add a new source of siacoins to the transaction graph,
// returning the index that this source can be referenced by. The provided
// output must have the address EmptyUnlockHash.
//
// The value is used as an input so that the graph can check whether all
// transactions are spending as many siacoins as they create.
func (tg *TransactionGraph) AddSiacoinSource(scoid types.SiacoinOutputID, value types.Currency) (int, error) {
	i := len(tg.siacoinInputs)
	tg.siacoinInputs = append(tg.siacoinInputs, types.SiacoinInput{
		ParentID: scoid,
	})
	tg.siacoinInputsValue = append(tg.siacoinInputsValue, value)
	return i, nil
}

// AddTransaction will add a new transaction to the transaction graph, following
// the guide of the input. The indexes of all the outputs created will be
// returned.
func (tg *TransactionGraph) AddTransaction(st SimpleTransaction) (newSiacoinInputs []int, err error) {
	var txn types.Transaction
	var totalIn types.Currency
	var totalOut types.Currency

	// Consume all of the inputs.
	for _, sci := range st.SiacoinInputs {
		_, exists := tg.siacoinInputsUsed[sci]
		if exists {
			return nil, errors.New("cannot use the same input twice in a graph")
		}
		if sci >= len(tg.siacoinInputs) {
			return nil, errors.New("no input of that index exists in the graph")
		}
		txn.SiacoinInputs = append(txn.SiacoinInputs, tg.siacoinInputs[sci])
		totalIn = totalIn.Add(tg.siacoinInputsValue[sci])
	}

	// Create all of the outputs.
	for _, scov := range st.SiacoinOutputs {
		txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
			UnlockHash: EmptyUnlockHash,
			Value:      scov,
		})
		totalOut = totalOut.Add(scov)
	}

	// Add all of the fees.
	txn.MinerFees = st.MinerFees
	for _, fee := range st.MinerFees {
		totalOut = totalOut.Add(fee)
	}

	// Check that the transaction is consistent.
	if totalIn.Cmp(totalOut) != 0 {
		return nil, errors.New("txn inputs and outputs mismatch " + totalIn.String() + " " + totalOut.String())
	}

	// Update the set of siacoin inputs that have been used successfully. This
	// must be done after all error checking is complete.
	for _, sci := range st.SiacoinInputs {
		tg.siacoinInputsUsed[sci] = struct{}{}
	}
	tg.transactions = append(tg.transactions, txn)
	for i, sco := range txn.SiacoinOutputs {
		newSiacoinInputs = append(newSiacoinInputs, len(tg.siacoinInputs))
		tg.siacoinInputs = append(tg.siacoinInputs, types.SiacoinInput{
			ParentID: txn.SiacoinOutputID(uint64(i)),
		})
		tg.siacoinInputsValue = append(tg.siacoinInputsValue, sco.Value)
	}
	return newSiacoinInputs, nil
}

// Transactions will return the transactions that were built up in the graph.
func (tg *TransactionGraph) Transactions() []types.Transaction {
	return tg.transactions
}

// NewTransactionGraph will return a blank transaction graph that is ready for
// use.
func NewTransactionGraph() *TransactionGraph {
	return &TransactionGraph{
		siacoinInputsUsed: make(map[int]struct{}),
	}
}
