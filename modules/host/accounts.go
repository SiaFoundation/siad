package host

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// accountExpiryTimeout dictates after what time stale accounts get pruned
	// from the account manager
	accountExpiryTimeout = 7 * 86400
)

var (
	errBlockedCallTimeout  = errors.New("blocked call timeout")
	errInsufficientBalance = errors.New("insufficient balance")
	errMaxBalanceExceeded  = errors.New("maximum account balance exceeded")
	errKnownFingerprint    = errors.New("known fingerprint")

	// accountMaxBalance is the maximum allowed balance
	accountMaxBalance = types.SiacoinPrecision.Mul64(1e3)

	// blockedCallTimeout is the maximum amount of time a call is blocked
	blockedCallTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      15 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// pruneExpiredAccountsFrequency is the frequency at which the hosts prunes
	// accounts which have been stale
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Second,
		Testing:  2 * time.Second,
	}).(time.Duration)
)

type (
	// accountManager is a subsystem that manages all ephemeral accounts.
	//
	// These accounts are a pubkey with a balance associated to it. They are
	// kept completely off-chain and serve as a method of payment.
	//
	// The account owner fully entrusts the money with the host, he has no
	// recourse at all if the host decides to steal the funds. Because of that,
	// the total amount of money an account can hold is capped.
	//
	// All operations on the account have ACID properties.
	accountManager struct {
		accounts     map[string]types.Currency
		updated      map[string]int64
		fingerprints map[crypto.Hash]struct{}
		blockedCalls []blockedCall

		mu           sync.Mutex
		persister    *accountsPersister
		dependencies modules.Dependencies
		*hostUtils
	}

	// blockedCall represents a waiting thread due to insufficient balance
	// upon deposit these calls get unblocked if the amount deposited was
	// sufficient
	blockedCall struct {
		id       string
		unblock  chan struct{}
		required types.Currency
	}
)

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager(dependencies modules.Dependencies) (*accountManager, error) {
	am := &accountManager{
		accounts:     make(map[string]types.Currency),
		updated:      make(map[string]int64),
		fingerprints: make(map[crypto.Hash]struct{}),
		blockedCalls: make([]blockedCall, 0),
		dependencies: dependencies,
		persister:    h.staticAccountPersister,
		hostUtils:    &h.hostUtils,
	}

	// Load account data
	data := am.persister.callLoadAccountData()
	am.accounts = data.Accounts

	go am.threadedPruneExpiredAccounts()

	am.tg.OnStop(func() {
		for _, d := range am.blockedCalls {
			close(d.unblock)
		}
	})

	return am, nil
}

// balanceOf will return the balance for given account
func (am *accountManager) balanceOf(id string) types.Currency {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.accounts[id]
}

// callDeposit will credit the amount to the account's balance
func (am *accountManager) callDeposit(id string, amount types.Currency) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Verify max balance
	uB := am.accounts[id].Add(amount)
	if accountMaxBalance.Cmp(uB) < 0 {
		am.hostUtils.log.Printf("ERROR: deposit of %v exceeded max balance for account %v", amount, id)
		return errMaxBalanceExceeded
	}

	// Update account balance
	am.accounts[id] = uB
	am.updated[id] = time.Now().Unix()

	// Loop over blocked calls and unblock where possible, keep track of the
	// remaining balance to allow unblocking multiple calls at the same time
	remaining := am.accounts[id]
	j := 0
	for i := 0; i < len(am.blockedCalls); i++ {
		b := am.blockedCalls[i]
		if b.id != id {
			continue
		}
		if remaining.Cmp(b.required) < 0 {
			am.blockedCalls[j] = b
			j++
		} else {
			close(b.unblock)
			remaining = remaining.Sub(b.required)
			if remaining.Equals(types.ZeroCurrency) {
				break
			}
		}
	}
	am.blockedCalls = am.blockedCalls[:j]

	err := am.persister.callSaveAccountsData(am.managedAccountsData())
	if err != nil {
		am.hostUtils.log.Println("ERROR: could not save accounts:", err)
	}

	return nil
}

// callSpend will try to spend from an account, it blocks if the account balance
// is insufficient
func (am *accountManager) callSpend(id string, amount types.Currency, fp crypto.Hash) error {
	am.mu.Lock()

	defer func() {
		var s struct{}
		am.fingerprints[fp] = s
	}()

	// Verify unique fingerprint
	if _, exists := am.fingerprints[fp]; exists {
		am.hostUtils.log.Printf("ERROR: fingerprint seen %v", fp)
		am.mu.Unlock()
		return errKnownFingerprint
	}

	// If current account balance is insufficient, we block until either the
	// blockCallTimeout expires, the account receives sufficient deposits or we
	// receive a message on the thread group's stop channel
	if am.accounts[id].Cmp(amount) < 0 {
		bc := blockedCall{
			id:       id,
			unblock:  make(chan struct{}),
			required: amount,
		}
		am.blockedCalls = append(am.blockedCalls, bc)
		am.mu.Unlock()

	BlockLoop:
		for {
			select {
			case <-am.tg.StopChan():
				return errors.New("ERROR: spend cancelled, stop received")
			case <-bc.unblock:
				am.mu.Lock()
				break BlockLoop
			case <-time.After(blockedCallTimeout):
				return errBlockedCallTimeout
			}
		}
	}

	if am.accounts[id].Cmp(amount) < 0 {
		am.mu.Unlock()
		return errInsufficientBalance
	}

	am.accounts[id] = am.accounts[id].Sub(amount)
	am.updated[id] = time.Now().Unix()
	am.mu.Unlock()

	return nil
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
func (am *accountManager) threadedPruneExpiredAccounts() {
	var force bool
	if am.dependencies.Disrupt("expireEphemeralAccounts") {
		force = true
	}

	for {
		var save bool

		am.mu.Lock()
		now := time.Now().Unix()
		for id := range am.accounts {
			last := am.updated[id]
			if force || now-last > accountExpiryTimeout {
				am.log.Debugf("DEBUG: expiring account %v at %v", id, now)
				delete(am.accounts, id)
				save = true
			}
		}
		am.mu.Unlock()

		if save {
			err := am.persister.callSaveAccountsData(am.managedAccountsData())
			if err != nil {
				am.hostUtils.log.Println("ERROR: could not save accounts:", err)
			}
		}

		// Block until next cycle.
		select {
		case <-am.tg.StopChan():
			return
		case <-time.After(pruneExpiredAccountsFrequency):
			continue
		}
	}
}

// managedAccountsData returns a struct containing all persisted account data
func (am *accountManager) managedAccountsData() *accountsData {
	data := &accountsData{am.accounts}
	return data
}
