package host

import (
	"math"
	"math/bits"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
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

	// Used for testing purposes
	errMaxRiskReached = errors.New("errMaxRiskReached")

	// pruneExpiredAccountsFrequency is the frequency at which the account
	// manager expires accounts which have been inactive for too long
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Minute,
		Testing:  2 * time.Second,
	}).(time.Duration)

	// blockedWithdrawalTimeout is the amount of time after which a blocked
	// withdrawal times out
	blockedWithdrawalTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      5 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)
)

// The AccountManager manages all of the ephemeral accounts on the host.
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
	// withdrawalMessage contains all details to process a withdrawal
	withdrawalMessage struct {
		account string
		expiry  types.BlockHeight
		amount  types.Currency
		nonce   uint64
	}

	// The accountManager manages all deposits and withdrawals from an ephemeral
	// account. It uses an accounts persister to save the account data to disk.
	// It keeps track of the fingerprints (HASH(withdrawalMessage)), to ensure
	// the same withdrawal can not be performed twice. The account manager is
	// hooked into consensus updates, this allows pruning all fingerprints of
	// withdrawals that have expired, so memory does not build up.
	accountManager struct {
		accounts                map[string]*account
		fingerprints            *fingerprintMap
		staticAccountsPersister *accountsPersister

		// The accountIndex keeps track of all account indexes. The account
		// index decides the location at which the account is stored on disk.
		index accountIndex

		// To increase performance of withdrawals, the account manager will
		// persist the account data in an asynchronous fashion. This will allow
		// users to withdraw money from an ephemeral account without having to
		// wait until the new account balance was successfully persisted. This
		// allows users to transact with the host with significantly less
		// latency. This also means that the money that has already been
		// withdrawn is at risk to the host. An unclean shutdown before the
		// account manager was able to persist the account balance to disk would
		// allow the user to withdraw that money twice. To limit this risk to
		// the host, he can set a maxephemeralaccountrisk, when that amount is
		// reached all withdrawals lock until the accounts have successfully
		// been persisted.
		currentRisk types.Currency

		// When the currentRisk exceeds the maxRisk, all withdrawals are
		// appended to the blockedWithdrawals queue. The persist threads will
		// unblock these in a FIFO fashion.
		blockedWithdrawals []*blockedWithdrawal

		mu sync.Mutex
		h  *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		index              uint32
		id                 string
		balance            types.Currency
		blockedWithdrawals blockedWithdrawalHeap

		// pendingRisk keeps track of how much of the account balance is
		// unsaved. We keep track of this on a per account basis because the
		// background thread persisting the account needs to know how much to
		// deduct from the overall pending risk. If the persist thread has to
		// wait, consecutive withdrawals can update the account balance in the
		// meantime, this builds up the pendingRisk. When the account is saved,
		// pending risk is lowered.
		pendingRisk types.Currency

		// lastTxnTime is the timestamp of the last transaction that occured
		// involving this ephemeral account. A transaction can be either a
		// deposit or withdrawal from the ephemeral account. We keep track of
		// this timestamp to allow pruning ephemeral accounts that have been
		// inactive for too long. The host can configure this expiry using the
		// ephemeralaccountexpiry setting.
		lastTxnTime int64
	}

	// accountIndex is a bitfield to keeps track of account indexes. It will
	// assign a free index when a new account needs to be created. It will
	// recycle the indexes of accounts that have expired.
	accountIndex []uint64

	// blockedWithdrawal represents a withdrawal call that is pending.
	blockedWithdrawal struct {
		withdrawal *withdrawalMessage
		result     chan error
		priority   int64
	}

	// blockedWithdrawalHeap is a heap of blocked withdrawal calls; the heap is
	// sorted based on the priority field
	blockedWithdrawalHeap []*blockedWithdrawal

	// fingerprintMap keeps track of all the fingerprints and serves as a lookup
	// table. To make sure these fingerprints are not kept in memory forever,
	// the account manager will remove them when the current block height
	// exceeds their expiry. It does this by keeping the fingerprints in two
	// separate maps. When the block height reaches a certain threshold, which
	// is calculated on the fly using the current block height and
	// bucketBlockRange, the account manager will remove all fingerprints in the
	// current map and replace them with the fingerprints from the 'next' map.
	fingerprintMap struct {
		bucketBlockRange int
		current          map[crypto.Hash]struct{}
		next             map[crypto.Hash]struct{}
	}
)

// Implementation of heap.Interface for blockedWithdrawalHeap.
func (bwh blockedWithdrawalHeap) Len() int           { return len(bwh) }
func (bwh blockedWithdrawalHeap) Less(i, j int) bool { return bwh[i].priority < bwh[j].priority }
func (bwh blockedWithdrawalHeap) Swap(i, j int)      { bwh[i], bwh[j] = bwh[j], bwh[i] }
func (bwh *blockedWithdrawalHeap) Push(x interface{}) {
	bw := x.(blockedWithdrawal)
	*bwh = append(*bwh, &bw)
}
func (bwh *blockedWithdrawalHeap) Pop() interface{} {
	old := *bwh
	n := len(old)
	bw := old[n-1]
	*bwh = old[0 : n-1]
	return bw
}
func (bwh blockedWithdrawalHeap) Value() types.Currency {
	var total types.Currency
	for _, bw := range bwh {
		total = total.Add(bw.withdrawal.amount)
	}
	return total
}

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager() (_ *accountManager, err error) {
	am := &accountManager{
		accounts:           make(map[string]*account),
		fingerprints:       newFingerprintMap(bucketBlockRange),
		blockedWithdrawals: make([]*blockedWithdrawal, 0),
		index:              make([]uint64, 0),
		h:                  h,
	}

	// Create the accounts persister
	am.staticAccountsPersister, err = h.newAccountsPersister(am)
	if err != nil {
		return nil, err
	}

	// Load the persisted accounts data from disk
	var data *accountsPersisterData
	if data, err = am.staticAccountsPersister.callLoadData(); err != nil {
		return nil, err
	}
	am.accounts = data.accounts
	am.fingerprints.next = data.fingerprints

	// Build the account index
	am.index.buildIndex(am.accounts)

	// Close any open file handles if we receive a stop signal
	am.h.tg.AfterStop(func() {
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

// callDeposit will deposit the given amount into the account.
func (am *accountManager) callDeposit(id string, amount types.Currency) (err error) {
	his := am.h.InternalSettings()
	maxBalance := his.MaxEphemeralAccountBalance
	currentBlockHeight := am.h.BlockHeight()

	// Defer a call to save the account here, outside the lock
	var index uint32
	var data *accountData
	defer func() {
		if err == nil {
			pErr := am.staticAccountsPersister.callSaveAccount(data, index)
			if pErr != nil {
				err = errors.Extend(pErr, ErrAccountPersist)
			}
		}
	}()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Open the account, this will create one if one does not exist yet
	acc := am.openAccount(id)
	index = acc.index

	// Verify the deposit does not exceed the ephemeral account maximum balance
	if acc.depositExceedsMaxBalance(amount, maxBalance) {
		err = ErrBalanceMaxExceeded
		return
	}

	// Update the account details
	acc.balance = acc.balance.Add(amount)
	acc.lastTxnTime = time.Now().Unix()

	// Unblock withdrawals now that the account is potentially sufficiently
	// funded to process them
	acc.unblockWithdrawals(currentBlockHeight)

	data = acc.accountData() // calculate data to persist -> see defer

	return nil
}

// callWithdraw will try to process the given withdrawal message. This call will
// block if either balance is insufficient, or if current risk exceeds the max.
// The caller can specify a priority. This priority defines the order in which
// the withdrawals get processed in the event they are blocked due to
// insufficient funds.
func (am *accountManager) callWithdraw(msg *withdrawalMessage, sig crypto.Signature, priority int64) (err error) {
	his := am.h.InternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	amount, id := msg.amount, msg.account
	cbh := am.h.BlockHeight()

	// Validate the message's expiry and signature
	fingerprint := crypto.HashAll(*msg)
	if err := msg.validate(cbh, fingerprint, sig); err != nil {
		return err
	}

	// Withdrawals can be blocked for two reasons. Either the account balance is
	// insufficient, or the current risk exceeds the max risk. In both cases we
	// want to block, not holding a lock.
	resultChan := make(chan error)
	var notFunded, maxRiskReached bool
	defer func() {
		if notFunded {
			err = am.waitForResult(resultChan)
			return
		}
		if maxRiskReached {
			if err == nil {
				err = am.waitForResult(resultChan)
			}
			return
		}
	}()
	am.mu.Lock()
	defer am.mu.Unlock()

	// Save the fingerprint in memory, if it is known an error is returned. If
	// the save was successful, call persist asynchronously.
	if err := am.fingerprints.add(fingerprint, msg.expiry, cbh); err != nil {
		return errors.Extend(err, ErrWithdrawalSpent)
	}
	if err := am.h.tg.Add(); err != nil {
		return errors.Extend(err, ErrWithdrawalCancelled)
	}
	go func() {
		defer am.h.tg.Done()
		am.threadedSaveFingerprint(fingerprint, msg.expiry, cbh)
	}()

	// Open the account, create if it does not exist yet
	acc := am.openAccount(id)

	// If the account balance is insufficient, block the withdrawal.
	if acc.balance.Cmp(amount) < 0 {
		acc.blockedWithdrawals.Push(blockedWithdrawal{
			withdrawal: msg,
			priority:   priority,
			result:     resultChan,
		})
		notFunded = true
		return nil
	}

	// If the currentRisk exceeds the maxRisk, block the withdrawal.
	if len(am.blockedWithdrawals) > 0 || am.currentRisk.Cmp(maxRisk) >= 0 {
		if am.h.dependencies.Disrupt("errMaxRiskReached") {
			return errMaxRiskReached // only for testing purposes
		}
		am.blockedWithdrawals = append(am.blockedWithdrawals, &blockedWithdrawal{
			withdrawal: msg,
			priority:   priority,
			result:     resultChan,
		})
		maxRiskReached = true
		return
	}

	// Update the account details
	acc.balance = acc.balance.Sub(amount)
	acc.lastTxnTime = time.Now().Unix()

	// Ensure that only one background thread is saving this account. This
	// enables us to update the account balance while we wait on the persister.
	if acc.pendingRisk.IsZero() {
		go am.threadedSaveAccount(id)
	}

	// Update risk
	am.currentRisk = am.currentRisk.Add(amount)
	acc.pendingRisk = acc.pendingRisk.Add(amount)

	return nil
}

// callConsensusChanged is called by the host whenever it processed a change to
// the consensus. We use it to remove fingerprints which have been expired.
func (am *accountManager) callConsensusChanged() {
	cbh := am.h.BlockHeight()

	// If the current blockheihgt is equal to the minimum block height of the
	// current bucket range, we want to rotate the buckets.
	if min, _ := currentBucketRange(cbh); min == cbh {
		am.mu.Lock()
		am.fingerprints.rotate()
		am.mu.Unlock()

		err := am.staticAccountsPersister.callRotateFingerprintBuckets()
		if err != nil {
			am.h.log.Critical("Could not rotate fingerprints", err)
		}
	}
}

// threadedSaveAccount will save the account with given id. There is only ever
// one background thread per account running. This is ensured by the account's
// pendingRisk, only if that increases from 0 -> something, a goroutine is
// scheduled to save the account.
func (am *accountManager) threadedSaveAccount(id string) {
	if err := am.h.tg.Add(); err != nil {
		return
	}
	defer am.h.tg.Done()

	// Fetch account data, the index and the pending risk. This pending risk
	// will be the delta between what is on-disk already and in-memory. It is
	// this delta that we can subtract from the current risk once the account
	// has been persisted.
	data, index, pendingRisk, ok := am.managedAccountInfo(id)
	if !ok {
		return
	}

	// Call save account (disrupt if triggered will introduce a sleep here,
	// simulating a slow persist which allows maxRisk to be reached)
	_ = am.h.dependencies.Disrupt("errMaxRiskReached")
	persister := am.staticAccountsPersister
	if err := persister.callSaveAccount(data, index); err != nil {
		am.h.log.Println("Failed to save account", err)
		return
	}

	currentBlockHeight := am.h.BlockHeight()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Take care of the pending risk in the account
	acc, exists := am.accounts[id]
	if exists {
		acc.pendingRisk = acc.pendingRisk.Sub(pendingRisk)
		if !acc.pendingRisk.IsZero() {
			go am.threadedSaveAccount(id)
		}
	}

	// Before updating the pending risk, we want to unblock blocked withdrawals.
	// As long as we have delta left, unblock blocked withdrawals. The unblocked
	// amount is subtracted from the delta.
	var unblocked int
	for i, bw := range am.blockedWithdrawals {
		amount, id := bw.withdrawal.amount, bw.withdrawal.account
		acc, exists := am.accounts[id]
		if !exists {
			continue
		}

		// validate the expiry
		if err := bw.withdrawal.validateExpiry(currentBlockHeight); err != nil {
			bw.result <- err
			continue
		}

		// sanity check
		if acc.balance.Cmp(amount) < 0 {
			build.Critical("blocked withdrawal has insufficient balance to process, due to the order of execution in the callWithdrawal, this should never happen")
		}

		if pendingRisk.Cmp(amount) < 0 {
			unblocked = i
			break
		}
		pendingRisk = pendingRisk.Sub(amount)

		// Update the account details
		acc.balance = acc.balance.Sub(amount)
		acc.lastTxnTime = time.Now().Unix()

		// Ensure that only one background thread is saving this account. This
		// enables us to update the account balance while we wait on the
		// persister.
		if acc.pendingRisk.IsZero() {
			go am.threadedSaveAccount(id)
		}
		acc.pendingRisk = acc.pendingRisk.Add(amount)
		close(bw.result)
	}
	am.blockedWithdrawals = am.blockedWithdrawals[unblocked:]

	// Update current risk
	am.currentRisk = am.currentRisk.Sub(pendingRisk)
}

// threadedSaveFingerprint will persist the fingerprint data
// Note that the caller adds this thread to the threadgroup. If the add is done
// inside the goroutine, we risk losing a fingerprint if the host shuts down.
func (am *accountManager) threadedSaveFingerprint(fp crypto.Hash, expiry, cbh types.BlockHeight) {
	if err := am.staticAccountsPersister.callSaveFingerprint(fp, expiry, cbh); err != nil {
		am.h.log.Critical("Could not save fingerprint", err)
	}
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// for too long. It does this by comparing the account's lastTxnTime to the
// current time. If it exceeds the EphemeralAccountExpiry, the account is
// considered expired.
//
// Note: threadgroup counter must be inside for loop. If not, calling 'Flush'
// on the threadgroup would deadlock.
func (am *accountManager) threadedPruneExpiredAccounts() {
	// Disrupt can trigger forceful expirys for testing purposes
	forceExpire := am.h.dependencies.Disrupt("expireEphemeralAccounts")

	for {
		// If the host set a timeout of 0, it means the accounts never expire.
		his := am.h.InternalSettings()
		accountExpiryTimeout := int64(his.EphemeralAccountExpiry)
		if accountExpiryTimeout == 0 {
			return
		}

		func() {
			if err := am.h.tg.Add(); err != nil {
				return
			}
			defer am.h.tg.Done()

			// Expire accounts that have been inactive for too long. Keep track
			// of the indexes that got expired
			expired := am.managedExpireAccounts(forceExpire, accountExpiryTimeout)
			if len(expired) == 0 {
				return
			}

			// Batch delete the expired accounts on disk
			deleted, err := am.staticAccountsPersister.callBatchDeleteAccount(expired)
			if err != nil {
				am.h.log.Println(errors.AddContext(err, "prune expired accounts failed"))
			}
			if len(deleted) == 0 {
				return
			}

			// Once deleted from disk, recycle the indexes.
			am.mu.Lock()
			for _, index := range deleted {
				am.index.releaseIndex(index)
			}
			am.mu.Unlock()
		}()

		// Block until next cycle.
		select {
		case <-am.h.tg.StopChan():
			return
		case <-time.After(pruneExpiredAccountsFrequency):
			continue
		}
	}
}

// waitForResult will block until it receives a message on the given result
// channel, or until it either times out or receives a stop signal
func (am *accountManager) waitForResult(resultChan chan error) error {
	select {
	case err := <-resultChan:
		if err != nil {
			return errors.AddContext(err, "blocked withdrawal failed")
		}
		return nil
	case <-time.After(blockedWithdrawalTimeout):
		return ErrBalanceInsufficient
	case <-am.h.tg.StopChan():
		return ErrWithdrawalCancelled
	}
}

// managedExpireAccounts will expire accounts where the lastTxnTime exceeds the
// given threshold, or when forced.
func (am *accountManager) managedExpireAccounts(force bool, threshold int64) []uint32 {
	am.mu.Lock()
	defer am.mu.Unlock()

	var deleted []uint32
	now := time.Now().Unix()
	for id, acc := range am.accounts {
		if force || now-acc.lastTxnTime > threshold {
			deleted = append(deleted, acc.index)
			am.currentRisk = am.currentRisk.Sub(acc.pendingRisk)
			delete(am.accounts, id)
		}
	}
	return deleted
}

// managedAccountInfo is a helper function that will return all required data to
// perform a threaded persist
func (am *accountManager) managedAccountInfo(id string) (*accountData, uint32, types.Currency, bool) {
	am.mu.Lock()
	defer am.mu.Unlock()

	acc, exists := am.accounts[id]
	if !exists {
		return nil, 0, types.ZeroCurrency, false
	}
	return acc.accountData(), acc.index, acc.pendingRisk, true
}

// openAccount will return a new account object
func (am *accountManager) openAccount(id string) *account {
	acc, exists := am.accounts[id]
	if !exists {
		acc = &account{
			id:                 id,
			index:              am.index.assignFreeIndex(),
			blockedWithdrawals: make(blockedWithdrawalHeap, 0),
		}
		// fmt.Println("assigning index:", acc.index)
		am.accounts[id] = acc
	}
	return acc
}

// assignFreeIndex will return the next available account index
func (ai *accountIndex) assignFreeIndex() uint32 {
	var i, pos int = 0, -1

	// Go through all bitmaps in random order to find a free index
	full := ^uint64(0)
	for i = range *ai {
		if (*ai)[i] != full {
			pos = bits.TrailingZeros(uint(^(*ai)[i]))
			break
		}
	}

	// Add a new bitfield if all bitfields are full, otherwise flip the bit
	if pos == -1 {
		pos = 0
		*ai = append(*ai, 1<<uint(pos))
		i = len(*ai) - 1
	} else {
		(*ai)[i] |= (1 << uint(pos))
	}

	// Calculate the index by multiplying the bitfield index by 64 (seeing as
	// the bitfields are of type uint64) and adding the position
	index := uint32((i * 64) + pos)
	return index
}

// releaseIndex will unset the bit corresponding to given index
func (ai *accountIndex) releaseIndex(index uint32) {
	i := index / 64
	pos := index % 64
	var mask uint64 = ^(1 << pos)
	(*ai)[i] &= mask
}

// buildAccountIndex will initialize bitfields representing all ephemeral
// accounts, the index is used to recycle account indices when the account
// expires.
func (ai *accountIndex) buildIndex(accounts map[string]*account) {
	var maxIndex uint32
	for _, acc := range accounts {
		if acc.index > maxIndex {
			maxIndex = acc.index
		}
	}

	// Add empty bitfields to accomodate all account indexes
	n := int(math.Floor(float64(maxIndex)/64)) + 1
	for i := 0; i < n; i++ {
		*ai = append(*ai, uint64(0))
	}

	// Range over the accounts and flip the bit corresponding to their index
	for _, acc := range accounts {
		i := acc.index / 64
		pos := acc.index % 64
		(*ai)[i] = (*ai)[i] << uint(pos)
	}
}

// add will add the given fingerprint to the fingerprintMap
func (fm *fingerprintMap) add(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) error {
	if fm.has(fp) {
		return errors.New("duplicate fingerprint")
	}

	_, max := currentBucketRange(currentBlockHeight)
	if expiry < max {
		fm.current[fp] = struct{}{}
		return nil
	}

	fm.next[fp] = struct{}{}
	return nil
}

// has returns true when the given fingerprint is present in the fingerprintMap
func (fm *fingerprintMap) has(fp crypto.Hash) bool {
	_, exists := fm.current[fp]
	if !exists {
		_, exists = fm.next[fp]
	}
	return exists
}

// rotate will swap the current fingerprints with the next fingerprints and
// recreate the next fingerprints map. This effectively removes all fingerprints
// in the current bucket.
func (fm *fingerprintMap) rotate() {
	fm.current = fm.next
	fm.next = make(map[crypto.Hash]struct{})
}

// validate is a helper function that composes validateExpiry and
// validateSignature
func (wm *withdrawalMessage) validate(currentBlockHeight types.BlockHeight, hash crypto.Hash, sig crypto.Signature) error {
	return errors.Compose(
		wm.validateExpiry(currentBlockHeight),
		wm.validateSignature(hash, sig),
	)
}

// validateExpiry returns an error if the withdrawal message is either already
// expired or if it expires too far into the future
func (wm *withdrawalMessage) validateExpiry(currentBlockHeight types.BlockHeight) error {
	// Verify the current blockheight does not exceed the expiry
	if currentBlockHeight > wm.expiry {
		return ErrWithdrawalExpired
	}

	// Verify the withdrawal is not too far into the future
	_, max := currentBucketRange(currentBlockHeight)
	if wm.expiry >= max+bucketBlockRange {
		return ErrWithdrawalExtremeFuture
	}

	return nil
}

// validateSignature returns an error if the provided signature is invalid
func (wm *withdrawalMessage) validateSignature(hash crypto.Hash, sig crypto.Signature) error {
	var spk types.SiaPublicKey
	spk.LoadString(wm.account)
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)

	err := crypto.VerifyHash(hash, pk, sig)
	if err != nil {
		return errors.Extend(err, ErrWithdrawalInvalidSignature)
	}
	return nil
}

// depositExceedsMaxBalance returns whether or not the deposit would exceed the
// ephemeraccountmaxbalance, taking the blocked withdrawals into account.
func (a *account) depositExceedsMaxBalance(deposit, maxBalance types.Currency) bool {
	blockedValue := a.blockedWithdrawals.Value()
	if deposit.Cmp(blockedValue) <= 0 {
		return false
	}
	updatedBalance := a.balance.Add(deposit.Sub(blockedValue))
	return updatedBalance.Cmp(maxBalance) > 0
}

// unblockWithdrawals goes through all blocked withdrawals and unblocks the ones
// for which the account has sufficient balance. This function alters the
// account balance to reflect all withdrawals that have been unblocked.
func (a *account) unblockWithdrawals(currentBlockHeight types.BlockHeight) {
	for a.blockedWithdrawals.Len() > 0 {
		bw := a.blockedWithdrawals.Pop().(*blockedWithdrawal)
		if err := bw.withdrawal.validateExpiry(currentBlockHeight); err != nil {
			bw.result <- err
			continue
		}

		// requeue if balance is insufficient
		if a.balance.Cmp(bw.withdrawal.amount) < 0 {
			a.blockedWithdrawals.Push(*bw)
			break
		}

		a.balance = a.balance.Sub(bw.withdrawal.amount)
		close(bw.result)
	}
}
