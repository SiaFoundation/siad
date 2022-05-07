package explorer

import (
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
)

// ChainStats contains a bunch of statistics about the consensus set as they
// were at a specific block.
type ChainStats struct {
	Block types.Block

	// Transaction type counts.
	SpentSiacoinsCount uint64
	SpentSiafundsCount uint64

	// Facts about file contracts.
	ActiveContractCost  types.Currency
	ActiveContractCount uint64
	ActiveContractSize  uint64
	TotalContractCost   types.Currency
	TotalContractSize   uint64
	TotalRevisionVolume uint64
}

// EncodeTo implements types.EncoderTo.
func (cs ChainStats) EncodeTo(e *types.Encoder) {
	cs.Block.Header.EncodeTo(e)
	e.WritePrefix(len(cs.Block.Transactions))
	for _, txn := range cs.Block.Transactions {
		txn.EncodeTo(e)
	}
	e.WriteUint64(cs.SpentSiacoinsCount)
	e.WriteUint64(cs.SpentSiafundsCount)
	cs.ActiveContractCost.EncodeTo(e)
	e.WriteUint64(cs.ActiveContractCount)
	e.WriteUint64(cs.ActiveContractSize)
	cs.TotalContractCost.EncodeTo(e)
	e.WriteUint64(cs.TotalContractSize)
	e.WriteUint64(cs.TotalRevisionVolume)
}

// DecodeFrom implements types.DecoderFrom.
func (cs *ChainStats) DecodeFrom(d *types.Decoder) {
	cs.Block.Header.DecodeFrom(d)
	cs.Block.Transactions = make([]types.Transaction, d.ReadPrefix())
	for i := range cs.Block.Transactions {
		cs.Block.Transactions[i].DecodeFrom(d)
	}
	cs.SpentSiacoinsCount = d.ReadUint64()
	cs.SpentSiafundsCount = d.ReadUint64()
	cs.ActiveContractCost.DecodeFrom(d)
	cs.ActiveContractCount = d.ReadUint64()
	cs.ActiveContractSize = d.ReadUint64()
	cs.TotalContractCost.DecodeFrom(d)
	cs.TotalContractSize = d.ReadUint64()
	cs.TotalRevisionVolume = d.ReadUint64()
}

// ChainStatsLatest returns stats about the latest black.
func (e *Explorer) ChainStatsLatest() (ChainStats, error) {
	return e.ChainStats(e.cs.Index)
}

// ChainStats returns stats about the black at the the specified height.
func (e *Explorer) ChainStats(index types.ChainIndex) (ChainStats, error) {
	return e.db.ChainStats(index)
}

// SiacoinBalance returns the siacoin balance of an address.
func (e *Explorer) SiacoinBalance(address types.Address) (types.Currency, error) {
	ids, err := e.UnspentSiacoinElements(address)
	if err != nil {
		return types.Currency{}, err
	}

	var sum types.Currency
	for _, id := range ids {
		elem, err := e.SiacoinElement(id)
		if err != nil {
			return types.Currency{}, err
		}
		sum = sum.Add(elem.Value)
	}
	return sum, nil
}

// SiafundBalance returns the siafund balance of an address.
func (e *Explorer) SiafundBalance(address types.Address) (uint64, error) {
	ids, err := e.UnspentSiafundElements(address)
	if err != nil {
		return 0, err
	}

	var sum uint64
	for _, id := range ids {
		elem, err := e.SiafundElement(id)
		if err != nil {
			return 0, err
		}
		sum += elem.Value
	}
	return sum, nil
}

// UnspentSiacoinElements returns unspent siacoin elements associated with the
// specified address.
func (e *Explorer) UnspentSiacoinElements(address types.Address) ([]types.ElementID, error) {
	return e.db.UnspentSiacoinElements(address)
}

// UnspentSiafundElements returns unspent siafund elements associated with the
// specified address.
func (e *Explorer) UnspentSiafundElements(address types.Address) ([]types.ElementID, error) {
	return e.db.UnspentSiafundElements(address)
}

// Transactions returns the latest n transaction IDs associated with the
// specified address.
func (e *Explorer) Transactions(address types.Address, amount, offset int) ([]types.TransactionID, error) {
	return e.db.Transactions(address, amount, offset)
}

// SiacoinElement returns the siacoin element associated with the specified ID.
func (e *Explorer) SiacoinElement(id types.ElementID) (types.SiacoinElement, error) {
	return e.db.SiacoinElement(id)
}

// SiafundElement returns the siafund element associated with the specified ID.
func (e *Explorer) SiafundElement(id types.ElementID) (types.SiafundElement, error) {
	return e.db.SiafundElement(id)
}

// FileContractElement returns the file contract element associated with the specified ID.
func (e *Explorer) FileContractElement(id types.ElementID) (types.FileContractElement, error) {
	return e.db.FileContractElement(id)
}

// Transaction returns the transaction with the given ID.
func (e *Explorer) Transaction(id types.TransactionID) (types.Transaction, error) {
	return e.db.Transaction(id)
}

// State returns the chain state for a given chain index.
func (e *Explorer) State(index types.ChainIndex) (consensus.State, error) {
	return e.db.State(index)
}
