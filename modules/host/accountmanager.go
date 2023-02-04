package host

import (
	"context"
	"math"
	"math/bits"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	// ErrAccountPersist occurs when an ephemeral account could not be persisted
	// to disk.
	ErrAccountPersist = errors.New("ephemeral account could not be persisted to disk")

	// ErrAccountExpired occurs when a blocked action can not complete because
	// the account has expired in the meantime.
	ErrAccountExpired = errors.New("ephemeral account expired")

	// ErrBalanceInsufficient occurs when a withdrawal could not be successfully
	// completed because the account balance was insufficient.
	ErrBalanceInsufficient = errors.New("ephemeral account balance was insufficient")

	// ErrBalanceMaxExceeded occurs when a deposit would push the account's
	// balance over the maximum allowed ephemeral account balance.
	ErrBalanceMaxExceeded = errors.New("ephemeral account maximum balance exceeded")

	// ErrDepositCancelled occurs when the host was willingly or unwillingly
	// stopped in the midst of a deposit process.
	ErrDepositCancelled = errors.New("ephemeral account deposit cancelled due to a shutdown")

	// ErrWithdrawalCancelled occurs when the host was willingly or unwillingly
	// stopped in the midst of a withdrawal process.
	ErrWithdrawalCancelled = errors.New("ephemeral account withdrawal cancelled due to a shutdown")

	// ErrWithdrawalSpent occurs when a withdrawal is requested using a
	// withdrawal message that has been spent already.
	ErrWithdrawalSpent = errors.New("withdrawal message was already spent")

	// ErrZeroAccountID occurs when an account is opened with the ZeroAccountID.
	ErrZeroAccountID = errors.New("can't open an account with an empty account id")

	// When the errMaxRiskReached dependency is specified this error is returned
	// on withdraw or deposit when max risk is reached. It enables easy
	// verification of when max risk is reached in tests. Used only in tests.
	errMaxRiskReached = errors.New("errMaxRiskReached")

	// pruneExpiredAccountsFrequency is the frequency at which the account
	// manager checks if it can expire accounts which have been inactive for too
	// long.
	pruneExpiredAccountsFrequency = build.Select(build.Var{
		Standard: 24 * time.Hour,
		Testnet:  24 * time.Hour,
		Dev:      15 * time.Minute,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// blockedWithdrawalTimeout is the amount of time after which a blocked
	// withdrawal times out.
	// NOTE: The standard case is set to 3 minutes since streams established
	// by renters are commonly timing out after 5 minutes, thus hiding
	// insufficient balance errors from the renter.
	blockedWithdrawalTimeout = build.Select(build.Var{
		Standard: 3 * time.Minute,
		Testnet:  3 * time.Minute,
		Dev:      time.Minute,
		Testing:  5 * time.Second,
	}).(time.Duration)
)

// The AccountManager manages all of the ephemeral accounts on the host.
//
// Ephemeral accounts are a service offered by hosts that allow users to connect
// a balance to a pubkey. Users can deposit funds into an ephemeral account with
// a host and then later use the funds to transact with the host.
//
// The ephemeral account owner fully entrusts the money with the host, he has no
// recourse at all if the host decides to steal the funds. For this reason,
// users should only keep tiny balances in ephemeral accounts and users should
// refill the ephemeral accounts frequently, even on the order of multiple times
// per minute.
type (
	// The accountManager manages all deposits and withdrawals to and from an
	// ephemeral account. It uses an accounts persister to save the account data
	// to disk. It keeps track of the fingerprints, which are hashes of the
	// withdrawal messages, to ensure the same withdrawal can not be performed
	// twice. The account manager is hooked into consensus updates. This allows
	// pruning fingerprints of withdrawals that have expired, so memory does not
	// build up.
	accountManager struct {
		accounts                map[modules.AccountID]*account
		fingerprints            *fingerprintMap
		staticAccountsPersister *accountsPersister

		// The accountBitfield keeps track of all account indexes using a
		// bitfield. Every account has a unique index. The account's index
		// decides at what location in the accounts file the account's data gets
		// persisted.
		accountBitfield accountBitfield

		// To increase performance, deposits get credited before the file
		// contract fsynced, and withdrawals do not block until the ephemeral
		// account is safely persisted. This allows users to transact with the
		// host with significantly less latency. This also means that the host
		// is at risk to lose money until the file contract and latest account
		// balance are persisted to disk. An unclean shutdown or fsync failure
		// would allow the user to withdraw money twice, or withdraw money that
		// not properly 'moved hands' in the file contract. We keep track of
		// this outstanding risk in currentRisk. To limit this risk, the host
		// can configure a maxephemeralaccountrisk. When that amount is reached,
		// withdrawals and deposits block until the accounts get persisted and
		// filecontracts get fsynced which in turn lower the outstanding risk.
		currentRisk types.Currency

		// When maxRisk is reached, all withdrawals are appended to a queue,
		// they will get processed in a FIFO fashion when risk is lowered.
		blockedWithdrawals []*blockedWithdrawal

		// When maxRisk is reached, all deposits are appended to a queue,
		// they will get processed in a FIFO fashion when risk is lowered.
		blockedDeposits []*blockedDeposit

		// withdrawalsInactive indicates whether the account manager allows
		// withdrawals or not. Withdrawals are inactive as long as the host is
		// not fully synced, or when it goes out of sync.
		withdrawalsInactive bool

		mu sync.Mutex
		h  *Host
	}

	// account contains all data related to an ephemeral account
	account struct {
		index              uint32
		id                 modules.AccountID
		balance            types.Currency
		blockedWithdrawals blockedWithdrawalHeap

		// persistResults contains a list of channels for threads that are
		// waiting for results on the persist status of an action performed on
		// the account.
		persistResults []*persistResult

		// pendingRisk keeps track of the unsaved account balance delta. We keep
		// track of this on a per account basis because we have to know how much
		// to subtract from the overall pending risk once an account gets
		// persisted to disk. Every time a withdrawal is made from the account,
		// pending risk is increased, if the background thread saves the
		// account, the pending risk is reset and the overall risk gets lowered
		// by the delta that got persisted to disk.
		pendingRisk types.Currency

		// lastTxnTime is the timestamp of the last transaction that occurred
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
	accountBitfield []uint64

	// blockedWithdrawal represents a withdrawal call that is pending to be
	// executed but is stalled because either maxRisk is reached or the
	// account's balance is insufficient.
	blockedWithdrawal struct {
		withdrawal   *modules.WithdrawalMessage
		priority     int64
		commitResult chan error
	}

	// blockedDeposit represents a deposit call that is pending to be
	// executed but is stalled because maxRisk is reached.
	blockedDeposit struct {
		id            modules.AccountID
		amount        types.Currency
		persistResult *persistResult
		syncResult    chan struct{}
	}

	// blockedWithdrawalHeap is a heap of blocked withdrawal calls; the heap is
	// sorted based on the priority field.
	blockedWithdrawalHeap []*blockedWithdrawal

	// fingerprintMap keeps track of all the fingerprints and serves as a lookup
	// table. It keeps track of the fingerprints by using two separate buckets,
	// fingerprints are added based on their expiry. These buckets rotate when
	// the current block height reaches a certain threshold, this is done to
	// ensure fingerprints are not kept in memory forever.
	fingerprintMap struct {
		current map[crypto.Hash]struct{}
		next    map[crypto.Hash]struct{}
	}

	// persistResult contains a channel which gets closed when the error from
	// the result has been determined, along with the error from the result. The
	// error is not allowed to be accessed until the result chan is closed,
	// except by thread that is reporting the error.
	persistResult struct {
		errAvail  chan struct{}
		externErr error
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
		total = total.Add(bw.withdrawal.Amount)
	}
	return total
}

// newAccountManager returns a new account manager ready for use by the host
func (h *Host) newAccountManager() (_ *accountManager, err error) {
	am := &accountManager{
		accounts:           make(map[modules.AccountID]*account),
		fingerprints:       newFingerprintMap(),
		blockedDeposits:    make([]*blockedDeposit, 0),
		blockedWithdrawals: make([]*blockedWithdrawal, 0),
		accountBitfield:    make(accountBitfield, 0),
		h:                  h,

		// withdrawals are inactive until the host is synced, consensus updates
		// will activate withdrawals when the host is fully synced, or
		// deactivate when it goes out of sync
		withdrawalsInactive: true,
	}

	// Create the accounts persister
	am.staticAccountsPersister, err = h.newAccountsPersister(am)
	if err != nil {
		return nil, err
	}

	// Load the accounts data from disk
	var data *accountsPersisterData
	if data, err = am.staticAccountsPersister.callLoadData(); err != nil {
		return nil, err
	}
	am.accounts = data.accounts
	am.fingerprints.next = data.fingerprints

	// Build the account index
	am.accountBitfield.buildIndex(am.accounts)

	// Close any open file handles if we receive a stop signal
	am.h.tg.AfterStop(func() {
		am.staticAccountsPersister.callClose()
	})

	go am.threadedPruneExpiredAccounts()

	return am, nil
}

// newFingerprintMap will create a new fingerprint map
func newFingerprintMap() *fingerprintMap {
	return &fingerprintMap{
		current: make(map[crypto.Hash]struct{}),
		next:    make(map[crypto.Hash]struct{}),
	}
}

// callDeposit calls managedDeposit with refund set to 'false'.
func (am *accountManager) callDeposit(id modules.AccountID, amount types.Currency, syncChan chan struct{}) error {
	// disrupt if the 'lowerDeposit' dependency is set, this dependency will
	// alter the deposit amount without the renter being aware of it, used to
	// test the balance sync after unclean shutdown
	if am.h.dependencies.Disrupt("lowerDeposit") {
		amount = amount.Sub(types.SiacoinPrecision.Div64(10))
	}

	return am.managedDeposit(id, amount, false, syncChan)
}

// callRefund calls managedDeposit with refund set to 'true' and a closed
// syncChan.
func (am *accountManager) callRefund(id modules.AccountID, amount types.Currency) error {
	// Nothing to refund.
	if amount.IsZero() {
		return nil
	}
	syncChan := make(chan struct{})
	close(syncChan)
	return am.managedDeposit(id, amount, true, syncChan)
}

// managedDeposit will deposit the amount into the ephemeral account with given
// id. This will increase the host's current risk by the deposit amount. This is
// because until the file contract has been fsynced, the host is at risk to
// losing money. The caller passes in a channel that gets closed when the file
// contract is fsynced. When that happens, the current risk is lowered.
//
// calling managedDeposit with refund = true will ignore the max EA balance
// restriction.
//
// The deposit is subject to maintaining ACID properties between the file
// contract (FC) and the ephemeral account (EA). In order to document the model,
// the following is a brief description of why it has to be ACID and an overview
// of the various failure modes.
//
// Deposit is called by the RPCs. An RPC will deposit an amount of money equal
// to the amount that changed hands in the FC revision. In order to make that
// money immediately available, the deposit is done before the FC gets fsynced.
// This puts the host at risk. The host is at risk to losing money until both
// the ephemeral account and the file contract are safely on disk. A call to
// deposit will block until the EA is fsynced. When the deposit returns the RPC
// will go ahead and fsync the FC. After the FC is fsynced, the sync chan gets
// closed, upon which the account manager lowers the host's outstanding risk.
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
func (am *accountManager) managedDeposit(id modules.AccountID, amount types.Currency, refund bool, syncChan chan struct{}) error {
	// Gather some variables.
	bh := am.h.BlockHeight()
	his := am.h.managedInternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk
	maxBalance := his.MaxEphemeralAccountBalance

	// Initiate the deposit.
	pr := &persistResult{
		errAvail: make(chan struct{}),
	}
	am.mu.Lock()
	err := am.deposit(id, amount, maxRisk, maxBalance, bh, refund, pr, syncChan)
	am.mu.Unlock()
	if err != nil {
		return errors.AddContext(err, "Deposit failed")
	}

	// Wait for the deposit to be persisted.
	return errors.AddContext(am.staticWaitForDepositResult(pr), "Deposit failed")
}

// callWithdraw will process the given withdrawal message. This call will block
// if either the account balance is insufficient, or if maxrisk is reached. The
// caller can specify a priority. This priority defines the order in which the
// withdrawals get processed in the event they are blocked due to insufficient
// funds.
func (am *accountManager) callWithdraw(msg *modules.WithdrawalMessage, sig crypto.Signature, priority int64, bh types.BlockHeight) error {
	// Gather some variables
	his := am.h.managedInternalSettings()
	maxRisk := his.MaxEphemeralAccountRisk

	// Validate the message's expiry and signature first
	fingerprint := crypto.HashAll(*msg)
	if err := msg.Validate(bh, bh+bucketBlockRange, fingerprint, sig); err != nil {
		return err
	}

	// Setup the commit result channel, once the account manager has committed
	// the withdrawal, it will send the result over this channel. Note we only
	// block until the withdrawal gets committed and not persisted.
	commitResultChan := make(chan error, 1)

	// Initiate the withdraw process.
	err := am.managedWithdraw(msg, fingerprint, priority, maxRisk, bh, commitResultChan)
	if err != nil {
		return errors.AddContext(err, "Withdraw failed")
	}

	// Wait for the withdrawal to be committed.
	return errors.AddContext(am.staticWaitForWithdrawalResult(commitResultChan), "Withdraw failed")
}

// callConsensusChanged is called by the host whenever it processed a change to
// the consensus. We use it to remove fingerprints which have been expired.
func (am *accountManager) callConsensusChanged(cc modules.ConsensusChange, oldHeight types.BlockHeight) {
	am.mu.Lock()
	defer am.mu.Unlock()

	// If the host is not synced, withdrawals are disabled. In this case we
	// also do not want to rotate the fingerprints.
	allowForTest := build.Release != "testing" || am.h.dependencies.Disrupt("OutOfSyncInTest")
	if !cc.Synced && allowForTest {
		am.withdrawalsInactive = true
		return
	}

	// If withdrawals were already active (meaning the host was synced) and
	// if the new blockheight is not one at which we expect to rotate, we
	// can return early. In all other cases we rotate the buckets both in
	// memory and on disk. We rotate when the new height crosses over the
	// new current bucket range. We have to take into account the old and
	// new height due to blockchain reorgs that could cause the blockheight
	// to increase (or decrease) by multiple blocks at a time, potentially
	// skipping over the min height of the bucket.
	min, _ := currentBucketRange(cc.BlockHeight)
	withdrawalsActive := !am.withdrawalsInactive
	shouldRotate := oldHeight < cc.BlockHeight && oldHeight < min && min <= cc.BlockHeight
	if withdrawalsActive && !shouldRotate {
		return
	}

	// Rotate fingerprint buckets on disk
	am.mu.Unlock()
	errRotate := am.staticAccountsPersister.callRotateFingerprintBuckets()
	am.mu.Lock()

	// Rotate in memory only if the on-disk rotation succeeded
	if errRotate == nil {
		am.fingerprints.rotate()
	} else if !errors.Contains(errRotate, errRotationDisabled) {
		am.h.log.Critical("ERROR: Could not rotate fingerprints on disk, withdrawals have been deactivated", errRotate)
	}

	// Disable withdrawals on failed rotation
	am.withdrawalsInactive = errRotate != nil
}

// deposit performs a couple of steps in preparation of the
// deposit. If everything checks out it will commit the deposit.
func (am *accountManager) deposit(id modules.AccountID, amount, maxRisk, maxBalance types.Currency, blockHeight types.BlockHeight, refund bool, pr *persistResult, syncChan chan struct{}) error {
	// Open the account, if the account does not exist yet, it will be created.
	acc, err := am.openAccount(id)
	if err != nil {
		err2 := errors.AddContext(err, "failed to open account for deposit")
		pr.externErr = err2
		close(pr.errAvail)
		return err2
	}

	// Verify if the deposit does not exceed the maximum
	if !refund && acc.depositExceedsMaxBalance(amount, maxBalance) {
		pr.externErr = ErrBalanceMaxExceeded
		close(pr.errAvail)
		return ErrBalanceMaxExceeded
	}

	// If current risk exceeds the max risk, add the deposit to the
	// blockedDeposits queue. These deposits will get dequeued by processes that
	// lower the current risk, such as FC fsyncs or account persists.
	if am.currentRisk.Cmp(maxRisk) > 0 {
		am.blockedDeposits = append(am.blockedDeposits, &blockedDeposit{
			id:            id,
			amount:        amount,
			persistResult: pr,
			syncResult:    syncChan,
		})
		return nil
	}

	// Commit the deposit
	am.commitDeposit(acc, amount, blockHeight, pr, syncChan)
	return nil
}

// managedWithdraw performs a couple of steps in preparation of the
// withdrawal. If everything checks out it will commit the withdrawal.
func (am *accountManager) managedWithdraw(msg *modules.WithdrawalMessage, fp crypto.Hash, priority int64, maxRisk types.Currency, blockHeight types.BlockHeight, commitResultChan chan error) (err error) {
	amount, id, expiry := msg.Amount, msg.Account, msg.Expiry

	am.mu.Lock()
	defer func() {
		if err == nil {
			am.staticAccountsPersister.callQueueSaveFingerprint(fp, expiry)
		}
	}()
	defer am.mu.Unlock()

	// Check if withdrawals are inactive. This will be the case when the host is
	// not synced yet. Until that is not the case, we do not allow trading.
	if am.withdrawalsInactive {
		return modules.ErrWithdrawalsInactive
	}

	// Save the fingerprint in memory. If the fingerprint is known we return an
	// error. Note that a call to the persister is deferred which'll save the
	// fingerprint on disk.
	exists := am.fingerprints.has(fp)
	if exists {
		return ErrWithdrawalSpent
	}
	am.fingerprints.add(fp, expiry, blockHeight)

	// Open the account, create if it does not exist yet
	acc, err := am.openAccount(id)
	if err != nil {
		return errors.AddContext(err, "failed to open account for withdrawal")
	}
	// If the account balance is insufficient, block the withdrawal.
	if acc.withdrawalExceedsBalance(amount) {
		acc.blockedWithdrawals.Push(blockedWithdrawal{
			withdrawal:   msg,
			priority:     priority,
			commitResult: commitResultChan,
		})
		return nil
	}

	// Block this withdrawal if maxRisk is exceeded
	if am.currentRisk.Cmp(maxRisk) > 0 || len(am.blockedWithdrawals) > 0 {
		if am.h.dependencies.Disrupt("errMaxRiskReached") {
			return errMaxRiskReached // only for testing purposes
		}
		am.blockedWithdrawals = append(am.blockedWithdrawals, &blockedWithdrawal{
			withdrawal:   msg,
			priority:     priority,
			commitResult: commitResultChan,
		})
		return nil
	}

	am.commitWithdrawal(acc, amount, blockHeight, commitResultChan)
	return nil
}

// managedAccountPersistInfo is a helper method that will collect all of the
// necessary data to perform the persist.
func (am *accountManager) managedAccountPersistInfo(id modules.AccountID) *accountPersistInfo {
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
		waiting: len(acc.persistResults),
	}
}

// threadedUpdateRiskAfterSync will update the current risk after it has
// received a signal on the syncChan. This thread will stop after a timeout of
// 10 mins, or if it receives a stop signal. This syncChan is passed in by the
// RPC, it will close this channel when the file contract has been fsynced.
func (am *accountManager) threadedUpdateRiskAfterSync(deposit types.Currency, syncChan chan struct{}) {
	if err := am.h.tg.Add(); err != nil {
		return
	}
	defer am.h.tg.Done()

	select {
	case <-syncChan:
		bh := am.h.BlockHeight()
		am.mu.Lock()
		am.currentRisk = am.currentRisk.Sub(deposit)

		// Now that risk is lowered, we need to unblock deposit and withdrawals
		// seeing as they might be blocked due to current risk exceeding the
		// maximum. Unblock deposit and withdrawals in this particular order
		// until the deposit (read: allowance) runs out.
		allowance := deposit
		allowance = am.unblockDeposits(allowance, bh)
		am.unblockWithdrawals(allowance, bh)
		am.mu.Unlock()
		return
	case <-am.h.tg.StopChan():
		return
	case <-time.After(10 * time.Minute):
		return
	}
}

// threadedSaveAccount will save the account with given id. The thread will keep
// calling this method as long as there are channels in persistResults. Which
// essentially means there are other threads awaiting the persist result. There
// is only ever one save thread per account.
//
// Note that the caller adds this thread to the threadgroup. If the add is done
// inside the goroutine, the host risks losing money even on graceful shutdowns.
func (am *accountManager) threadedSaveAccount(id modules.AccountID) (waiting int) {
	// Gather all information required to persist and process it afterwards
	accInfo := am.managedAccountPersistInfo(id)
	if accInfo == nil {
		// Account expired
		return
	}

	// Call save account (disrupt, if triggered, will introduce a sleep here,
	// simulating a slow persist which allows maxRisk to be reached)
	_ = am.h.dependencies.Disrupt("errMaxRiskReached")
	persister := am.staticAccountsPersister
	err := persister.callSaveAccount(accInfo.data, accInfo.index)
	bh := am.h.BlockHeight()

	am.mu.Lock()
	defer am.mu.Unlock()

	// Take care of the pending risk in the account. We lower the risk by the
	// amount of risk that was captured in account info. This is necessary
	// seeing the pendingRisk can have been increased in the mean time, and that
	// risk has not yet been persisted to disk.
	acc, exists := am.accounts[id]
	if exists {
		acc.pendingRisk = acc.pendingRisk.Sub(accInfo.risk)

		// Send the result to all persistResultChans that where waiting the
		// moment we calculated the account data. If there are remaining
		// persistResultChans after this operation, we signal this to the caller
		// through the waiting return value. If there are channels still
		// waiting, threadedSaveAccount will be called again.
		acc.sendResult(err, accInfo.waiting)
		waiting = len(acc.persistResults)

		// Sanity check
		if waiting == 0 && !acc.pendingRisk.IsZero() {
			build.Critical("The account's pending risk should be zero if there are no threads awaiting a persist")
		}
	}

	// Lower the current risk by the amount of risk that just got persisted.
	am.currentRisk = am.currentRisk.Sub(accInfo.risk)

	// Risk is lowered - see if we can unblock deposits and/or withdrawals. We
	// unblock in this particular order to ensure deposits are unblocked first.
	allowance := accInfo.risk
	allowance = am.unblockDeposits(allowance, bh)
	am.unblockWithdrawals(allowance, bh)
	return
}

// commitDeposit deposits the amount to the account balance and schedules a
// persist to save the account data to disk.
func (am *accountManager) commitDeposit(a *account, amount types.Currency, blockHeight types.BlockHeight, pr *persistResult, syncChan chan struct{}) {
	// Update the account details
	a.balance = a.balance.Add(amount)
	a.lastTxnTime = time.Now().Unix()

	// As soon as the account balance has been updated in memory, we want to
	// increase the host's current risk by the deposit amount. When the file
	// contracts get fsynced, the sync chan will close. At that point we will
	// lower the current risk by the deposit amount.
	am.updateRiskAfterDeposit(amount, syncChan)

	// Unblock withdrawals that were waiting for more funds.
	for a.blockedWithdrawals.Len() > 0 {
		bw := a.blockedWithdrawals.Pop().(*blockedWithdrawal)
		err := bw.withdrawal.ValidateExpiry(blockHeight, blockHeight+bucketBlockRange)
		if err != nil {
			select {
			case bw.commitResult <- err:
			default:
			}
			continue
		}

		// Requeue if balance is insufficient
		if bw.withdrawal.Amount.Cmp(a.balance) > 0 {
			a.blockedWithdrawals.Push(*bw)
			break
		}

		// Commit the withdrawal
		am.commitWithdrawal(a, bw.withdrawal.Amount, blockHeight, bw.commitResult)
	}
	am.schedulePersist(a, pr)
}

// commitWithdrawal withdraws the amount from the account balance and schedules
// a persist to save the account data to disk.
func (am *accountManager) commitWithdrawal(a *account, amount types.Currency, blockHeight types.BlockHeight, commitResultChan chan error) {
	// Update the account details
	a.balance = a.balance.Sub(amount)
	a.lastTxnTime = time.Now().Unix()
	close(commitResultChan)

	// Update the current risk and the account's pending risk. By allowing money
	// to be withdrawn from the account without awaiting the persist, the host
	// is at risk at losing the balance delta. This is added to the risk now,
	// but will get subtracted again when the account was persisted.
	a.pendingRisk = a.pendingRisk.Add(amount)
	am.currentRisk = am.currentRisk.Add(amount)

	pr := &persistResult{
		errAvail: make(chan struct{}),
	}
	am.schedulePersist(a, pr)
}

// updateRiskAfterDeposit will update the current risk after a deposit has been
// performed. The deposit amount is added to the host's current risk. The host
// is at risk for this amount as long as the file contract has not been fsynced.
// When the RPC is done with the FC fsync it will close the syncChan so the
// account manager can lower the risk.
func (am *accountManager) updateRiskAfterDeposit(deposit types.Currency, syncChan chan struct{}) {
	// The syncChan might already be closed, perform a quick check to verify
	// this. If it is the case we do not need to update risk at all.
	select {
	case <-syncChan:
		return
	default:
	}

	// Add the deposit to the outstanding risk
	am.currentRisk = am.currentRisk.Add(deposit)
	go am.threadedUpdateRiskAfterSync(deposit, syncChan)
}

// schedulePersist will append the persistResultChan to the persist queue and
// call threadedSaveAccount for the given id. This way persists are happening in
// a FIFO fashion, ensuring an account is never overwritten by older account
// data.
func (am *accountManager) schedulePersist(acc *account, pr *persistResult) {
	persistScheduled := len(acc.persistResults) > 0
	acc.persistResults = append(acc.persistResults, pr)

	// Return early if we weren't the first to schedule a persist for this
	// account. This is to ensure there's only one save thread per account.
	if persistScheduled {
		return
	}

	go func() {
		// Need to call tg.Add inside of the goroutine because 'schedulePersist'
		// is holding a lock and it's not okay to call tg.Add() while holding a
		// lock (can cause a deadlock at shutdown if the accountManager mutex is
		// grabbed during an OnStop or AfterStop function).
		if err := am.h.tg.Add(); err != nil {
			return
		}
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
func (am *accountManager) unblockDeposits(allowance types.Currency, bh types.BlockHeight) (remaining types.Currency) {
	var numUnblocked int
	for i, bd := range am.blockedDeposits {
		amount, id := bd.amount, bd.id
		acc, exists := am.accounts[id]
		if !exists {
			// Account has expired
			continue
		}

		if allowance.Cmp(amount) < 0 {
			// Allowance ran out
			numUnblocked = i
			break
		}

		// Commit the deposit
		am.commitDeposit(acc, amount, bh, bd.persistResult, bd.syncResult)
		allowance = allowance.Sub(amount)
	}
	am.blockedDeposits = am.blockedDeposits[numUnblocked:]
	remaining = allowance
	return
}

// unblockWithdrawals will unblock pending withdrawals until the allowance runs
// out. The allowance is the amount of risk that got freed up by a persist or a
// commit (FC fsync).
func (am *accountManager) unblockWithdrawals(allowance types.Currency, bh types.BlockHeight) {
	var numUnblocked int
	for i, bw := range am.blockedWithdrawals {
		amount, id := bw.withdrawal.Amount, bw.withdrawal.Account
		acc, exists := am.accounts[id]
		if !exists {
			// Account has expired
			continue
		}

		// Validate the expiry - this is necessary seeing as the blockheight can
		// have been changed since the withdrawal was blocked, potentially
		// pushing it over its expiry.
		if err := bw.withdrawal.ValidateExpiry(bh, bh+bucketBlockRange); err != nil {
			select {
			case bw.commitResult <- err:
			default:
			}
			continue
		}

		// Sanity check
		if acc.balance.Cmp(amount) < 0 {
			build.Critical("blocked withdrawal has insufficient balance to process, due to the order of execution in the callWithdrawal, this should never happen")
		}

		if allowance.Cmp(amount) < 0 {
			// Allowance ran out
			numUnblocked = i
			break
		}

		// Commit the withdrawal
		am.commitWithdrawal(acc, bw.withdrawal.Amount, bh, bw.commitResult)
		allowance = allowance.Sub(amount)
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
	for {
		his := am.h.managedInternalSettings()
		accountExpiryTimeout := int64(his.EphemeralAccountExpiry.Seconds())

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
			// of the indexes that got expired.
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
				am.accountBitfield.releaseIndex(index)
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

// staticWaitForDepositResult will block until it receives a message on the
// given result channel, or until it receives a stop signal.
func (am *accountManager) staticWaitForDepositResult(pr *persistResult) error {
	select {
	case <-pr.errAvail:
		return pr.externErr
	case <-am.h.tg.StopChan():
		return ErrDepositCancelled
	}
}

// staticWaitForWithdrawalResult will block until it receives a message on the
// given result channel, or until it either times out or receives a stop signal.
func (am *accountManager) staticWaitForWithdrawalResult(commitResultChan chan error) error {
	ctx, cancel := context.WithTimeout(am.h.tg.StopCtx(), blockedWithdrawalTimeout)
	defer cancel()

	select {
	case err := <-commitResultChan:
		return err
	case <-ctx.Done():
		return ErrBalanceInsufficient
	case <-am.h.tg.StopChan():
		return ErrWithdrawalCancelled
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
			for _, c := range acc.persistResults {
				c.externErr = ErrAccountExpired
				close(c.errAvail)
			}
			delete(am.accounts, id)
			deleted = append(deleted, acc.index)
		}
	}
	return deleted
}

// callAccountBalance will return the balance of an account.
func (am *accountManager) callAccountBalance(id modules.AccountID) types.Currency {
	am.mu.Lock()
	defer am.mu.Unlock()
	account, exists := am.accounts[id]
	if !exists {
		return types.ZeroCurrency
	}
	return account.balance
}

// openAccount will return an account object. If the account does not exist it
// will be created.
func (am *accountManager) openAccount(id modules.AccountID) (*account, error) {
	if id.IsZeroAccount() {
		return nil, ErrZeroAccountID
	}
	acc, exists := am.accounts[id]
	if exists {
		return acc, nil
	}
	acc = &account{
		id:                 id,
		index:              am.accountBitfield.assignFreeIndex(),
		blockedWithdrawals: make(blockedWithdrawalHeap, 0),
		persistResults:     make([]*persistResult, 0),
	}
	am.accounts[id] = acc
	return acc, nil
}

// assignFreeIndex will return the next available account index
func (ab *accountBitfield) assignFreeIndex() uint32 {
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
func (ab *accountBitfield) releaseIndex(index uint32) {
	i := index / 64
	pos := index % 64
	var mask uint64 = ^(1 << pos)
	(*ab)[i] &= mask
}

// buildIndex will initialize bitfields representing all ephemeral accounts.
// Upon account expiry, its index will be freed up by unsetting the
// corresponding bit. When a new account is opened, it will grab the first
// available index, effectively recycling the expired account indexes.
func (ab *accountBitfield) buildIndex(accounts map[modules.AccountID]*account) {
	var maxIndex uint32
	for _, acc := range accounts {
		if acc.index > maxIndex {
			maxIndex = acc.index
		}
	}

	// Add empty bitfields to accommodate all account indexes
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
func (fm *fingerprintMap) add(fp crypto.Hash, expiry, blockHeight types.BlockHeight) {
	_, max := currentBucketRange(blockHeight)
	if expiry <= max {
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
	return withdrawal.Cmp(a.balance) > 0
}

// sendResult will send the given result to the result channels that are waiting
func (a *account) sendResult(result error, waiting int) {
	for i := 0; i < waiting; i++ {
		persistResult := a.persistResults[i]
		err := errors.AddContext(result, ErrAccountPersist.Error())
		persistResult.externErr = err
		close(persistResult.errAvail)
	}
	a.persistResults = a.persistResults[waiting:]
}
