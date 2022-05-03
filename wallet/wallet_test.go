package wallet_test

import (
	"testing"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"

	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/internal/walletutil"
)

func TestWallet(t *testing.T) {
	sim := chainutil.NewChainSim()

	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)
	w := walletutil.NewTestingWallet(cm.TipState())
	cm.AddSubscriber(w, cm.Tip())

	// fund the wallet with 100 coins
	ourAddr := w.NewAddress()
	fund := types.SiacoinOutput{Value: types.Siacoins(100), Address: ourAddr}
	if err := cm.AddTipBlock(sim.MineBlockWithSiacoinOutputs(fund)); err != nil {
		t.Fatal(err)
	}

	// wallet should now have a transaction, one element, and a non-zero balance
	if txns, _ := w.Transactions(time.Time{}, -1); len(txns) != 1 {
		t.Fatal("expected a single transaction, got", txns)
	} else if utxos, _ := w.UnspentSiacoinElements(); len(utxos) != 1 {
		t.Fatal("expected a single unspent element, got", utxos)
	} else if w.Balance().IsZero() {
		t.Fatal("expected non-zero balance after mining")
	}

	// mine 5 blocks, each containing a transaction that sends some coins to
	// the void and some to ourself
	for i := 0; i < 5; i++ {
		sendAmount := types.Siacoins(7)
		txn := types.Transaction{
			SiacoinOutputs: []types.SiacoinOutput{{
				Address: types.VoidAddress,
				Value:   sendAmount,
			}},
		}
		if err := w.FundAndSign(&txn); err != nil {
			t.Fatal(err)
		}
		prevBalance := w.Balance()

		if err := cm.AddTipBlock(sim.MineBlockWithTxns(txn)); err != nil {
			t.Fatal(err)
		}

		if !prevBalance.Sub(w.Balance()).Equals(sendAmount) {
			t.Fatal("after send, balance should have decreased accordingly")
		}
	}
}
