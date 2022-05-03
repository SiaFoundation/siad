package txpool_test

import (
	"testing"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"

	"go.sia.tech/siad/v2/internal/chainutil"
	"go.sia.tech/siad/v2/txpool"
)

func TestPoolFlexibility(t *testing.T) {
	sim := chainutil.NewChainSim()

	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)
	tp := txpool.New(sim.Genesis.State)
	cm.AddSubscriber(tp, cm.Tip())

	// Create three transactions that are valid as of the current block.
	txns := make([]types.Transaction, 3)
	for i := range txns {
		txns[i] = sim.TxnWithSiacoinOutputs(types.SiacoinOutput{
			Value:   types.Siacoins(1),
			Address: types.VoidAddress,
		})
	}

	// Add the first transaction to the pool; it should be accepted.
	if err := tp.AddTransaction(txns[0]); err != nil {
		t.Fatal("pool rejected control transaction:", err)
	}

	// Mine a block and add the second transaction. Its proofs are now outdated,
	// but only by one block, so the pool should still accept it.
	if err := cm.AddTipBlock(sim.MineBlock()); err != nil {
		t.Fatal(err)
	} else if err := tp.AddTransaction(txns[1]); err != nil {
		t.Fatal("pool rejected slightly outdated transaction:", err)
	}

	// Mine another block and add the third transaction. Its proofs are now
	// outdated by two blocks, so the pool should reject it.
	if err := cm.AddTipBlock(sim.MineBlock()); err != nil {
		t.Fatal(err)
	} else if err := tp.AddTransaction(txns[2]); err == nil {
		t.Fatal("pool did not reject very outdated transaction")
	}
}

func TestEphemeralOutput(t *testing.T) {
	sim := chainutil.NewChainSim()

	cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)
	tp := txpool.New(sim.Genesis.State)
	cm.AddSubscriber(tp, cm.Tip())

	// create parent, child, and grandchild transactions
	privkey := types.GeneratePrivateKey()
	parent := sim.TxnWithSiacoinOutputs(types.SiacoinOutput{
		Value:   types.Siacoins(10),
		Address: types.StandardAddress(privkey.PublicKey()),
	})
	child := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      parent.EphemeralSiacoinElement(0),
			SpendPolicy: types.PolicyPublicKey(privkey.PublicKey()),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:   types.Siacoins(9),
			Address: types.StandardAddress(privkey.PublicKey()),
		}},
		MinerFee: types.Siacoins(1),
	}
	child.SiacoinInputs[0].Signatures = []types.Signature{privkey.SignHash(sim.State.InputSigHash(child))}

	grandchild := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			Parent:      child.EphemeralSiacoinElement(0),
			SpendPolicy: types.PolicyPublicKey(privkey.PublicKey()),
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:   types.Siacoins(7),
			Address: types.VoidAddress,
		}},
		MinerFee: types.Siacoins(2),
	}
	grandchild.SiacoinInputs[0].Signatures = []types.Signature{privkey.SignHash(sim.State.InputSigHash(grandchild))}

	// add all three transactions to the pool
	if err := tp.AddTransaction(parent); err != nil {
		t.Fatal(err)
	} else if err := tp.AddTransaction(child); err != nil {
		t.Fatal(err)
	} else if err := tp.AddTransaction(grandchild); err != nil {
		t.Fatal(err)
	} else if len(tp.Transactions()) != 3 {
		t.Fatal("wrong number of transactions in pool:", len(tp.Transactions()))
	}

	// mine a block containing parent and child
	pcState := sim.State
	pcBlock := sim.MineBlockWithTxns(parent, child)
	if err := cm.AddTipBlock(pcBlock); err != nil {
		t.Fatal(err)
	}

	// pool should now contain just grandchild
	if txns := tp.Transactions(); len(txns) != 1 {
		t.Fatal("wrong number of transactions in pool:", len(tp.Transactions()))
	}
	grandchild = tp.Transactions()[0]
	if grandchild.SiacoinInputs[0].Parent.LeafIndex == types.EphemeralLeafIndex {
		t.Fatal("grandchild's input should no longer be ephemeral")
	}
	// mine a block containing grandchild
	gState := sim.State
	gBlock := sim.MineBlockWithTxns(grandchild)
	if err := cm.AddTipBlock(gBlock); err != nil {
		t.Fatal(err)
	}

	// revert the grandchild block
	tp.ProcessChainRevertUpdate(&chain.RevertUpdate{
		RevertUpdate: consensus.RevertBlock(gState, gBlock),
		Block:        gBlock,
	})

	// pool should contain the grandchild transaction again
	if len(tp.Transactions()) != 1 {
		t.Fatal("wrong number of transactions in pool:", len(tp.Transactions()))
	}
	grandchild = tp.Transactions()[0]
	if grandchild.SiacoinInputs[0].Parent.LeafIndex == types.EphemeralLeafIndex {
		t.Fatal("grandchild's input should not be ephemeral")
	}

	// revert the parent+child block
	tp.ProcessChainRevertUpdate(&chain.RevertUpdate{
		RevertUpdate: consensus.RevertBlock(pcState, pcBlock),
		Block:        pcBlock,
	})

	// pool should contain all three transactions again
	if len(tp.Transactions()) != 3 {
		t.Fatal("wrong number of transactions in pool:", len(tp.Transactions()))
	}
	// grandchild's input should be ephemeral again
	for _, txn := range tp.Transactions() {
		if txn.SiacoinOutputs[0].Address == types.VoidAddress {
			if txn.SiacoinInputs[0].Parent.LeafIndex != types.EphemeralLeafIndex {
				t.Fatal("grandchild's input should be ephemeral again")
			}
			break
		}
	}
}
