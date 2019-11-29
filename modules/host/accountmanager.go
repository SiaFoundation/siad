package host

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// TODO: when implementing extractPaymentForRPC,
	// move these errors and the withdrawalMessage to the modules package

	// ErrAccountPersist occurs when an ephemeral account could not be persisted
	// to disk
	ErrAccountPersist = errors.New("ephemeral account could not persisted to disk")

	// ErrBalanceInsufficient occurs when a withdrawal could not be succesfully
	// completed because the account's balance was insufficient
	ErrBalanceInsufficient = errors.New("ephemeral account balance was insufficient")

	// ErrBalanceMaxExceeded occurs when a deposit is sufficiently large it
	// would exceed the maximum ephemeral account balance
	ErrBalanceMaxExceeded = errors.New("ephemeral account balance exceeded the maximum")

	// ErrWithdrawalSpent occurs when a withdrawal is being performed using a
	// withdrawal message that has been spent already
	ErrWithdrawalSpent = errors.New("ephemeral account withdrawal message was already spent")

	// ErrWithdrawalExpired occurs when the withdrawal message's expiry
	// blockheight is in the past
	ErrWithdrawalExpired = errors.New("ephemeral account withdrawal message expired")

	// ErrWithdrawalExtremeFuture occurs when the withdrawal message's expiry
	// blockheight is too far into the future
	ErrWithdrawalExtremeFuture = errors.New("ephemeral account withdrawal message expires too far into the future")

	// ErrWithdrawalInvalidSignature occurs when the provided withdrawal
	// signature was deemed invalid
	ErrWithdrawalInvalidSignature = errors.New("ephemeral account withdrawal message signature is invalid")

	// ErrWithdrawalCancelled occurs when the host was willingly or unwillingly
	// stopped in the midst of a withdrawal process
	ErrWithdrawalCancelled = errors.New("ephemeral account withdrawal cancelled due to a shutdown")

	// Only used for testing purposes
	errMaxRiskReached = errors.New("errMaxRiskReached")

	// pruneExpiredAccountsFrequency is the frequency at which the account
	// manager prunes ephemeral accounts which have been inactive
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Minute,
		Testing:  2 * time.Second,
	}).(time.Duration)

	// blockedCallTimeout is the amount of time after which a blocked withdrawal
	// times out
	blockedCallTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      5 * time.Minute,
		Testing:  3 * time.Second,
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

	// The accountManager manages all ephemeral accounts and keeps track of the
	// fingerprints for withdrawals that have been processed. The account
	// manager is hooked into the consensus changes and keeps track of the
	// current blockheight in order to prune the expired fingerprints. It has an
	// accounts persister that will persist all ephemeral account data to disk
	accountManager struct {
		accounts                map[string]*account
		fingerprints            *fingerprintMap
		staticAccountsPersister *accountsPersister

		// To increase performance, the account manager will persist the account
		// data in an asynchronous fashion. This will allow users to withdraw
		// money from an ephemeral account without requiring the user to wait
		// until the new account balance was successfully persisted. This allows
		// users to transact with the host with significantly less latency. This
		// also means that the money that has already been withdrawn is at risk
		// to the host. An unclean shutdown before the account manager was able
		// to persist the account balance to disk would allow the user to
		// withdraw that money twice. To limit this risk to the host, he can set
		// a maxephemeralaccountrisk, when that amount is reached all
		// withdrawals lock until the accounts have successfully been persisted.
		currentRisk types.Currency

		mu sync.Mutex
		h  *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		index        uint32
		id           string
		balance      types.Currency
		blockedCalls blockedCallHeap

		// lastTxnTime is the timestamp of the last transaction that occured
		// involving this ephemeral account. We keep track of this last
		// transaction timestamp to allow pruning the ephemeral account after
		// the account expiry timeout.
		lastTxnTime int64
	}

	// blockedCall represents a pending withdrawal
	blockedCall struct {
		withdrawal *withdrawalMessage
		result     chan error
		priority   int64
	}

	// blockedCallHeap is a heap of blocking transactions; the heap is sorted
	// based on the priority field in the blocked call
	blockedCallHeap []*blockedCall

	// fingerprintMap holds all fingerprints and serves as a lookup table. It
	// does so by keeping them in two separate buckets, the current and next
	// fingerprints. The current fingerprints are the ones which expire before
	// the current threshold. Fingerprints with a blockheight that is higher
	// than this threshold are kept in the next bucket. By keeping these in two
	// separate buckets, we allow pruning the fingerprints which expired in
	// constant time. When the current blockheight catches up to the current
	// threshold, the current bucket is thrown away and is replaced by the next.
	fingerprintMap struct {
		bucketBlockRange int
		current          map[crypto.Hash]struct{}
		next             map[crypto.Hash]struct{}
	}
)

// Implementation of heap.Interface for blockedCallHeap.
func (bch blockedCallHeap) Len() int           { return len(bch) }
func (bch blockedCallHeap) Less(i, j int) bool { return bch[i].priority < bch[j].priority }
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
func (bch blockedCallHeap) Value() types.Currency {
	var total types.Currency
	for _, bTxn := range bch {
		total = total.Add(bTxn.withdrawal.amount)
	}
	return total
}

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager() (_ *accountManager, err error) {
	am := &accountManager{
		accounts:     make(map[string]*account),
		fingerprints: newFingerprintMap(bucketBlockRange),
		h:            h,
	}

	// Create the accounts persister
	am.staticAccountsPersister, err = h.newAccountsPersister(am)
	if err != nil {
		return nil, err
	}

	// Load the persisted accounts data onto the account manager
	data, err := am.staticAccountsPersister.callLoadData()
	if err != nil {
		return nil, err
	}
	am.accounts = data.accounts
	am.fingerprints.next = data.fingerprints

	// Close any open file handles if we receive a stop signal
	am.h.tg.OnStop(func() {
		am.staticAccountsPersister.close()
	})

	go am.threadedPruneExpiredAccounts()

	return am, nil
}

// newFingerprintMap will create a new fingerprint map
func newFingerprintMap(bucketBlockRange int) *fingerprintMap {
	return &fingerprintMap{
		bucketBlockRange: bucketBlockRange,
		current:          make(map[crypto.Hash]struct{}),
		next:             make(map[crypto.Hash]struct{}),
	}
}

// callDeposit will deposit the given amount into the account
func (am *accountManager) callDeposit(id string, amount types.Currency) error {
	his := am.h.InternalSettings()
	maxBalance := his.MaxEphemeralAccountBalance
	currentBlockHeight := am.h.BlockHeight()

	// Fetch a free index in case we need one to create an account with
	index := am.staticAccountsPersister.callAssignFreeIndex()
	am.mu.Lock()
	acc, exists := am.accounts[id]
	if !exists {
		acc = am.newAccount(id, index)
		am.accounts[id] = acc
	} else {
		// If an account already existed, make sure to release the index
		defer am.staticAccountsPersister.callReleaseIndex(index)
	}

	// Verify the deposit does not exceed the ephemeral account maximum balance
	if acc.depositExceedsMaxBalance(amount, maxBalance) {
		am.mu.Unlock()
		return ErrBalanceMaxExceeded
	}

	// Range over the blocked withdrawals and subtract it from the updated
	// account balance, effectively fulfilling the withdrawal
	updatedBalance := acc.balance.Add(amount)
	for acc.blockedCalls.Len() > 0 {
		bc := acc.blockedCalls.Pop().(*blockedCall)
		if err := bc.withdrawal.validateExpiry(currentBlockHeight); err != nil {
			bc.result <- err
			continue
		}

		if updatedBalance.Cmp(bc.withdrawal.amount) < 0 {
			acc.blockedCalls.Push(*bc)
			break
		}

		close(bc.result)
		updatedBalance = updatedBalance.Sub(bc.withdrawal.amount)
	}

	// Update account details
	acc.balance = updatedBalance
	acc.lastTxnTime = time.Now().Unix()

	accountData := acc.accountData()
	am.mu.Unlock()

	// Persist the account
	err := am.staticAccountsPersister.callSaveAccount(acc.index, accountData)
	if err != nil {
		am.h.log.Println("ERROR: could not save account:", id, err)
		return ErrAccountPersist
	}

	return nil
}

// callWithdraw will try to withdraw money from an ephemeral account. If the
// account balance is insufficient, it will block until either a timeout
// expires, the account receives sufficient funds or we receive a stop message.
//
// The caller can specify a priority, which will define the order in which the
// withdrawals get processed in the event they are blocked.
//
// Note that this function has very intricate locking, be extra cautious when
// making changes
func (am *accountManager) callWithdraw(msg *withdrawalMessage, sig crypto.Signature, priority int64) error {
	his := am.h.InternalSettings()
	maxRisk, amount, id := his.MaxEphemeralAccountRisk, msg.amount, msg.account

	// Validate the expiry block height & signature
	currentBlockHeight := am.h.BlockHeight()
	if err := msg.validateExpiry(currentBlockHeight); err != nil {
		return err
	}
	if err := msg.validateSignature(sig); err != nil {
		return err
	}

	// Fetch a free index in case we need one to create an account with
	index := am.staticAccountsPersister.callAssignFreeIndex()
	am.mu.Lock()
	acc, exists := am.accounts[id]
	if !exists {
		acc = am.newAccount(id, index)
		am.accounts[id] = acc
	} else {
		// If an account already existed, make sure to release the index
		defer am.staticAccountsPersister.callReleaseIndex(index)
	}

	// Verify if we have processed this withdrawal by checking its fingerprint
	fingerprint := crypto.HashAll(*msg)
	if exists := am.fingerprints.has(fingerprint); exists {
		am.mu.Unlock()
		return ErrWithdrawalSpent
	}

	// Save the fingerprint
	am.fingerprints.save(fingerprint, msg.expiry, currentBlockHeight)
	if err := am.h.tg.Add(); err != nil {
		am.mu.Unlock()
		return ErrWithdrawalCancelled
	}
	go func() {
		defer am.h.tg.Done()
		am.staticAccountsPersister.callSaveFingerprint(fingerprint, msg.expiry, currentBlockHeight)
	}()

	// If the account balance is insufficient to perform the withdrawal, block
	// the withdrawal.
	if acc.balance.Cmp(amount) < 0 {
		tx := blockedCall{
			withdrawal: msg,
			priority:   priority,
			result:     make(chan error),
		}
		acc.blockedCalls.Push(tx)
		am.mu.Unlock()
		return am.blockedWithdrawalResult(tx.result)
	}

	defer am.mu.Unlock()

	// Update the account details
	acc.balance = acc.balance.Sub(amount)
	acc.lastTxnTime = time.Now().Unix()
	accountData := acc.accountData()

	// Persist the account data asynchronously
	am.currentRisk = am.currentRisk.Add(amount)
	maxRiskReached := maxRisk.Cmp(am.currentRisk) < 0
	awaitPersist := make(chan struct{})

	// Persist the account in a separate goroutine. Ensure we increment the
	// threadgroup counter before to properly await pending saves if the host
	// shuts down
	if err := am.h.tg.Add(); err != nil {
		return ErrWithdrawalCancelled
	}
	go func() {
		defer am.h.tg.Done()

		// Disrupt will add latency to the persist by sleeping for the
		// predefined duration, this causes currentRisk to build up so we are
		// able to reach maxRisk in tests
		_ = am.h.dependencies.Disrupt("errMaxRiskReached")

		if err := am.staticAccountsPersister.callSaveAccount(acc.index, accountData); err != nil {
			am.h.log.Println(errors.AddContext(err, "couldn't save account"))
		}

		close(awaitPersist)

		am.mu.Lock()
		am.currentRisk = am.currentRisk.Sub(amount)
		am.mu.Unlock()
	}()

	// If we have reached the max risk, await the persist
	if maxRiskReached {
		<-awaitPersist

		// Disrupt will return a custom error here when the max risk was
		// reached, we used this for testing purposes only.
		if am.h.dependencies.Disrupt("errMaxRiskReached") {
			return errMaxRiskReached
		}
		return nil
	}

	return nil
}

// callConsensusChanged is called by the host whenever it processed a change to
// the consensus. We use it to rotate the fingerprints as we do so based on the
// current blockheight
func (am *accountManager) callConsensusChanged() {
	currentBlockHeight := am.h.BlockHeight()
	am.mu.Lock()
	am.tryRotateFingerprints(currentBlockHeight)
	am.mu.Unlock()
}

// newAccount will return a new account object
func (am *accountManager) newAccount(id string, index uint32) *account {
	return &account{
		id:           id,
		index:        index,
		blockedCalls: make(blockedCallHeap, 0),
	}
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// it does this by nullifying the account's balance which frees up the account
// at that index
func (am *accountManager) threadedPruneExpiredAccounts() {
	his := am.h.InternalSettings()
	accountExpiryTimeout := int64(his.EphemeralAccountExpiry)

	// An expiry timeout of 0 means the accounts never expire
	if accountExpiryTimeout == 0 {
		return
	}

	var forceExpire bool
	if am.h.dependencies.Disrupt("expireEphemeralAccounts") {
		forceExpire = true
	}

	for {
		var pruned []uint32

		// Increment the threadgroup counter but ensure it is decremented within
		// a single iteration of this loop. If it would be outside of the loop
		// it'd cause 'Flush' to enter into a deadlock when called
		if err := am.h.tg.Add(); err != nil {
			return
		}
		am.mu.Lock()

		now := time.Now().Unix()
		for id, acc := range am.accounts {
			if forceExpire {
				am.h.log.Debugf("DEBUG: force expiring account %v", id)
				acc.balance = types.ZeroCurrency
				pruned = append(pruned, acc.index)
				continue
			}

			if acc.balance.Equals(types.ZeroCurrency) {
				am.h.log.Debugf("DEBUG: expiring account %v at %v", id, now)
				pruned = append(pruned, acc.index)
				continue
			}

			if now-acc.lastTxnTime > accountExpiryTimeout {
				am.h.log.Debugf("DEBUG: expiring account %v at %v", id, now)
				acc.balance = types.ZeroCurrency
				pruned = append(pruned, acc.index)
			}
		}

		am.mu.Unlock()
		am.h.tg.Done()

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

// blockedWithdrawalResult will block until it receives the withdrawal result,
// expires after a time out or receives a stop signal
func (am *accountManager) blockedWithdrawalResult(resultChan chan error) error {
	for {
		select {
		case err := <-resultChan:
			if err != nil {
				return errors.AddContext(err, "blocked withdrawal failed")
			}
			return nil
		case <-time.After(blockedCallTimeout):
			return ErrBalanceInsufficient
		case <-am.h.tg.StopChan():
			return ErrWithdrawalCancelled
		}
	}
}

// tryRotateFingerprints is a helper method that tries to rotate the
// fingerprints both in-memory and on disk.
func (am *accountManager) tryRotateFingerprints(currentBlockHeight types.BlockHeight) {
	am.fingerprints.tryRotate(currentBlockHeight)

	if err := am.staticAccountsPersister.tryRotateFingerprintBuckets(currentBlockHeight); err != nil {
		am.h.log.Println("ERROR: could not rotate fingerprint buckets")
	}
}

// save will add the given fingerprint to the appropriate bucket
func (fm *fingerprintMap) save(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) {
	threshold := calculateBucketThreshold(currentBlockHeight, fm.bucketBlockRange)

	if expiry < threshold {
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
	threshold := calculateBucketThreshold(currentBlockHeight, fm.bucketBlockRange)

	// If the current blockheihgt is less than the threshold, we do not need to
	// rotate the bucket files on the file system
	if currentBlockHeight < threshold {
		return
	}

	// If it is, we swap the current and next buckets and recreate next
	fm.current = fm.next
	fm.next = make(map[crypto.Hash]struct{})
}

// validateExpiry returns an error if the withdrawal message is either already
// expired or if it expires too far into the future
func (wm *withdrawalMessage) validateExpiry(currentBlockHeight types.BlockHeight) error {
	// Verify the current blockheight does not exceed the expiry
	if currentBlockHeight > wm.expiry {
		return ErrWithdrawalExpired
	}

	// Verify the withdrawal is not too far into the future
	if wm.expiry >= calculateBucketThreshold(currentBlockHeight, bucketBlockRange)+bucketBlockRange {
		return ErrWithdrawalExtremeFuture
	}

	return nil
}

// validateSignature returns an error if the provided signature is invalid
func (wm *withdrawalMessage) validateSignature(sig crypto.Signature) error {
	var spk types.SiaPublicKey
	spk.LoadString(wm.account)
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)

	err := crypto.VerifyHash(crypto.HashAll(*wm), pk, sig)
	if err != nil {
		return ErrWithdrawalInvalidSignature
	}
	return nil
}

// depositExceedsMaxBalance returns whether or not the deposit would exceed the
// ephemeraccount max balance, taking the blocked withdrawals into account.
func (a *account) depositExceedsMaxBalance(deposit, maxBalance types.Currency) bool {
	blocked := a.blockedCalls.Value()
	if deposit.Cmp(blocked) <= 0 {
		return false
	}

	updatedBalance := a.balance.Add(deposit.Sub(blocked))
	return updatedBalance.Cmp(maxBalance) > 0
}
