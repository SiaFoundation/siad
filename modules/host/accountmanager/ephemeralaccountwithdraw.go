package accountmanager

import (
	"crypto"
	"errors"
	"fmt"
	"time"

	"gitlab.com/NebulousLabs/Sia/types"
)

// managedWithdraw will subtract the amount from the account's balance and store
// the receipt if the action was successful.
func (am *AccountManager) managedWithdraw(accountID string, amount types.Currency, receipt crypto.Hash) error {
	err := am.tg.Add()
	if err != nil {
		return err
	}
	defer am.tg.Done()

	// Return early if amount is zero, we return an error here seeing as it's
	// unexpected behaviour
	if amount.IsZero() {
		return errors.New("ERROR: Trying to withdraw zero currency")
	}

	// Ensure the account exists
	if am.accounts[accountID] == nil {
		return fmt.Errorf("ERROR: Unknown account with ID: %v", accountID)
	}

	// Verify balance (use RLock here to avoid potentially unnecessary Lock)
	acc := am.accounts[accountID]
	acc.mu.RLock()
	if acc.balance.Cmp(amount) < 0 {
		acc.mu.RUnlock()
		return errors.New("ERROR: Balance insufficient")
	}
	acc.mu.RUnlock()

	// Acquire a lock and verify balance, other thread might have updated it
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if acc.balance.Cmp(amount) < 0 {
		return errors.New("ERROR: Balance insufficient")
	}

	acc.balance = acc.balance.Sub(amount)
	acc.updated = time.Now()

	// Keep track of the receipt
	// TODO: validate the receipt
	am.mu.Lock()
	defer am.mu.Unlock()
	am.receipts = append(am.receipts, receipt)

	return nil
}
