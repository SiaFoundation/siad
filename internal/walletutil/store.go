package walletutil

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"

	"go.sia.tech/siad/v2/wallet"
)

// EphemeralStore implements wallet.Store in memory.
type EphemeralStore struct {
	mu        sync.Mutex
	addrs     map[types.Address]uint64
	elems     []types.SiacoinElement
	txns      []wallet.Transaction
	tip       types.ChainIndex
	seedIndex uint64
}

// SeedIndex implements wallet.Store.
func (s *EphemeralStore) SeedIndex() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedIndex
}

// AddAddress implements wallet.Store.
func (s *EphemeralStore) AddAddress(addr types.Address, index uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.addrs[addr] = index
	if index >= s.seedIndex {
		s.seedIndex = index + 1
	}
	return nil
}

// AddressIndex implements wallet.Store.
func (s *EphemeralStore) AddressIndex(addr types.Address) (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	index, ok := s.addrs[addr]
	return index, ok
}

// SpendableSiacoinElements implements wallet.Store.
func (s *EphemeralStore) SpendableSiacoinElements() []types.SiacoinElement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var elems []types.SiacoinElement
	for _, sce := range s.elems {
		if s.tip.Height >= sce.MaturityHeight {
			sce.MerkleProof = append([]types.Hash256(nil), sce.MerkleProof...)
			elems = append(elems, sce)
		}
	}
	return elems
}

// Transactions returns all transactions relevant to the wallet, ordered
// oldest-to-newest.
func (s *EphemeralStore) Transactions(since time.Time, max int) (txns []wallet.Transaction) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, txn := range s.txns {
		if len(txns) == max {
			return
		} else if txn.Timestamp.After(since) {
			txns = append(txns, txn)
		}
	}
	return
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (s *EphemeralStore) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, _ bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// delete spent elements
	rem := s.elems[:0]
	for _, sce := range s.elems {
		if !cau.SiacoinElementWasSpent(sce) {
			rem = append(rem, sce)
		}
	}
	s.elems = rem

	// update proofs for our elements
	for i := range s.elems {
		cau.UpdateElementProof(&s.elems[i].StateElement)
	}

	// add new elements
	for _, o := range cau.NewSiacoinElements {
		if _, ok := s.addrs[o.Address]; ok {
			s.elems = append(s.elems, o)
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
	rem := s.elems[:0]
	for _, o := range s.elems {
		if !cru.SiacoinElementWasRemoved(o) {
			rem = append(rem, o)
		}
	}
	s.elems = rem

	// re-add elements that were spent in the reverted block
	for _, o := range cru.SpentSiacoins {
		if _, ok := s.addrs[o.Address]; ok {
			o.MerkleProof = append([]types.Hash256(nil), o.MerkleProof...)
			s.elems = append(s.elems, o)
		}
	}

	// update proofs for our elements
	for i := range s.elems {
		cru.UpdateElementProof(&s.elems[i].StateElement)
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
func NewEphemeralStore() *EphemeralStore {
	return &EphemeralStore{
		addrs: make(map[types.Address]uint64),
	}
}

// JSONStore implements wallet.Store in memory, backed by a JSON file.
type JSONStore struct {
	*EphemeralStore
	dir string
}

type persistData struct {
	Tip          types.ChainIndex
	SeedIndex    uint64
	Addrs        map[string]uint64
	Elements     []types.SiacoinElement
	Transactions []wallet.Transaction
}

func (s *JSONStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	addrs := make(map[string]uint64, len(s.addrs))
	for k, v := range s.addrs {
		addrs[hex.EncodeToString(k[:])] = v
	}
	js, _ := json.MarshalIndent(persistData{
		Tip:          s.tip,
		SeedIndex:    s.seedIndex,
		Addrs:        addrs,
		Elements:     s.elems,
		Transactions: s.txns,
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
	var p persistData
	if js, err := os.ReadFile(filepath.Join(s.dir, "wallet.json")); os.IsNotExist(err) {
		// set defaults
		s.addrs = make(map[types.Address]uint64)
		s.tip = tip
		return tip, nil
	} else if err != nil {
		return types.ChainIndex{}, err
	} else if err := json.Unmarshal(js, &p); err != nil {
		return types.ChainIndex{}, err
	}
	s.addrs = make(map[types.Address]uint64, len(p.Addrs))
	for k, v := range p.Addrs {
		var addr types.Address
		hex.Decode(addr[:], []byte(k))
		s.addrs[addr] = v
	}
	s.elems = p.Elements
	s.txns = p.Transactions
	s.tip = tip
	s.seedIndex = p.SeedIndex
	return p.Tip, nil
}

// AddAddress implements wallet.Store.
func (s *JSONStore) AddAddress(addr types.Address, index uint64) error {
	s.EphemeralStore.AddAddress(addr, index)
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

// ProcessChainRevertUpdate implements chain.Subscriber.
func (s *JSONStore) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	return s.EphemeralStore.ProcessChainRevertUpdate(cru)
}

// NewJSONStore returns a new JSONStore.
func NewJSONStore(dir string, tip types.ChainIndex) (*JSONStore, types.ChainIndex, error) {
	s := &JSONStore{
		EphemeralStore: NewEphemeralStore(),
		dir:            dir,
	}
	tip, err := s.load(tip)
	if err != nil {
		return nil, types.ChainIndex{}, err
	}
	return s, tip, nil
}
