package transactionpool

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/Sia/types/typesutil"
)

// TestEvictionPolicy will test that the transaction set minimizer in the
// typesutil package is properly minimizing transaction sets and that those
// minimized sets can be put onto the blockchain and then propagated
// accordingly.
func TestEvictionPolicy(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test group with two miners. The miners will be sending
	// transactions to eachother in a way that ensures transaction set
	// minimization is occuring correctly.
	groupParams := siatest.GroupParams{
		Miners: 1,
	}
	testDir := tpoolTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	minerA := tg.Miners()[0]

	// Create source outputs for transaction graphs.
	var sources []types.SiacoinOutputID
	numSources := 1
	sourceSize := types.SiacoinPrecision.Mul64(1e3)
	var outputs []types.SiacoinOutput
	for i := 0; i < numSources; i++ {
		outputs = append(outputs, types.SiacoinOutput{
			UnlockHash: typesutil.AnyoneCanSpendUnlockHash,
			Value:      sourceSize,
		})
	}
	wsmp, err := minerA.WalletSiacoinsMultiPost(outputs)
	if err != nil {
		t.Fatal(err)
	}
	sourceTransactions := wsmp.Transactions
	lastTxn := len(sourceTransactions) - 1
	for i := 0; i < numSources; i++ {
		sources = append(sources, wsmp.Transactions[lastTxn].SiacoinOutputID(uint64(i)))
	}

	// Confirm that the transaction was received by minerA.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err := minerA.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 2 {
			return fmt.Errorf("expected 2 transactions but got %v", len(tptg.Transactions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine empty blocks until right before the eviction policy would kick out
	// the transaction. minerD can do the mining.
	for i := types.BlockHeight(0); i < transactionpool.MaxTransactionAge-1; i++ {
		err = minerA.MineEmptyBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Ensure that the transaction is still in minerA's tpool.
	tptg, err := minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 2 {
		t.Fatal(fmt.Errorf("expected 2 transactions but got %v", len(tptg.Transactions)))
	}

	// Mine one more empty block, this should cause an eviction of the
	// transactions.
	err = minerA.MineEmptyBlock()
	if err != nil {
		t.Fatal(err)
	}
	tptg, err = minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 0 {
		t.Fatal(fmt.Errorf("expected 0 transactions but got %v", len(tptg.Transactions)))
	}

	// Since the transaction got evicted, needs to be submitted again.
	err = minerA.TransactionPoolRawPost(sourceTransactions[lastTxn], sourceTransactions[:lastTxn])
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that the transaction was received by minerA.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err := minerA.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 2 {
			return fmt.Errorf("expected 2 transactions but got %v", len(tptg.Transactions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine empty blocks until right before the eviction policy would kick out
	// the transaction. minerD can do the mining.
	for i := types.BlockHeight(0); i < transactionpool.MaxTransactionAge-1; i++ {
		err = minerA.MineEmptyBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Use the source output to create a transaction. Submit the transaction to
	// minerA, this should prevent the prereq transactions from being evicted.
	graph1 := typesutil.NewTransactionGraph()
	source1Index, err := graph1.AddSiacoinSource(sources[0], sourceSize)
	if err != nil {
		t.Fatal(err)
	}
	// Add txn1, which consumes src1 and produces out1
	_, err = graph1.AddTransaction(typesutil.SimpleTransaction{
		SiacoinInputs:  []int{source1Index},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision.Mul64(999)},

		MinerFees: []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph1Txns := graph1.Transactions()
	// Give the transactions from graph1 to minerA.
	err = minerA.TransactionPoolRawPost(graph1Txns[0], graph1Txns[:0])
	if err != nil {
		t.Fatal(err)
	}
	// There should now be 3 transactions in the transaction pool for minerA.
	tptg, err = minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 3 {
		t.Fatal("expecting 3 transactions after mining block, got", len(tptg.Transactions))
	}

	// Mine empty blocks until right before the eviction policy would kick out
	// the new transaction. There should still be all 3 transactions in the
	// transaction pool.
	for i := types.BlockHeight(0); i < transactionpool.MaxTransactionAge-1; i++ {
		err = minerA.MineEmptyBlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	// There should still be 3 transactions in the transaction pool for minerA.
	tptg, err = minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 3 {
		t.Fatal("expecting 3 transactions after mining block, got", len(tptg.Transactions))
	}

	// Mine one more empty block, this should cause an eviction of the
	// transactions.
	err = minerA.MineEmptyBlock()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that the transaction was received by minerA.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err = minerA.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 0 {
			return fmt.Errorf("expected 0 transactions but got %v", len(tptg.Transactions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
