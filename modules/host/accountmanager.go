package host

import (
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	errAccountPersist = errors.New("ephemeral account could not persisted to disk")

	errBalanceInsufficient = errors.New("ephemeral account balance was insufficient")
	errBalanceMaxExceeded  = errors.New("ephemeral account balance exceeded the maximum")

	errWithdrawalSpent            = errors.New("ephemeral account withdrawal message was already spent")
	errWithdrawalExpired          = errors.New("ephemeral account withdrawal message expired")
	errWithdrawalExtremeFuture    = errors.New("ephemeral account withdrawal message expires too far into the future")
	errWithdrawalInvalidSignature = errors.New("ephemeral account withdrawal message signature is invalid")

	// accountExpiryTimeout defines the maximum amount of time an account can be
	// inactive before it gets pruned
	accountExpiryTimeout = build.Select(build.Var{
		Standard: 7 * 24 * time.Hour,
		Dev:      1 * 24 * time.Minute,
		Testing:  1 * time.Minute,
	}).(time.Duration)

	// pruneExpiredAccountsFrequency is the frequency at which the account
	// manager prunes ephemeral accounts which have been inactive
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// blockedCallTimeout is the amount of time after which a blocked withdrawal
	// times out
	blockedCallTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      1 * time.Minute,
		Testing:  5 * time.Second,
	}).(time.Duration)
)

// The AccountManager subsystem manages all of the ephemeral accounts on the
// host.
//
// Ephemeral accounts are a service offered by hosts that allow users to connect
// a balance to a pubkey. Users can deposit funds into an ephemeral account with
// a host and then later use the funds to transact with the host.
//
// The account owner fully entrusts the money with the host, he has no recourse
// at all if the host decides to steal the funds. For this reason, users should
// only keep tiny balances in ephemeral accounts and users should refill the
// ephemeral accounts frequently, even on the order of multiple times per
// minute.
type (
	// withdrawalMessage is a struct that contains all withdrawal details
	withdrawalMessage struct {
		account string
		expiry  types.BlockHeight
		amount  types.Currency
		nonce   uint64
	}

	// accountManager is a struct that holds all ephemeral accounts in memory,
	// along with a lookup table of all fingerprints it has already processed.
	// It has an accounts persister that will persist all ephemeral account data
	// to disk
	accountManager struct {
		accounts                map[string]*account
		fingerprints            *fingerprintMap
		staticAccountsPersister *accountsPersister

		// the account manager keeps track of the current blockheight, every
		// time there's a consensus change this value is updated to the current
		// block height
		blockHeight types.BlockHeight

		// To increase performance, the account manager will persist the account
		// data in an asynchronous fashion. This will allow users to withdraw
		// money from an ephemeral account without requiring the user to wait
		// until the new account balance was successfully persisted. This allows
		// users to transact with the host with significantly less latency. This
		// also means that the money that has already been withdrawn is at risk
		// to the host. An unclean shutdown before the account manager was able
		// to persist the account balance to disk would allow the user to
		// withdraw that money twice. To limit this risk to the host, he can set
		// a maxunsaved delta, when that amount is reached all withdrawals lock
		// until the accounts have successfully been persisted.
		atomicUnsavedDelta uint64

		mu sync.Mutex
		h  *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		index        uint32
		id           string
		balance      types.Currency
		blockedCalls blockedCallHeap

		// the reserved balance keeps track of how much balance is contained
		// withint the blocked call heap. We need to take this into
		// consideration to avoid race conditions where withdrawals fulfil
		// leaving recently unblocked withdrawals with insufficient balance.
		reservedBalance types.Currency

		// lastTxnTime is the timestamp of the last transaction that occured
		// involving this ephemeral account. We keep track of this last
		// transaction timestamp to allow pruning the ephemeral account after
		// the account expiry timeout.
		lastTxnTime int64
	}

	// blockedCall represents a pending withdraw
	blockedCall struct {
		unblock   chan struct{}
		required  types.Currency
		timestamp int64
	}

	// blockedCallHeap is a heap of blocking transactions; the heap is sorted in
	// a FIFO fashion
	blockedCallHeap []*blockedCall

	// fingerprintMap holds all fingerprints and serves as a lookup table. It
	// does so by keeping them in two separate buckets, the current and next
	// fingerprints. The current fingerprints are the ones which expire before
	// the current threshold. Fingerprints with a blockheight that is higher
	// than this threshold are kept in the next bucket. By keeping these in two
	// separate buckets, we allow pruning the fingerprints which are no longer
	// valid, due to the current blockheight that has caught up with their
	// expiry, to be pruned in constant time. When the blockheight exceeds the
	// current threshold, the buckets get swapped and the next bucket gets
	// renewed. Effectively pruning the entire 'current' bucket in constant
	// time.
	fingerprintMap struct {
		bucketBlockRange int
		currentThreshold types.BlockHeight
		current          map[crypto.Hash]struct{}
		next             map[crypto.Hash]struct{}
	}
)

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

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager() (_ *accountManager, err error) {
	h.mu.Lock()
	currentBlockHeight := h.blockHeight
	h.mu.Unlock()

	am := &accountManager{
		accounts:     make(map[string]*account),
		fingerprints: newFingerprintMap(h.blockHeight, bucketBlockRange),
		blockHeight:  currentBlockHeight,
		h:            h,
	}

	// Create the accounts persister
	am.staticAccountsPersister, err = h.newAccountsPersister(am)
	if err != nil {
		return nil, err
	}

	// Close any open file handles if we receive a stop signal
	am.h.tg.OnStop(func() {
		am.staticAccountsPersister.close()
	})

	go am.threadedPruneExpiredAccounts()

	return am, nil
}

// newAccount will create a new account and add it to the list of accounts
// managed by the account manager, note it is important an account has a valid
// account index at all times so it gets persisted properly
func (am *accountManager) newAccount(id string) *account {
	acc := &account{
		id:           id,
		index:        am.staticAccountsPersister.callAssignFreeIndex(),
		blockedCalls: make(blockedCallHeap, 0),
	}
	am.accounts[id] = acc
	return acc
}

// newFingerprintMap will create a new fingerprint map
func newFingerprintMap(currentBlockHeight types.BlockHeight, bucketBlockRange int) *fingerprintMap {
	return &fingerprintMap{
		bucketBlockRange: bucketBlockRange,
		currentThreshold: calculateBucketThreshold(currentBlockHeight, bucketBlockRange),
		current:          make(map[crypto.Hash]struct{}),
		next:             make(map[crypto.Hash]struct{}),
	}
}

// callSetData will set the given accounts data on the account manager
func (am *accountManager) callSetData(accounts map[string]*account, fingerprints map[crypto.Hash]struct{}) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.accounts = accounts
	am.fingerprints.next = fingerprints
}

// callDeposit will deposit the given amount into the account
func (am *accountManager) callDeposit(id string, amount types.Currency) error {
	hIS := am.h.InternalSettings()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Ensure the account exists
	acc, exists := am.accounts[id]
	if !exists {
		acc = am.newAccount(id)
	}

	// Verify maxbalance
	updatedBalance := acc.balance.Add(amount)
	maxBalance := hIS.MaxEphemeralAccountBalance
	if maxBalance.Cmp(updatedBalance) < 0 {
		return errBalanceMaxExceeded
	}

	// Update account details
	acc.balance = updatedBalance
	acc.lastTxnTime = time.Now().Unix()

	// Unblock blocked calls if the remaining balance allows it
	remaining := acc.balance
	for acc.blockedCalls.Len() > 0 {
		bTxn := acc.blockedCalls.Pop().(*blockedCall)
		if bTxn.required.Cmp(remaining) > 0 {
			acc.blockedCalls.Push(*bTxn)
			break
		}
		close(bTxn.unblock)
		remaining = remaining.Sub(bTxn.required)
	}

	// Persist the account
	err := am.staticAccountsPersister.callSaveAccount(acc)
	if err != nil {
		am.h.log.Println("ERROR: could not save account:", id, err)
		return errAccountPersist
	}

	return nil
}

// callWithdraw will try to withdraw money from an ephemeral account. If the
// account balance is insufficient, it will block until either a timeout
// expires, the account receives sufficient funds or we receive a stop message
//
// Note that this function has intricate locking going on, beware when making
// changes to properly release the lock before returning
func (am *accountManager) callWithdraw(msg *withdrawalMessage, sig crypto.Signature) error {
	hIS := am.h.InternalSettings()

	am.mu.Lock()

	// Extract for readability
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

	// Make sure to include reserved balance when seeing if balance is
	// sufficient, the reserved balance is money that's waiting to withdrawn by
	// earlier blocked withdrawals.
	requiredBalance := amount.Add(acc.reservedBalance)

	// If the account balance is insufficient, we block until either the
	// blockCallTimeout expires, the account receives sufficient deposits or we
	// receive a message on the thread group's stop channel
	if acc.balance.Cmp(requiredBalance) < 0 {
		tx := blockedCall{
			unblock:   make(chan struct{}),
			required:  amount,
			timestamp: time.Now().Unix(),
		}
		acc.blockedCalls.Push(tx)

		// Update reserved balance
		acc.reservedBalance = requiredBalance

		// Unlock the mutex! (important unlock as select case re-acquire it)
		am.mu.Unlock()

	BlockLoop:
		for {
			select {
			case <-am.h.tg.StopChan():
				return errors.New("ERROR: withdraw cancelled, stop received")
			case <-tx.unblock:
				// Re-acquire the lock)
				am.mu.Lock()

				// Release reserved balance
				acc.reservedBalance = acc.reservedBalance.Sub(amount)

				// Verify current blockheight does not exceed expiry (sufficient
				// time may have passed between now and previous check)
				if am.blockHeight > msg.expiry {
					am.mu.Unlock()
					return errWithdrawalExpired
				}

				break BlockLoop
			case <-time.After(blockedCallTimeout):
				// Release reserved balance
				am.mu.Lock()
				acc.reservedBalance = acc.reservedBalance.Sub(amount)
				am.mu.Unlock()
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
	acc.lastTxnTime = time.Now().Unix()
	am.fingerprints.save(fp, msg.expiry)

	// Track the unsaved delta by adding the amount we are spending
	amountAsUint64, _ := amount.Uint64()
	atomic.AddUint64(&am.atomicUnsavedDelta, amountAsUint64)

	// Persist the account data and fingerprint data asynchronously
	awaitPersist := make(chan struct{})
	go func() {
		if err := am.staticAccountsPersister.callSaveAccount(acc); err != nil {
			am.h.log.Println("ERROR: could not save account", id, err)
		}

		if err := am.staticAccountsPersister.callSaveFingerprint(fp, expiry); err != nil {
			am.h.log.Println("ERROR: could not save fingerprint", fp, err)
		}

		// Track the unsaved delta, decrement the unsaved delta with the amount
		// we just persisted to disk
		atomic.AddUint64(&am.atomicUnsavedDelta, ^uint64(amountAsUint64-1))
		close(awaitPersist)
	}()

	// If the unsaved delta exceeds the maximum, block until we persisted
	maxUnsavedDelta, _ := hIS.MaxUnsavedDelta.Uint64()
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
func (am *accountManager) callConsensusChanged() {
	am.h.mu.Lock()
	currentBlockHeight := am.h.blockHeight
	am.h.mu.Unlock()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Rotate fingerprints both in-memory and on disk
	am.blockHeight = currentBlockHeight
	am.fingerprints.tryRotate(currentBlockHeight)
	err := am.staticAccountsPersister.staticFingerprintManager.tryRotate(currentBlockHeight)
	if err != nil {
		am.h.log.Println("ERROR: could not rotate fingerprint buckets")
	}
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// it does this by nullifying the account's balance which frees up the account
// at that index
func (am *accountManager) threadedPruneExpiredAccounts() {
	if err := am.h.tg.Add(); err != nil {
		return
	}
	defer am.h.tg.Done()

	var force bool
	if am.h.dependencies.Disrupt("expireEphemeralAccounts") {
		force = true
	}

	var accountExpiryTimeoutAsInt64 = int64(accountExpiryTimeout)

	for {
		pruned := make([]uint32, 0)

		am.mu.Lock()
		now := time.Now().Unix()
		for id, acc := range am.accounts {
			if force {
				am.h.log.Debugf("DEBUG: force expiring account %v", id)
				acc.balance = types.ZeroCurrency
				pruned = append(pruned, acc.index)
				continue
			}

			if acc.balance.Cmp(types.ZeroCurrency) != 0 {
				last := acc.lastTxnTime
				if now-last > accountExpiryTimeoutAsInt64 {
					am.h.log.Debugf("DEBUG: expiring account %v at %v", id, now)
					acc.balance = types.ZeroCurrency
					pruned = append(pruned, acc.index)
				}
			}
		}
		am.mu.Unlock()

		for _, index := range pruned {
			am.staticAccountsPersister.callReleaseIndex(index)
		}

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
func (am *accountManager) validateWithdrawal(msg *withdrawalMessage, sig crypto.Signature) (crypto.Hash, error) {

	// Verify we have not processed this withdrawal before
	fp := crypto.HashAll(*msg)
	if exists := am.fingerprints.has(fp); exists {
		return crypto.Hash{}, errWithdrawalSpent
	}

	// Verify the current blockheight does not exceed the expiry
	if am.blockHeight > msg.expiry {
		return crypto.Hash{}, errWithdrawalExpired
	}

	// Verify the withdrawal is not too far into the future
	if msg.expiry > calculateBucketThreshold(am.blockHeight, bucketBlockRange)+bucketBlockRange {
		return crypto.Hash{}, errWithdrawalExtremeFuture
	}

	// Verify the signature
	spk := types.SiaPublicKey{}
	spk.LoadString(msg.account)
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)
	err := crypto.VerifyHash(fp, pk, sig)
	if err != nil {
		return crypto.Hash{}, errWithdrawalInvalidSignature
	}

	return fp, nil
}

// save will add the given fingerprint to the appropriate bucket
func (fm *fingerprintMap) save(fp crypto.Hash, expiry types.BlockHeight) {
	if expiry <= fm.currentThreshold {
		fm.current[fp] = struct{}{}
		return
	}

	fm.next[fp] = struct{}{}
}

// has will return true when the fingerprint was present in either of the two
// buckets
func (fm *fingerprintMap) has(fp crypto.Hash) bool {
	_, exists := fm.current[fp]
	if !exists {
		_, exists = fm.next[fp]
	}
	return exists
}

// tryRotate will rotate the bucket if necessary, depending on the current block
// height. It swaps the current for the next bucket and reallocates the next
func (fm *fingerprintMap) tryRotate(currentBlockHeight types.BlockHeight) {
	// If the current blockheihgt is less or equal than the current bucket's
	// threshold, we do not need to rotate the bucket files on the file system
	if currentBlockHeight <= fm.currentThreshold {
		return
	}

	// If it is, we swap the current and next buckets and recreate next
	fm.current = fm.next
	fm.next = make(map[crypto.Hash]struct{})

	// Recalculate the threshold
	fm.currentThreshold = calculateBucketThreshold(currentBlockHeight, fm.bucketBlockRange)
}
