package transactionpool

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
)

func TestTpoolTransactionSetGet(t *testing.T) {
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
	tpsg, err := miner.TransactionPoolSetsGET()
	if err != nil {
		t.Fatal(err)
	}
	if len(tpsg.TransactionSets) != 0 {
		t.Fatal("expected no transaction sets got", len(tpsg.TransactionSets))
	}

	// miner sends a txn to itself and mines it.
	uc, err := miner.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	_, err = miner.WalletSiacoinsPost(types.SiacoinPrecision, uc.Address)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the transaction pool
	tpsg, err = miner.TransactionPoolSetsGET()
	if err != nil {
		t.Fatal(err)
	}
	if len(tpsg.TransactionSets) != 1 {
		t.Fatal("expected 1 transaction set got", len(tpsg.TransactionSets))
	}

	// Mine a block to confirm the transaction
	if err := miner.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Transaction pool should now be empty
	tpsg, err = miner.TransactionPoolSetsGET()
	if err != nil {
		t.Fatal(err)
	}
	if len(tpsg.TransactionSets) != 0 {
		t.Fatal("expected no transaction sets got", len(tpsg.TransactionSets))
	}
}
