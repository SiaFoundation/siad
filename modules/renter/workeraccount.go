package renter

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// withdrawalValidityPeriod defines the period (in blocks) a withdrawal
	// message remains spendable after it has been created. Together with the
	// current block height at time of creation, this period makes up the
	// WithdrawalMessage's expiry height.
	withdrawalValidityPeriod = 6

	// fundAccountGougingPercentageThreshold is the percentage threshold, in
	// relation to the allowance, at which we consider the cost of funding an
	// account to be too expensive. E.g. the cost of funding the account as many
	// times as necessary to spend the total allowance should never exceed 1% of
	// the total allowance.
	fundAccountGougingPercentageThreshold = .01
)

const (
	// the following categories are constants used to determine the
	// corresponding spending field in the account's spending details whenever
	// we pay for an rpc request using the ephemeral account as payment method.
	categoryErr spendingCategory = iota
	categoryDownload
	categoryRegistryRead
	categoryRegistryWrite
	categoryRepairDownload
	categoryRepairUpload
	categorySnapshotDownload
	categorySnapshotUpload
	categorySubscription
	categoryUpload
)

var (
	// accountIdleCheckFrequency establishes how frequently the sync function
	// should check whether the worker is idle. A relatively high frequency is
	// okay, because this function only runs while the worker is frozen and
	// expecting to perform an expensive sync operation.
	accountIdleCheckFrequency = build.Select(build.Var{
		Dev:      time.Second * 4,
		Standard: time.Second * 5,
		Testnet:  time.Second * 5,
		Testing:  time.Second * 3,
	}).(time.Duration)

	// accountSyncRandWaitMilliseconds defines the number of random milliseconds
	// that are added to the wait time. Randomness is used to ensure that
	// workers are not all syncing at the same time - the sync operation freezes
	// workers. This number should be larger than the expected amount of time a
	// worker will be frozen multiplied by the total number of workers.
	accountSyncRandWaitMilliseconds = build.Select(build.Var{
		Dev:      int(1e3 * 60),          // 1 minute
		Standard: int(3 * 1e3 * 60 * 60), // 3 hours
		Testnet:  int(3 * 1e3 * 60 * 60), // 3 hours
		Testing:  int(1e3 * 15),          // 15 seconds - needs to be long even in testing
	}).(int)

	// accountSyncMinWaitTime defines the minimum amount of time that a worker
	// will wait between performing sync checks. This should be a large number,
	// on the order of the amount of time a worker is expected to be frozen
	// multiplied by the total number of workers.
	accountSyncMinWaitTime = build.Select(build.Var{
		Dev:      time.Minute,
		Standard: 60 * time.Minute, // 1 hour
		Testnet:  60 * time.Minute, // 1 hour
		Testing:  10 * time.Second, // needs to be long even in testing
	}).(time.Duration)

	// accountIdleMaxWait defines the max amount of time that the worker will
	// wait to reach an idle state before firing a build.Critical and giving up
	// on becoming idle. Generally this will indicate that somewhere in the
	// worker code there is a job that is not timing out correctly.
	accountIdleMaxWait = build.Select(build.Var{
		Dev:      10 * time.Minute,
		Standard: 40 * time.Minute,
		Testnet:  40 * time.Minute,
		Testing:  5 * time.Minute, // needs to be long even in testing
	}).(time.Duration)
)

type (
	// account represents a renter's ephemeral account on a host.
	account struct {
		// Information related to host communications.
		staticID        modules.AccountID
		staticHostKey   types.SiaPublicKey
		staticSecretKey crypto.SecretKey

		// Money has multiple states in an account, this is all the information
		// we need to understand the current state of the account's balance and
		// pending updates.
		//
		// The two drift fields keep track of the delta between our version of
		// the balance and the host's version of the balance. We want to keep
		// track of this drift as in the future we might add code that acts upon
		// it and penalizes the host if we find they are cheating us, or
		// behaving sub-optimally.
		balance              types.Currency
		balanceDriftPositive types.Currency
		balanceDriftNegative types.Currency
		pendingDeposits      types.Currency
		pendingWithdrawals   types.Currency
		negativeBalance      types.Currency

		// Spending details contain a breakdown of how much money from the
		// ephemeral account got spent on what type of action. Examples of such
		// actions are downloads, registry reads, registry writes, etc.
		spending spendingDetails

		// Error tracking.
		recentErr         error
		recentErrTime     time.Time
		recentSuccessTime time.Time

		// syncAt defines what time the renter should be syncing the account to
		// the host.
		syncAt time.Time

		// Variables to manage a race condition around account creation, where
		// the account must be available in the data structure before it has
		// been synced to disk successfully (to avoid holding a lock on the
		// account manager during a disk fsync). Anyone trying to use the
		// account will need to block on 'staticReady', and then after that is
		// closed needs to check the status of 'externActive', 'false'
		// indicating that account creation failed and the account was deleted.
		//
		// 'externActive' can be accessed freely once 'staticReady' has been
		// closed.
		staticReady  chan struct{}
		externActive bool

		// Utils. The offset refers to the offset within the file that the
		// account uses.
		mu           sync.Mutex
		staticFile   modules.File
		staticOffset int64
		staticRenter *Renter
	}

	// spendingDetails contains a breakdown of all spending metrics, all money
	// that is being spent from an ephemeral account is accounted for in one of
	// these categories. Every field of this struct should have a corresponding
	// 'spendingCategory'.
	spendingDetails struct {
		downloads         types.Currency
		registryReads     types.Currency
		registryWrites    types.Currency
		repairDownloads   types.Currency
		repairUploads     types.Currency
		snapshotDownloads types.Currency
		snapshotUploads   types.Currency
		subscriptions     types.Currency
		uploads           types.Currency
	}

	// spendingCategory defines an enum that represent a category in the
	// spending details
	spendingCategory uint64
)

// update will add the the spend of given amount to the appropriate field
// depending on the given category
func (s *spendingDetails) update(category spendingCategory, amount types.Currency) {
	if category == categoryErr {
		build.Critical("category is not set, developer error")
		return
	}

	switch category {
	case categoryDownload:
		s.downloads = s.downloads.Add(amount)
	case categorySnapshotDownload:
		s.snapshotDownloads = s.snapshotDownloads.Add(amount)
	case categorySnapshotUpload:
		s.snapshotUploads = s.snapshotUploads.Add(amount)
	case categoryRegistryRead:
		s.registryReads = s.registryReads.Add(amount)
	case categoryRegistryWrite:
		s.registryWrites = s.registryWrites.Add(amount)
	case categoryRepairDownload:
		s.repairDownloads = s.repairDownloads.Add(amount)
	case categoryRepairUpload:
		s.repairUploads = s.repairUploads.Add(amount)
	case categorySubscription:
		s.subscriptions = s.subscriptions.Add(amount)
	case categoryUpload:
		s.uploads = s.uploads.Add(amount)
	default:
		build.Critical("category is not handled, developer error")
	}
}

// ProvidePayment takes a stream and various payment details and handles the
// payment by sending and processing payment request and response objects.
// Returns an error in case of failure.
//
// Note that this implementation does not 'Read' from the stream. This allows
// the caller to pass in a buffer if he so pleases in order to optimise the
// amount of writes on the actual stream.
func (a *account) ProvidePayment(stream io.ReadWriter, amount types.Currency, blockHeight types.BlockHeight) error {
	// NOTE: we purposefully do not verify if the account has sufficient funds.
	// Seeing as withdrawals are a blocking action on the host, it is perfectly
	// ok to trigger them from an account with insufficient balance.

	// create a withdrawal message
	msg := newWithdrawalMessage(a.staticID, amount, blockHeight)
	sig := crypto.SignHash(crypto.HashObject(msg), a.staticSecretKey)

	// send PaymentRequest
	err := modules.RPCWrite(stream, modules.PaymentRequest{Type: modules.PayByEphemeralAccount})
	if err != nil {
		return err
	}

	// send PayByEphemeralAccountRequest
	err = modules.RPCWrite(stream, modules.PayByEphemeralAccountRequest{
		Message:   msg,
		Signature: sig,
	})
	if err != nil {
		return err
	}

	return nil
}

// availableBalance returns the amount of money that is available to
// spend. It is calculated by taking into account pending spends and pending
// funds.
func (a *account) availableBalance() types.Currency {
	total := a.balance.Add(a.pendingDeposits)
	if total.Cmp(a.negativeBalance) <= 0 {
		return types.ZeroCurrency
	}
	total = total.Sub(a.negativeBalance)
	if a.pendingWithdrawals.Cmp(total) < 0 {
		return total.Sub(a.pendingWithdrawals)
	}
	return types.ZeroCurrency
}

// callNeedsToSync returns whether or not the account needs to sync to the host.
func (a *account) callNeedsToSync() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.syncAt.Before(time.Now())
}

// callSpendingDetails returns the spending details for the account
func (a *account) callSpendingDetails() spendingDetails {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.spending
}

// managedAvailableBalance returns the amount of money that is available to
// spend. It is calculated by taking into account pending spends and pending
// funds.
func (a *account) managedAvailableBalance() types.Currency {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.availableBalance()
}

// managedMaxExpectedBalance returns the max amount of money that this
// account is expected to contain after the renter has shut down.
func (a *account) managedMaxExpectedBalance() types.Currency {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maxExpectedBalance()
}

// maxExpectedBalance returns the max amount of money that this account is
// expected to contain after the renter has shut down.
func (a *account) maxExpectedBalance() types.Currency {
	// NOTE: negativeBalance will never be larger than the sum of the pending
	// deposits. If that does happen, this will build.Critical which indicates
	// that something is incorrect within the worker's internal accounting.
	return a.balance.Add(a.pendingDeposits).Sub(a.negativeBalance)
}

// managedMinExpectedBalance returns the min amount of money that this
// account is expected to contain after the renter has shut down.
func (a *account) managedMinExpectedBalance() types.Currency {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.minExpectedBalance()
}

// minExpectedBalance returns the min amount of money that this account is
// expected to contain after the renter has shut down.
func (a *account) minExpectedBalance() types.Currency {
	// subtract the negative balance
	balance := a.balance
	if balance.Cmp(a.negativeBalance) <= 0 {
		return types.ZeroCurrency
	}
	balance = balance.Sub(a.negativeBalance)

	// subtract all pending withdrawals
	if balance.Cmp(a.pendingWithdrawals) <= 0 {
		return types.ZeroCurrency
	}
	balance = balance.Sub(a.pendingWithdrawals)
	return balance
}

// managedCommitDeposit commits a pending deposit, either after success or
// failure. Depending on the outcome the given amount will be added to the
// balance or not. If the pending delta is zero, and we altered the account
// balance, we update the account.
func (a *account) managedCommitDeposit(amount types.Currency, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// (no need to sanity check - the implementation of 'Sub' does this for us)
	a.pendingDeposits = a.pendingDeposits.Sub(amount)

	// reflect the successful deposit in the balance field
	if success {
		if amount.Cmp(a.negativeBalance) <= 0 {
			a.negativeBalance = a.negativeBalance.Sub(amount)
		} else {
			amount = amount.Sub(a.negativeBalance)
			a.negativeBalance = types.ZeroCurrency
			a.balance = a.balance.Add(amount)
		}
	}
}

// managedCommitWithdrawal commits a pending withdrawal, either after success or
// failure. Depending on the outcome the given withdrawal amount will be
// deducted from the balance or not. If the pending delta is zero, and we
// altered the account balance, we update the account. The refund is given
// because both the refund and the withdrawal amount need to be subtracted from
// the pending withdrawals, seeing is it is no longer 'pending'. Only the
// withdrawal amount has to be subtracted from the balance because the refund
// got refunded by the host already.
func (a *account) managedCommitWithdrawal(category spendingCategory, withdrawal, refund types.Currency, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// (no need to sanity check - the implementation of 'Sub' does this for us)
	a.pendingWithdrawals = a.pendingWithdrawals.Sub(withdrawal.Add(refund))

	// reflect the successful withdrawal in the balance field
	if success {
		if a.balance.Cmp(withdrawal) >= 0 {
			a.balance = a.balance.Sub(withdrawal)
		} else {
			withdrawal = withdrawal.Sub(a.balance)
			a.balance = types.ZeroCurrency
			a.negativeBalance = a.negativeBalance.Add(withdrawal)
		}

		// only in case of success we track the spend and what it was spent on
		a.trackSpending(category, withdrawal)
	}
}

// managedNeedsToRefill returns whether or not the account needs to be refilled.
func (a *account) managedNeedsToRefill(target types.Currency) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.availableBalance().Cmp(target) < 0
}

// managedSyncBalance updates the account's balance related fields to "sync"
// with the given balance, which was returned by the host. If the given balance
// is higher or lower than the account's available balance, we update the drift
// fields in the positive or negative direction.
func (a *account) managedSyncBalance(balance types.Currency) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Determine how long to wait before attempting to sync again, and then
	// update the syncAt time. There is significant randomness in the
	// waiting because syncing with the host requires freezing up the
	// worker. We do not want to freeze up a large number of workers at
	// once, nor do we want to freeze them frequently.
	defer func() {
		randWait := fastrand.Intn(accountSyncRandWaitMilliseconds)
		waitTime := time.Duration(randWait) * time.Millisecond
		waitTime += accountSyncMinWaitTime
		a.syncAt = time.Now().Add(waitTime)
	}()

	// If our balance is equal to what the host communicated, we're done.
	currBalance := a.availableBalance()
	if currBalance.Equals(balance) {
		return
	}

	// However, if it is lower we want to reset our account balance and track
	// the amount we drifted.
	if currBalance.Cmp(balance) < 0 {
		a.resetBalance(balance)
		delta := balance.Sub(currBalance)
		a.balanceDriftPositive = a.balanceDriftPositive.Add(delta)
	}

	// If it's higher we only track the amount we drifted.
	if currBalance.Cmp(balance) > 0 {
		delta := currBalance.Sub(balance)
		a.balanceDriftNegative = a.balanceDriftNegative.Add(delta)
	}

	// Persist the account
	err := a.persist()
	if err != nil {
		a.staticRenter.log.Printf("could not persist account, err: %v\n", err)
	}
}

// managedStatus returns the status of the account
func (a *account) managedStatus() modules.WorkerAccountStatus {
	a.mu.Lock()
	defer a.mu.Unlock()

	var recentErrStr string
	if a.recentErr != nil {
		recentErrStr = a.recentErr.Error()
	}

	return modules.WorkerAccountStatus{
		AvailableBalance: a.availableBalance(),
		NegativeBalance:  a.negativeBalance,

		RecentErr:         recentErrStr,
		RecentErrTime:     a.recentErrTime,
		RecentSuccessTime: a.recentSuccessTime,
	}
}

// managedTrackDeposit keeps track of pending deposits by adding the given
// amount to the 'pendingDeposits' field.
func (a *account) managedTrackDeposit(amount types.Currency) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingDeposits = a.pendingDeposits.Add(amount)
}

// managedTrackWithdrawal keeps track of pending withdrawals by adding the given
// amount to the 'pendingWithdrawals' field.
func (a *account) managedTrackWithdrawal(amount types.Currency) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingWithdrawals = a.pendingWithdrawals.Add(amount)
}

// resetBalance sets the given balance and resets the account's balance
// delta state variables. This happens when we have performanced a balance
// inquiry on the host and we decide to trust his version of the balance.
func (a *account) resetBalance(balance types.Currency) {
	a.balance = balance
	a.pendingDeposits = types.ZeroCurrency
	a.pendingWithdrawals = types.ZeroCurrency
	a.negativeBalance = types.ZeroCurrency
}

// trackSpending will keep track of the amount spent, taking into account the
// given refund as well, within the given spend category
func (a *account) trackSpending(category spendingCategory, amount types.Currency) {
	// sanity check the category was set
	if category == categoryErr {
		build.Critical("tracked a spend using an uninitialized category, this is prevented as we want to track all money that is being spent without exception")
		return
	}

	// update the spending metrics
	a.spending.update(category, amount)

	// every time we update we write the account to disk
	err := a.persist()
	if err != nil {
		a.staticRenter.log.Printf("failed to persist account, err: %v\n", err)
	}
}

// newWithdrawalMessage is a helper function that takes a set of parameters and
// a returns a new WithdrawalMessage.
func newWithdrawalMessage(id modules.AccountID, amount types.Currency, blockHeight types.BlockHeight) modules.WithdrawalMessage {
	expiry := blockHeight + withdrawalValidityPeriod
	var nonce [modules.WithdrawalNonceSize]byte
	fastrand.Read(nonce[:])
	return modules.WithdrawalMessage{
		Account: id,
		Expiry:  expiry,
		Amount:  amount,
		Nonce:   nonce,
	}
}

// externSyncAccountBalanceToHost is executed before the worker loop and
// corrects the account balance in case of an unclean renter shutdown. It does
// so by performing the AccountBalanceRPC and resetting the account to the
// balance communicated by the host. This only happens if our account balance is
// zero, which indicates an unclean shutdown.
//
// NOTE: it is important this function is only used when the worker has no
// in-progress jobs, neither serial nor async, to ensure the account balance
// sync does not leave the account in an undesired state. The worker should not
// be launching new jobs while this function is running. To achieve this, we
// ensure that this thread is only run from the primary work loop, which is also
// the only thread that is allowed to launch jobs. As long as this function is
// only called by that thread, and no other thread launches jobs, this function
// is threadsafe.
func (w *worker) externSyncAccountBalanceToHost() {
	// Spin/block until the worker has no jobs in motion. This should only be
	// called from the primary loop of the worker, meaning that no new jobs will
	// be launched while we spin.
	isIdle := func() bool {
		sls := w.staticLoopState
		a := atomic.LoadUint64(&sls.atomicSerialJobRunning) == 0
		b := atomic.LoadUint64(&sls.atomicAsyncJobsRunning) == 0
		return a && b
	}
	start := time.Now()
	for !isIdle() {
		if time.Since(start) > accountIdleMaxWait {
			// The worker failed to go idle for too long. Print the loop state,
			// so we know what kind of task is keeping it busy.
			w.renter.log.Printf("Worker static loop state: %+v\n\n", w.staticLoopState)
			// Get the stack traces of all running goroutines.
			buf := make([]byte, modules.StackSize) // 64MB
			n := runtime.Stack(buf, true)
			w.renter.log.Println(string(buf[:n]))
			w.renter.log.Critical(fmt.Sprintf("worker has taken more than %v minutes to go idle", accountIdleMaxWait.Minutes()))
			return
		}
		awake := w.renter.tg.Sleep(accountIdleCheckFrequency)
		if !awake {
			return
		}
	}

	// Do a check to ensure that the worker is still idle after the function is
	// complete. This should help to catch any situation where the worker is
	// spinning up new jobs, even though it is not supposed to be spinning up
	// new jobs while it is performing the sync operation.
	defer func() {
		if !isIdle() {
			w.renter.log.Critical("worker appears to be spinning up new jobs during managedSyncAccountBalanceToHost")
		}
	}()

	// Sanity check the account's deltas are zero, indicating there are no
	// in-progress jobs
	w.staticAccount.mu.Lock()
	deltasAreZero := w.staticAccount.pendingDeposits.IsZero() && w.staticAccount.pendingWithdrawals.IsZero()
	w.staticAccount.mu.Unlock()
	if !deltasAreZero {
		build.Critical("managedSyncAccountBalanceToHost is called on a worker with an account that has non-zero deltas, indicating in-progress jobs")
	}

	// Track the outcome of the account sync - this ensures a proper working of
	// the maintenance cooldown mechanism.
	balance, err := w.staticHostAccountBalance()
	w.managedTrackAccountSyncErr(err)
	if err != nil {
		w.renter.log.Debugf("ERROR: failed to check account balance on host %v failed, err: %v\n", w.staticHostPubKeyStr, err)
		return
	}

	// Sync the account with the host's version of our balance. This will update
	// our balance in case the host tells us we actually have more money, and it
	// will keep track of drift in both directions.
	w.staticAccount.managedSyncBalance(balance)

	// TODO perform a thorough balance comparison to decide whether the drift in
	// the account balance is warranted. If not the host needs to be penalized
	// accordingly. Perform this check at startup and periodically.
}

// managedNeedsToRefillAccount will check whether the worker's account needs to
// be refilled. This function will return false if any conditions are met which
// are likely to prevent the refill from being successful.
func (w *worker) managedNeedsToRefillAccount() bool {
	// No need to refill the account if the worker is on maintenance cooldown.
	if w.managedOnMaintenanceCooldown() {
		return false
	}
	// No need to refill if the price table is not valid, as it would only
	// result in failure anyway.
	if !w.staticPriceTable().staticValid() {
		return false
	}

	return w.staticAccount.managedNeedsToRefill(w.staticBalanceTarget.Div64(2))
}

// managedNeedsToSyncAccountBalanceToHost returns true if the renter needs to
// sync the renter's account balance with the host's version of the account.
func (w *worker) managedNeedsToSyncAccountBalanceToHost() bool {
	// No need to sync the account if the worker's RHP3 is on cooldown.
	if w.managedOnMaintenanceCooldown() {
		return false
	}
	// No need to sync if the price table is not valid, as it would only
	// result in failure anyway.
	if !w.staticPriceTable().staticValid() {
		return false
	}

	return w.staticAccount.callNeedsToSync()
}

// managedRefillAccount will refill the account if it needs to be refilled
func (w *worker) managedRefillAccount() {
	if w.renter.deps.Disrupt("DisableFunding") {
		return // don't refill account
	}
	// The account balance dropped to below half the balance target, refill. Use
	// the max expected balance when refilling to avoid exceeding any host
	// maximums.
	balance := w.staticAccount.managedMaxExpectedBalance()
	amount := w.staticBalanceTarget.Sub(balance)
	pt := w.staticPriceTable().staticPriceTable

	// If the target amount is larger than the remaining money, adjust the
	// target. Make sure it can still cover the funding cost.
	if contract, ok := w.renter.hostContractor.ContractByPublicKey(w.staticHostPubKey); ok {
		if amount.Add(pt.FundAccountCost).Cmp(contract.RenterFunds) > 0 && contract.RenterFunds.Cmp(pt.FundAccountCost) > 0 {
			amount = contract.RenterFunds.Sub(pt.FundAccountCost)
		}
	}

	// We track that there is a deposit in progress. Because filling an account
	// is an interactive protocol with another machine, we are never sure of the
	// exact moment that the deposit has reached our account. Instead, we track
	// the deposit as a "maybe" until we know for sure that the deposit has
	// either reached the remote machine or failed.
	//
	// At the same time that we track the deposit, we defer a function to check
	// the error on the deposit
	w.staticAccount.managedTrackDeposit(amount)
	var err error
	defer func() {
		// If there was no error, the account should now be full, and will not
		// need to be refilled until the worker has spent up the funds in the
		// account.
		w.staticAccount.managedCommitDeposit(amount, err == nil)

		// Track the outcome of the account refill - this ensures a proper
		// working of the maintenance cooldown mechanism.
		cd := w.managedTrackAccountRefillErr(err)

		// If the error is nil, return.
		if err == nil {
			w.staticAccount.mu.Lock()
			w.staticAccount.recentSuccessTime = time.Now()
			w.staticAccount.mu.Unlock()
			return
		}

		// Track the error on the account for debugging purposes.
		w.staticAccount.mu.Lock()
		w.staticAccount.recentErr = err
		w.staticAccount.recentErrTime = time.Now()
		w.staticAccount.mu.Unlock()

		// If the error could be caused by a revision number mismatch,
		// signal it by setting the flag.
		if errCausedByRevisionMismatch(err) {
			w.staticSetSuspectRevisionMismatch()
			w.staticWake()
		}

		// Have the threadgroup wake the worker when the account comes off of
		// cooldown.
		w.renter.tg.AfterFunc(cd.Sub(time.Now()), func() {
			w.staticWake()
		})
	}()

	// check the current price table for gouging errors
	err = checkFundAccountGouging(w.staticPriceTable().staticPriceTable, w.staticCache().staticRenterAllowance, w.staticBalanceTarget)
	if err != nil {
		return
	}

	// Defer a function that schedules a price table update in case we received
	// an error that indicates the host deems our price table invalid.
	defer func() {
		if modules.IsPriceTableInvalidErr(err) {
			w.staticTryForcePriceTableUpdate()
		}
	}()

	// create a new stream
	var stream net.Conn
	stream, err = w.staticNewStream()
	if err != nil {
		err = errors.AddContext(err, "Unable to create a new stream")
		return
	}
	defer func() {
		closeErr := stream.Close()
		if closeErr != nil {
			w.renter.log.Println("ERROR: failed to close stream", closeErr)
		}
	}()

	// prepare a buffer so we can optimize our writes
	buffer := bytes.NewBuffer(nil)

	// write the specifier
	err = modules.RPCWrite(buffer, modules.RPCFundAccount)
	if err != nil {
		err = errors.AddContext(err, "could not write fund account specifier")
		return
	}

	// send price table uid
	err = modules.RPCWrite(buffer, pt.UID)
	if err != nil {
		err = errors.AddContext(err, "could not write price table uid")
		return
	}

	// send fund account request
	err = modules.RPCWrite(buffer, modules.FundAccountRequest{Account: w.staticAccount.staticID})
	if err != nil {
		err = errors.AddContext(err, "could not write the fund account request")
		return
	}

	// write contents of the buffer to the stream
	_, err = stream.Write(buffer.Bytes())
	if err != nil {
		err = errors.AddContext(err, "could not write the buffer contents")
		return
	}

	// build payment details
	details := contractor.PaymentDetails{
		Host:          w.staticHostPubKey,
		Amount:        amount.Add(pt.FundAccountCost),
		RefundAccount: modules.ZeroAccountID,
		SpendingDetails: modules.SpendingDetails{
			FundAccountSpending: amount,
			MaintenanceSpending: modules.MaintenanceSpending{
				FundAccountCost: pt.FundAccountCost,
			},
		},
	}

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, &pt, details)
	if err != nil && strings.Contains(err.Error(), "balance exceeded") {
		// The host reporting that the balance has been exceeded suggests that
		// the host believes that we have more money than we believe that we
		// have.
		if !w.renter.deps.Disrupt("DisableCriticalOnMaxBalance") {
			// Log a critical in testing as this is very unlikely to happen due
			// to the order of events in the worker loop, seeing as we just
			// synced our account balance with the host if that was necessary
			if build.Release == "testing" {
				build.Critical("worker account refill failed with a max balance - are the host max balance settings lower than the threshold balance?")
			}
			w.renter.log.Println("worker account refill failed", err)
		}
		w.staticAccount.mu.Lock()
		w.staticAccount.syncAt = time.Time{}
		w.staticAccount.mu.Unlock()
	}
	if err != nil {
		err = errors.AddContext(err, "could not provide payment for the account")
		return
	}

	// receive FundAccountResponse. The response contains a receipt and a
	// signature, which is useful for places where accountability is required,
	// but no accountability is required in this case, so we ignore the
	// response.
	var resp modules.FundAccountResponse
	err = modules.RPCRead(stream, &resp)
	if err != nil {
		err = errors.AddContext(err, "could not read the account response")
	}

	// Wake the worker so that any jobs potentially blocking on getting more
	// money in the account can be activated.
	w.staticWake()
	return
}

// staticHostAccountBalance performs the AccountBalanceRPC on the host
func (w *worker) staticHostAccountBalance() (_ types.Currency, err error) {
	// Sanity check - only one account balance check should be running at a
	// time.
	if !atomic.CompareAndSwapUint64(&w.atomicAccountBalanceCheckRunning, 0, 1) {
		w.renter.log.Critical("account balance is being checked in two threads concurrently")
	}
	defer atomic.StoreUint64(&w.atomicAccountBalanceCheckRunning, 0)

	// Defer a function that schedules a price table update in case we received
	// an error that indicates the host deems our price table invalid.
	defer func() {
		if modules.IsPriceTableInvalidErr(err) {
			w.staticTryForcePriceTableUpdate()
		}
	}()

	// Get a stream.
	stream, err := w.staticNewStream()
	if err != nil {
		return types.ZeroCurrency, err
	}
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Println("ERROR: failed to close stream", err)
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCAccountBalance)
	if err != nil {
		return types.ZeroCurrency, err
	}

	// send price table uid
	pt := w.staticPriceTable().staticPriceTable
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return types.ZeroCurrency, err
	}

	// build payment details
	details := contractor.PaymentDetails{
		Host:          w.staticHostPubKey,
		Amount:        pt.AccountBalanceCost,
		RefundAccount: w.staticAccount.staticID,
		SpendingDetails: modules.SpendingDetails{
			MaintenanceSpending: modules.MaintenanceSpending{
				AccountBalanceCost: pt.AccountBalanceCost,
			},
		},
	}

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, &pt, details)
	if err != nil {
		// If the error could be caused by a revision number mismatch,
		// signal it by setting the flag.
		if errCausedByRevisionMismatch(err) {
			w.staticSetSuspectRevisionMismatch()
			w.staticWake()
		}
		return types.ZeroCurrency, err
	}

	// prepare the request.
	abr := modules.AccountBalanceRequest{Account: w.staticAccount.staticID}
	err = modules.RPCWrite(stream, abr)
	if err != nil {
		return types.ZeroCurrency, err
	}

	// read the response
	var resp modules.AccountBalanceResponse
	err = modules.RPCRead(stream, &resp)
	if err != nil {
		return types.ZeroCurrency, err
	}
	return resp.Balance, nil
}

// checkFundAccountGouging verifies the cost of funding an ephemeral account on
// the host is reasonable, if deemed unreasonable we will block the refill and
// the worker will eventually be put into cooldown.
func checkFundAccountGouging(pt modules.RPCPriceTable, allowance modules.Allowance, targetBalance types.Currency) error {
	// If there is no allowance, price gouging checks have to be disabled,
	// because there is no baseline for understanding what might count as price
	// gouging.
	if allowance.Funds.IsZero() {
		return nil
	}

	// In order to decide whether or not the fund account cost is too expensive,
	// we first calculate how many times we can refill the account, taking into
	// account the refill amount and the cost to effectively fund the account.
	//
	// Note: we divide the target balance by two because more often than not the
	// refill happens the moment we drop below half of the target, this means
	// that we actually refill half the target amount most of the time.
	costOfRefill := targetBalance.Div64(2).Add(pt.FundAccountCost)
	numRefills, err := allowance.Funds.Div(costOfRefill).Uint64()
	if err != nil {
		return errors.AddContext(err, "unable to check fund account gouging, could not calculate the amount of refills")
	}

	// The cost of funding is considered too expensive if the total cost is
	// above a certain % of the allowance.
	totalFundAccountCost := pt.FundAccountCost.Mul64(numRefills)
	if totalFundAccountCost.Cmp(allowance.Funds.MulFloat(fundAccountGougingPercentageThreshold)) > 0 {
		return fmt.Errorf("fund account cost %v is considered too high, the total cost of refilling the account to spend the total allowance exceeds %v%% of the allowance - price gouging protection enabled", pt.FundAccountCost, fundAccountGougingPercentageThreshold)
	}

	return nil
}
