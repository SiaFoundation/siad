package transactionpool

// TODO: There is some connecting and disconnecting in this test, use the
// blacklisting feature instead because it is more reliable.

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/Sia/types/typesutil"

	"gitlab.com/NebulousLabs/errors"
)

// TestMinimizeTransactionSet will test that the transaction set minimizer in
// the typesutil package is properly minimizing transaction sets and that those
// minimized sets can be put onto the blockchain and then propagated
// accordingly.
func TestMinimizeTransactionSet(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test group with two miners. The miners will be sending
	// transactions to eachother in a way that ensures transaction set
	// minimization is occuring correctly.
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
	// Give simple names to the miners.
	minerA := tg.Miners()[0]
	minerB := tg.Miners()[1]

	// Create source outputs for transaction graphs.
	var sources []types.SiacoinOutputID
	numSources := 2
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
	lastTxn := len(wsmp.Transactions) - 1
	for i := 0; i < numSources; i++ {
		sources = append(sources, wsmp.Transactions[lastTxn].SiacoinOutputID(uint64(i)))
	}

	// Confirm that the transactions are propagating to minerB.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err := minerA.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 2 {
			return fmt.Errorf("expected 2 transactions but got %v", len(tptg.Transactions))
		}
		tptg, err = minerB.TransactionPoolTransactionsGet()
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

	// Get the source outputs confirmed in a block.
	err = minerB.MineBlock()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that the transaction pools of both miners are empty.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err := minerA.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 0 {
			return errors.New("expecting 0 transactions after mining a block")
		}
		tptg, err = minerB.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 0 {
			return errors.New("expecting 0 transactions after mining a block")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Disconnect minerA and minerB, this will enable a setup of a double spend.
	// minerA is going to have a transaction graph with two soruce depedencies.
	// minerB is going to have a transaction graph that double spends one of
	// those sources. The second source in minerA will have a set of children
	// which could be independently sent to minerA if transaction sets are being
	// split apart correctly.
	gwg, err := minerB.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	err = minerA.GatewayDisconnectPost(gwg.NetAddress)
	if err != nil {
		t.Log(err)
	}

	// Use the first two source inputs to create the following graph. The key
	// feature of this graph is that txn2 and txn4 are fully independent of txn1
	// and txn3, and therefore should be able to confirm separately. This is the
	// graph that will be given to minerA.
	//
	// src1 -> txn1 ---> out1 -
	//                         \
	//                           --> txn3
	//                         /
	// src2 -> txn2 ---> out2 -
	//              \
	//                -> out3 -----> txn4
	//
	graph1 := typesutil.NewTransactionGraph()
	source1Index, err := graph1.AddSiacoinSource(sources[0], sourceSize)
	if err != nil {
		t.Fatal(err)
	}
	source2Index, err := graph1.AddSiacoinSource(sources[1], sourceSize)
	if err != nil {
		t.Fatal(err)
	}
	// Add txn1, which consumes src1 and produces out1
	txn1Outputs, err := graph1.AddTransaction(typesutil.SimpleTransaction{
		SiacoinInputs:  []int{source1Index},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision.Mul64(999)},

		MinerFees: []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Add txn2, which consumes src2 and creates out2 and out3.
	txn2Outputs, err := graph1.AddTransaction(typesutil.SimpleTransaction{
		SiacoinInputs: []int{source2Index},
		SiacoinOutputs: []types.Currency{
			types.SiacoinPrecision.Mul64(499),
			types.SiacoinPrecision.Mul64(500),
		},

		MinerFees: []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Add txn3, which consumes the first output of each previous transaction.
	_, err = graph1.AddTransaction(typesutil.SimpleTransaction{
		SiacoinInputs: []int{
			txn1Outputs[0],
			txn2Outputs[0],
		},
		SiacoinOutputs: []types.Currency{
			types.SiacoinPrecision.Mul64(998),
			types.SiacoinPrecision.Mul64(499),
		},

		MinerFees: []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Add txn4, which consumes the second output of txn2.
	_, err = graph1.AddTransaction(typesutil.SimpleTransaction{
		SiacoinInputs: []int{
			txn2Outputs[1],
		},
		SiacoinOutputs: []types.Currency{
			types.SiacoinPrecision.Mul64(499),
		},

		MinerFees: []types.Currency{types.SiacoinPrecision},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph1Txns := graph1.Transactions()

	// Create the double spend for minerB. This graph is simpler:
	//
	// src1 -> txn1x
	graph2 := typesutil.NewTransactionGraph()
	graph2Source, err := graph2.AddSiacoinSource(sources[0], sourceSize)
	if err != nil {
		t.Fatal(err)
	}
	// Add txn1x, which consumes src1. Ensure the transaction is different so
	// that there is a proper conflict.
	_, err = graph2.AddTransaction(typesutil.SimpleTransaction{
		SiacoinInputs:  []int{graph2Source},
		SiacoinOutputs: []types.Currency{types.SiacoinPrecision.Mul64(998)},

		MinerFees: []types.Currency{types.SiacoinPrecision.Mul64(2)},
	})
	if err != nil {
		t.Fatal(err)
	}
	graph2Txns := graph2.Transactions()

	// Give the first 3 transactions from graph1 to minerA.
	err = minerA.TransactionPoolRawPost(graph1Txns[2], graph1Txns[:2])
	if err != nil {
		t.Fatal(err)
	}
	// There should now be 3 transactions in the transaction pool for minerA.
	tptg, err := minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 3 {
		t.Fatal("expecting 3 transactions after mining block, got", len(tptg.Transactions))
	}
	// There should not be any transactions in minerB because minerA and minerB
	// are not connected.
	tptg, err = minerB.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 0 {
		t.Fatal("expecting 0 transactions after mining block, got", len(tptg.Transactions))
	}

	// Give the double spend from graph2 to minerB.
	err = minerB.TransactionPoolRawPost(graph2Txns[0], graph2Txns[:0])
	if err != nil {
		t.Fatal(err)
	}
	// There should now be 1 transaction in the transaction pool for minerB.
	tptg, err = minerB.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 1 {
		t.Fatal("expecting 1 transactions after mining block, got", len(tptg.Transactions))
	}
	// minerA should still have 3 transactions.
	tptg, err = minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 3 {
		t.Fatal("expecting 3 transactions after mining block, got", len(tptg.Transactions))
	}

	// Reconnect minerA to minerB. They should have a spending conflict.
	gwg, err = minerB.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	err = minerA.GatewayConnectPost(gwg.NetAddress)
	if err != nil {
		t.Fatal(err)
	}

	// Submit the fourth transaction to minerA. Check that all 4 transactions
	// are in minerA.
	err = minerA.TransactionPoolRawPost(graph1Txns[3], graph1Txns[:0])
	if err != nil {
		t.Fatal(err)
	}
	tptg, err = minerA.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 4 {
		t.Fatal("expecting 4 transactions after adding 4th transaction to minerA, got", len(tptg.Transactions))
	}

	// The independent transactions should propagate to minerB, meaning that
	// minerB should have 3 transactions total.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err = minerB.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 3 {
			return fmt.Errorf("expecting 3 transactions in minerB after adding txn to minerA, got %v", len(tptg.Transactions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine a block with minerB, both transaction pools for minerA and minerB
	// should be cleared out.
	err = minerB.MineBlock()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that the transaction pools of both miners are empty.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		tptg, err := minerA.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 0 {
			return errors.New("expecting 0 transactions after mining a block")
		}
		tptg, err = minerB.TransactionPoolTransactionsGet()
		if err != nil {
			return err
		}
		if len(tptg.Transactions) != 0 {
			return errors.New("expecting 0 transactions after mining a block")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
