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
	"gitlab.com/NebulousLabs/fastrand"
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

	// Used for testing purposes
	errMaxRiskReached = errors.New("errMaxRiskReached")

	// pruneExpiredAccountsFrequency is the frequency at which the account
	// manager expires accounts which have been inactive for too long
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
	// current blockheight, it will prune expired fingerprints when the
	// blockheight exceeds their epxiry. It has an accounts persister that will
	// persist all ephemeral account data to disk
	accountManager struct {
		accounts                map[string]*account
		fingerprints            *fingerprintMap
		staticAccountsPersister *accountsPersister

		// The accountIndex keeps track of every account index using a bitfield.
		// The index specifies at what location the account is persisted on
		// disk. When a new account is opened, it will be assigned a free index.
		// When an account expires, its index will be recycled and become
		// available.
		index *accountIndex

		// To increase performance, the account manager will persist the account
		// data in an asynchronous fashion. This will allow users to withdraw
		// money from an ephemeral account without having to wait until the new
		// account balance was successfully persisted. This allows users to
		// transact with the host with significantly less latency. This also
		// means that the money that has already been withdrawn is at risk to
		// the host. An unclean shutdown before the account manager was able to
		// persist the account balance to disk would allow the user to withdraw
		// that money twice. To limit this risk to the host, he can set a
		// maxephemeralaccountrisk, when that amount is reached all withdrawals
		// lock until the accounts have successfully been persisted.
		currentRisk     types.Currency
		currentRiskCond *sync.Cond

		mu sync.Mutex
		h  *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		index        uint32
		id           string
		balance      types.Currency
		blockedCalls blockedCallHeap

		// pendingRisk keeps track of how much of the account balance is
		// unsaved. We need to keep track of this on a per account basis,
		// because there is only one background thread saving an account,
		// meanwhile its balance can get updated by other threads. We need
		// pendingRisk to know what to subtract from the currentRisk once the
		// account is saved.
		pendingRisk types.Currency

		// lastTxnTime is the timestamp of the last transaction that occured
		// involving this ephemeral account. A transaction can be either a
		// deposit or withdrawal from the ephemeral account. We keep track of
		// this timestamp to allow pruning ephemeral accounts that have been
		// inactive for too long. The host can configure this expiry using the
		// ephemeralaccountexpiry setting.
		lastTxnTime int64
	}

	// accountIndex keeps track of account indexes. It will assign a free index
	// when a new account needs to be created. If an account expires it will
	// recycle its index.
	accountIndex struct {
		bitfields []uint64
		deleting  map[uint32]string
	}

	// blockedCall represents a pending withdrawal
	blockedCall struct {
		withdrawal *withdrawalMessage
		result     chan error
		priority   int64
	}

	// blockedCallHeap is a heap of blocking withdrawal calls; the heap is
	// sorted based on the priority field in the blocked call
	blockedCallHeap []*blockedCall

	// fingerprintMap keeps track of all the fingerprints and serves as a lookup
	// table. To make sure these fingerprints are not kept in memory forever,
	// the account manager will remove them when the current block height
	// exceeds their expiry block height. It does this by keeping the
	// fingerprints in two separate maps. When the block height reaches a
	// certain threshold, which is calculated on the fly using the
	// bucketBlockRange, the account manager will remove all fingerprints in the
	// current map and replace them with the fingerprints from the 'next' map.
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
	bc := x.(blockedCall)
	*bch = append(*bch, &bc)
}
func (bch *blockedCallHeap) Pop() interface{} {
	old := *bch
	n := len(old)
	bc := old[n-1]
	*bch = old[0 : n-1]
	return bc
}
func (bch blockedCallHeap) Value() types.Currency {
	var total types.Currency
	for _, bc := range bch {
		total = total.Add(bc.withdrawal.amount)
	}
	return total
}

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager() (_ *accountManager, err error) {
	am := &accountManager{
		accounts:     make(map[string]*account),
		fingerprints: newFingerprintMap(bucketBlockRange),
		index:        &accountIndex{deleting: make(map[uint32]string)},
		h:            h,
	}
	am.currentRiskCond = sync.NewCond(&am.mu)

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
func (am *accountManager) callDeposit(id string, amount types.Currency) error {
	his := am.h.InternalSettings()
	maxBalance := his.MaxEphemeralAccountBalance
	currentBlockHeight := am.h.BlockHeight()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Open the account, this will create one if one does not exist yet
	acc := am.openAccount(id)

	// Verify the deposit does not exceed the ephemeral account maximum balance
	if acc.depositExceedsMaxBalance(amount, maxBalance) {
		return ErrBalanceMaxExceeded
	}

	// Update the account details
	acc.balance = acc.balance.Add(amount)
	acc.lastTxnTime = time.Now().Unix()

	// Unblock any blocked calls now that the account is potentially solvent
	// enough to process them
	acc.unblockBlockedCalls(currentBlockHeight)

	// Persist the account
	if err := am.staticAccountsPersister.callSaveAccount(acc.accountData(), acc.index); err != nil {
		return ErrAccountPersist
	}

	return nil
}

// callWithdraw will try to withdraw money from an ephemeral account.
//
// If the account balance is insufficient, it will block until either a timeout
// expires, the account receives sufficient funds or we receive a stop signal.
//
// The caller can specify a priority. This priority defines the order in which
// the withdrawals get processed in the event they are blocked.
//
// Note that this function has very intricate locking, be extra cautious when
// making changes
func (am *accountManager) callWithdraw(msg *withdrawalMessage, sig crypto.Signature, priority int64) error {
	his := am.h.InternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	amount, id := msg.amount, msg.account

	// Validate the message's expiry and signature
	currentBlockHeight := am.h.BlockHeight()
	if err := msg.validate(currentBlockHeight, sig); err != nil {
		return err
	}

	am.mu.Lock()

	// If the currentRisk exceeds the maxRisk, we block. This block is lifted
	// when the persist threads have successfully persisted account data to
	// disk, lowering outstanding risk.
	if err := am.blockedMaxRiskReached(maxRisk); err != nil {
		am.mu.Unlock()
		return err
	}

	// Verify the fingerprint has not been processed before
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

	// Open the account, create if it does not exist yet
	acc := am.openAccount(id)

	// If the account balance is insufficient, block the withdrawal.
	if acc.balance.Cmp(amount) < 0 {
		bc := blockedCall{
			withdrawal: msg,
			priority:   priority,
			result:     make(chan error),
		}
		acc.blockedCalls.Push(bc)
		am.mu.Unlock()
		return am.blockedWithdrawalResult(bc.result)
	}

	// Update the account details
	acc.balance = acc.balance.Sub(amount)
	acc.lastTxnTime = time.Now().Unix()

	// Update outstanding risk
	am.currentRisk = am.currentRisk.Add(amount)
	acc.pendingRisk = acc.pendingRisk.Add(amount)
	if acc.pendingRisk.Equals(amount) {
		// Ensure that only one background thread is saving this account. This
		// enables us to update the account balance while we wait on the
		// persister.
		go am.threadedSaveAccount(id)
	}

	am.mu.Unlock()
	return nil
}

// callConsensusChanged is called by the host whenever it processed a change to
// the consensus. We use it to remove fingerprints which have been expired.
func (am *accountManager) callConsensusChanged() {
	currentBlockHeight := am.h.BlockHeight()
	if err := am.managedRotateFingerprints(currentBlockHeight); err != nil {
		am.h.log.Critical("Could not rotate fingerprints", err)
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

	// Acquire a lock and gather the data we are about to persist
	am.mu.Lock()
	acc, exists := am.accounts[id]
	if !exists {
		am.mu.Unlock()
		return
	}
	_, deleting := am.index.deleting[acc.index]
	if deleting {
		am.currentRisk = am.currentRisk.Sub(acc.pendingRisk)
		am.mu.Unlock()
		return
	}
	pendingRisk := acc.pendingRisk
	accountData := acc.accountData()
	am.mu.Unlock()

	// Persist the data
	_ = am.h.dependencies.Disrupt("errMaxRiskReached")
	if err := am.staticAccountsPersister.callSaveAccount(accountData, acc.index); err != nil {
		am.h.log.Println("Failed to save account", err)
	}

	// Reacquire the lock and update the pendingRisk and currentRisk. Broadcast
	// this update to the currentRisk so pending calls unblock.
	am.mu.Lock()
	am.currentRisk = am.currentRisk.Sub(pendingRisk)
	am.currentRiskCond.Broadcast()
	acc, exists = am.accounts[id]
	if !exists {
		am.mu.Unlock()
		return
	}

	acc.pendingRisk = acc.pendingRisk.Sub(pendingRisk)
	if !acc.pendingRisk.Equals(types.ZeroCurrency) {
		go am.threadedSaveAccount(id)
	}
	am.mu.Unlock()
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// for too long. It does this by comparing the account's lastTxnTime to the
// current time. If it exceeds the EphemeralAccountExpiry, the account is
// considered expired.
func (am *accountManager) threadedPruneExpiredAccounts() {
	his := am.h.InternalSettings()
	accountExpiryTimeout := int64(his.EphemeralAccountExpiry)
	forceExpire := am.h.dependencies.Disrupt("expireEphemeralAccounts")

	// If the host set a timeout of 0, it means the accounts never expire.
	if accountExpiryTimeout == 0 {
		return
	}

	for {
		// Note: threadgroup counter must be inside for loop. If not, calling
		// 'Flush' on the threadgroup would deadlock.
		if err := am.h.tg.Add(); err != nil {
			return
		}

		// Loop all accounts and expire the ones that have been inactive for too
		// long. Keep track of it using the index.
		am.mu.Lock()
		now := time.Now().Unix()
		for id, acc := range am.accounts {
			if forceExpire {
				am.index.deleting[acc.index] = id
				continue
			}

			if now-acc.lastTxnTime > accountExpiryTimeout {
				am.h.log.Debugf("DEBUG: expiring account %v at %v", id, now)
				am.index.deleting[acc.index] = id
			}
		}
		am.mu.Unlock()

		// Call deleteAccount on the persister for all accounts that expired.
		var deleted []uint32
		for index := range am.index.deleting {
			if err := am.staticAccountsPersister.callDeleteAccount(index); err != nil {
				am.h.log.Println("ERROR: account could not be deleted")
				continue
			}
			deleted = append(deleted, index)
		}

		// Once successfully removed from disk, delete the account and free up
		// its index
		am.mu.Lock()
		for _, index := range deleted {
			id := am.index.deleting[index]
			delete(am.accounts, id)
			delete(am.index.deleting, index)
			am.index.releaseIndex(index)
		}
		am.mu.Unlock()

		am.h.tg.Done()

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
// expires after a timeout, or receives a stop signal
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

// blockedMaxRiskReached will block until it the curentRisk drops below the
// allow maximum, or until we receive a timeout.
func (am *accountManager) blockedMaxRiskReached(maxRisk types.Currency) error {
	done := make(chan error)
	for am.currentRisk.Cmp(maxRisk) >= 0 {
		go func() {
			if err := am.h.tg.Add(); err != nil {
				done <- err
				return
			}
			defer am.h.tg.Done()
			am.currentRiskCond.Wait()
			close(done)
		}()

		select {
		case err := <-done:
			// signal max risk is reached for testing purposes
			if am.h.dependencies.Disrupt("errMaxRiskReached") {
				return errMaxRiskReached
			}
			return err
		case <-time.After(blockedCallTimeout):
			return ErrWithdrawalCancelled
		}
	}
	return nil
}

// managedRotateFingerprints is a helper method that tries to rotate the
// fingerprints both in-memory and on disk.
func (am *accountManager) managedRotateFingerprints(currentBlockHeight types.BlockHeight) error {
	am.mu.Lock()
	am.fingerprints.tryRotate(currentBlockHeight)
	am.mu.Unlock()
	return am.staticAccountsPersister.callRotateFingerprintBuckets(currentBlockHeight)
}

// openAccount will return a new account object
func (am *accountManager) openAccount(id string) *account {
	acc, exists := am.accounts[id]
	if !exists {
		acc = &account{
			id:           id,
			index:        am.index.assignFreeIndex(),
			blockedCalls: make(blockedCallHeap, 0),
		}
		am.accounts[id] = acc
	}
	return acc
}

// assignFreeIndex will return the next available account index
func (ai *accountIndex) assignFreeIndex() uint32 {
	var i, pos int = 0, -1

	// Go through all bitmaps in random order to find a free index
	full := ^uint64(0)
	for i := range fastrand.Perm(len(ai.bitfields)) {
		if ai.bitfields[i] != full {
			pos = bits.TrailingZeros(uint(^ai.bitfields[i]))
			break
		}
	}

	// Add a new bitfield if all account indices are taken
	if pos == -1 {
		pos = 0
		ai.bitfields = append(ai.bitfields, 1<<uint(pos))
	} else {
		ai.bitfields[i] |= (1 << uint(pos))
	}

	// Calculate the index my multiplying the bitfield index by 64 (seeing as
	// the bitfields are of type uint64) and adding the position
	index := uint32((i * 64) + pos)

	return index
}

// releaseIndex will unset the bit corresponding to given index
func (ai *accountIndex) releaseIndex(index uint32) {
	i := index / 64
	pos := index % 64
	var mask uint64 = ^(1 << pos)
	ai.bitfields[i] &= mask
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

	// Add empty bitfields
	n := int(math.Floor(float64(maxIndex)/64)) + 1
	for i := 0; i < n; i++ {
		ai.bitfields = append(ai.bitfields, uint64(0))
	}

	// Range over the accounts and flip their index bit
	for _, acc := range accounts {
		i := acc.index / 64
		pos := acc.index % 64
		ai.bitfields[i] = ai.bitfields[i] << uint(pos)
	}
}

// save will add the given fingerprint to the appropriate bucket
func (fm *fingerprintMap) save(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) {
	threshold := calculateExpiryThreshold(currentBlockHeight, fm.bucketBlockRange)
	if expiry < threshold {
		fm.current[fp] = struct{}{}
		return
	}
	fm.next[fp] = struct{}{}
}

// has returns true when the given fingerprint is present in the map
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
	threshold := calculateExpiryThreshold(currentBlockHeight, fm.bucketBlockRange)

	// If the current blockheihgt is less than the threshold, we do not need to
	// rotate the bucket files on the file system
	if currentBlockHeight < threshold {
		return
	}

	// If it is, we swap the current and next buckets and recreate next
	fm.current = fm.next
	fm.next = make(map[crypto.Hash]struct{})
}

// validate is a helper function that composes validateExpiry and
// validateSignature
func (wm *withdrawalMessage) validate(currentBlockHeight types.BlockHeight, sig crypto.Signature) error {
	return errors.Compose(
		wm.validateExpiry(currentBlockHeight),
		wm.validateSignature(sig),
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
	if wm.expiry >= calculateExpiryThreshold(currentBlockHeight, bucketBlockRange)+bucketBlockRange {
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
// ephemer account max balance, taking the blocked withdrawals into account.
func (a *account) depositExceedsMaxBalance(deposit, maxBalance types.Currency) bool {
	blocked := a.blockedCalls.Value()
	if deposit.Cmp(blocked) <= 0 {
		return false
	}

	updatedBalance := a.balance.Add(deposit.Sub(blocked))
	return updatedBalance.Cmp(maxBalance) > 0
}

// depositExceedsMaxBalance returns whether or not the deposit would exceed the
// ephemer account max balance, taking the blocked withdrawals into account.
func (a *account) unblockBlockedCalls(currentBlockHeight types.BlockHeight) {
	for a.blockedCalls.Len() > 0 {
		bc := a.blockedCalls.Pop().(*blockedCall)
		if err := bc.withdrawal.validateExpiry(currentBlockHeight); err != nil {
			bc.result <- err
			continue
		}

		// requeue if balance is insufficient
		if a.balance.Cmp(bc.withdrawal.amount) < 0 {
			a.blockedCalls.Push(*bc)
			break
		}

		a.balance = a.balance.Sub(bc.withdrawal.amount)
		close(bc.result)
	}
}
