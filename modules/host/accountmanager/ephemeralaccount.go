package accountmanager

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// EphemeralAccount represents an account which is kept off-chain, and which
	// can be used as a form of payment method. Every payable RPC can be paid
	// through direct payment or through an ephemeral account payment.
	//
	// Operations on an ephemeral account have ACID properties.
	EphemeralAccount struct {
		// Ephemeral accounts are covered by a RWMutex lock.
		// A reader can acquire an RLock to perform balance checks,
		// while writers will need to acquire a Lock and potentially wait for it
		// to be released
		mu sync.RWMutex

		accountID types.SiaPublicKey
		balance   types.Currency

		// Keep track of when this account was last updated, accounts which have
		// been stale are deleted by the host (see accountExpiryTimeout)
		updated time.Time
	}

	// EphemeralAccountDetails contain all information necessary to identify and
	// use an ephemeral account
	EphemeralAccountDetails struct {}
)
