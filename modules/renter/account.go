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

// withdrawalValidityPeriod defines the period (in blocks) a withdrawal message
// remains spendable after it has been created. Together with the current block
// height at time of creation, this period makes up the WithdrawalMessage's
// expiry height.
const withdrawalValidityPeriod = 6

type (

	// account represents a renter's ephemeral account on a host.
	account struct {
		staticID        modules.AccountID
		staticHostKey   types.SiaPublicKey
		staticOffset    int64
		staticSecretKey crypto.SecretKey

		balance            types.Currency
		pendingWithdrawals types.Currency
		pendingDeposits    types.Currency

		staticMu sync.RWMutex
	}
)

// managedOpenAccount returns an account for the given host. If it does not
// exist already one is created.
func (r *Renter) managedOpenAccount(hostKey types.SiaPublicKey) (*account, error) {
	id := r.mu.Lock()
	defer r.mu.Unlock(id)

	acc, ok := r.accounts[hostKey.String()]
	if ok {
		return acc, nil
	}
	acc, err := r.newAccount(hostKey)
	if err != nil {
		return nil, err
	}
	r.accounts[hostKey.String()] = acc
	return acc, nil
}

// newAccount returns a new account object for the given host.
func (r *Renter) newAccount(hostKey types.SiaPublicKey) (*account, error) {
	// calculate the account's offset
	offset := (len(r.accounts) + 1) * accountSize // +1 for metadata

	// create the account
	aid, sk := modules.NewAccountID()
	acc := &account{
		staticID:        aid,
		staticHostKey:   hostKey,
		staticOffset:    int64(offset),
		staticSecretKey: sk,
	}

	if err := errors.Compose(
		acc.managedPersist(r.staticAccountsFile),
		r.staticAccountsFile.Sync(),
	); err != nil {
		return nil, err
	}

	return acc, nil
}

// ProvidePayment takes a stream and various payment details and handles the
// payment by sending and processing payment request and response objects.
// Returns an error in case of failure.
func (a *account) ProvidePayment(stream siamux.Stream, host types.SiaPublicKey, rpc types.Specifier, amount types.Currency, blockHeight types.BlockHeight) error {
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

	// receive PayByEphemeralAccountResponse
	//
	// TODO: this should not be blocking! handle as a callback
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

	// reflect the successful deposit in the balance field
	if success {
		a.balance = a.balance.Add(amount)
	}
}

// managedCommitWithdrawal commits a pending withdrawal, either after success or
// failure. Depending on the outcome the given amount will be deducted from the
// balance or not. If the pending delta is zero, and we altered the account
// balance, we update the account.
func (a *account) managedCommitWithdrawal(amount types.Currency, success bool) {
	a.staticMu.Lock()
	defer a.staticMu.Unlock()

	// (no need to sanity check - the implementation of 'Sub' does this for us)
	a.pendingWithdrawals = a.pendingWithdrawals.Sub(amount)

	// reflect the successful withdrawal in the balance field
	if success {
		a.balance = a.balance.Sub(amount)
	}
}

// managedTrackDeposit keeps track of pending deposits by adding the given
// amount to the 'pendingDeposits' field.
func (a *account) managedTrackDeposit(amount types.Currency) {
	a.staticMu.Lock()
	defer a.staticMu.Unlock()
	a.pendingDeposits = a.pendingDeposits.Add(amount)
}

// managedTrackWithdrawal keeps track of pending withdrawals by adding the given
// amount to the 'pendingWithdrawals' field.
func (a *account) managedTrackWithdrawal(amount types.Currency) {
	a.staticMu.Lock()
	defer a.staticMu.Unlock()
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
