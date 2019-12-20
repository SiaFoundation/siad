package host

import (
	"math"
	"math/bits"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrWithdrawalsInactive occurs when the host is not yet synced. If that is
	// the case the account manager does not allow trading money from the
	// ephemeral accounts.
	ErrWithdrawalsInactive = errors.New("ephemeral account withdrawals are inactive, the host is not synced")

	// ErrAccountPersist occurs when an ephemeral account could not be persisted
	// to disk.
	ErrAccountPersist = errors.New("ephemeral account could not be persisted to disk")

	// ErrAccountExpired occurs when a blocked action can not complete because
	// the account has expired in the mean time.
	ErrAccountExpired = errors.New("ephemeral account expired")

	// ErrBalanceInsufficient occurs when a withdrawal could not be succesfully
	// completed because the account balance was insufficient.
	ErrBalanceInsufficient = errors.New("ephemeral account balance was insufficient")

	// ErrBalanceMaxExceeded occurs when a deposit is so large that if it would
	// get added to the account balance it would exceed the ephemeral account
	// maximum balance.
	ErrBalanceMaxExceeded = errors.New("ephemeral account maximam balance exceeded")

	// ErrWithdrawalSpent occurs when a withdrawal is being performed using a
	// withdrawal message that has been spent already.
	ErrWithdrawalSpent = errors.New("withdrawal message was already spent")

	// ErrWithdrawalExpired occurs when the withdrawal message's expiry
	// block height is in the past.
	ErrWithdrawalExpired = errors.New("ephemeral account withdrawal message expired")

	// ErrWithdrawalExtremeFuture occurs when the withdrawal message's expiry
	// block height is too far into the future.
	ErrWithdrawalExtremeFuture = errors.New("ephemeral account withdrawal message expires too far into the future")

	// ErrWithdrawalInvalidSignature occurs when the signature provided with the
	// withdrawal message was invalid.
	ErrWithdrawalInvalidSignature = errors.New("ephemeral account withdrawal message signature is invalid")

	// ErrWithdrawalCancelled occurs when the host was willingly or unwillingly
	// stopped in the midst of a withdrawal process.
	ErrWithdrawalCancelled = errors.New("ephemeral account withdrawal cancelled due to a shutdown")

	// ErrDepositCancelled occurs when the host was willingly or unwillingly
	// stopped in the midst of a deposit process.
	ErrDepositCancelled = errors.New("ephemeral account deposit cancelled due to a shutdown")

	// Used for testing purposes. This error gets returned when a certain
	// dependency is specified, this way we signal the caller max risk is
	// reached, which is otherwise an internal state that is never exposed.
	errMaxRiskReached = errors.New("errMaxRiskReached")

	// pruneExpiredAccountsFrequency is the frequency at which the account
	// manager checks if it can expire accounts which have been inactive for too
	// long.
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 1 * time.Hour,
		Dev:      15 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// blockedWithdrawalTimeout is the amount of time after which a blocked
	// withdrawal times out.
	blockedWithdrawalTimeout = build.Select(build.Var{
		Standard: 15 * time.Minute,
		Dev:      5 * time.Minute,
		Testing:  2 * time.Second,
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

	// The accountManager manages all deposits and withdrawals to and from an
	// ephemeral account. It uses an accounts persister to save the account data
	// to disk. It keeps track of the fingerprints, which are hashes of the
	// withdrawal messages, to ensure the same withdrawal can not be performed
	// twice. The account manager is hooked into consensus updates. This allows
	// pruning fingerprints of withdrawals that have expired, so memory does not
	// build up.
	accountManager struct {
		accounts                map[string]*account
		fingerprints            *fingerprintMap
		staticAccountsPersister *accountsPersister

		// The accountIndex keeps track of all account indexes. The account
		// index decides the location at which the account is stored in the
		// accounts file on disk.
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
		// reached all consecutive withdrawals and deposits block until the
		// account manager has successfully persisted all updates to disk and
		// risk is lowered.
		currentRisk types.Currency

		// When maxRisk is reached, all withdrawals are appended to a queue,
		// they will get processed in a FIFO fashion when risk is lowered.
		blockedWithdrawals []*blockedWithdrawal

		// When maxRisk is reached, all deposits are appended to a queue,
		// they will get processed in a FIFO fashion when risk is lowered.
		blockedDeposits []*blockedDeposit

		// withdrawalsInactive indicates whether the account manager allows
		// withdrawals or not. This will be false if the host is not synced.
		withdrawalsInactive bool

		mu sync.Mutex
		h  *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		index              uint32
		id                 string
		balance            types.Currency
		blockedWithdrawals blockedWithdrawalHeap

		// resultChans is a queue on which the result of deposit or withdrawals
		// are sent once the account's balance has been persisted to disk.
		resultChans []chan error

		// pendingRisk keeps track of how much of the account balance is
		// unsaved. We keep track of this on a per account basis because the
		// background thread persisting the account needs to know how much to
		// deduct from the overall pending risk. If the persist thread has to
		// wait, consecutive withdrawals can update the account balance in the
		// meantime, this builds up the pendingRisk. When the account is saved,
		// pending risk is lowered.
		pendingRisk types.Currency

		// lastTxnTime is the timestamp of the last transaction that occured
		// involving the ephemeral account. A transaction can be either a
		// deposit or withdrawal from the ephemeral account. We keep track of
		// this timestamp to allow pruning ephemeral accounts that have been
		// inactive for too long. The host can configure this expiry using the
		// ephemeralaccountexpiry setting.
		lastTxnTime int64
	}

	// accountIndex is a bitfield to keep track of account indexes. It will
	// assign a free index when a new account needs to be opened. It will
	// recycle the indexes of accounts that have expired.
	accountIndex []uint64

	// blockedWithdrawal represents a withdrawal call that is pending to be
	// executed but is stalled because either maxRisk is reached or the
	// account's balance is insufficient.
	blockedWithdrawal struct {
		withdrawal *withdrawalMessage
		result     chan error
		priority   int64
	}

	// blockedDeposit represents a deposit call that is pending to be
	// executed but is stalled because maxRisk is reached.
	blockedDeposit struct {
		id     string
		amount types.Currency
		result chan error
	}

	// blockedWithdrawalHeap is a heap of blocked withdrawal calls; the heap is
	// sorted based on the priority field.
	blockedWithdrawalHeap []*blockedWithdrawal

	// fingerprintMap keeps track of all the fingerprints and serves as a lookup
	// table. To make sure these fingerprints are not kept in memory forever,
	// the account manager will remove them when the current block height
	// exceeds their expiry. It does this by keeping the fingerprints in two
	// separate maps. When the block height reaches a certain threshold, which
	// is calculated on the fly using the current block height and the bucket
	// block range, the account manager will remove all fingerprints in the
	// current map and replace them with the fingerprints from the next map.
	fingerprintMap struct {
		bucketBlockRange int
		current          map[crypto.Hash]struct{}
		next             map[crypto.Hash]struct{}
	}

	// accountPersistInfo is a helper struct that contains all necessary
	// variables needed by threadedSaveAccount to successfully persist an
	// account, process blocked calls and update risk
	accountPersistInfo struct {
		index   uint32
		data    *accountData
		risk    types.Currency
		waiting int
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
		blockedDeposits:    make([]*blockedDeposit, 0),
		blockedWithdrawals: make([]*blockedWithdrawal, 0),
		index:              make([]uint64, 0),
		h:                  h,

		// withdrawals are inactive until the host is synced, consensus updates
		// will activate withdrawals when the host is fully synced
		withdrawalsInactive: true,
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
	// Gather information outside of the lock
	cbh := am.h.BlockHeight()
	his := am.h.InternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	maxBalance := his.MaxEphemeralAccountBalance

	// Setup the result channel - deposit are always blocking, meaning they will
	// wait to return the result until the account has successfully been saved
	// to disk.
	resultChan := make(chan error)
	defer func() {
		if err == nil {
			err = am.waitForDepositResult(resultChan)
		}
	}()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Open the account and verify if the deposit does not exceed the maximum
	// balance. If the account does not exist yet, it will be created.
	acc := am.openAccount(id)
	if acc.depositExceedsMaxBalance(amount, maxBalance) {
		err = ErrBalanceMaxExceeded
		return
	}

	// Block deposit if maxRisk is exceeded. A blocked deposit does not add to
	// the current risk as it has not been credited yet and is thus not
	// spendable.
	if am.currentRisk.Cmp(maxRisk) > 0 {
		bd := &blockedDeposit{id: id, amount: amount, result: resultChan}
		am.blockedDeposits = append(am.blockedDeposits, bd)
		return
	}
	am.currentRisk = am.currentRisk.Add(amount)

	// Handle risk introduced by deposit. Note that the host is at risk for
	// twice the deposit amount. When a deposit is done, the money is credited
	// to the account immediately, potentially before the file contract and the
	// account are persisted to disk. Because the money is spendable, the host
	// is on the hook for the amount twice. The risk is lowered again when, the
	// file contract is saved to disk (after which callCommitDeposit is called),
	// and when the account is saved to disk.
	addedRisk := acc.deposit(amount, cbh)
	acc.pendingRisk = acc.pendingRisk.Add(addedRisk)
	am.currentRisk = am.currentRisk.Add(addedRisk)

	am.schedulePersist(acc, resultChan)
	return
}

// callCommitDeposit is called after the file contracts is fsynced to disk. When
// this is called we can effectively lower the current risk risk since the file
// contract is safely stored on disk now, and we are not at risk of losing the
// funds should the host experience an unexpected shutdown.
func (am *accountManager) callCommitDeposit(amount types.Currency) {
	cbh := am.h.BlockHeight()

	am.mu.Lock()
	defer am.mu.Unlock()

	am.currentRisk = am.currentRisk.Sub(amount)

	// Risk is lowered - see if we can unblock deposits and/or withdrawals. We
	// unblock in this particular order to ensure deposits are unblocked first.
	am.unblockDeposits(&amount, cbh)
	am.unblockWithdrawals(&amount, cbh)
}

// callWithdraw will process the given withdrawal message. This call will block
// if either the account balance is insufficient, or if maxrisk is reached. The
// caller can specify a priority. This priority defines the order in which the
// withdrawals get processed in the event they are blocked due to insufficient
// funds.
func (am *accountManager) callWithdraw(msg *withdrawalMessage, sig crypto.Signature, priority int64) (err error) {
	// Validate the message's expiry and signature first
	cbh := am.h.BlockHeight()
	fingerprint := crypto.HashAll(*msg)
	if err := msg.validate(cbh, fingerprint, sig); err != nil {
		return err
	}

	// Gather information outside of the lock
	his := am.h.InternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	amount, id, expiry := msg.amount, msg.account, msg.expiry

	// Setup a result channel in the case we are blocking and have to wait for
	// the result. We only wait if the withdrawal has not been executed, we do
	// not wait for the account to be persisted to disk.
	var awaitResult bool
	resultChan := make(chan error)
	defer func() {
		if awaitResult && err == nil {
			err = am.waitForWithdrawalResult(resultChan)
		}
	}()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Check if withdrawals are inactive. This will be the case when the host is
	// not synced yet. Until that is not the case, we do not allow trading.
	if am.withdrawalsInactive {
		err = ErrWithdrawalsInactive
		return
	}

	// Save the fingerprint in memory and call persist asynchronously. If the
	// fingerprint is known we return an error.
	if err := am.fingerprints.add(fingerprint, expiry, cbh); err != nil {
		return errors.Extend(err, ErrWithdrawalSpent)
	}
	if err := am.h.tg.Add(); err != nil {
		return errors.Extend(err, ErrWithdrawalCancelled)
	}
	go func() {
		defer am.h.tg.Done()
		am.threadedSaveFingerprint(fingerprint, expiry, cbh)
	}()

	// Open the account, create if it does not exist yet
	acc := am.openAccount(id)

	// If the account balance is insufficient, block the withdrawal.
	if acc.withdrawalExceedsBalance(amount) {
		acc.blockedWithdrawals.Push(blockedWithdrawal{
			withdrawal: msg,
			priority:   priority,
			result:     resultChan,
		})
		awaitResult = true
		return
	}

	// Block this withdrawal if maxRisk is exceeded
	if am.currentRisk.Cmp(maxRisk) > 0 || len(am.blockedWithdrawals) > 0 {
		if am.h.dependencies.Disrupt("errMaxRiskReached") {
			return errMaxRiskReached // only for testing purposes
		}
		bw := &blockedWithdrawal{
			withdrawal: msg,
			priority:   priority,
			result:     resultChan,
		}
		am.blockedWithdrawals = append(am.blockedWithdrawals, bw)
		awaitResult = true
		return
	}

	// Handle risk introduced by deposit
	addedRisk := acc.withdraw(amount)
	acc.pendingRisk = acc.pendingRisk.Add(addedRisk)
	am.currentRisk = am.currentRisk.Add(addedRisk)

	am.schedulePersist(acc, resultChan)
	return
}

// callConsensusChanged is called by the host whenever it processed a change to
// the consensus. We use it to remove fingerprints which have been expired.
func (am *accountManager) callConsensusChanged(cc modules.ConsensusChange) {
	var rotate bool
	defer func() {
		if rotate {
			err := am.staticAccountsPersister.callRotateFingerprintBuckets()
			if err != nil {
				am.h.log.Critical("Could not rotate fingerprints on disk", err)
			}
		}
	}()

	cbh := am.h.BlockHeight()

	am.mu.Lock()
	defer am.mu.Unlock()

	if cc.Synced {
		am.withdrawalsInactive = false

		// If the current block height is equal to the minimum block height of
		// the current bucket range, we want to rotate the buckets.
		if min, _ := currentBucketRange(cbh); min == cbh {
			am.fingerprints.rotate()
			rotate = true
		}
		return
	}
	am.withdrawalsInactive = true
}

// schedulePersist will append the resultChan to the persist queue and call
// threadedSaveAccount for the given id. This way persists are happening in a
// FIFO fashion, ensuring an account is never overwritten by older account data.
func (am *accountManager) schedulePersist(acc *account, resultChan chan error) {
	acc.resultChans = append(acc.resultChans, resultChan)
	if len(acc.resultChans) == 1 {
		if err := am.h.tg.Add(); err != nil {
			return
		}
		go func() {
			defer am.h.tg.Done()
			waiting := am.threadedSaveAccount(acc.id)
			for waiting > 0 {
				waiting = am.threadedSaveAccount(acc.id)
			}
		}()
	}
}

// managedAccountPersistInfo is a helper method that will collect all of the
// necessary data to perform the persist.
func (am *accountManager) managedAccountPersistInfo(id string) *accountPersistInfo {
	am.mu.Lock()
	defer am.mu.Unlock()

	acc, exists := am.accounts[id]
	if !exists {
		return nil
	}

	return &accountPersistInfo{
		index:   acc.index,
		data:    acc.accountData(),
		risk:    acc.pendingRisk,
		waiting: len(acc.resultChans),
	}
}

// threadedSaveAccount will save the account with given id. There is only ever
// one background thread per account running. This is ensured by the account's
// pendingRisk, only if that increases from 0 -> something, a goroutine is
// scheduled to save the account.
//
// Note that the caller adds this thread to the threadgroup. If the add is done
// inside the goroutine, the host risks losing money even on graceful shutdowns.
func (am *accountManager) threadedSaveAccount(id string) (waiting int) {
	// Gather all information required to persist and process it afterwars
	accInfo := am.managedAccountPersistInfo(id)
	if accInfo == nil {
		return
	}

	// Call save account (disrupt, if triggered, will introduce a sleep here,
	// simulating a slow persist which allows maxRisk to be reached)
	_ = am.h.dependencies.Disrupt("errMaxRiskReached")
	persister := am.staticAccountsPersister
	err := persister.callSaveAccount(accInfo.data, accInfo.index)
	cbh := am.h.BlockHeight()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Take care of the pending risk in the account. We lower the risk by the
	// amount of risk that was captured in account info. This is necessary
	// seeing the pendingRisk can have been increased in the mean time, and that
	// risk has not yet been persisted to disk.
	acc, exists := am.accounts[id]
	if exists {
		acc.pendingRisk = acc.pendingRisk.Sub(accInfo.risk)
		// Only lower the current risk if the account still exists. If it
		// expired in the mean time the current risk will already have been
		// lowered.
		am.currentRisk = am.currentRisk.Sub(accInfo.risk)
	}

	// Send the result to all resultChans that where waiting the moment we
	// calculated the account data. If there are remaining resultChans after
	// this operation, we signal this to the caller through the waiting return
	// value. If there are resultChans still waiting, threadedSaveAccount will
	// be called again.
	acc.sendResult(err, accInfo.waiting)
	waiting = len(acc.resultChans)

	// Risk is lowered - see if we can unblock deposits and/or withdrawals. We
	// unblock in this particular order to ensure deposits are unblocked first.
	am.unblockDeposits(&accInfo.risk, cbh)
	am.unblockWithdrawals(&accInfo.risk, cbh)
	return
}

// threadedSaveFingerprint will persist the fingerprint data.
//
// Note that the caller adds this thread to the threadgroup. If the add is done
// inside the goroutine, we risk losing a fingerprint if the host shuts down.
func (am *accountManager) threadedSaveFingerprint(fp crypto.Hash, expiry, cbh types.BlockHeight) {
	if err := am.staticAccountsPersister.callSaveFingerprint(fp, expiry, cbh); err != nil {
		am.h.log.Critical("Could not save fingerprint", err)
	}
}

// unblockDeposits will unblock pending deposits until the unblockAllowance runs
// out. The unblockAllowance is the amount of risk that got freed up by a
// persist or a commit (FC fsync).
func (am *accountManager) unblockDeposits(unblockAllowance *types.Currency, cbh types.BlockHeight) {
	var numUnblocked int
	for i, bd := range am.blockedDeposits {
		amount, id := bd.amount, bd.id
		acc, exists := am.accounts[id]
		if !exists {
			// The resultchan will have been closed by the code that expired the
			// account.
			continue
		}

		if unblockAllowance.Cmp(amount) < 0 {
			numUnblocked = i
			break
		}

		// Handle risk introduced by deposit
		addedRisk := acc.deposit(bd.amount, cbh)
		acc.pendingRisk = acc.pendingRisk.Add(addedRisk)
		am.currentRisk = am.currentRisk.Add(addedRisk)

		am.schedulePersist(acc, bd.result)

		(*unblockAllowance) = (*unblockAllowance).Sub(amount)
	}
	am.blockedDeposits = am.blockedDeposits[numUnblocked:]
}

// unblockWithdrawals will unblock pending withdrawals until the
// unblockAllowance runs out. The unblockAllowance is the amount of risk that
// got freed up by a persist or a commit (FC fsync).
func (am *accountManager) unblockWithdrawals(unblockAllowance *types.Currency, cbh types.BlockHeight) {
	var numUnblocked int
	for i, bw := range am.blockedWithdrawals {
		amount, id := bw.withdrawal.amount, bw.withdrawal.account
		acc, exists := am.accounts[id]
		if !exists {
			// The resultchan will have been closed by the code that expired the
			// account.
			continue
		}

		// Validate the expiry - this is necessary seeing as the blockheight can
		// have been changed since the withdrawal was blocked, potentially
		// pushing it over its expiry.
		if err := bw.withdrawal.validateExpiry(cbh); err != nil {
			bw.result <- err
			continue
		}

		// Sanity check
		if acc.balance.Cmp(amount) < 0 {
			build.Critical("blocked withdrawal has insufficient balance to process, due to the order of execution in the callWithdrawal, this should never happen")
		}

		if unblockAllowance.Cmp(amount) < 0 {
			numUnblocked = i
			break
		}

		// Handle risk introduced by deposit
		addedRisk := acc.withdraw(amount)
		acc.pendingRisk = acc.pendingRisk.Add(addedRisk)
		am.currentRisk = am.currentRisk.Add(addedRisk)

		am.schedulePersist(acc, bw.result)

		(*unblockAllowance) = (*unblockAllowance).Sub(amount)
	}
	am.blockedWithdrawals = am.blockedWithdrawals[numUnblocked:]
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// for too long. It does this by comparing the account's lastTxnTime to the
// current time. If it exceeds the EphemeralAccountExpiry, the account is
// considered expired.
//
// Note: threadgroup counter must be inside for loop. If not, calling 'Flush'
// on the threadgroup would deadlock.
func (am *accountManager) threadedPruneExpiredAccounts() {
	// Disrupt can trigger forceful account expiry for testing purposes
	forceExpire := am.h.dependencies.Disrupt("expireEphemeralAccounts")

	for {
		his := am.h.InternalSettings()
		accountExpiryTimeout := int64(his.EphemeralAccountExpiry)

		func() {
			// A timeout of zero means the host never wants to expire accounts.
			if accountExpiryTimeout == 0 {
				return
			}

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

			// Once deleted from disk, recycle the indexes by releasing them.
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

// waitForWithdrawalResult will block until it receives a message on the given
// result channel, or until it either times out or receives a stop signal.
func (am *accountManager) waitForWithdrawalResult(resultChan chan error) error {
	select {
	case err := <-resultChan:
		return errors.AddContext(err, "blocked withdrawal failed")
	case <-time.After(blockedWithdrawalTimeout):
		return ErrBalanceInsufficient
	case <-am.h.tg.StopChan():
		return ErrWithdrawalCancelled
	}
}

// waitForDepositResult will block until it receives a message on the given
// result channel, or until it receives a stop signal.
func (am *accountManager) waitForDepositResult(resultChan chan error) error {
	select {
	case err := <-resultChan:
		return errors.AddContext(err, "deposit failed")
	case <-am.h.tg.StopChan():
		return ErrDepositCancelled
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
			// Signal all waiting result chans this account has expired
			for _, c := range acc.resultChans {
				c <- ErrAccountExpired
			}
			am.currentRisk = am.currentRisk.Sub(acc.pendingRisk)
			delete(am.accounts, id)
			deleted = append(deleted, acc.index)
		}
	}
	return deleted
}

// openAccount will return an account object. If the account does not exist it
// will be created.
func (am *accountManager) openAccount(id string) *account {
	acc, exists := am.accounts[id]
	if !exists {
		acc = &account{
			id:                 id,
			index:              am.index.assignFreeIndex(),
			blockedWithdrawals: make(blockedWithdrawalHeap, 0),
			resultChans:        make([]chan error, 0),
		}
		am.accounts[id] = acc
	}
	return acc
}

// assignFreeIndex will return the next available account index
func (ai *accountIndex) assignFreeIndex() uint32 {
	i, pos := 0, -1

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
// accounts. Upon account expiry, its index will be freed up by unsetting the
// corresponding bit. When a new account is opened, it will grab the first
// available index, effectively recycling the expired account indexes.
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

// add will add the given fingerprint to the fingerprintMap. If the map already
// contains the fingerprint it will return an error.
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

// withdrawalExceedsBalance returns true if withdrawal is larger than the
// account balance.
func (a *account) withdrawalExceedsBalance(withdrawal types.Currency) bool {
	return a.balance.Cmp(withdrawal) < 0
}

// sendResult will send the given result to the result channels that are waiting
func (a *account) sendResult(result error, waiting int) {
	for i := 0; i < waiting; i++ {
		resultChan := a.resultChans[i]
		select {
		case resultChan <- errors.AddContext(result, ErrAccountPersist.Error()):
			close(resultChan)
		default:
			close(resultChan)
		}
	}
	a.resultChans = a.resultChans[waiting:]
}

// deposit will add the given amount to the balance, properly adjust the risk
// and unblock any pending withdrawals. It returns the balance delta.
func (a *account) deposit(amount types.Currency, cbh types.BlockHeight) types.Currency {
	before := a.balance

	// Update the account details
	a.balance = a.balance.Add(amount)
	a.lastTxnTime = time.Now().Unix()

	// Unblock withdrawals that were waiting for more funds
	a.unblockWithdrawals(cbh)

	// Return balance delta
	var delta types.Currency
	if a.balance.Cmp(before) > 0 {
		delta = a.balance.Sub(before)
	} else {
		delta = before.Sub(a.balance)
	}
	return delta
}

// withdraw will subtract the given amount from the balance and properly adjust
// the risk. It returns the balance delta.
func (a *account) withdraw(amount types.Currency) types.Currency {
	before := a.balance

	// Update the account details
	a.balance = a.balance.Sub(amount)
	a.lastTxnTime = time.Now().Unix()

	// Return balance delta
	delta := before.Sub(a.balance)
	return delta
}

// unblockWithdrawals goes through all blocked withdrawals and unblocks the ones
// for which the account has sufficient balance. This function alters the
// account balance to reflect all withdrawals that have been unblocked.
func (a *account) unblockWithdrawals(currentBlockHeight types.BlockHeight) {
	for a.blockedWithdrawals.Len() > 0 {
		bw := a.blockedWithdrawals.Pop().(*blockedWithdrawal)
		if err := bw.withdrawal.validateExpiry(currentBlockHeight); err != nil {
			select {
			case bw.result <- err:
				close(bw.result)
			default:
				close(bw.result)
			}

			continue
		}

		// requeue if balance is insufficient
		if bw.withdrawal.amount.Cmp(a.balance) > 0 {
			a.blockedWithdrawals.Push(*bw)
			break
		}

		a.withdraw(bw.withdrawal.amount)

		close(bw.result)
	}
}
