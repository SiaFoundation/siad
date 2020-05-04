package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// TODO: try to load account from persistence
//
// TODO: for now the account is a separate object that sits as first class
// object on the worker, most probably though this will move as to not have two
// separate mutex domains.

// withdrawalValidityPeriod defines the period (in blocks) a withdrawal message
// remains spendable after it has been created. Together with the current block
// height at time of creation, this period makes up the WithdrawalMessage's
// expiry height.
const withdrawalValidityPeriod = 6

// account represents a renter's ephemeral account on a host.
type account struct {
	staticID        modules.AccountID
	staticHostKey   types.SiaPublicKey
	staticSecretKey crypto.SecretKey

	pendingDeposits    types.Currency
	pendingWithdrawals types.Currency
	balance            types.Currency

	staticMu sync.Mutex
}

// managedOpenAccount returns an account for the given host key. If it does not
// exist already one is created.
func (r *Renter) managedOpenAccount(hostKey types.SiaPublicKey) *account {
	// TODO: implementation follows in #4422 (adds persistence)
	return newAccount(hostKey)
}

// newAccount returns an new account object for the given host.
func newAccount(hostKey types.SiaPublicKey) *account {
	aid, sk := modules.NewAccountID()
	return &account{
		staticID:        aid,
		staticHostKey:   hostKey,
		staticSecretKey: sk,
	}
}

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

	// TODO (follow-up !4256) cover with test yet

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

	// receive PayByEphemeralAccountResponse
	//
	// TODO: this should not be blocking! handle in a separate goroutine
	var payByResponse modules.PayByEphemeralAccountResponse
	err = modules.RPCRead(stream, &payByResponse)
	if err != nil {
		return err
	}
	return nil
}

// managedAvailableBalance returns the amount of money that is available to
// spend. It is calculated by taking into account pending spends and pending
// funds.
func (a *account) managedAvailableBalance() types.Currency {
	a.staticMu.Lock()
	defer a.staticMu.Unlock()

	total := a.balance.Add(a.pendingDeposits)
	if a.pendingWithdrawals.Cmp(total) < 0 {
		return total.Sub(a.pendingWithdrawals)
	}
	return types.ZeroCurrency
}

// managedCommitDeposit commits a pending deposit, either after success or
// failure. Depending on the outcome the given amount will be added to the
// balance or not. If the pending delta is zero, and we altered the account
// balance, we update the account.
func (a *account) managedCommitDeposit(amount types.Currency, success bool) {
	a.staticMu.Lock()
	defer a.staticMu.Unlock()

	// (no need to sanity check - the implementation of 'Sub' does this for us)
	a.pendingDeposits = a.pendingDeposits.Sub(amount)

	// reflect the successful deposit in the balance field, if the pending delta
	// is zero we update the account on disk.
	if success {
		a.balance = a.balance.Add(amount)
	}
}

// managedTryRefill will check if the current available balance is below the
// threshold and schedules a refill if that is the case.
func (a *account) managedTryRefill(threshold, amount types.Currency, refill func(types.Currency) error) {
	a.staticMu.Lock()
	defer a.staticMu.Unlock()

	var available types.Currency
	total := a.balance.Add(a.pendingDeposits)
	if a.pendingWithdrawals.Cmp(total) < 0 {
		available = total.Sub(a.pendingWithdrawals)
	}

	if available.Cmp(threshold) >= 0 {
		return
	}

	// perform the refill in a separate goroutine
	a.pendingDeposits = a.pendingDeposits.Add(amount)
	go func() {
		err := refill(amount)
		a.managedCommitDeposit(amount, err == nil)
	}()
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
