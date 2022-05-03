package wallet

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
)

// A Seed generates ed25519 keys deterministically from some initial entropy.
type Seed struct {
	entropy [16]byte
}

// String implements fmt.Stringer.
func (s Seed) String() string { return hex.EncodeToString(s.entropy[:]) }

// deriveKeyPair derives the keypair for the specified index.
func (s Seed) deriveKeyPair(index uint64) types.PrivateKey {
	buf := make([]byte, len(s.entropy)+8)
	n := copy(buf, s.entropy[:])
	binary.LittleEndian.PutUint64(buf[n:], index)
	return types.NewPrivateKeyFromSeed(types.HashBytes(buf))
}

// PublicKey derives the types.SiaPublicKey for the specified index.
func (s Seed) PublicKey(index uint64) (pk types.PublicKey) {
	key := s.deriveKeyPair(index)
	copy(pk[:], key[32:])
	return pk
}

// PrivateKey derives the ed25519 private key for the specified index.
func (s Seed) PrivateKey(index uint64) types.PrivateKey {
	return s.deriveKeyPair(index)
}

// SeedFromEntropy returns the Seed derived from the supplied entropy.
func SeedFromEntropy(entropy [16]byte) Seed {
	return Seed{entropy: entropy}
}

// SeedFromString returns the Seed derived from the supplied string.
func SeedFromString(s string) (Seed, error) {
	var entropy [16]byte
	if n, err := hex.Decode(entropy[:], []byte(s)); err != nil {
		return Seed{}, fmt.Errorf("seed string contained invalid characters: %w", err)
	} else if n != 16 {
		return Seed{}, errors.New("invalid seed string length")
	}
	return SeedFromEntropy(entropy), nil
}

// NewSeed returns a random Seed.
func NewSeed() Seed {
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		panic("insufficient system entropy")
	}
	return SeedFromEntropy(entropy)
}

// AddressInfo contains useful metadata about an address.
type AddressInfo struct {
	Index       uint64 `json:"index"`
	Description string `json:"description"`
}

// A Transaction is an on-chain transaction relevant to a particular wallet,
// paired with useful metadata.
type Transaction struct {
	Raw       types.Transaction
	Index     types.ChainIndex
	ID        types.TransactionID
	Inflow    types.Currency
	Outflow   types.Currency
	Timestamp time.Time
}

// ErrUnknownAddress is returned by Store.AddressInfo for addresses not known to
// the wallet.
var ErrUnknownAddress = errors.New("address not tracked by wallet")

// A Store stores wallet state. Implementations are assumed to be thread safe.
type Store interface {
	SeedIndex() uint64
	Balance() (types.Currency, uint64)
	AddAddress(addr types.Address, info AddressInfo) error
	AddressInfo(addr types.Address) (AddressInfo, error)
	Addresses() ([]types.Address, error)
	UnspentSiacoinElements() ([]types.SiacoinElement, error)
	UnspentSiafundElements() ([]types.SiafundElement, error)
	Transactions(since time.Time, max int) ([]Transaction, error)
}

// A TransactionBuilder helps construct transactions.
type TransactionBuilder struct {
	mu    sync.Mutex
	store Store
	used  map[types.ElementID]bool
}

// ReleaseInputs is a helper function that releases the inputs of txn for
// use in other transactions. It should only be called on transactions that are
// invalid or will never be broadcast.
func (tb *TransactionBuilder) ReleaseInputs(txn types.Transaction) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	for _, in := range txn.SiacoinInputs {
		delete(tb.used, in.Parent.ID)
	}
	for _, in := range txn.SiafundInputs {
		delete(tb.used, in.Parent.ID)
	}
}

// ReserveSiacoins returns siacoins inputs worth at least the requested amount.
// The inputs will not be available to future calls to ReserveSiacoins or
// FundSiacoins unless ReleaseInputs is called.
func (tb *TransactionBuilder) ReserveSiacoins(amount types.Currency, pool []types.Transaction) ([]types.SiacoinElement, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if amount.IsZero() {
		return nil, nil
	}

	// avoid reusing any inputs currently in the transaction pool
	inPool := make(map[types.ElementID]bool)
	for _, ptxn := range pool {
		for _, in := range ptxn.SiacoinInputs {
			inPool[in.Parent.ID] = true
		}
	}

	utxos, err := tb.store.UnspentSiacoinElements()
	if err != nil {
		return nil, err
	}
	var outputSum types.Currency
	var fundingElements []types.SiacoinElement
	for _, sce := range utxos {
		if tb.used[sce.ID] || inPool[sce.ID] {
			continue
		}
		fundingElements = append(fundingElements, sce)
		outputSum = outputSum.Add(sce.Value)
		if outputSum.Cmp(amount) >= 0 {
			break
		}
	}
	if outputSum.Cmp(amount) < 0 {
		return nil, errors.New("insufficient balance")
	}

	for _, o := range fundingElements {
		tb.used[o.ID] = true
	}
	return fundingElements, nil
}

// FundSiacoins adds siacoins inputs worth at least the requested amount to the
// provided transaction. A change output is also added if necessary. The inputs
// will not be available to future calls to ReserveSiacoins or FundSiacoins
// unless ReleaseInputs is called.
func (tb *TransactionBuilder) FundSiacoins(cs consensus.State, txn *types.Transaction, amount types.Currency, seed Seed, pool []types.Transaction) ([]types.ElementID, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if amount.IsZero() {
		return nil, nil
	}

	// avoid reusing any inputs currently in the transaction pool
	inPool := make(map[types.ElementID]bool)
	for _, ptxn := range pool {
		for _, in := range ptxn.SiacoinInputs {
			inPool[in.Parent.ID] = true
		}
	}

	utxos, err := tb.store.UnspentSiacoinElements()
	if err != nil {
		return nil, err
	}
	var outputSum types.Currency
	var fundingElements []types.SiacoinElement
	for _, sce := range utxos {
		if tb.used[sce.ID] || inPool[sce.ID] || cs.Index.Height < sce.MaturityHeight {
			continue
		}
		fundingElements = append(fundingElements, sce)
		outputSum = outputSum.Add(sce.Value)
		if outputSum.Cmp(amount) >= 0 {
			break
		}
	}
	if outputSum.Cmp(amount) < 0 {
		return nil, errors.New("insufficient balance")
	} else if outputSum.Cmp(amount) > 0 {
		// generate a change address
		info := AddressInfo{
			Index:       tb.store.SeedIndex(),
			Description: "change addr for " + txn.ID().String(),
		}
		addr := types.StandardAddress(seed.PublicKey(info.Index))
		if err := tb.store.AddAddress(addr, info); err != nil {
			return nil, err
		}
		txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
			Value:   outputSum.Sub(amount),
			Address: addr,
		})
	}

	toSign := make([]types.ElementID, len(fundingElements))
	for i, sce := range fundingElements {
		info, err := tb.store.AddressInfo(sce.Address)
		if err != nil {
			return nil, err
		}
		txn.SiacoinInputs = append(txn.SiacoinInputs, types.SiacoinInput{
			Parent:      sce,
			SpendPolicy: types.PolicyPublicKey(seed.PublicKey(info.Index)),
		})

		toSign[i] = sce.ID
		tb.used[sce.ID] = true
	}

	return toSign, nil
}

// SignTransaction adds a signature to each of the specified inputs using the
// provided seed. If len(toSign) == 0, a signature is added for every input with
// a known key.
func (tb *TransactionBuilder) SignTransaction(cs consensus.State, txn *types.Transaction, toSign []types.ElementID, seed Seed) error {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	sigHash := cs.InputSigHash(*txn)

	if len(toSign) == 0 {
		for _, in := range txn.SiacoinInputs {
			if info, err := tb.store.AddressInfo(in.Parent.Address); err == nil {
				in.Signatures = []types.Signature{seed.PrivateKey(info.Index).SignHash(sigHash)}
			} else if err != ErrUnknownAddress {
				return err
			}
		}
		return nil
	}

	inputWithID := func(id types.ElementID) *types.SiacoinInput {
		for i := range txn.SiacoinInputs {
			if in := &txn.SiacoinInputs[i]; in.Parent.ID == id {
				return in
			}
		}
		return nil
	}
	for _, id := range toSign {
		in := inputWithID(id)
		if in == nil {
			return errors.New("no input with specified ID")
		}
		info, err := tb.store.AddressInfo(in.Parent.Address)
		if err == ErrUnknownAddress {
			return errors.New("no key for specified input")
		} else if err != nil {
			return err
		}
		in.Signatures = append(in.Signatures, seed.PrivateKey(info.Index).SignHash(sigHash))
	}
	return nil
}

// NewTransactionBuilder returns a TransactionBuilder using the provided Store.
func NewTransactionBuilder(store Store) *TransactionBuilder {
	return &TransactionBuilder{
		store: store,
		used:  make(map[types.ElementID]bool),
	}
}
