package host

import (
	"math/bits"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
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
	errBalanceInsufficient = errors.New("insufficient account balance")
	errBalanceMaxExceeded  = errors.New("maximum account balance exceeded")

	errWithdrawalSpent            = errors.New("ephemeral account withdrawal was already spent")
	errWithdrawalExpired          = errors.New("ephemeral account withdrawal expired")
	errWithdrawalExtremeFuture    = errors.New("ephemeral account withdrawal expires too far into the future")
	errWithdrawalInvalidSignature = errors.New("ephemeral account withdrawal signature is invalid")

	// pruneExpiredAccountsFrequency is the frequency at which the host prunes
	// accounts which have been inactive
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Second,
		Testing:  2 * time.Second,
	}).(time.Duration)

	// blockedCallTimeout is the amount of time after which a blocked withdrawal
	// times out
	blockedCallTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      15 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)
)

type (
	// withdrawalMessage is a struct that contains all withdrawal details
	withdrawalMessage struct {
		account string
		expiry  types.BlockHeight
		amount  types.Currency
		nonce   uint64
	}

	// accountManager is a subsystem that manages all ephemeral accounts.
	//
	// Ephemeral accounts are a service offered by hosts that allow users to
	// connect a balance to a pubkey. Users can deposit funds into an ephemeral
	// account with a host and then later use the funds to transact with the
	// host.
	//
	// The account owner fully entrusts the money with the host, he has no
	// recourse at all if the host decides to steal the funds. For this reason,
	// users should only keep tiny balances in ephemeral accounts and users
	// should refill the ephemeral accounts frequently, even on the order of
	// multiple times per minute.
	accountManager struct {
		accounts               map[string]*account
		fingerprints           *memoryBucket
		indexBitfields         []uint64
		atomicUnsavedDelta     uint64
		staticAccountPersister *accountsPersister

		mu           sync.Mutex
		dependencies modules.Dependencies
		h            *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		id      string
		balance types.Currency
		lastTxn int64

		index        uint32
		blockedCalls blockedCallHeap
	}

	// blockedCall represents a pending withdraw
	blockedCall struct {
		id        string
		unblock   chan struct{}
		required  types.Currency
		timestamp int64
	}

	// blockedCallHeap is a heap of blocking transactions; the heap is sorted in
	// a FIFO fashion
	blockedCallHeap []*blockedCall
)

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager(dependencies modules.Dependencies, ap *accountsPersister) (*accountManager, error) {
	am := &accountManager{
		accounts:               make(map[string]*account),
		fingerprints:           newMemoryBucket(bucketSize, h.blockHeight),
		staticAccountPersister: ap,
		dependencies:           dependencies,
		h:                      h,
	}

	// Load data
	am.managedLoadData()

	am.h.tg.OnStop(func() {
		// Unblock all pending transactions
		for _, acc := range am.accounts {
			for _, txn := range acc.blockedCalls {
				close(txn.unblock)
			}
		}
		// Close all open file handles
		am.staticAccountPersister.Close()
	})

	go am.threadedPruneExpiredAccounts()

	return am, nil
}

// managedLoadData will load the accounts data
func (am *accountManager) managedLoadData() {
	am.mu.Lock()
	defer am.mu.Unlock()

	data := am.staticAccountPersister.callLoadAccountsData()
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
	maxBalance := am.h.InternalSettings().MaxEphemeralAccountBalance
	if maxBalance.Cmp(updatedBalance) < 0 {
		return errBalanceMaxExceeded
	}

	// Update account details
	acc.balance = updatedBalance
	acc.lastTxn = time.Now().Unix()

	// Try to unblock waiting txns by popping them off the heap one by one
	remaining := acc.balance
	for i := 0; i < acc.blockedCalls.Len(); i++ {
		bTxn := acc.blockedCalls.Pop().(*blockedCall)
		if remaining.Cmp(bTxn.required) >= 0 {
			close(bTxn.unblock)
			remaining = remaining.Sub(bTxn.required)
		} else {
			acc.blockedCalls.Push(bTxn)
			break
		}
	}

	err := am.staticAccountPersister.callSaveAccount(acc)
	if err != nil {
		am.h.log.Println("ERROR: could not save account:", id, err)
	}

	return nil
}

// callWithdraw will try to spend from an account, this call will block if the
// account balance is insufficient
func (am *accountManager) callWithdraw(msg *withdrawalMessage, sig crypto.Signature) error {
	am.mu.Lock()

	id, amount, expiry := msg.account, msg.amount, msg.expiry

	// Validat the withdrawal
	fp, err := am.validateWithdrawal(msg, sig)
	if err != nil {
		am.h.log.Printf("ERROR: invalid withdrawal %v", err)
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
		tx := blockedCall{
			id:        id,
			unblock:   make(chan struct{}),
			required:  amount,
			timestamp: time.Now().Unix(),
		}
		acc.blockedCalls.Push(tx)
		am.mu.Unlock()

	BlockLoop:
		for {
			select {
			case <-am.h.tg.StopChan():
				return errors.New("ERROR: withdraw cancelled, stop received")
			case <-tx.unblock:
				am.mu.Lock()
				break BlockLoop
			case <-time.After(blockedCallTimeout):
				return errBalanceInsufficient
			}
		}
	}

	// Sanity check to avoid the negative currency panic possible by subtracting
	if acc.balance.Cmp(amount) < 0 {
		am.mu.Unlock()
		return errBalanceInsufficient
	}
	acc.balance = acc.balance.Sub(amount)
	acc.lastTxn = time.Now().Unix()
	am.fingerprints.save(*fp, msg.expiry)

	// Track the unsaved delta by adding the amount we are spending
	unsaved, _ := amount.Uint64()
	atomic.AddUint64(&am.atomicUnsavedDelta, unsaved)

	// Persist the account data and fingerprint data asynchronously
	awaitPersist := make(chan struct{})
	go func() {
		if err := am.staticAccountPersister.callSaveAccount(acc); err != nil {
			am.h.log.Println("ERROR: could not save account", id, err)
		}

		if err := am.staticAccountPersister.callSaveFingerprint(fp, expiry); err != nil {
			am.h.log.Println("ERROR: could not save fingerprint", fp, err)
		}

		// Track the unsaved delta, decrement the unsaved delta with the amount
		// we just persisted to disk
		atomic.AddUint64(&am.atomicUnsavedDelta, ^uint64(unsaved-1))
		close(awaitPersist)
	}()

	// If the unsaved delta exceeds the maximum, block until we persisted
	maxUnsavedDelta, _ := am.h.InternalSettings().MaxUnsavedDelta.Uint64()
	if atomic.LoadUint64(&am.atomicUnsavedDelta) > maxUnsavedDelta {
		<-awaitPersist
		am.mu.Unlock()
		return nil
	}

	am.mu.Unlock()
	return nil
}

// callConsensusChanged is called by the host whenever it processed a
// change to the consensus, we use it to rotate the fingerprints as we do so
// based on the current blockheight
func (am *accountManager) callConsensusChanged(currentBlockHeight types.BlockHeight) {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Rotate fingerprints both in-memory and on disk
	am.fingerprints.tryRotate(currentBlockHeight)
	err := am.staticAccountPersister.fingerprints.tryRotate(currentBlockHeight)
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
		blockedCalls: make(blockedCallHeap, 0),
	}
	am.accounts[id] = acc
	return acc
}

// buildAccountIndex will initialize bitfields representing all ephemeral
// accounts, the index is used to recycle account indices
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

// validateWithdrawal returns an error if the withdrawal has already been
// processed or if the withdrawal message is either expired or too far into the
// future. If the signature is valid, it returns the fingerprint which is the
// hash of the withdrawal message
func (am *accountManager) validateWithdrawal(msg *withdrawalMessage, sig crypto.Signature) (*crypto.Hash, error) {

	// Verify we have not processed this withdrawal before
	fp := crypto.HashAll(*msg)
	if exists := am.fingerprints.has(fp); exists {
		return nil, errWithdrawalSpent
	}

	// Verify the current blockheight does not exceed the expiry
	currentBlockHeight := am.h.blockHeight
	if msg.expiry < currentBlockHeight {
		return nil, errWithdrawalExpired
	}

	// Verify the withdrawal is not too far into the future
	if msg.expiry > currentBlockHeight+bucketSize {
		return nil, errWithdrawalExtremeFuture
	}

	// Verify the signature
	spk := types.SiaPublicKey{}
	spk.LoadString(msg.account)
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)
	err := crypto.VerifyHash(fp, pk, sig)
	if err != nil {
		return nil, errWithdrawalInvalidSignature
	}

	return &fp, nil
}

// Implementation of heap.Interface for blockedCallHeap.
func (bch blockedCallHeap) Len() int           { return len(bch) }
func (bch blockedCallHeap) Less(i, j int) bool { return bch[i].timestamp < bch[j].timestamp }
func (bch blockedCallHeap) Swap(i, j int)      { bch[i], bch[j] = bch[j], bch[i] }
func (bch *blockedCallHeap) Push(x interface{}) {
	bTxn := x.(blockedCall)
	*bch = append(*bch, &bTxn)
}
func (bch *blockedCallHeap) Pop() interface{} {
	old := *bch
	n := len(old)
	bTxn := old[n-1]
	*bch = old[0 : n-1]
	return bTxn
}
