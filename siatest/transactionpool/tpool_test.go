package transactionpool

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestTpoolTransactionsGet probes the API end point from returning the
// transactions of the tpool
func TestTpoolTransactionsGet(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create testing directory.
	testdir := tpoolTestDir(t.Name())

	// Create a miners
	miner, err := siatest.NewNode(siatest.Miner(filepath.Join(testdir, "miner")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := miner.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Transaction pool should be empty to start
	tptg, err := miner.TransactionPoolTxnsGET()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 0 {
		t.Fatal("expected no transactions got", len(tptg.Transactions))
	}

	// miner sends a txn to itself
	uc, err := miner.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	_, err = miner.WalletSiacoinsPost(types.SiacoinPrecision, uc.Address)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the transaction pool
	tptg, err = miner.TransactionPoolTxnsGET()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 1 {
		t.Fatal("expected 1 transaction got", len(tptg.Transactions))
	}

	// Mine a block to confirm the transaction
	if err := miner.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Transaction pool should now be empty
	tptg, err = miner.TransactionPoolTxnsGET()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 0 {
		t.Fatal("expected no transactions got", len(tptg.Transactions))
	}
}
