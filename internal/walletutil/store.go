package walletutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"

	"go.sia.tech/siad/v2/wallet"
)

// EphemeralStore implements wallet.Store in memory.
type EphemeralStore struct {
	mu        sync.Mutex
	tip       types.ChainIndex
	seedIndex uint64
	addrs     map[types.Address]wallet.AddressInfo
	scElems   []types.SiacoinElement
	sfElems   []types.SiafundElement
	txns      []wallet.Transaction
}

// SeedIndex implements wallet.Store.
func (s *EphemeralStore) SeedIndex() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedIndex
}

// Balance implements wallet.Store.
func (s *EphemeralStore) Balance() (sc types.Currency, sf uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sce := range s.scElems {
		if sce.MaturityHeight < s.tip.Height {
			sc = sc.Add(sce.Value)
		}
	}
	for _, sfe := range s.sfElems {
		sf += sfe.Value
	}
	return
}

// AddAddress implements wallet.Store.
func (s *EphemeralStore) AddAddress(addr types.Address, info wallet.AddressInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addrs[addr] = info
	if info.Index >= s.seedIndex {
		s.seedIndex = info.Index + 1
	}
	return nil
}

// AddressInfo implements wallet.Store.
func (s *EphemeralStore) AddressInfo(addr types.Address) (wallet.AddressInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.addrs[addr]
	if !ok {
		return wallet.AddressInfo{}, wallet.ErrUnknownAddress
	}
	return info, nil
}

// Addresses implements wallet.Store.
func (s *EphemeralStore) Addresses() ([]types.Address, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addrs := make([]types.Address, 0, len(s.addrs))
	for addr := range s.addrs {
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// UnspentSiacoinElements implements wallet.Store.
func (s *EphemeralStore) UnspentSiacoinElements() ([]types.SiacoinElement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var elems []types.SiacoinElement
	for _, sce := range s.scElems {
		sce.MerkleProof = append([]types.Hash256(nil), sce.MerkleProof...)
		elems = append(elems, sce)
	}
	return elems, nil
}

// UnspentSiafundElements implements wallet.Store.
func (s *EphemeralStore) UnspentSiafundElements() ([]types.SiafundElement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var elems []types.SiafundElement
	for _, sce := range s.sfElems {
		sce.MerkleProof = append([]types.Hash256(nil), sce.MerkleProof...)
		elems = append(elems, sce)
	}
	return elems, nil
}

// Transactions returns all transactions relevant to the wallet, ordered
// oldest-to-newest.
func (s *EphemeralStore) Transactions(since time.Time, max int) ([]wallet.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var txns []wallet.Transaction
	for _, txn := range s.txns {
		if len(txns) == max {
			break
		} else if txn.Timestamp.After(since) {
			txns = append(txns, txn)
		}
	}
	return txns, nil
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (s *EphemeralStore) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// delete spent elements
	remSC := s.scElems[:0]
	for _, sce := range s.scElems {
		if !cau.SiacoinElementWasSpent(sce) {
			remSC = append(remSC, sce)
		}
	}
	s.scElems = remSC
	remSF := s.sfElems[:0]
	for _, sfe := range s.sfElems {
		if !cau.SiafundElementWasSpent(sfe) {
			remSF = append(remSF, sfe)
		}
	}
	s.sfElems = remSF

	// update proofs for our elements
	for i := range s.scElems {
		cau.UpdateElementProof(&s.scElems[i].StateElement)
	}
	for i := range s.sfElems {
		cau.UpdateElementProof(&s.sfElems[i].StateElement)
	}

	// add new elements
	for _, o := range cau.NewSiacoinElements {
		if _, ok := s.addrs[o.Address]; ok {
			s.scElems = append(s.scElems, o)
		}
	}
	for _, o := range cau.NewSiafundElements {
		if _, ok := s.addrs[o.Address]; ok {
			s.sfElems = append(s.sfElems, o)
		}
	}

	// add relevant transactions
	for _, txn := range cau.Block.Transactions {
		// a transaction is relevant if any of its inputs or outputs reference a
		// wallet-controlled address
		var inflow, outflow types.Currency
		for _, out := range txn.SiacoinOutputs {
			if _, ok := s.addrs[out.Address]; ok {
				inflow = inflow.Add(out.Value)
			}
		}
		for _, in := range txn.SiacoinInputs {
			if _, ok := s.addrs[in.Parent.Address]; ok {
				outflow = outflow.Add(in.Parent.Value)
			}
		}
		if !inflow.IsZero() || !outflow.IsZero() {
			s.txns = append(s.txns, wallet.Transaction{
				Raw:       txn.DeepCopy(),
				Index:     cau.Context.Index, // same as cau.Block.Index()
				ID:        txn.ID(),
				Inflow:    inflow,
				Outflow:   outflow,
				Timestamp: cau.Block.Header.Timestamp,
			})
		}
	}

	s.tip = cau.Context.Index
	return nil
}

// ProcessChainRevertUpdate implements chain.Subscriber.
func (s *EphemeralStore) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// delete removed elements
	remSC := s.scElems[:0]
	for _, o := range s.scElems {
		if !cru.SiacoinElementWasRemoved(o) {
			remSC = append(remSC, o)
		}
	}
	s.scElems = remSC
	remSF := s.sfElems[:0]
	for _, o := range s.sfElems {
		if !cru.SiafundElementWasRemoved(o) {
			remSF = append(remSF, o)
		}
	}
	s.sfElems = remSF

	// re-add elements that were spent in the reverted block
	for _, o := range cru.SpentSiacoins {
		if _, ok := s.addrs[o.Address]; ok {
			o.MerkleProof = append([]types.Hash256(nil), o.MerkleProof...)
			s.scElems = append(s.scElems, o)
		}
	}
	for _, o := range cru.SpentSiafunds {
		if _, ok := s.addrs[o.Address]; ok {
			o.MerkleProof = append([]types.Hash256(nil), o.MerkleProof...)
			s.sfElems = append(s.sfElems, o)
		}
	}

	// update proofs for our elements
	for i := range s.scElems {
		cru.UpdateElementProof(&s.scElems[i].StateElement)
	}
	for i := range s.sfElems {
		cru.UpdateElementProof(&s.sfElems[i].StateElement)
	}

	// delete transactions originating in this block
	index := cru.Block.Index()
	for i, txn := range s.txns {
		if txn.Index == index {
			s.txns = s.txns[:i]
			break
		}
	}

	s.tip = cru.Context.Index
	return nil
}

// NewEphemeralStore returns a new EphemeralStore.
func NewEphemeralStore(tip types.ChainIndex) *EphemeralStore {
	return &EphemeralStore{
		tip:   tip,
		addrs: make(map[types.Address]wallet.AddressInfo),
	}
}

// JSONStore implements wallet.Store in memory, backed by a JSON file.
type JSONStore struct {
	*EphemeralStore
	dir string
}

type jsonPersistData struct {
	Tip             types.ChainIndex
	SeedIndex       uint64
	Addrs           map[types.Address]wallet.AddressInfo
	SiacoinElements []types.SiacoinElement
	SiafundElements []types.SiafundElement
	Transactions    []wallet.Transaction
}

func (s *JSONStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	js, _ := json.MarshalIndent(jsonPersistData{
		Tip:             s.tip,
		SeedIndex:       s.seedIndex,
		Addrs:           s.addrs,
		SiacoinElements: s.scElems,
		SiafundElements: s.sfElems,
		Transactions:    s.txns,
	}, "", "  ")

	// atomic save
	dst := filepath.Join(s.dir, "wallet.json")
	f, err := os.OpenFile(dst+"_tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0660)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(js); err != nil {
		return err
	} else if f.Sync(); err != nil {
		return err
	} else if f.Close(); err != nil {
		return err
	} else if err := os.Rename(dst+"_tmp", dst); err != nil {
		return err
	}
	return nil
}

func (s *JSONStore) load(tip types.ChainIndex) (types.ChainIndex, error) {
	var p jsonPersistData
	if js, err := os.ReadFile(filepath.Join(s.dir, "wallet.json")); os.IsNotExist(err) {
		// set defaults
		s.tip = tip
		s.addrs = make(map[types.Address]wallet.AddressInfo)
		return tip, nil
	} else if err != nil {
		return types.ChainIndex{}, err
	} else if err := json.Unmarshal(js, &p); err != nil {
		return types.ChainIndex{}, err
	}
	s.tip = tip
	s.seedIndex = p.SeedIndex
	s.addrs = p.Addrs
	s.scElems = p.SiacoinElements
	s.sfElems = p.SiafundElements
	s.txns = p.Transactions
	return p.Tip, nil
}

// AddAddress implements wallet.Store.
func (s *JSONStore) AddAddress(addr types.Address, info wallet.AddressInfo) error {
	s.EphemeralStore.AddAddress(addr, info)
	return s.save()
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (s *JSONStore) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	s.EphemeralStore.ProcessChainApplyUpdate(cau, mayCommit)
	if mayCommit {
		return s.save()
	}
	return nil
}

// NewJSONStore returns a new JSONStore.
func NewJSONStore(dir string, tip types.ChainIndex) (*JSONStore, types.ChainIndex, error) {
	s := &JSONStore{
		EphemeralStore: NewEphemeralStore(tip),
		dir:            dir,
	}
	tip, err := s.load(tip)
	if err != nil {
		return nil, types.ChainIndex{}, err
	}
	return s, tip, nil
}

// A TestingWallet is a simple hot wallet, useful for sending and receiving
// transactions in a testing environment.
type TestingWallet struct {
	mu sync.Mutex
	*EphemeralStore
	Seed wallet.Seed
	tb   *wallet.TransactionBuilder
	vc   consensus.ValidationContext
}

// Balance returns the wallet's siacoin balance.
func (w *TestingWallet) Balance() types.Currency {
	sc, _ := w.EphemeralStore.Balance()
	return sc
}

// Address returns an address controlled by the wallet.
func (w *TestingWallet) Address() types.Address {
	w.mu.Lock()
	defer w.mu.Unlock()
	return types.StandardAddress(w.Seed.PublicKey(w.SeedIndex()))
}

// NewAddress derives a new address and adds it to the wallet.
func (w *TestingWallet) NewAddress() types.Address {
	w.mu.Lock()
	defer w.mu.Unlock()
	info := wallet.AddressInfo{
		Index: w.SeedIndex(),
	}
	addr := types.StandardAddress(w.Seed.PublicKey(info.Index))
	w.AddAddress(addr, info)
	return addr
}

// FundTransaction funds the provided transaction, adding a change output if
// necessary.
func (w *TestingWallet) FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	toSign, err := w.tb.FundSiacoins(w.vc, txn, amount, w.Seed, pool)
	return toSign, func() { w.tb.ReleaseInputs(*txn) }, err
}

// SignTransaction funds the provided transaction, adding a change output if
// necessary.
func (w *TestingWallet) SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tb.SignTransaction(vc, txn, toSign, w.Seed)
}

// FundAndSign funds and signs the provided transaction, adding a change output
// if necessary.
func (w *TestingWallet) FundAndSign(txn *types.Transaction) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var amount types.Currency
	for _, sco := range txn.SiacoinOutputs {
		amount = amount.Add(sco.Value)
	}
	amount = amount.Add(txn.MinerFee)
	for _, sci := range txn.SiacoinInputs {
		amount = amount.Sub(sci.Parent.Value)
	}

	toSign, err := w.tb.FundSiacoins(w.vc, txn, amount, w.Seed, nil)
	if err != nil {
		return err
	}
	return w.tb.SignTransaction(w.vc, txn, toSign, w.Seed)
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (w *TestingWallet) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.EphemeralStore.ProcessChainApplyUpdate(cau, mayCommit)
	w.vc = cau.Context
	return nil
}

// ProcessChainRevertUpdate implements chain.Subscriber.
func (w *TestingWallet) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.EphemeralStore.ProcessChainRevertUpdate(cru)
	w.vc = cru.Context
	return nil
}

// NewTestingWallet creates a TestingWallet with the provided validation context
// and an ephemeral store.
func NewTestingWallet(vc consensus.ValidationContext) *TestingWallet {
	store := NewEphemeralStore(vc.Index)
	return &TestingWallet{
		EphemeralStore: store,
		Seed:           wallet.NewSeed(),
		tb:             wallet.NewTransactionBuilder(store),
		vc:             vc,
	}
}
