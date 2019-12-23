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
	ErrWithdrawalsInactive = errors.New("ephemeral account withdrawals are inactive because the host is not synced")

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
		Standard: 24 * time.Hour,
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

		// The accountBifield keeps track of all account indexes using a
		// bitfield. The account index decides the location at which the account
		// is stored in the accounts file on disk.
		accountBifield accountBifield

		// To increase performance, a withdrawal will not await the persist of
		// the account. This allows users to transact with the host with
		// significantly less latency. This also means that the money that has
		// already been withdrawn is at risk to the host. An unclean shutdown
		// before the account manager was able to persist the account balance to
		// disk would allow the user to withdraw that money twice. To limit this
		// risk to the host, he can set a maxephemeralaccountrisk, when that
		// amount is reached all consecutive withdrawals and deposits block
		// until the account manager has successfully persisted all updates to
		// disk and risk is lowered.
		currentRisk types.Currency

		// When maxRisk is reached, all withdrawals are appended to a queue,
		// they will get processed in a FIFO fashion when risk is lowered.
		blockedWithdrawals []*blockedWithdrawal

		// When maxRisk is reached, all deposits are appended to a queue,
		// they will get processed in a FIFO fashion when risk is lowered.
		blockedDeposits []*blockedDeposit

		// withdrawalsInactive indicates whether the account manager allows
		// withdrawals or not. This will be true as long as the host is not
		// fully synced.
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

	// accountBitfield is a bitfield to keep track of account indexes. When an
	// account is opened, it is assigned a free index. When an account expires
	// due to inactivity, its index gets recycled.
	accountBifield []uint64

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
		accountBifield:     make(accountBifield, 0),
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
	am.accountBifield.buildIndex(am.accounts)

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

// callDeposit will deposit the amount into the account with given id. This will
// increase the host's current risk by the deposit amount. This is because until
// the file contract has been fsynced, the host is at risk for losing money. The
// caller has to pass in a syncChan that should be closed when the file contract
// is fsynced. When that happens, the current risk is lowered.
//
// The deposit is subject to mainting ACID properties between the file contract
// and the ephemeral account. In order to document the model, the following is a
// brief description of why it has to be ACID and an overview of the various
// failure modes.
//
// Deposit will get called by the RPC. The RPC will update the account balance
// by depositing the amount of money that moved hands in the file contract. Both
// the ephemeral account (EA) and the file contract (FC) need to be fsynced to
// disk. In order to make the money immediately available, the RPC will perform
// the deposit before initiating the FC sync. This puts the host at risk, and
// thus a deposit will increase the current risk. When the account manager
// returns and indicates the EA was synced, the RPC will sync the FC. When this
// fsync is done it will close the syncChan. This syncChan was passed during the
// deposit, and will lower risk when it is closed.
//
// Failure Modes:
//
// 1. Failure before RPC calls deposit: EA not updated, FC not updated, OK
//
// 2. Failure after RPC calls deposit, but before EA is updated: EA not updated,
// FC not updated, OK
//
// 3. Failure after RPC calls deposit, after EA is updated, but before AM
// returns to the RPC: EA is updated, FC is not, the host is at risk for the
// full deposit amount.
//
// 4. Failure after RPC calls deposit, after EA is updated, after AM returns,
// before FC sync: EA is updated, FC is not, this reduces to failure mode 3
//
// 5. Failure after RPC calls deposit, after EA is updated, after AM returns,
// after FC sync: EA is updated, FC is updated, there is no risk to the host at
// this point
func (am *accountManager) callDeposit(id string, amount types.Currency, syncChan chan error) error {
	// Gather some variables.
	cbh := am.h.BlockHeight()
	his := am.h.InternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	maxBalance := his.MaxEphemeralAccountBalance

	// Setup the result channel, once the account manager has persisted the
	// account to disk it will send the result over this channel.
	resultChan := make(chan error)

	// Perform the actual deposit.
	err := am.managedPerformDeposit(id, amount, maxRisk, maxBalance, cbh, syncChan, resultChan)
	if err != nil {
		return errors.AddContext(err, "Deposit failed")
	}

	// Wait for the deposit result.
	err = am.waitForDepositResult(resultChan)
	if err != nil {
		return err
	}

	// If the deposit was successful, we want to increase the current risk. This
	// risk gets lowered as soon as the file contract is successfully fsynced.
	// The RPC will signal this through the syncChan.
	am.managedUpdateRiskAfterDeposit(amount, syncChan)
	return nil
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

	// Setup the result channel, once the account manager has performed the
	// withdrawal it will send the result over this channel. Note we do not
	// await the fsync of the account in case of withdrawals.
	resultChan := make(chan error)

	// Perform the withdraw.
	withdrawDone, err := am.managedPerformWithdraw(msg, fingerprint, priority, maxRisk, cbh, resultChan)
	if err != nil {
		return errors.AddContext(err, "Withdraw failed")
	}

	if !withdrawDone {
		return am.waitForWithdrawalResult(resultChan)
	}
	return nil
}

// callConsensusChanged is called by the host whenever it processed a change to
// the consensus. We use it to remove fingerprints which have been expired.
func (am *accountManager) callConsensusChanged(cc modules.ConsensusChange) {
	cbh := am.h.BlockHeight()

	// If the host is not synced, withdrawals are disabled. In this case we also
	// do not want to rotate the fingerprints.
	am.mu.Lock()
	if !cc.Synced {
		am.withdrawalsInactive = true
		am.mu.Unlock()
		return
	}
	am.withdrawalsInactive = false

	// Only if the current block height is equal to the minimum block height of
	// the current bucket range, we want to rotate the buckets.
	min, _ := currentBucketRange(cbh)
	if min != cbh {
		am.mu.Unlock()
		return
	}

	am.fingerprints.rotate()
	am.mu.Unlock()

	err := am.staticAccountsPersister.callRotateFingerprintBuckets()
	if err != nil {
		am.h.log.Critical("Could not rotate fingerprints on disk", err)
	}
}

// managedPerformDeposit will deposit the given amount into the account and
// communicate the result over the given channel.
func (am *accountManager) managedPerformDeposit(id string, amount, maxRisk, maxBalance types.Currency, blockHeight types.BlockHeight, syncedChan, resultChan chan error) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Open the account, if the account does not exist yet, it will be created.
	acc := am.openAccount(id)

	// Verify if the deposit does not exceed the maximum
	if acc.depositExceedsMaxBalance(amount, maxBalance) {
		return ErrBalanceMaxExceeded
	}

	// If current risk exceeds the max risk, add the deposit to the
	// blockedDeposits queue. These deposits will get dequeued by processes that
	// lower the current risk, such as FC fsyncs or account persists.
	if am.currentRisk.Cmp(maxRisk) > 0 {
		am.blockedDeposits = append(am.blockedDeposits, &blockedDeposit{
			id:     id,
			amount: amount,
			result: resultChan,
		})
		return nil
	}

	// Update the account details
	acc.balance = acc.balance.Add(amount)
	acc.lastTxnTime = time.Now().Unix()

	// Unblock withdrawals that were waiting for more funds.
	acc.unblockWithdrawals(blockHeight)

	// Persist the account
	am.schedulePersist(acc, resultChan)
	return nil
}

// managedUpdateRiskAfterDeposit will update the current risk after a deposit
// has been performed. The deposit amount is added to the host's current risk.
// The host is at risk for this amount as long as the file contract has not been
// fsynced. When the RPC is done with the FC fsync it will close the doneChan so
// the account manager can lower the risk.
func (am *accountManager) managedUpdateRiskAfterDeposit(deposit types.Currency, syncChan chan error) {
	// The syncChan might already be closed, perform a quick check to verify
	// this. If it is the case we do not need to update risk at all. This saves
	// unnecessarily acquiring the lock.
	select {
	case <-syncChan:
		return
	default:
	}

	// Add the deposit to the outstanding risk
	am.mu.Lock()
	am.currentRisk = am.currentRisk.Add(deposit)
	am.mu.Unlock()

	go am.threadedUpdateRiskAfterSync(deposit, syncChan)
}

// managedPerformWithdraw will try and perform the given withdrawal. It will
// ensure the fingerprint is unique and block in case the withdraw exceeds the
// account balance, or we are at maxrisk. Important to note is that a withdrawal
// does not await the fsync of the account to disk.
func (am *accountManager) managedPerformWithdraw(msg *withdrawalMessage, fp crypto.Hash, priority int64, maxRisk types.Currency, blockHeight types.BlockHeight, resultChan chan error) (bool, error) {
	amount, id, expiry := msg.amount, msg.account, msg.expiry

	am.mu.Lock()
	defer am.mu.Unlock()

	// Check if withdrawals are inactive. This will be the case when the host is
	// not synced yet. Until that is not the case, we do not allow trading.
	if am.withdrawalsInactive {
		return false, ErrWithdrawalsInactive
	}

	// Save the fingerprint in memory and call persist asynchronously. If the
	// fingerprint is known we return an error.
	exists := am.fingerprints.has(fp)
	if exists {
		return false, ErrWithdrawalSpent
	}
	am.fingerprints.add(fp, expiry, blockHeight)
	if err := am.h.tg.Add(); err != nil {
		return false, ErrWithdrawalCancelled
	}
	func() {
		defer am.h.tg.Done()
		go am.threadedSaveFingerprint(fp, expiry, blockHeight)
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
		return false, nil
	}

	// Block this withdrawal if maxRisk is exceeded
	if am.currentRisk.Cmp(maxRisk) > 0 || len(am.blockedWithdrawals) > 0 {
		if am.h.dependencies.Disrupt("errMaxRiskReached") {
			return false, errMaxRiskReached // only for testing purposes
		}
		am.blockedWithdrawals = append(am.blockedWithdrawals, &blockedWithdrawal{
			withdrawal: msg,
			priority:   priority,
			result:     resultChan,
		})
		return false, nil
	}

	delta := acc.withdraw(amount)

	// Update the current risk and the account's pending risk. By allowing money
	// to be withdrawn from the account, without awaiting the persist, the host
	// is at risk at losing the balance delta. This is added to the risk now,
	// but will get subtracted again when the account was persisted.
	acc.pendingRisk = acc.pendingRisk.Add(delta)
	am.currentRisk = am.currentRisk.Add(delta)

	am.schedulePersist(acc, resultChan)
	return true, nil
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

// threadedUpdateRiskAfterSync will update the current risk after it has
// received a signal on the doneChan. This thread will stop after a timeout of
// 10 mins, or if it receives a stop signal. This doneChan is passed in by the
// RPC, it will close this channel when the file contract has been fsynced.
func (am *accountManager) threadedUpdateRiskAfterSync(deposit types.Currency, syncChan chan error) {
	if err := am.h.tg.Add(); err != nil {
		return
	}
	defer am.h.tg.Done()

	select {
	case <-syncChan:
		cbh := am.h.BlockHeight()
		am.mu.Lock()
		am.currentRisk = am.currentRisk.Sub(deposit)

		// Now that risk is lowered, we need to unblock deposit and withdrawals
		// seeing as they might be blocked due to current risk exceeding the
		// maximum. Unblock deposit and withdrawals in this particular order
		// until the deposit (read: allowance) runs out.
		deposit = am.unblockDeposits(deposit, cbh)
		am.unblockWithdrawals(deposit, cbh)
		am.mu.Unlock()
		return
	case <-am.h.tg.StopChan():
		return
	case <-time.After(10 * time.Minute):
		return
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
	// Gather all information required to persist and process it afterwards
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
	allowance := accInfo.risk
	allowance = am.unblockDeposits(allowance, cbh)
	am.unblockWithdrawals(allowance, cbh)
	return
}

// threadedSaveFingerprint will persist the fingerprint data.
//
// Note that the caller adds this thread to the threadgroup. If the add is done
// inside the goroutine, we risk losing a fingerprint if the host shuts down.
func (am *accountManager) threadedSaveFingerprint(fp crypto.Hash, expiry, cbh types.BlockHeight) {
	err := am.staticAccountsPersister.callSaveFingerprint(fp, expiry, cbh)
	if err != nil {
		am.h.log.Critical("Could not save fingerprint", err)
	}
}

// schedulePersist will append the resultChan to the persist queue and call
// threadedSaveAccount for the given id. This way persists are happening in a
// FIFO fashion, ensuring an account is never overwritten by older account data.
func (am *accountManager) schedulePersist(acc *account, resultChan chan error) {
	acc.resultChans = append(acc.resultChans, resultChan)
	if len(acc.resultChans) > 1 {
		return
	}

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

// unblockDeposits will unblock pending deposits until the allowance runs out.
// The allowance is the amount of risk that got freed up by a persist or a
// commit (FC fsync).
func (am *accountManager) unblockDeposits(allowance types.Currency, cbh types.BlockHeight) (remaining types.Currency) {
	var numUnblocked int
	for i, bd := range am.blockedDeposits {
		amount, id := bd.amount, bd.id
		acc, exists := am.accounts[id]
		if !exists {
			// Account has expired
			continue
		}

		if allowance.Cmp(amount) < 0 {
			numUnblocked = i
			break
		}

		// Update the account details
		acc.balance = acc.balance.Add(amount)
		acc.lastTxnTime = time.Now().Unix()

		// Unblock withdrawals that were waiting for more funds
		acc.unblockWithdrawals(cbh)

		// Persist the account
		am.schedulePersist(acc, bd.result)
		allowance = allowance.Sub(amount)
	}
	am.blockedDeposits = am.blockedDeposits[numUnblocked:]
	remaining = allowance
	return
}

// unblockWithdrawals will unblock pending withdrawals until the allowance runs
// out. The allowance is the amount of risk that got freed up by a persist or a
// commit (FC fsync).
func (am *accountManager) unblockWithdrawals(allowance types.Currency, cbh types.BlockHeight) (remaining types.Currency) {
	var numUnblocked int
	for i, bw := range am.blockedWithdrawals {
		amount, id := bw.withdrawal.amount, bw.withdrawal.account
		acc, exists := am.accounts[id]
		if !exists {
			// Account has expired
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

		if allowance.Cmp(amount) < 0 {
			numUnblocked = i
			break
		}

		// Update the current risk and the account's pending risk introduced by
		// this withdrawal. The host is at risk for this amount because the
		// account has not yet been persisted to disk.
		delta := acc.withdraw(amount)
		acc.pendingRisk = acc.pendingRisk.Add(delta)
		am.currentRisk = am.currentRisk.Add(delta)

		am.schedulePersist(acc, bw.result)

		allowance = allowance.Sub(amount)
	}
	am.blockedWithdrawals = am.blockedWithdrawals[numUnblocked:]
	remaining = allowance
	return
}

// threadedPruneExpiredAccounts will expire accounts which have been inactive
// for too long. It does this by comparing the account's lastTxnTime to the
// current time. If it exceeds the EphemeralAccountExpiry, the account is
// considered expired.
//
// Note: threadgroup counter must be inside for loop. If not, calling 'Flush'
// on the threadgroup would deadlock.
func (am *accountManager) threadedPruneExpiredAccounts() {
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
			expired := am.managedExpireAccounts(accountExpiryTimeout)
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
				am.accountBifield.releaseIndex(index)
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
// given threshold.
func (am *accountManager) managedExpireAccounts(threshold int64) []uint32 {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Disrupt can trigger forceful account expiry for testing purposes
	force := am.h.dependencies.Disrupt("expireEphemeralAccounts")

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
	if exists {
		return acc
	}
	acc = &account{
		id:                 id,
		index:              am.accountBifield.assignFreeIndex(),
		blockedWithdrawals: make(blockedWithdrawalHeap, 0),
		resultChans:        make([]chan error, 0),
	}
	am.accounts[id] = acc
	return acc
}

// assignFreeIndex will return the next available account index
func (ab *accountBifield) assignFreeIndex() uint32 {
	i, off := 0, -1

	// Go through all bitmaps in random order to find a free index
	full := ^uint64(0)
	for i = range *ab {
		if (*ab)[i] != full {
			off = bits.TrailingZeros(uint(^(*ab)[i]))
			break
		}
	}

	// Add a new bitfield if all bitfields are full, otherwise flip the bit
	if off == -1 {
		off = 0
		*ab = append(*ab, 1<<uint(off))
		i = len(*ab) - 1
	} else {
		(*ab)[i] |= (1 << uint(off))
	}

	// Calculate the index by multiplying the bitfield index by 64 (seeing as
	// the bitfields are of type uint64) and adding the position
	return uint32((i * 64) + off)
}

// releaseIndex will unset the bit corresponding to given index
func (ab *accountBifield) releaseIndex(index uint32) {
	i := index / 64
	pos := index % 64
	var mask uint64 = ^(1 << pos)
	(*ab)[i] &= mask
}

// buildAccountIndex will initialize bitfields representing all ephemeral
// accounts. Upon account expiry, its index will be freed up by unsetting the
// corresponding bit. When a new account is opened, it will grab the first
// available index, effectively recycling the expired account indexes.
func (ab *accountBifield) buildIndex(accounts map[string]*account) {
	var maxIndex uint32
	for _, acc := range accounts {
		if acc.index > maxIndex {
			maxIndex = acc.index
		}
	}

	// Add empty bitfields to accomodate all account indexes
	n := int(math.Floor(float64(maxIndex)/64)) + 1
	*ab = make([]uint64, n)

	// Range over the accounts and flip the bit corresponding to their index
	for _, acc := range accounts {
		i := acc.index / 64
		pos := acc.index % 64
		(*ab)[i] = (*ab)[i] << uint(pos)
	}
}

// add will add the given fingerprint to the fingerprintMap. If the map already
// contains the fingerprint we will return false to signal it has not been
// added.
func (fm *fingerprintMap) add(fp crypto.Hash, expiry, currentBlockHeight types.BlockHeight) {
	_, max := currentBucketRange(currentBlockHeight)
	if expiry < max {
		fm.current[fp] = struct{}{}
		return
	}
	fm.next[fp] = struct{}{}
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
