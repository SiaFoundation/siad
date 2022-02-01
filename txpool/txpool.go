package txpool

import (
	"errors"
	"fmt"
	"sync"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
)

type updater interface {
	UpdateElementProof(e *types.StateElement)
	UpdateWindowProof(sp *types.StorageProof)
}

func updateTxnProofs(txn *types.Transaction, u updater) {
	for i := range txn.SiacoinInputs {
		if txn.SiacoinInputs[i].Parent.LeafIndex != types.EphemeralLeafIndex {
			u.UpdateElementProof(&txn.SiacoinInputs[i].Parent.StateElement)
		}
	}
	for i := range txn.SiafundInputs {
		if txn.SiafundInputs[i].Parent.LeafIndex != types.EphemeralLeafIndex {
			u.UpdateElementProof(&txn.SiafundInputs[i].Parent.StateElement)
		}
	}
	for i := range txn.FileContractRevisions {
		u.UpdateElementProof(&txn.FileContractRevisions[i].Parent.StateElement)
	}
	for i := range txn.FileContractResolutions {
		u.UpdateElementProof(&txn.FileContractResolutions[i].Parent.StateElement)
		u.UpdateWindowProof(&txn.FileContractResolutions[i].StorageProof)
	}
}

func updateEphemeralInputs(txn *types.Transaction, u updater) {
	switch u := u.(type) {
	case *consensus.ApplyUpdate:
		// if txn spends an ephemeral output that has since been mined, replace
		// the ephemeral output with the actual element
		for i := range txn.SiacoinInputs {
			in := &txn.SiacoinInputs[i]
			if in.Parent.LeafIndex == types.EphemeralLeafIndex {
				for _, sce := range u.NewSiacoinElements {
					if sce.ID == in.Parent.ID {
						in.Parent = sce
						in.Parent.MerkleProof = append([]types.Hash256(nil), in.Parent.MerkleProof...)
						break
					}
				}
			}
		}
	case *consensus.RevertUpdate:
		// if txn spends an output belonging to a transaction that has been
		// returned to the pool, mark the element as ephemeral
		for i := range txn.SiacoinInputs {
			in := &txn.SiacoinInputs[i]
			for _, sce := range u.NewSiacoinElements {
				if sce.ID == in.Parent.ID {
					in.Parent.LeafIndex = types.EphemeralLeafIndex
					in.Parent.MerkleProof = nil
					break
				}
			}
		}
	default:
		panic(fmt.Sprintf("invalid updater type: %T", u))
	}
}

func txnContainsRevertedElements(txn types.Transaction, cru *chain.RevertUpdate) bool {
	for i := range txn.SiacoinInputs {
		if cru.SiacoinElementWasRemoved(txn.SiacoinInputs[i].Parent) {
			return true
		}
	}
	for i := range txn.SiafundInputs {
		if cru.SiafundElementWasRemoved(txn.SiafundInputs[i].Parent) {
			return true
		}
	}
	for i := range txn.FileContractRevisions {
		if cru.FileContractElementWasRemoved(txn.FileContractRevisions[i].Parent) {
			return true
		}
	}
	for i := range txn.FileContractResolutions {
		if cru.FileContractElementWasRemoved(txn.FileContractResolutions[i].Parent) {
			return true
		}
	}
	return false
}

// A Pool holds transactions that may be included in future blocks.
type Pool struct {
	txns       map[types.TransactionID]types.Transaction
	vc         consensus.ValidationContext
	prevVC     consensus.ValidationContext
	prevUpdate consensus.ApplyUpdate
	mu         sync.Mutex
}

func (p *Pool) validateTransaction(txn types.Transaction) error {
	// Perform standard validation checks, with added flexibility: if the
	// transaction merely has outdated proofs, update them and attempt to
	// validate again.
	err := p.vc.ValidateTransaction(txn)
	if err != nil && p.prevVC.ValidateTransaction(txn) == nil {
		updateTxnProofs(&txn, &p.prevUpdate)
		err = p.vc.ValidateTransaction(txn)
	}
	if err != nil {
		return err
	}

	// validate ephemeral outputs
	//
	// TODO: fix ordering when creating and spending ephemeral outputs
	// TODO: keep this map around instead of rebuilding it every time
	availableSC := make(map[types.ElementID]struct{})
	availableSF := make(map[types.ElementID]struct{})
	for txid, txn := range p.txns {
		for i := range txn.SiacoinOutputs {
			oid := types.ElementID{
				Source: types.Hash256(txid),
				Index:  uint64(i),
			}
			availableSC[oid] = struct{}{}
		}

		// set the initial index to account for the siacoin outputs and siafund
		// claim outputs.
		index := uint64(len(txn.SiacoinOutputs) + len(txn.SiafundInputs))
		for range txn.SiafundOutputs {
			oid := types.ElementID{
				Source: types.Hash256(txid),
				Index:  index,
			}
			availableSF[oid] = struct{}{}
			index++
		}

		// set the initial index to account for the siacoin outputs, siafund
		// claim outputs, and new file contracts.
		index = uint64(len(txn.SiacoinOutputs) + len(txn.SiafundInputs) + len(txn.SiafundOutputs) + len(txn.FileContracts))
		for _, fcr := range txn.FileContractRevisions {
			if fc := fcr.Revision; fc.CanResolveEarly() {
				renterID := types.ElementID{
					Source: types.Hash256(txid),
					Index:  index,
				}
				index++
				hostID := types.ElementID{
					Source: types.Hash256(txid),
					Index:  index,
				}
				index++
				availableSC[renterID] = struct{}{}
				availableSC[hostID] = struct{}{}
			}
		}
	}
	for _, in := range txn.SiacoinInputs {
		if in.Parent.LeafIndex == types.EphemeralLeafIndex {
			oid := in.Parent.ID
			if _, ok := availableSC[oid]; !ok {
				return errors.New("transaction references an unknown ephemeral siacoin output")
			}
			delete(availableSC, oid)
		}
	}
	for _, in := range txn.SiafundInputs {
		if in.Parent.LeafIndex == types.EphemeralLeafIndex {
			oid := in.Parent.ID
			if _, ok := availableSC[oid]; !ok {
				return errors.New("transaction references an unknown ephemeral siafund output")
			}
			delete(availableSF, oid)
		}
	}

	return nil
}

func (p *Pool) addTransaction(txn types.Transaction) error {
	txid := txn.ID()
	if _, ok := p.txns[txid]; ok {
		return nil // already in pool
	}
	txn = txn.DeepCopy()
	if err := p.validateTransaction(txn); err != nil {
		return err
	}
	p.txns[txid] = txn
	return nil
}

// AddTransactionSet validates a transaction set and adds it to the pool. If any
// transaction references ephemeral parent outputs those parent outputs must be
// created by transactions in the transaction set or already in the pool. All
// proofs in the transaction set must be up-to-date.
func (p *Pool) AddTransactionSet(txns []types.Transaction) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.vc.ValidateTransactionSet(txns); err != nil {
		return fmt.Errorf("failed to validate transaction set: %w", err)
	}

	for i, txn := range txns {
		if err := p.addTransaction(txn); err != nil {
			return fmt.Errorf("failed to add transaction %v: %w", i, err)
		}
	}
	return nil
}

// FeeEstimate returns a minimum and maximum estimate of the fee for
// broadcasting a transaction
func (p *Pool) FeeEstimate() (min, max types.Currency, _ error) {
	return
}

// AddTransaction validates a transaction and adds it to the pool. If the
// transaction references ephemeral parent outputs, those outputs must be
// created by other transactions already in the pool. The transaction's proofs
// must be up-to-date.
func (p *Pool) AddTransaction(txn types.Transaction) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addTransaction(txn)
}

// Transaction returns the transaction with the specified ID, if it is currently
// in the pool.
func (p *Pool) Transaction(id types.TransactionID) (types.Transaction, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	txn, ok := p.txns[id]
	return txn, ok
}

// Transactions returns the transactions currently in the pool.
func (p *Pool) Transactions() []types.Transaction {
	p.mu.Lock()
	defer p.mu.Unlock()
	txns := make([]types.Transaction, 0, len(p.txns))
	for _, txn := range p.txns {
		txns = append(txns, txn.DeepCopy())
	}
	return txns
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (p *Pool) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, _ bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// delete confirmed txns
	for _, txn := range cau.Block.Transactions {
		delete(p.txns, txn.ID())
	}

	// update unconfirmed txns
	for id, txn := range p.txns {
		updateTxnProofs(&txn, &cau.ApplyUpdate)
		updateEphemeralInputs(&txn, &cau.ApplyUpdate)

		// verify that the transaction is still valid
		//
		// NOTE: in theory we should only need to run height-dependent checks
		// here (e.g. timelocks); but it doesn't hurt to be extra thorough. Easy
		// to remove later if it becomes a bottleneck.
		if err := cau.Context.ValidateTransaction(txn); err != nil {
			delete(p.txns, id)
			continue
		}

		p.txns[id] = txn
	}

	// update the current and previous validation contexts
	p.prevVC, p.vc = p.vc, cau.Context
	p.prevUpdate = cau.ApplyUpdate
	return nil
}

// ProcessChainRevertUpdate implements chain.Subscriber.
func (p *Pool) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// update unconfirmed txns
	for id, txn := range p.txns {
		updateEphemeralInputs(&txn, &cru.RevertUpdate)
		if txnContainsRevertedElements(txn, cru) {
			delete(p.txns, id)
			continue
		}
		updateTxnProofs(&txn, &cru.RevertUpdate)
		p.txns[id] = txn

		// verify that the transaction is still valid
		if err := cru.Context.ValidateTransaction(txn); err != nil {
			delete(p.txns, id)
			continue
		}
	}

	// put reverted txns back in the pool
	for _, txn := range cru.Block.Transactions {
		p.txns[txn.ID()] = txn.DeepCopy()
	}

	// update validation context
	p.vc = cru.Context
	return nil
}

// New creates a new transaction pool.
func New(vc consensus.ValidationContext) *Pool {
	return &Pool{
		txns:   make(map[types.TransactionID]types.Transaction),
		vc:     vc,
		prevVC: vc,
	}
}
