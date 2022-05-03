package cpuminer

import (
	"math"
	"sync"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/internal/chainutil"
	"lukechampine.com/frand"
)

// TransactionPool is an interface for getting transactions from the txpool.
type TransactionPool interface {
	Transactions() []types.Transaction
}

// A CPUMiner mines blocks.
type CPUMiner struct {
	addr types.Address
	tp   TransactionPool

	mu sync.Mutex
	cs consensus.State
}

func (m *CPUMiner) txnsForBlock() (blockTxns []types.Transaction) {
	poolTxns := make(map[types.TransactionID]types.Transaction)
	for _, txn := range m.tp.Transactions() {
		poolTxns[txn.ID()] = txn
	}

	// define a helper function that returns the unmet dependencies of a given
	// transaction
	calcDeps := func(txn types.Transaction) (deps []types.Transaction) {
		added := make(map[types.TransactionID]bool)
		var addDeps func(txn types.Transaction)
		addDeps = func(txn types.Transaction) {
			added[txn.ID()] = true
			for _, in := range txn.SiacoinInputs {
				parentID := types.TransactionID(in.Parent.ID.Source)
				if parent, inPool := poolTxns[parentID]; inPool && !added[parentID] {
					// NOTE: important that we add the parent's deps before the
					// parent itself
					addDeps(parent)
					deps = append(deps, parent)
				}
			}
		}
		addDeps(txn)
		return
	}

	capacity := m.cs.MaxBlockWeight()
	for _, txn := range poolTxns {
		// prepend the txn with its dependencies
		group := append(calcDeps(txn), txn)
		// if the weight of the group exceeds the remaining capacity of the
		// block, skip it
		groupWeight := m.cs.BlockWeight(group)
		if groupWeight > capacity {
			continue
		}
		// add the group to the block
		blockTxns = append(blockTxns, group...)
		capacity -= groupWeight
		for _, txn := range group {
			delete(poolTxns, txn.ID())
		}
	}

	return
}

// MineBlock mines a valid block, using transactions drawn from the txpool.
func (m *CPUMiner) MineBlock() types.Block {
	for {
		// TODO: if the miner and txpool don't have the same tip, we'll
		// construct an invalid block
		m.mu.Lock()
		parent := m.cs.Index
		target := types.HashRequiringWork(m.cs.Difficulty)
		addr := m.addr
		txns := m.txnsForBlock()
		commitment := m.cs.Commitment(addr, txns)
		m.mu.Unlock()
		b := types.Block{
			Header: types.BlockHeader{
				Height:       parent.Height + 1,
				ParentID:     parent.ID,
				Nonce:        frand.Uint64n(math.MaxUint64),
				Timestamp:    types.CurrentTimestamp(),
				Commitment:   commitment,
				MinerAddress: addr,
			},
			Transactions: txns,
		}

		// grind
		chainutil.FindBlockNonce(&b.Header, target)

		// check whether the tip has changed since we started
		// grinding; if it has, we need to start over
		m.mu.Lock()
		tipChanged := m.cs.Index != parent
		m.mu.Unlock()
		if !tipChanged {
			return b
		}
	}
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (m *CPUMiner) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cs = cau.State
	return nil
}

// ProcessChainRevertUpdate implements chain.Subscriber.
func (m *CPUMiner) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cs = cru.State
	return nil
}

// New returns a CPUMiner initialized with the provided state.
func New(cs consensus.State, addr types.Address, tp TransactionPool) *CPUMiner {
	return &CPUMiner{
		cs:   cs,
		addr: addr,
		tp:   tp,
	}
}
