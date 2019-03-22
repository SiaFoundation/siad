package siatest

import (
	"math"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// ConfirmedBalance returns the confirmed siacoin balance of the node's
// wallet.
func (tn *TestNode) ConfirmedBalance() (types.Currency, error) {
	wg, err := tn.WalletGet()
	return wg.ConfirmedSiacoinBalance, err
}

// ConfirmedTransactions returns all of the wallet's tracked confirmed
// transactions.
func (tn *TestNode) ConfirmedTransactions() ([]modules.ProcessedTransaction, error) {
	wtg, err := tn.WalletTransactionsGet(0, math.MaxUint64)
	return wtg.ConfirmedTransactions, err
}
