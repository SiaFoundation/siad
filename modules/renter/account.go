package renter

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// TODO: added in separate MR

// account represents a renter's ephemeral account on a host.
type account struct {
	staticID string
}

// openAccount returns a new account for the given host. Every time this
// a new account is opened, it's created using a new keypair.
func (r *Renter) openAccount(hostKey types.SiaPublicKey) *account {
	// TODO: added in separate MR. This will call managedOpenAccount on the
	// renter, which keeps a map of  accounts, distinct to the host
	return &account{staticID: "TODO"}
}

// Balance returns the eventual account balance. This is calculated taking into
// account pending spends and pending funds.
func (a *account) Balance() types.Currency {
	// TODO: added in separate MR. This returns the eventual balance, this is
	// the balance that the account is going to have, after all pending funds
	// and spends are processed/consumed.
	return types.ZeroCurrency
}
