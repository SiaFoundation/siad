package host

import (
	"math/bits"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// accountExpiryTimeout defines the maximum amount of time an account can be
	// inactive before it gets pruned
	accountExpiryTimeout = 7 * 24 * time.Hour
)

var (
	errInsufficientBalance = errors.New("insufficient account balance")
	errMaxBalanceExceeded  = errors.New("maximum account balance exceeded")

	// TODO make host setting
	maxAccountBalance = types.SiacoinPrecision
	maxUnsavedDelta   = types.SiacoinPrecision

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
		accounts     map[string]*account
		fingerprints *memoryBucket

		indexBitfields []uint64

		maxAccountBalance  types.Currency
		maxUnsavedDelta    uint64
		atomicUnsavedDelta uint64

		mu           sync.Mutex
		persister    *accountsPersister
		dependencies modules.Dependencies
		h            *Host
	}

	// account contains the account identifier
	account struct {
		id      string
		balance types.Currency
		lastTxn int64

		index        uint32
		blockedCalls []blockedCall
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
	maxUnsavedDelta, _ := maxUnsavedDelta.Uint64()
	am := &accountManager{
		accounts:     make(map[string]*account),
		fingerprints: newMemoryBucket(bucketSize, h.blockHeight),

		maxAccountBalance: maxAccountBalance,
		maxUnsavedDelta:   maxUnsavedDelta,

		dependencies: dependencies,
		persister:    h.staticAccountPersister,
		h:            h,
	}

	// Load data
	am.managedLoadData()

	am.h.tg.OnStop(func() {
		// Close all open unblock channels
		for _, acc := range am.accounts {
			for _, bc := range acc.blockedCalls {
				close(bc.unblock)
			}
		}
		// Close all open file handles
		am.persister.Close()
	})

	go am.threadedPruneExpiredAccounts()

	return am, nil
}

// managedLoad will load the accounts data
func (am *accountManager) managedLoadData() {
	am.mu.Lock()
	defer am.mu.Unlock()

	data := am.persister.callLoadAccountsData()
	am.accounts = data.Accounts
	(*am.fingerprints.current) = data.Fingerprints
	am.buildAccountIndex()
}

// callDeposit will deposit the given amount into the account
func (am *accountManager) callDeposit(id string, amount types.Currency) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Ensure the account exists
	acc, exists := am.accounts[id]
	if !exists {
		acc = am.newAccount(id)
	}

	// Verify maxbalance
	updatedBalance := acc.balance.Add(amount)
	if am.maxAccountBalance.Cmp(updatedBalance) < 0 {
		return errMaxBalanceExceeded
	}

	// Update account details
	acc.balance = updatedBalance
	acc.lastTxn = time.Now().Unix()

	// Loop over blocked calls and unblock where possible, keep track of the
	// remaining balance to allow unblocking multiple calls at the same time
	remaining := acc.balance
	j := 0
	for i := 0; i < len(acc.blockedCalls); i++ {
		b := acc.blockedCalls[i]
		if b.id != id {
			continue
		}
		if remaining.Cmp(b.required) < 0 {
			acc.blockedCalls[j] = b
			j++
		} else {
			close(b.unblock)
			remaining = remaining.Sub(b.required)
			if remaining.Equals(types.ZeroCurrency) {
				break
			}
		}
	}
	acc.blockedCalls = acc.blockedCalls[:j]

	err := am.persister.callSaveAccount(acc)
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

	// Ensure the account exists
	acc, exists := am.accounts[id]
	if !exists {
		acc = am.newAccount(id)
	}

	// If current account balance is insufficient, we block until either the
	// blockCallTimeout expires, the account receives sufficient deposits or we
	// receive a message on the thread group's stop channel
	if acc.balance.Cmp(amount) < 0 {
		bc := blockedCall{
			id:       id,
			unblock:  make(chan struct{}),
			required: amount,
		}
		acc.blockedCalls = append(acc.blockedCalls, bc)
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

	// Sanity check to avoid the negative currency panic possible by subtracting
	if acc.balance.Cmp(amount) < 0 {
		am.mu.Unlock()
		return errInsufficientBalance
	}
	acc.balance = acc.balance.Sub(amount)
	acc.lastTxn = time.Now().Unix()
	am.fingerprints.save(fp)

	// Track the unsaved delta by adding the amount we are withrdrawing
	unsaved, _ := amount.Uint64()
	atomic.AddUint64(&am.atomicUnsavedDelta, unsaved)

	// Persist the account data and fingerprint data asynchronously
	blockChan := make(chan struct{})
	go func() {
		if err := am.persister.callSaveAccount(acc); err != nil {
			am.h.log.Println("ERROR: could not save account", id, err)
		}

		if err := am.persister.callSaveFingerprint(fp); err != nil {
			am.h.log.Println("ERROR: could not save fingerprint", fp.Hash, err)
		}

		// Track the unsaved delta, decrement the unsaved delta with the amount
		// we just persisted to disk
		atomic.AddUint64(&am.atomicUnsavedDelta, ^uint64(unsaved-1))
		close(blockChan)
	}()

	if atomic.LoadUint64(&am.atomicUnsavedDelta) <= am.maxUnsavedDelta {
		am.mu.Unlock()
		return nil
	}

	<-blockChan
	am.mu.Unlock()
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

// newAccount will create a new account and add it to the list of accounts
// managed by the account manager, note it is important an account has a valid
// account index at all times so it gets persisted properly
func (am *accountManager) newAccount(id string) *account {
	acc := &account{
		id:           id,
		index:        am.getFreeIndex(),
		blockedCalls: make([]blockedCall, 0),
	}
	am.accounts[id] = acc
	return acc
}

// buildAccountIndex is called on load and will initialize all bitfields with a
// uint that has a bit set representing an account at index
func (am *accountManager) buildAccountIndex() {
	n := len(am.accounts) / 64
	for i := 0; i < n; i++ {
		am.indexBitfields = append(am.indexBitfields, ^uint64(0))
	}

	r := len(am.accounts) % 64
	if r > 0 {
		am.indexBitfields = append(am.indexBitfields, (1<<uint(r))-1)
	}
}

// setIndex will set the bit corresponding to given index
func (am *accountManager) setIndex(index uint32) {
	i := index / 64
	p := index % 64
	am.indexBitfields[i] = am.indexBitfields[i] << p
}

// unsetIndex will unset the bit corresponding to given index
func (am *accountManager) unsetIndex(index uint32) {
	i := index / 64
	pos := index % 64
	var mask uint64 = ^(1 << pos)
	am.indexBitfields[i] &= mask
}

// getFreeIndex will return the next available account index
func (am *accountManager) getFreeIndex() uint32 {
	var i, pos int = 0, -1

	// Go through all bitmaps in random order to find a free index
	full := ^uint64(0)
	for i := range fastrand.Perm(len(am.indexBitfields)) {
		if am.indexBitfields[i] != full {
			pos = bits.TrailingZeros(uint(^am.indexBitfields[i]))
			break
		}
	}

	// Add a new bitfield if all account indices are taken
	if pos == -1 {
		pos = 0
		am.indexBitfields = append(am.indexBitfields, 1<<uint(pos))
	}

	index := uint32((i * 64) + pos)
	am.setIndex(index)
	return index
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
		for id, acc := range am.accounts {
			if force {
				am.h.log.Debugf("DEBUG: force expiring account %v", id)
				acc.balance = types.ZeroCurrency
				am.unsetIndex(acc.index)
				continue
			}

			if acc.balance.Cmp(types.ZeroCurrency) != 0 {
				last := acc.lastTxn
				if now-last > accountExpiryTimeoutAsInt64 {
					am.h.log.Debugf("DEBUG: expiring account %v at %v", id, now)
					acc.balance = types.ZeroCurrency
					am.unsetIndex(acc.index)
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
