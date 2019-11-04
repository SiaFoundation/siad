package host

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// accountExpiryTimeout defines the maximum amount of time an account can be
	// inactive before it gets pruned
	accountExpiryTimeout = 7 * 24 * time.Hour
)

var (
	errInsufficientBalance = errors.New("insufficient account balance")
	errMaxBalanceExceeded  = errors.New("maximum account balance exceeded")

	// accountMaxBalance defines how many coins an account can hold at most
	// TODO make host setting or change to max unsaved delta (?)
	accountMaxBalance = types.SiacoinPrecision

	// pruneExpiredAccountsFrequency is the frequency at which the host prunes
	// accounts which have been inactive
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Second,
		Testing:  2 * time.Second,
	}).(time.Duration)

	// blockedCallTimeout is the maximum amount of time a call is blocked before
	// it times out
	blockedCallTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      15 * time.Second,
		Testing:  3 * time.Second,
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
	accountManager struct {
		accounts          map[string]types.Currency
		accountIndices    map[string]uint32
		accountUpdated    map[string]int64
		accountMaxBalance types.Currency

		blockedCalls map[string][]blockedCall
		fingerprints *memoryBucket

		mu           sync.Mutex
		persister    *accountsPersister
		dependencies modules.Dependencies
		h            *Host
	}

	// blockedCall represents a waiting thread, it is waiting due to
	// an insufficient balance in the account to perform the withdrawal and can
	// be unblocked by a deposit
	blockedCall struct {
		id       string
		unblock  chan struct{}
		required types.Currency
	}
)

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager(dependencies modules.Dependencies) (*accountManager, error) {
	am := &accountManager{
		accounts:          make(map[string]types.Currency),
		accountIndices:    make(map[string]uint32),
		accountUpdated:    make(map[string]int64),
		accountMaxBalance: accountMaxBalance,

		fingerprints: newMemoryBucket(bucketSize, h.blockHeight),
		blockedCalls: make(map[string][]blockedCall, 0),

		dependencies: dependencies,
		persister:    h.staticAccountPersister,
		h:            h,
	}

	// Load data
	am.managedLoad()

	am.h.tg.OnStop(func() {
		// Close all open unblock channels
		for _, bcs := range am.blockedCalls {
			for _, bc := range bcs {
				close(bc.unblock)
			}
		}
		am.persister.Close()
	})

	// Prune expired accounts and fingerprints in a background loop
	go am.threadedPruneExpiredAccounts()

	return am, nil
}

// managedLoad will load the accounts data
func (am *accountManager) managedLoad() {
	am.mu.Lock()
	defer am.mu.Unlock()

	data := am.persister.callLoadAccountsData()
	am.accounts = data.Accounts
	am.accountIndices = data.AccountIndices
	am.accountUpdated = data.AccountUpdated
	(*am.fingerprints.current) = data.Fingerprints
}

// callDeposit will credit the amount to the account's balance
func (am *accountManager) callDeposit(id string, amount types.Currency) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Verify the updated balance does not exceed the max balance
	uB := am.accounts[id].Add(amount)
	if am.accountMaxBalance.Cmp(uB) < 0 {
		am.h.log.Printf("ERROR: deposit of %v exceeded max balance for account %v\n", amount, id)
		return errMaxBalanceExceeded
	}

	// Ensure the account has an index associated to it
	index, exists := am.accountIndices[id]
	if !exists {
		am.accountIndices[id] = am.freeAccountIndex()
		index = am.accountIndices[id]
	}

	// Update account balance
	am.accounts[id] = uB
	am.accountUpdated[id] = time.Now().Unix()

	// Loop over blocked calls and unblock where possible, keep track of the
	// remaining balance to allow unblocking multiple calls at the same time
	remaining := am.accounts[id]
	j := 0
	for i := 0; i < len(am.blockedCalls[id]); i++ {
		b := am.blockedCalls[id][i]
		if b.id != id {
			continue
		}
		if remaining.Cmp(b.required) < 0 {
			am.blockedCalls[id][j] = b
			j++
		} else {
			close(b.unblock)
			remaining = remaining.Sub(b.required)
			if remaining.Equals(types.ZeroCurrency) {
				break
			}
		}
	}
	am.blockedCalls[id] = am.blockedCalls[id][:j]

	err := am.persister.callSaveAccount(index, am.account(id))
	if err != nil {
		am.h.log.Println("ERROR: could not save account:", id, err)
	}

	return nil
}

// callSpend will try to spend from an account, it blocks if the account balance
// is insufficient
func (am *accountManager) callSpend(id string, amount types.Currency, fp *fingerprint) error {
	am.mu.Lock()

	// Lookup fingerprint
	if exists := am.fingerprints.has(fp); exists {
		am.mu.Unlock()
		return errKnownFingerprint
	}

	// Validate fingerprint
	if err := fp.validate(am.h.blockHeight); err != nil {
		am.h.log.Printf("ERROR: invalid fingerprint, current blockheight %v, error: %v", am.h.blockHeight, err)
		am.mu.Unlock()
		return err
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
		am.blockedCalls[id] = append(am.blockedCalls[id], bc)
		am.mu.Unlock()

	BlockLoop:
		for {
			select {
			case <-am.h.tg.StopChan():
				return errors.New("ERROR: spend cancelled, stop received")
			case <-bc.unblock:
				am.mu.Lock()
				break BlockLoop
			case <-time.After(blockedCallTimeout):
				return errInsufficientBalance
			}
		}
	}

	if am.accounts[id].Cmp(amount) < 0 {
		am.mu.Unlock()
		return errInsufficientBalance
	}

	am.accounts[id] = am.accounts[id].Sub(amount)
	am.accountUpdated[id] = time.Now().Unix()
	am.fingerprints.save(fp)
	am.mu.Unlock()

	// Persist data
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := am.persister.callSaveAccount(am.accountIndices[id], am.account(id)); err != nil {
			am.h.log.Println("ERROR: could not save account", id, err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := am.persister.callSaveFingerprint(fp); err != nil {
			am.h.log.Println("ERROR: could not save fingerprint", fp.Hash, err)
		}
	}()
	wg.Wait()

	return nil
}

// callConsensusChanged is called by the host whenever it processed a
// change to the consensus, we use it to rotate the fingerprints as they are
// blockheight based
func (am *accountManager) callConsensusChanged() {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.fingerprints.tryRotate(am.h.blockHeight)
	err := am.persister.fingerprints.tryRotate(am.h.blockHeight)
	if err != nil {
		am.h.log.Println("ERROR: could not rotate fingerprint buckets")
	}
}

// balanceOf will return the balance for given account
func (am *accountManager) balanceOf(id string) types.Currency {
	am.mu.Lock()
	defer am.mu.Unlock()
	return am.accounts[id]
}

// balanceOf will return the balance for given account
func (am *accountManager) account(id string) *account {
	a := &account{
		Balance: am.accounts[id],
		Updated: am.accountUpdated[id],
	}
	a.Id.LoadString(id)
	return a
}

// freeAccountIndex will return the next available account index
func (am *accountManager) freeAccountIndex() uint32 {
	var max uint32 = 0
	if len(am.accounts) == 0 {
		return max
	}

	for id := range am.accounts {
		if am.accounts[id].IsZero() {
			return am.accountIndices[id]
		}
		if am.accountIndices[id] > max {
			max = am.accountIndices[id]
		}
	}
	return max + 1
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// it does this by nullifying the account's balance which frees up the account
// at that index
func (am *accountManager) threadedPruneExpiredAccounts() {
	var force bool
	if am.dependencies.Disrupt("expireEphemeralAccounts") {
		force = true
	}

	var accountExpiryTimeoutAsInt64 = int64(accountExpiryTimeout)

	for {
		am.mu.Lock()
		now := time.Now().Unix()
		for id := range am.accounts {
			if force {
				am.h.log.Debugf("DEBUG: force expiring account %v", id)
				am.accounts[id] = types.ZeroCurrency
				delete(am.accountIndices, id)
				continue
			}

			if am.accounts[id].Cmp(types.ZeroCurrency) != 0 {
				last := am.accountUpdated[id]
				if now-last > accountExpiryTimeoutAsInt64 {
					am.h.log.Debugf("DEBUG: expiring account %v at %v", id, now)
					am.accounts[id] = types.ZeroCurrency
					delete(am.accountIndices, id)
				}
			}
		}
		am.mu.Unlock()

		// Block until next cycle.
		select {
		case <-am.h.tg.StopChan():
			return
		case <-time.After(pruneExpiredAccountsFrequency):
			continue
		}
	}
}
