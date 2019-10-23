package accountmanager

import (
	"crypto"
	"errors"
	"time"

	"gitlab.com/NebulousLabs/Sia/types"
)

// managedDeposit will add the amount to the account's balance and store the
// receipt if the action was successful. When the account is not found, it is
// created.
func (am *AccountManager) managedDeposit(accountID string, amount types.Currency, receipt crypto.Hash) error {
	err := am.tg.Add()
	if err != nil {
		return err
	}
	defer am.tg.Done()

	// Return early if amount is zero, we return an error here seeing as it's
	// unexpected behaviour
	if amount.IsZero() {
		return errors.New("ERROR: Trying to deposit zero currency")
	}

	// Ensure the account exists
	if am.accounts[accountID] == nil {
		err := am.managedCreateAccount(accountID)
		if err != nil {
			am.log.Printf("ERROR: CreateAccount failed: %v", err)
			return err
		}
	}

	acc := am.accounts[accountID]
	acc.mu.Lock()
	defer acc.mu.Unlock()
	acc.balance = acc.balance.Add(amount)
	acc.updated = time.Now()

	// Keep track of the receipt
	// TODO: validate the receipt
	am.mu.Lock()
	defer am.mu.Unlock()
	am.receipts = append(am.receipts, receipt)

	return nil
}
