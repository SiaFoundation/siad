package renter

import (
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// withdrawalValidityPeriod defines the period (in blocks) a withdrawal message
// remains spendable after it has been created. Together with the current block
// height at time of creation, this period makes up the WithdrawalMessage's
// expiry height.
const withdrawalValidityPeriod = 6

var (
	// accountIdleCheckFrequency establishes how frequently the sync function
	// should check whether the worker is idle. A relatively high frequency is
	// okay, because this function only runs while the worker is frozen and
	// expecting to perform an expensive sync operation.
	accountIdleCheckFrequency = build.Select(build.Var{
		Dev:      time.Second * 4,
		Standard: time.Second * 5,
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
		Testing:  int(1e3 * 15),          // 15 seconds - needs to be long even in testing
	}).(int)

	// accountSyncMinWaitTime defines the minimum amount of time that a worker
	// will wait between performing sync checks. This should be a large number,
	// on the order of the amount of time a worker is expected to be frozen
	// multiplied by the total number of workers.
	accountSyncMinWaitTime = build.Select(build.Var{
		Dev:      time.Minute,
		Standard: 60 * time.Minute, // 1 hour
		Testing:  10 * time.Second, // needs to be long even in testing
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
		balance            types.Currency
		negativeBalance    types.Currency
		pendingWithdrawals types.Currency
		pendingDeposits    types.Currency

		// Error handling and cooldown tracking.
		consecutiveFailures uint64
		cooldownUntil       time.Time
		recentErr           error

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
)

// ProvidePayment takes a stream and various payment details and handles the
// payment by sending and processing payment request and response objects.
// Returns an error in case of failure.
func (a *account) ProvidePayment(stream siamux.Stream, host types.SiaPublicKey, rpc types.Specifier, amount types.Currency, refundAccount modules.AccountID, blockHeight types.BlockHeight) error {
	if rpc == modules.RPCFundAccount && !refundAccount.IsZeroAccount() {
		return errors.New("Refund account is expected to be the zero account when funding an ephemeral account")
	}
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
	return a.balance.Add(a.pendingDeposits)
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
// failure. Depending on the outcome the given amount will be deducted from the
// balance or not. If the pending delta is zero, and we altered the account
// balance, we update the account.
func (a *account) managedCommitWithdrawal(amount types.Currency, success bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// (no need to sanity check - the implementation of 'Sub' does this for us)
	a.pendingWithdrawals = a.pendingWithdrawals.Sub(amount)

	// reflect the successful withdrawal in the balance field
	if success {
		if a.balance.Cmp(amount) >= 0 {
			a.balance = a.balance.Sub(amount)
		} else {
			amount = amount.Sub(a.balance)
			a.balance = types.ZeroCurrency
			a.negativeBalance = a.negativeBalance.Add(amount)
		}
	}
}

// managedOnCooldown returns true if the account is on cooldown and therefore
// unlikely to receive additional funding in the near future.
func (a *account) managedOnCooldown() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cooldownUntil.After(time.Now())
}

// managedResetBalance sets the given balance and resets the account's balance
// delta state variables. This happens when we have performanced a balance
// inquiry on the host and we decide to trust his version of the balance.
func (a *account) managedResetBalance(balance types.Currency) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.balance = balance
	a.pendingDeposits = types.ZeroCurrency
	a.pendingWithdrawals = types.ZeroCurrency
	a.negativeBalance = types.ZeroCurrency
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

// managedAccountNeedsRefill will check whether the worker's account needs to be
// refilled. This function will return false if any conditions are met which
// are likely to prevent the refill from being successful.
func (w *worker) managedAccountNeedsRefill() bool {
	// Check if the host version is compatible with accounts.
	cache := w.staticCache()
	if build.VersionCmp(cache.staticHostVersion, minAsyncVersion) < 0 {
		return false
	}
	// Check if the price table is valid.
	if !w.staticPriceTable().staticValid() {
		return false
	}
	// Check if the account is synced.
	if w.managedNeedsToSyncAccountToHost() {
		return false
	}

	// Check if there is a cooldown in place, and check if the balance is low
	// enough to justify a refill.
	w.staticAccount.mu.Lock()
	cooldownUntil := w.staticAccount.cooldownUntil
	balance := w.staticAccount.availableBalance()
	w.staticAccount.mu.Unlock()
	if time.Now().Before(cooldownUntil) {
		return false
	}
	refillAt := w.staticBalanceTarget.Div64(2)
	if balance.Cmp(refillAt) >= 0 {
		return false
	}

	// A refill is needed.
	return true
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
		if err == nil {
			return
		}

		// If the error is not nil, increment the cooldown.
		w.staticAccount.mu.Lock()
		cd := cooldownUntil(w.staticAccount.consecutiveFailures)
		w.staticAccount.cooldownUntil = cd
		w.staticAccount.consecutiveFailures++
		w.staticAccount.recentErr = err
		w.staticAccount.mu.Unlock()

		// Have the threadgroup wake the worker when the account comes off of
		// cooldown.
		w.renter.tg.AfterFunc(cd.Sub(time.Now()), func() {
			w.staticWake()
		})
	}()

	// create a new stream
	var stream siamux.Stream
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

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		err = errors.AddContext(err, "could not write fund account specifier")
		return
	}

	// send price table uid
	pt := w.staticPriceTable().staticPriceTable
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		err = errors.AddContext(err, "could not write price table uid")
		return
	}

	// send fund account request
	err = modules.RPCWrite(stream, modules.FundAccountRequest{Account: w.staticAccount.staticID})
	if err != nil {
		err = errors.AddContext(err, "could not write the fund account request")
		return
	}

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, w.staticHostPubKey, modules.RPCFundAccount, amount.Add(pt.FundAccountCost), modules.ZeroAccountID, w.staticCache().staticBlockHeight)
	if err != nil && strings.Contains(err.Error(), "balance exceeded") {
		// The host reporting that the balance has been exceeded suggests that
		// the host believes that we have more money than we believe that we
		// have.
		w.staticAccount.mu.Lock()
		w.staticAccount.syncAt = 0
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

	// TODO: We need to parse the response and check for an error, such as
	// MaxBalanceExceeded. In the specific case of MaxBalanceExceeded, we need
	// to do a balance inquiry and check that the balance is actually high
	// enough.
	//
	// If we are stuck, and the host won't let us get to a good balance level,
	// we need to go on cooldown, this worker is no good. That will happen as
	// long as we return an error.
	//
	// If we are not stuck, and we have enough balance, we can set the error to
	// nil (to prevent entering cooldown) even though it technically failed,
	// because the failure does not indicate a problem.

	// Wake the worker so that any jobs potentially blocking on getting more
	// money in the account can be activated.
	w.staticWake()
	return
}

// managedSyncAccountBalanceToHost is executed  before the worker loop and
// corrects the account balance in case of an unclean renter shutdown. It does
// so by performing the AccountBalanceRPC and resetting the account to the
// balance communicated by the host. This only happens if our account balance is
// zero, which indicates an unclean shutdown.
//
// NOTE: it is important this function is only used when the worker has no
// in-progress jobs, neither serial nor async, to ensure the account balance
// sync does not leave the account in an undesired state. The worker should not
// be launching new jobs while this function is running.
func (w *worker) managedSyncAccountBalanceToHost() {
	// Spin/block until the worker has no jobs in motion. This should only be
	// called from the primary loop of the worker, meaning that no new jobs will
	// be created while we spin.
	isIdle := func() bool {
		sls := w.staticLoopState
		a := atomic.LoadUint64(&sls.atomicSerialJobRunning) != 0
		b := atomic.LoadUint64(&sls.atomicReadDataOutstanding) != 0
		c := atomic.LoadUint64(&sls.atomicWriteDataOutstanding) != 0
		return !a && !b && !c
	}
	start := time.Now()
	for !isIdle() {
		if time.Since(start) > time.Minute*40 {
			w.renter.log.Critical("worker has taken more than 40 minutes to go idle")
		}
		w.renter.tg.Sleep(accountIdleCheckFrequency)
	}

	// Sanity check the account's deltas are zero, indicating there are no
	// in-progress jobs
	w.staticAccount.mu.Lock()
	deltasAreZero := w.staticAccount.negativeBalance.IsZero() &&
		w.staticAccount.pendingDeposits.IsZero() &&
		w.staticAccount.pendingWithdrawals.IsZero()
	w.staticAccount.mu.Unlock()
	if !deltasAreZero {
		build.Critical("managedSyncAccountBalanceToHost is called on a worker with an account that has non-zero deltas, indicating in-progress jobs")
	}

	balance, err := w.staticHostAccountBalance()
	if err != nil {
		w.renter.log.Printf("ERROR: failed to check account balance on host %v failed, err: %v\n", w.staticHostPubKeyStr, err)
		return
	}

	// If our account balance is lower than the balance indicated by the host,
	// we want to sync our balance by resetting it.
	if w.staticAccount.managedAvailableBalance().Cmp(balance) < 0 {
		w.staticAccount.managedResetBalance(balance)
	}

	// Determine how long to wait before attempting to sync again, and then
	// update the syncAt time. There is significant randomness in the waiting
	// because syncing with the host requires freezing up the worker. We do not
	// want to freeze up a large number of workers at once, nor do we want to
	// freeze them frequently.
	waitTime := time.Duration(fastrand.Intn(accountSyncRandWaitMilliseconds)) * time.Millisecond
	waitTime += accountSyncMinWaitTime
	w.staticAccount.mu.Lock()
	w.staticAccount.syncAt = time.Now().Add(waitTime)
	w.staticAccount.mu.Unlock()

	// TODO perform a thorough balance comparison to decide whether the drift in
	// the account balance is warranted. If not the host needs to be penalized
	// accordingly. Perform this check at startup and periodically.
}

// managedNeedsToSyncAccountToHost returns true if the renter needs to sync the
// account to the host.
func (w *worker) managedNeedsToSyncAccountToHost() bool {
	// There is no need to sync the account to the host if the worker does not
	// support RHP3.
	if build.VersionCmp(w.staticCache().staticHostVersion, minAsyncVersion) < 0 {
		return false
	}

	w.staticAccount.mu.Lock()
	defer w.staticAccount.mu.Unlock()
	return w.staticAccount.syncAt.Before(time.Now())
}

// staticHostAccountBalance performs the AccountBalanceRPC on the host
func (w *worker) staticHostAccountBalance() (types.Currency, error) {
	// Sanity check - only one account balance check should be running at a
	// time.
	if !atomic.CompareAndSwapUint64(&w.atomicAccountBalanceCheckRunning, 0, 1) {
		w.renter.log.Critical("account balance is being checked in two threads concurrently")
	}
	defer atomic.StoreUint64(&w.atomicAccountBalanceCheckRunning, 0)

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

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, w.staticHostPubKey, modules.RPCAccountBalance, pt.AccountBalanceCost, w.staticAccount.staticID, w.staticCache().staticBlockHeight)
	if err != nil {
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
