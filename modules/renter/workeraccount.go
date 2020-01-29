package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TODO: try to load account from persistence
//
// TODO: for now the account is a separate object that sits as first class
// object on the worker, most probably though this will move as to not have two
// separate mutex domains.

// account represents a renter's ephemeral account on a host.
type account struct {
	staticID        string
	staticHostKey   types.SiaPublicKey
	staticSecretKey crypto.SecretKey

	pendingSpends types.Currency
	pendingFunds  types.Currency
	balance       types.Currency

	mu sync.Mutex
	c  hostContractor
}

// openAccount returns an account for the given host. In the case it does
// not exist yet, it gets created. Every time a new account is created, a new
// keypair is used.
func openAccount(hostKey types.SiaPublicKey, contractor hostContractor) *account {
	// generate a new key pair
	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// create the account
	return &account{
		staticID:        spk.String(),
		staticHostKey:   hostKey,
		staticSecretKey: sk,
		c:               contractor,
	}
}

// AvailableBalance returns the amount of money that is available to spend. It
// is calculated by taking into account pending spends and pending funds.
func (a *account) AvailableBalance() types.Currency {
	a.mu.Lock()
	defer a.mu.Unlock()

	total := a.balance.Add(a.pendingFunds)
	if a.pendingSpends.Cmp(total) < 0 {
		return total.Sub(a.pendingSpends)
	}
	return types.ZeroCurrency
}
