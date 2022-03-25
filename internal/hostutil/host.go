// Package hostutil implements barebones, reference, ephemeral host stores.
// Should be used for testing purposes only.
package hostutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	corehost "go.sia.tech/core/host"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/host"
)

var (
	// DefaultSettings are the default settings for a ghost host.
	DefaultSettings = rhp.HostSettings{
		AcceptingContracts:     true,
		ContractFee:            types.Siacoins(1),
		Collateral:             types.Siacoins(1).Div64(1 << 22).Div64(4320), // 1 SC per sector per month
		MaxCollateral:          types.Siacoins(5000),
		MaxDuration:            4960,
		StoragePrice:           types.Siacoins(1).Div64(1 << 22).Div64(4320), // 1 SC per sector per sector per month
		DownloadBandwidthPrice: types.Siacoins(1).Div64(1 << 22),             // 1 SC per sector
		UploadBandwidthPrice:   types.Siacoins(1).Div64(1 << 22),             // 1 SC per sector
		SectorSize:             1 << 22,
		WindowSize:             144,

		RPCFundAccountCost:    types.NewCurrency64(1),
		RPCAccountBalanceCost: types.NewCurrency64(1),
		RPCHostSettingsCost:   types.NewCurrency64(1),
		RPCLatestRevisionCost: types.NewCurrency64(1),
	}
)

// A MemEphemeralAccountStore is an in-memory implementation of the ephemeral
// account store. Implements host.EphemeralAccountStore.
type MemEphemeralAccountStore struct {
	mu       sync.Mutex
	balances map[types.PublicKey]types.Currency
}

// Balance returns the balance of the ephemeral account.
func (ms *MemEphemeralAccountStore) Balance(accountID types.PublicKey) (types.Currency, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.balances[accountID], nil
}

// Credit adds the specified amount to the account, returning the current
// balance.
func (ms *MemEphemeralAccountStore) Credit(accountID types.PublicKey, amount types.Currency) (types.Currency, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.balances[accountID] = ms.balances[accountID].Add(amount)
	return ms.balances[accountID], nil
}

// Refund returns the amount to the ephemeral account.
func (ms *MemEphemeralAccountStore) Refund(accountID types.PublicKey, amount types.Currency) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.balances[accountID] = ms.balances[accountID].Add(amount)
	return nil
}

// Debit subtracts the specified amount from the account, returning the current
// balance.
func (ms *MemEphemeralAccountStore) Debit(accountID types.PublicKey, requestID types.Hash256, amount types.Currency) (types.Currency, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	bal, exists := ms.balances[accountID]
	if !exists || bal.Cmp(amount) < 0 {
		return bal, errors.New("insufficient funds")
	}

	ms.balances[accountID] = ms.balances[accountID].Sub(amount)
	return ms.balances[accountID], nil
}

// NewMemAccountStore intializes a new AccountStore.
func NewMemAccountStore() *MemEphemeralAccountStore {
	return &MemEphemeralAccountStore{
		balances: make(map[types.PublicKey]types.Currency),
	}
}

type sectorMeta struct {
	data       [rhp.SectorSize]byte
	references uint64
}

// An EphemeralSectorStore implements an in-memory sector store.
type EphemeralSectorStore struct {
	mu      sync.Mutex
	sectors map[types.Hash256]*sectorMeta
}

// Delete removes a sector from the store.
func (es *EphemeralSectorStore) Delete(root types.Hash256, refs uint64) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	sector, exists := es.sectors[root]
	if !exists {
		return errors.New("sector not found")
	} else if sector.references <= refs {
		delete(es.sectors, root)
	} else {
		es.sectors[root].references--
	}
	return nil
}

// Exists checks if the sector exists in the store.
func (es *EphemeralSectorStore) Exists(root types.Hash256) (bool, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	_, exists := es.sectors[root]
	return exists, nil
}

// Add adds the sector with the specified root to the store.
func (es *EphemeralSectorStore) Add(root types.Hash256, sector *[rhp.SectorSize]byte) error {
	es.mu.Lock()
	defer es.mu.Unlock()
	if _, exists := es.sectors[root]; exists {
		es.sectors[root].references++
		return nil
	}
	es.sectors[root] = &sectorMeta{
		data: *sector,
	}
	return nil
}

// Update overwrites data in the sector with the specified root.
func (es *EphemeralSectorStore) Update(root types.Hash256, offset uint64, data []byte) (types.Hash256, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if rhp.SectorSize < offset+uint64(len(data)) {
		return types.Hash256{}, errors.New("")
	}
	current, exists := es.sectors[root]
	if !exists {
		return types.Hash256{}, errors.New("sector not found")
	}
	sector := current.data
	copy(sector[offset:], data)
	root = rhp.SectorRoot(&sector)
	if _, exists := es.sectors[root]; exists {
		es.sectors[root].references++
		return root, nil
	}
	es.sectors[root] = &sectorMeta{
		data: sector,
	}
	return root, errors.New("sector not found")
}

// Read reads the sector with the given root, offset and length
// into w. Returns the number of bytes read or an error.
func (es *EphemeralSectorStore) Read(root types.Hash256, w io.Writer, offset, length uint64) (uint64, error) {
	es.mu.Lock()
	defer es.mu.Unlock()
	sector, exists := es.sectors[root]
	if !exists {
		return 0, errors.New("sector not found")
	}
	if offset+length > rhp.SectorSize {
		return 0, errors.New("read out of bounds")
	}
	n, err := w.Write(sector.data[offset : offset+length])
	return uint64(n), err
}

// NewEphemeralSectorStore initializes a new EphemeralSectorStore.
func NewEphemeralSectorStore() *EphemeralSectorStore {
	return &EphemeralSectorStore{
		sectors: make(map[types.Hash256]*sectorMeta),
	}
}

// An EphemeralContractStore implements an in-memory contract store.
type EphemeralContractStore struct {
	mu        sync.Mutex
	index     types.ChainIndex
	contracts map[types.ElementID]*corehost.Contract
	roots     map[types.ElementID][]types.Hash256
}

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

// ActiveContracts returns a list of contracts that are unresolved.
func (cs *EphemeralContractStore) ActiveContracts(limit, skip uint64) (contracts []corehost.Contract, _ error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, contract := range cs.contracts {
		if contract.Confirmed && contract.State == corehost.ContractStateUnresolved {
			contracts = append(contracts, *contract)
		}
	}
	return contracts, nil
}

// ResolvedContracts returns all contracts that have been resolved.
func (cs *EphemeralContractStore) ResolvedContracts(limit, skip uint64) (contracts []corehost.Contract, _ error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for _, contract := range cs.contracts {
		if contract.State != corehost.ContractStateUnresolved {
			contracts = append(contracts, *contract)
		}
	}
	return contracts, nil
}

// Exists checks if the contract exists in the store.
func (cs *EphemeralContractStore) Exists(id types.ElementID) (exists bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	_, exists = cs.contracts[id]
	return
}

// Get returns the contract with the given ID.
func (cs *EphemeralContractStore) Get(id types.ElementID) (rhp.Contract, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.contracts[id].Contract, nil
}

// Add stores the provided contract, returning an error if the contract already
// exists.
func (cs *EphemeralContractStore) Add(contract rhp.Contract, formationTxn types.Transaction) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, exists := cs.contracts[contract.ID]; exists {
		return errors.New("contract already exists")
	}
	cs.contracts[contract.ID] = &corehost.Contract{
		Contract: contract,
	}
	return nil
}

// Delete removes the contract with the given ID.
func (cs *EphemeralContractStore) Delete(id types.ElementID) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.contracts, id)
	return nil
}

// Revise updates the current revision associated with a contract.
func (cs *EphemeralContractStore) Revise(revision rhp.Contract) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if _, exists := cs.contracts[revision.ID]; !exists {
		return errors.New("contract not found")
	}
	cs.contracts[revision.ID].Contract = revision
	return nil
}

// Roots returns the roots of all sectors stored by the contract.
func (cs *EphemeralContractStore) Roots(id types.ElementID) ([]types.Hash256, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.roots[id], nil
}

// SetRoots updates the roots of the contract.
func (cs *EphemeralContractStore) SetRoots(id types.ElementID, roots []types.Hash256) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.roots[id] = append([]types.Hash256(nil), roots...)
	return nil
}

// ProcessChainApplyUpdate applies a consensus update to contracts in the
// contract store.
func (cs *EphemeralContractStore) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for id := range cs.contracts {
		if !cs.contracts[id].Confirmed || cs.contracts[id].State == corehost.ContractStateUnresolved {
			continue
		}

		// if the contract formation transaction was reverted, attempt to keep
		// the proofs updated.
		if !cs.contracts[id].Confirmed {
			updateTxnProofs(&cs.contracts[id].FormationTransaction, cau)
		}

		// if the contract is in the proof window update the history proof
		if cs.contracts[id].Revision.WindowStart == cau.Block.Header.Height {
			cs.contracts[id].StorageProof.WindowStart = cau.Context.Index
			cs.contracts[id].StorageProof.WindowProof = cau.HistoryProof()
		}

		if cau.Block.Header.Height >= cs.contracts[id].Revision.WindowStart {
			cau.UpdateWindowProof(&cs.contracts[id].StorageProof)
		}

		cau.UpdateElementProof(&cs.contracts[id].Parent.StateElement)
	}

	// update the state elements for any contracts created or revised in this
	// block.
	for _, fce := range cau.NewFileContracts {
		if _, exists := cs.contracts[fce.ID]; !exists {
			continue
		}
		cs.contracts[fce.ID].Parent = fce
		cs.contracts[fce.ID].Confirmed = true
	}
	for _, fce := range cau.RevisedFileContracts {
		if _, exists := cs.contracts[fce.ID]; !exists {
			continue
		}
		cs.contracts[fce.ID].Parent = fce
		cs.contracts[fce.ID].Confirmed = true
		cau.UpdateElementProof(&cs.contracts[fce.ID].Parent.StateElement)
	}
	resolutions := make(map[types.ElementID]types.FileContractResolution)
	for _, txn := range cau.Block.Transactions {
		for _, fcr := range txn.FileContractResolutions {
			resolutions[fcr.Parent.ID] = fcr
		}
	}
	for _, fce := range cau.ResolvedFileContracts {
		if _, exists := cs.contracts[fce.ID]; !exists {
			continue
		}
		cs.contracts[fce.ID].ResolutionHeight = cau.Context.Index.Height
		fcr := resolutions[fce.ID]

		var state corehost.ContractState
		switch {
		case fcr.HasRenewal():
			state = corehost.ContractStateRenewed
		case fcr.HasFinalization():
			state = corehost.ContractStateFinalized
		case fcr.HasStorageProof():
			state = corehost.ContractStateValid
		default:
			state = corehost.ContractStateMissed
		}
		cs.contracts[fce.ID].State = state
	}
	cs.index = cau.Context.Index
	return nil
}

// ProcessChainRevertUpdate reverts a consensus update from the contract store.
func (cs *EphemeralContractStore) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for id := range cs.contracts {
		cru.UpdateElementProof(&cs.contracts[id].Parent.StateElement)

		if cs.contracts[id].Revision.WindowStart > cru.Block.Header.Height {
			cru.UpdateWindowProof(&cs.contracts[id].StorageProof)
		}
	}

	for _, fce := range cru.NewFileContracts {
		if _, exists := cs.contracts[fce.ID]; !exists {
			continue
		}
		cs.contracts[fce.ID].Confirmed = false
		cs.contracts[fce.ID].ResolutionHeight = 0
		updateTxnProofs(&cs.contracts[fce.ID].FormationTransaction, cru)
	}

	for _, fce := range cru.RevisedFileContracts {
		if _, exists := cs.contracts[fce.ID]; !exists {
			continue
		}

		cs.contracts[fce.ID].Parent = fce
		cs.contracts[fce.ID].ResolutionHeight = 0
	}
	for _, fce := range cru.ResolvedFileContracts {
		if _, exists := cs.contracts[fce.ID]; !exists {
			continue
		}
		cs.contracts[fce.ID].ResolutionHeight = 0
	}
	cs.index = cru.Context.Index
	return nil
}

// ContractAction determines which contracts in the store need to have a lifecycle action taken. Implements ContractStore.ContractAction
func (cs *EphemeralContractStore) ContractAction(vc consensus.ValidationContext, contractFn func(consensus.ValidationContext, corehost.Contract)) error {
	cs.mu.Lock()
	var actionableContracts []corehost.Contract
	for _, c := range cs.contracts {
		if !c.Confirmed || c.ShouldSubmitResolution(vc.Index) || c.ShouldSubmitRevision(vc.Index) {
			actionableContracts = append(actionableContracts, *c)
		}
	}
	// global mutex must be unlocked before calling contractFn
	cs.mu.Unlock()

	for _, c := range actionableContracts {
		contractFn(vc, c)
	}
	return nil
}

// NewEphemeralContractStore initializes a new EphemeralContractStore.
func NewEphemeralContractStore() *EphemeralContractStore {
	return &EphemeralContractStore{
		contracts: make(map[types.ElementID]*corehost.Contract),
		roots:     make(map[types.ElementID][]types.Hash256),
	}
}

// EphemeralRegistryStore implements an in-memory registry key-value store.
// Implements host.RegistryStore.
type EphemeralRegistryStore struct {
	mu sync.Mutex

	cap    uint64
	values map[types.Hash256]rhp.RegistryValue
}

// Get returns the registry value for the given key.
func (es *EphemeralRegistryStore) Get(key types.Hash256) (rhp.RegistryValue, error) {
	es.mu.Lock()
	defer es.mu.Unlock()

	val, exists := es.values[key]
	if !exists {
		return rhp.RegistryValue{}, errors.New("not found")
	}
	return val, nil
}

// Set sets the registry value for the given key.
func (es *EphemeralRegistryStore) Set(key types.Hash256, value rhp.RegistryValue, expiration uint64) (rhp.RegistryValue, error) {
	es.mu.Lock()
	defer es.mu.Unlock()

	if _, exists := es.values[key]; !exists && uint64(len(es.values)) >= es.cap {
		return rhp.RegistryValue{}, errors.New("capacity exceeded")
	}

	es.values[key] = value
	return value, nil
}

// Len returns the number of entries in the registry.
func (es *EphemeralRegistryStore) Len() uint64 {
	es.mu.Lock()
	defer es.mu.Unlock()

	return uint64(len(es.values))
}

// Cap returns the maximum number of entries the registry can hold.
func (es *EphemeralRegistryStore) Cap() uint64 {
	return es.cap
}

// NewEphemeralRegistryStore initializes a new EphemeralRegistryStore.
func NewEphemeralRegistryStore(limit uint64) *EphemeralRegistryStore {
	return &EphemeralRegistryStore{
		cap:    limit,
		values: make(map[types.Hash256]rhp.RegistryValue),
	}
}

// StdOutLogger implements a logger that writes to stdout.
type StdOutLogger struct {
	scope string
}

func (sl *StdOutLogger) println(level string, msg string) {
	fmt.Fprintf(os.Stdout, "[%s] %s: %s\n", level, sl.scope, msg)
}

// Scope initializes a new logger with the given scope.
func (sl *StdOutLogger) Scope(scope string) host.Logger {
	if len(sl.scope) == 0 {
		return &StdOutLogger{
			scope: scope,
		}
	}
	return &StdOutLogger{
		scope: fmt.Sprintf("%s: %s", sl.scope, scope),
	}
}

// Errorf logs an error message.
func (sl *StdOutLogger) Errorf(f string, v ...interface{}) {
	sl.println("ERROR", fmt.Sprintf(f, v...))
}

// Errorln logs an error message.
func (sl *StdOutLogger) Errorln(v ...interface{}) {
	sl.println("ERROR", fmt.Sprint(v...))
}

// Infof logs a message at the INFO level.
func (sl *StdOutLogger) Infof(f string, v ...interface{}) {
	sl.println("INFO", fmt.Sprintf(f, v...))
}

// Infoln logs a message at the INFO level.
func (sl *StdOutLogger) Infoln(v ...interface{}) {
	sl.println("INFO", fmt.Sprint(v...))
}

// Warnf logs a warning message.
func (sl *StdOutLogger) Warnf(f string, v ...interface{}) {
	sl.println("WARN", fmt.Sprintf(f, v...))
}

// Warnln logs a message at the Warn level.
func (sl *StdOutLogger) Warnln(v ...interface{}) {
	sl.println("WARN", fmt.Sprint(v...))
}

// NewStdOutLogger initializes a new StdOutLogger.
func NewStdOutLogger() host.Logger {
	return &StdOutLogger{}
}

// An EphemeralMetricRecorder records host events to stdout and websocket subscribers.
type EphemeralMetricRecorder struct {
	subscribers map[host.EventSubscriber]struct{}
}

// RecordEvent records an event to stdout and notifies all subscribers.
func (em *EphemeralMetricRecorder) RecordEvent(m host.Event) {
	switch v := m.(type) {
	case host.EventSessionStart:
		fmt.Printf("[%s][SessionStart] Session UID: %x Renter IP: %s\n", time.Now(), v.UID, v.RenterIP)
	case host.EventSessionEnd:
		fmt.Printf("[%s][SessionEnd] Session UID: %x Duration: %vms\n", time.Now(), v.UID, v.Elapsed)
	case host.EventRPCStart:
		fmt.Printf("[%s][RPCStart] Session UID: %x RPC UID: %x RPC: %s\n", time.Now(), v.SessionUID, v.UID, v.RPC)
	case host.EventRPCEnd:
		fmt.Printf("[%s][RPCEnd] Session UID: %x RPC UID: %x Spent: %s Duration: %vms Write Bytes: %v Read Bytes: %v Error: %v\n", time.Now(), v.SessionUID, v.UID, v.Spending, v.Elapsed, v.WriteBytes, v.ReadBytes, v.Error)
	case host.EventInstructionExecuted:
		fmt.Printf("[%s][InstructionExecuted] Session UID: %x RPC UID: %x Program UID: %x Instruction: %s Write Bytes: %v Read Bytes: %v\n", v.StartTimestamp.Add(time.Millisecond*time.Duration(v.Elapsed)), v.SessionUID, v.RPCUID, v.ProgramUID, v.Instruction, v.WriteBytes, v.ReadBytes)
	case host.EventContractFormed:
		fmt.Printf("[%s][ContractFormed] Session UID: %x RPC UID: %x Contract ID: %v\n", time.Now(), v.SessionUID, v.RPCUID, v.Contract.ID)
	case host.EventContractRenewed:
		fmt.Printf("[%s][ContractRenewed] Session UID: %x RPC UID: %x Finalized ID: %v Renewed ID: %v\n", time.Now(), v.SessionUID, v.RPCUID, v.FinalizedContract.ID, v.RenewedContract.ID)
	}
	for sub := range em.subscribers {
		sub.OnEvent(m)
	}
}

// Subscribe adds the event subscriber
func (em *EphemeralMetricRecorder) Subscribe(es host.EventSubscriber) {
	em.subscribers[es] = struct{}{}
}

// Unsubscribe removes the event subscriber.
func (em *EphemeralMetricRecorder) Unsubscribe(es host.EventSubscriber) {
	delete(em.subscribers, es)
}

// NewEphemeralMetricRecorder initializes a new EphemeralMetricRecorder.
func NewEphemeralMetricRecorder() host.MetricRecorder {
	return &EphemeralMetricRecorder{
		subscribers: make(map[host.EventSubscriber]struct{}),
	}
}
