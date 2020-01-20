package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

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
	r  *Renter
}

// managedOpenAccount returns an account for the given host. In the case it does
// not exist yet, it gets created. Every time a new account is created, a new
// keypair is used.
func (r *Renter) managedOpenAccount(hostKey types.SiaPublicKey) *account {
	id := r.mu.Lock()
	defer r.mu.Unlock(id)

	hpk := hostKey.String()
	acc, exists := r.accounts[hpk]
	if exists {
		return acc
	}

	// generate a new key pair
	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// create the account and set it on the renter
	acc = &account{
		staticID:        spk.String(),
		staticHostKey:   hostKey,
		staticSecretKey: sk,
		c:               r.hostContractor,
		r:               r,
	}
	r.accounts[hpk] = acc
	return acc
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
