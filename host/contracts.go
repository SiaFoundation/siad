package host

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/host"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
)

type locker struct {
	c       chan struct{}
	waiters int
}

// A ContractManager manages contracts' lifecycle
type ContractManager struct {
	store   host.ContractStore
	sectors host.SectorStore
	tpool   host.TransactionPool
	wallet  host.Wallet
	cm      *chain.Manager
	log     Logger

	// contracts must be locked while they are being modified
	mu    sync.Mutex
	locks map[types.ElementID]*locker
}

// Lock locks a contract for modification.
func (cm *ContractManager) Lock(id types.ElementID, timeout time.Duration) (rhp.Contract, error) {
	cm.mu.Lock()
	contract, err := cm.store.Get(id)
	if err != nil {
		return rhp.Contract{}, err
	}

	// if the contract isn't already locked, create a new lock
	if _, exists := cm.locks[id]; !exists {
		cm.locks[id] = &locker{
			c:       make(chan struct{}, 1),
			waiters: 0,
		}
		cm.mu.Unlock()
		return contract, nil
	}
	cm.locks[id].waiters++
	c := cm.locks[id].c
	// mutex must be unlocked before waiting on the channel to prevent deadlock.
	cm.mu.Unlock()
	select {
	case <-c:
		contract, err := cm.store.Get(id)
		if err != nil {
			return rhp.Contract{}, fmt.Errorf("failed to get contract: %w", err)
		}
		return contract, nil
	case <-time.After(timeout):
		return rhp.Contract{}, errors.New("contract lock timeout")
	}
}

// Unlock unlocks a locked contract.
func (cm *ContractManager) Unlock(id types.ElementID) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	lock, exists := cm.locks[id]
	if !exists {
		return
	} else if lock.waiters <= 0 {
		delete(cm.locks, id)
		return
	}
	lock.waiters--
	lock.c <- struct{}{}
}

// Add stores the provided contract, overwriting any previous contract
// with the same ID.
func (cm *ContractManager) Add(contract rhp.Contract, formationTxn types.Transaction) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.store.Add(contract, formationTxn)
}

// Revise updates the current revision associated with a contract.
func (cm *ContractManager) Revise(contract rhp.Contract) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.store.Revise(contract)
}

// Roots returns the roots of all sectors stored by the contract.
func (cm *ContractManager) Roots(id types.ElementID) ([]types.Hash256, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.store.Roots(id)
}

// SetRoots updates the roots of the contract.
func (cm *ContractManager) SetRoots(id types.ElementID, roots []types.Hash256) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.store.SetRoots(id, roots)
}

// handleContractAction performs a lifecycle action on a contract.
func (cm *ContractManager) handleContractAction(vc consensus.ValidationContext, contract host.Contract) {
	switch {
	case !contract.Confirmed:
		if err := cm.tpool.AddTransaction(contract.FormationTransaction); err != nil {
			cm.log.Warnf("contract %v: failed to rebroadcast formation transaction: %v", contract.ID, err)
			cm.store.Delete(contract.ID)
			return
		}
	case contract.ShouldSubmitRevision(vc.Index):
		revisionTxn := types.Transaction{
			FileContractRevisions: []types.FileContractRevision{{
				Parent:   contract.Parent,
				Revision: contract.Revision,
			}},
		}
		revisionTxn.MinerFee = cm.tpool.RecommendedFee().Mul64(vc.TransactionWeight(revisionTxn))
		toSign, cleanup, err := cm.wallet.FundTransaction(&revisionTxn, revisionTxn.MinerFee, nil)
		if err != nil {
			cm.log.Warnf("contract %v: failed to fund revision transaction: %v", contract.ID, err)
			return
		}
		defer cleanup()
		if err := cm.wallet.SignTransaction(vc, &revisionTxn, toSign); err != nil {
			cm.log.Warnf("contract %v: failed to sign revision transaction: %v", contract.ID, err)
			return
		} else if err := cm.tpool.AddTransaction(revisionTxn); err != nil {
			cm.log.Warnf("contract %v: failed to broadcast revision transaction: %v", contract.ID, err)
			return
		}
	case contract.ShouldSubmitResolution(vc.Index):
		resolution := types.FileContractResolution{
			Parent: contract.Parent,
		}

		// TODO: consider cost of proof submission and build proof.
		// if vc.Index.Height < contract.Revision.WindowEnd && contract.Revision.Filesize > 0 {}

		resolutionTxn := types.Transaction{
			FileContractResolutions: []types.FileContractResolution{resolution},
		}
		resolutionTxn.MinerFee = cm.tpool.RecommendedFee().Mul64(vc.TransactionWeight(resolutionTxn))
		toSign, cleanup, err := cm.wallet.FundTransaction(&resolutionTxn, resolutionTxn.MinerFee, nil)
		if err != nil {
			cm.log.Warnf("contract %v: failed to fund resolution transaction: %v", contract.ID, err)
			return
		}
		defer cleanup()
		if err := cm.wallet.SignTransaction(vc, &resolutionTxn, toSign); err != nil {
			cm.log.Warnf("contract %v: failed to sign revision transaction: %v", contract.ID, err)
			return
		} else if err := cm.tpool.AddTransaction(resolutionTxn); err != nil {
			cm.log.Warnf("contract %v: failed to broadcast resolution transaction: %v", contract.ID, err)
			return
		}
	}
}

// ProcessChainApplyUpdate applies a block update to the contract manager.
func (cm *ContractManager) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.store.ContractAction(cau.Context, cm.handleContractAction)
	return nil
}

// ProcessChainRevertUpdate reverts a block update from the contract manager.
func (cm *ContractManager) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.store.ContractAction(cru.Context, cm.handleContractAction)
	return nil
}
