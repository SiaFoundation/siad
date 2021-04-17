package modules

import (
	"go.sia.tech/siad/types"
)

const (
	// AccountingDir is the name of the directory that is used to store the
	// accounting persistent data
	AccountingDir = "accounting"
)

type (
	// AccountingInfo contains accounting information relating to various modules.
	AccountingInfo struct {
		// Not implemented yet
		//
		// FeeManager FeeManagerAccounting `json:"feemanager"`
		// Host       HostAccounting       `json:"host"`
		// Miner      MinerAccounting      `json:"miner"`

		Renter RenterAccounting `json:"renter"`
		Wallet WalletAccounting `json:"wallet"`
	}

	// RenterAccounting contains the accounting information related to the Renter
	// Module
	RenterAccounting struct {
		// UnspentUnallocated are the funds currently tied up in the current period
		// contracts that have not been allocated for upload, download, or storage
		// spending.
		UnspentUnallocated types.Currency `json:"unspentunallocated"`

		// WithheldFunds are the funds currently tied up in expired contracts that
		// have not been released yet.
		WithheldFunds types.Currency `json:"withheldfunds"`
	}

	// WalletAccounting contains the accounting information related to the Wallet
	// Module
	WalletAccounting struct {
		// ConfirmedSiacoinBalance is the confirmed siacoin balance of the wallet
		ConfirmedSiacoinBalance types.Currency `json:"confirmedsiacoinbalance"`

		// ConfirmedSiafundBalance is the confirmed siafund balance of the wallet
		ConfirmedSiafundBalance types.Currency `json:"confirmedsiafundbalance"`
	}
)

// Accounting is an interface for getting accounting information about the Sia
// node.
type Accounting interface {
	// Accounting returns the current accounting information
	Accounting() (AccountingInfo, error)

	// Close closes the accounting module
	Close() error
}
