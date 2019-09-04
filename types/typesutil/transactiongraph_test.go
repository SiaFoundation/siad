package typesutil

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

// TestTransactionGraph will check that the basic construction of a transaction
// graph works as expected.
func TestTransactionGraph(t *testing.T) {
	// Make a basic transaction.
	var source types.SiacoinOutputID
	tg := NewTransactionGraph()
	index, err := tg.AddSiacoinSource(source, types.SiacoinPrecision.Mul64(3))
	if err != nil {
		t.Fatal(err)
	}
	_, err = tg.AddSiacoinSource(source, types.SiacoinPrecision.Mul64(3))
	if !errors.Contains(err, ErrSiacoinSourceAlreadyAdded) {
		t.Fatal("should not be able to add the same siacoin input source multiple times")
	}
	newIndexes, err := tg.AddTransaction(SimpleTransaction{
		SiacoinInputs:  []int{index},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision.Mul64(2)},
		MinerFees:      []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	txns := tg.Transactions()
	if len(txns) != 1 {
		t.Fatal("expected to get one transaction")
	}
	// Check that the transaction is standalone valid.
	err = txns[0].StandaloneValid(0)
	if err != nil {
		t.Fatal("transactions produced by graph should be valid")
	}

	// Try to build a transaction that has a value mismatch, ensure there is an
	// error.
	_, err = tg.AddTransaction(SimpleTransaction{
		SiacoinInputs:  []int{newIndexes[0]},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision.Mul64(2)},
		MinerFees:      []types.Currency{types.SiacoinPrecision},
	})
	if !errors.Contains(err, ErrSiacoinInputsOutputsMismatch) {
		t.Fatal("An error should be returned when a transaction's outputs and inputs mismatch")
	}
	_, err = tg.AddTransaction(SimpleTransaction{
		SiacoinInputs:  []int{2},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision},
		MinerFees:      []types.Currency{types.SiacoinPrecision},
	})
	if !errors.Contains(err, ErrNoSuchSiacoinInput) {
		t.Fatal("An error should be returned when a transaction spends a missing input")
	}
	_, err = tg.AddTransaction(SimpleTransaction{
		SiacoinInputs:  []int{0},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision},
		MinerFees:      []types.Currency{types.SiacoinPrecision},
	})
	if !errors.Contains(err, ErrSiacoinInputAlreadyUsed) {
		t.Fatal("Error should be returned when a transaction spends an input that has been spent before")
	}

	// Build a correct second transaction, see that it validates.
	_, err = tg.AddTransaction(SimpleTransaction{
		SiacoinInputs:  []int{newIndexes[0]},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision},
		MinerFees:      []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal("Transaction was built incorrectly", err)
	}
}
