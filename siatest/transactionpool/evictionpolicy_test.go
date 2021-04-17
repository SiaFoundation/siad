package transactionpool

import (
	"fmt"
	"testing"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules/transactionpool"
	"go.sia.tech/siad/siatest"
	"go.sia.tech/siad/types"
	"go.sia.tech/siad/types/typesutil"
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
	// minimization is occurring correctly.
	groupParams := siatest.GroupParams{
		Miners: 2,
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
	minerB := tg.Miners()[1]

	// Mine until we are above the foundation hardfork height to prevent
	// transactions from becoming invalid as they cross the hardfork threshold.
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}
	height, err := minerA.BlockHeight()
	if err != nil {
		t.Fatal(err)
	}
	for i := height; i <= types.FoundationHardforkHeight; i++ {
		err = minerB.MineBlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Define checkNumTxns helper function. If we are evicting txns, it is OK to
	// mine additional blocks to avoid NDFs.
	checkNumTxns := func(m *siatest.TestNode, numTxns int, mineBlocks bool) error {
		tries := 1
		return build.Retry(50, 100*time.Millisecond, func() error {
			// If the mineBlocks bool is true, mine an empty block every 10 tries
			if (tries%10 == 0) && mineBlocks {
				err = m.MineEmptyBlock()
				if err != nil {
					return err
				}
			}
			tries++

			// Get the transactions
			tptg, err := m.TransactionPoolTransactionsGet()
			if err != nil {
				return err
			}
			if len(tptg.Transactions) != numTxns {
				return fmt.Errorf("expected %v transactions but got %v", numTxns, len(tptg.Transactions))
			}
			return nil
		})
	}

	// Helper log function
	logTxns := func(m *siatest.TestNode) {
		tptg, err := m.TransactionPoolTransactionsGet()
		if err != nil {
			t.Fatal(err)
		}
		for _, txn := range tptg.Transactions {
			t.Log(txn.ID())
		}
	}

	// We should be starting with 0 transactions
	err = checkNumTxns(minerA, 0, true)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}

	// Create source outputs for transaction graphs.
	sourceSize := types.SiacoinPrecision.Mul64(1e3)
	output := types.SiacoinOutput{
		UnlockHash: typesutil.AnyoneCanSpendUnlockHash,
		Value:      sourceSize,
	}
	wsmp, err := minerA.WalletSiacoinsMultiPost([]types.SiacoinOutput{output})
	if err != nil {
		t.Fatal(err)
	}
	sourceTransactions := wsmp.Transactions
	lastTxn := len(sourceTransactions) - 1
	sources := []types.SiacoinOutputID{wsmp.Transactions[lastTxn].SiacoinOutputID(0)}

	// Confirm that the transaction was received by minerA.
	err = checkNumTxns(minerA, 2, false)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}

	// Mine empty blocks until right before the eviction policy would kick out
	// the transaction. minerD can do the mining.
	for i := types.BlockHeight(0); i < transactionpool.MaxTransactionAge-1; i++ {
		err = minerA.MineEmptyBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Make sure Miners are synced after rapid mining
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Ensure that the transaction is still in minerA's tpool.
	err = checkNumTxns(minerA, 2, false)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}

	// Mine one more empty block, this should cause an eviction of the
	// transactions.
	err = minerA.MineEmptyBlock()
	if err != nil {
		t.Fatal(err)
	}
	err = checkNumTxns(minerA, 0, true)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}

	// Since the transaction got evicted, needs to be submitted again.
	err = minerA.TransactionPoolRawPost(sourceTransactions[lastTxn], sourceTransactions[:lastTxn])
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the transaction was received by minerA.
	err = checkNumTxns(minerA, 2, false)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}

	// Mine empty blocks until right before the eviction policy would kick out
	// the transaction. minerD can do the mining.
	for i := types.BlockHeight(0); i < transactionpool.MaxTransactionAge-1; i++ {
		err = minerA.MineEmptyBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Make sure Miners are synced after rapid mining
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
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
	err = checkNumTxns(minerA, 3, false)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
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

	// Make sure Miners are synced after rapid mining
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// There should still be 3 transactions in the transaction pool for minerA.
	err = checkNumTxns(minerA, 3, false)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}

	// Mine one more empty block, this should cause an eviction of the
	// transactions.
	err = minerA.MineEmptyBlock()
	if err != nil {
		t.Fatal(err)
	}
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the transaction was evicted by minerA.
	err = checkNumTxns(minerA, 0, true)
	if err != nil {
		t.Log("Debug output")
		logTxns(minerA)
		t.Error(err)
	}
}
