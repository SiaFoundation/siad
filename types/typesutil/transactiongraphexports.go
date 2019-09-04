package typesutil

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// EmptyUnlockHash is the unlock hash of unlock conditions that are
	// trivially spendable.
	EmptyUnlockHash types.UnlockHash = types.UnlockConditions{}.UnlockHash()
)

var (
	// ErrSiacoinSourceAlreadyAdded is the error returned when a user tries to
	// provide the same source siacoin input multiple times.
	ErrSiacoinSourceAlreadyAdded = errors.New("source siacoin input has already been used")

	// ErrSiacoinInputAlreadyUsed warns a user that a siacoin input has already
	// been used in the transaction graph.
	ErrSiacoinInputAlreadyUsed = errors.New("cannot use the same siacoin input twice in a graph")

	// ErrNoSuchSiacoinInput warns a user that they are trying to reference a
	// siacoin input which does not yet exist.
	ErrNoSuchSiacoinInput = errors.New("no siacoin input exists with that index")

	// ErrSiacoinInputsOutputsMismatch warns a user that they have constructed a
	// transaction which does not spend the same amount of siacoins that it
	// consumes.
	ErrSiacoinInputsOutputsMismatch = errors.New("siacoin input value to transaction does not match siacoin output value of transaction")
)

// siacoinInput defines a siacoin input within the transaction graph, containing
// the input itself, the value of the input, and a flag indicating whether or
// not the input has been used within the graph already.
type siacoinInput struct {
	input types.SiacoinInput
	used  bool
	value types.Currency
}

// TransactionGraph is a helper tool to allow a user to easily construct
// elaborate transaction graphs. The transaction tool will handle creating valid
// transactions, providing the user with a clean interface for building
// transactions.
type TransactionGraph struct {
	// A map that tracks which source inputs have been consumed, to double check
	// that the user is not supplying the same source inputs multiple times.
	usedSiacoinInputSources map[types.SiacoinOutputID]struct{}

	siacoinInputs []siacoinInput

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
		SiafundInputs  []int            // Which inputs to use, by index.
		SiafundOutputs []types.Currency // The values of each output.

		FileContracts         int   // The number of file contracts to create.
		FileContractRevisions []int // Which file contracts to revise.
		StorageProofs         []int // Which file contracts to create proofs for.
	*/

	MinerFees []types.Currency // The fees used.

	/*
		ArbitraryData [][]byte // Arbitrary data to include in the transaction.
	*/
}

// AddSiacoinSource will add a new source of siacoins to the transaction graph,
// returning the index that this source can be referenced by. The provided
// output must have the address EmptyUnlockHash.
//
// The value is used as an input so that the graph can check whether all
// transactions are spending as many siacoins as they create.
func (tg *TransactionGraph) AddSiacoinSource(scoid types.SiacoinOutputID, value types.Currency) (int, error) {
	// Check if this scoid has already been used.
	_, exists := tg.usedSiacoinInputSources[scoid]
	if exists {
		return -1, ErrSiacoinSourceAlreadyAdded
	}

	i := len(tg.siacoinInputs)
	tg.siacoinInputs = append(tg.siacoinInputs, siacoinInput{
		input: types.SiacoinInput{
			ParentID: scoid,
		},
		value: value,
	})
	tg.usedSiacoinInputSources[scoid] = struct{}{}
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
		if sci >= len(tg.siacoinInputs) {
			return nil, ErrNoSuchSiacoinInput
		}
		if tg.siacoinInputs[sci].used {
			return nil, ErrSiacoinInputAlreadyUsed
		}
		txn.SiacoinInputs = append(txn.SiacoinInputs, tg.siacoinInputs[sci].input)
		totalIn = totalIn.Add(tg.siacoinInputs[sci].value)
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
		valuesErr := fmt.Errorf("total input: %s, total output: %s", totalIn, totalOut)
		extendedErr := errors.Extend(ErrSiacoinInputsOutputsMismatch, valuesErr)
		return nil, extendedErr
	}

	// Update the set of siacoin inputs that have been used successfully. This
	// must be done after all error checking is complete.
	for _, sci := range st.SiacoinInputs {
		tg.siacoinInputs[sci].used = true
	}
	tg.transactions = append(tg.transactions, txn)
	for i, sco := range txn.SiacoinOutputs {
		newSiacoinInputs = append(newSiacoinInputs, len(tg.siacoinInputs))
		tg.siacoinInputs = append(tg.siacoinInputs, siacoinInput{
			input: types.SiacoinInput{
				ParentID: txn.SiacoinOutputID(uint64(i)),
			},
			value: sco.Value,
		})
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
		usedSiacoinInputSources: make(map[types.SiacoinOutputID]struct{}),
	}
}
