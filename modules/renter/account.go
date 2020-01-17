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

// openAccount returns a new account for the given host. Every time this
// a new account is opened, it's created using a new keypair.
func (r *Renter) openAccount(hostKey types.SiaPublicKey) *account {
	hpk := hostKey.String()
	acc, exists := r.accounts[hpk]
	if exists {
		return acc
	}

	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

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

// Balance returns the eventual account balance. This is calculated taking into
// account pending spends and pending funds.
func (a *account) Balance() types.Currency {
	a.mu.Lock()
	defer a.mu.Unlock()

	total := a.balance.Add(a.pendingFunds)
	var eventual types.Currency
	if a.pendingSpends.Cmp(total) < 0 {
		eventual = total.Sub(a.pendingSpends)
	}
	return eventual
}
