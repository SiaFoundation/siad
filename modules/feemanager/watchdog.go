package feemanager

import (
	"math"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// errTxnExists is the error returned if a transaction is already being
	// monitored by the watchdog
	errTxnExists = errors.New("transaction already exists in the watchdog")

	// errTxnNotFound is the error returned if a transaction is not being
	// monitored by the watchdog
	errTxnNotFound = errors.New("transaction not found in the watchdog")

	// transactionCheckInterval is how often the watchdog will check the
	// transactions
	transactionCheckInterval = build.Select(build.Var{
		Standard: time.Minute * 10,
		Dev:      time.Minute,
		Testing:  time.Second * 3,
	}).(time.Duration)
)

type (

	// trackedTransaction contains information about a transaction that the
	// watchdog is tracking
	trackedTransaction struct {
		feeUIDs []modules.FeeUID
		txn     types.Transaction
	}

	// watchdog monitors transactions for the FeeManager to ensure that
	// transactions for Fees are being confirmed on chain
	watchdog struct {
		// feeUIDToTxnID maps fee UIDs to transaction IDs
		feeUIDToTxnID map[modules.FeeUID]types.TransactionID

		// Transactions that the watchdog is monitoring
		txns map[types.TransactionID]trackedTransaction

		// Utilities
		staticCommon *feeManagerCommon
		mu           sync.Mutex
	}
)

// addFeesToTransaction adds a list of feeUIDs to a trackedTransaction and
// updates the watchdog
func (w *watchdog) addFeesToTransaction(feeUIDs []modules.FeeUID, tt trackedTransaction) error {
	txnID := tt.txn.ID()
	for _, feeUID := range feeUIDs {
		tt.feeUIDs = append(tt.feeUIDs, feeUID)
		w.feeUIDToTxnID[feeUID] = txnID
	}
	w.txns[txnID] = tt

	return nil
}

// callAddFeesToTransaction adds a list of feeUIDs to a trackedTransaction and
// updates the watchdog
func (w *watchdog) callAddFeesToTransaction(feeUIDs []modules.FeeUID, txnID types.TransactionID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	tt, ok := w.txns[txnID]
	if !ok {
		return errTxnNotFound
	}
	return w.addFeesToTransaction(feeUIDs, tt)
}

// callClearTransaction clears a transaction from the watchdog
func (w *watchdog) callClearTransaction(txnID types.TransactionID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	tt, ok := w.txns[txnID]
	if !ok {
		return errTxnNotFound
	}

	w.clearTransaction(tt)
	return nil
}

// callFeeTracked returns whether or not the fee is being tracked by the
// watchdog and the transaction ID it is associated with
func (w *watchdog) callFeeTracked(feeUID modules.FeeUID) (types.TransactionID, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	txnID, ok := w.feeUIDToTxnID[feeUID]
	return txnID, ok
}

// callMonitorTransaction adds a transaction to the watchdog to monitor with the
// associated fees.
func (w *watchdog) callMonitorTransaction(feeUIDs []modules.FeeUID, txn types.Transaction) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check to see if the transaction is already being monitored by the
	// watchdog
	txnID := txn.ID()
	_, ok := w.txns[txnID]
	if ok {
		// This is a developer error, we should only be calling this method
		// after creating a new transaction
		err := errors.AddContext(errTxnExists, txnID.String())
		build.Critical(err)
		return err
	}

	// Initialize the trackedTransaction
	tt := trackedTransaction{
		txn: txn,
	}
	// Add transaction to the watchdog
	return w.addFeesToTransaction(feeUIDs, tt)
}

// callTransactionTracked returns whether or not the transaction is being tracked by the
// watchdog
func (w *watchdog) callTransactionTracked(txnID types.TransactionID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.txns[txnID]
	return ok
}

// clearTransaction clears a transaction from the watchdog
func (w *watchdog) clearTransaction(tt trackedTransaction) {
	txnID := tt.txn.ID()
	delete(w.txns, txnID)
	for _, feeUID := range tt.feeUIDs {
		// We don't want to clear the fee if it has been added again for a new
		// transaction so only remove it the transaction ID matches
		feeTxnID, ok := w.feeUIDToTxnID[feeUID]
		if !ok {
			continue
		}
		if txnID == feeTxnID {
			delete(w.feeUIDToTxnID, feeUID)
		}
	}
}

// managedClearTransaction clears a transaction from the watchdog
func (w *watchdog) managedClearTransaction(tt trackedTransaction) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clearTransaction(tt)
}

// managedTrackedTxns returns all the transactions that are being tracked by the
// watchdog
func (w *watchdog) managedTrackedTxns() []trackedTransaction {
	w.mu.Lock()
	defer w.mu.Unlock()
	var txns []trackedTransaction
	for _, tt := range w.txns {
		txns = append(txns, tt)
	}
	return txns
}

// callDropTransaction drops a transaction from the watchdog and creates a
// persist event
func (fm *FeeManager) callDropTransaction(txnID types.TransactionID) error {
	w := fm.staticCommon.staticWatchdog

	w.mu.Lock()
	tt, ok := w.txns[txnID]
	w.mu.Unlock()
	if !ok {
		return errTxnNotFound
	}
	return fm.managedDropTransaction(tt)
}

// managedConfirmTransaction marks a transaction as confirmed and the associated
// Fees as Paid then persists the event.
func (fm *FeeManager) managedConfirmTransaction(tt trackedTransaction) error {
	p := fm.staticCommon.staticPersist
	w := fm.staticCommon.staticWatchdog
	txnID := tt.txn.ID()

	// Persist the Transaction confirmed event
	err := p.callPersistTxnConfirmed(tt.feeUIDs, txnID)
	if err != nil {
		return errors.AddContext(err, "unable to persist transaction confirmed")
	}

	// Update in memory that the payment is complete for the fees
	fm.mu.Lock()
	for _, feeUID := range tt.feeUIDs {
		fee, ok := fm.fees[feeUID]
		if !ok {
			// Fees should not be able to be cancelled once they are being tracked by
			// the watchdog.
			build.Critical(errors.AddContext(feeNotFoundError(feeUID), "fee not found after watchdog dropped transaction"))
			// Fine to continue in production as the fee is no longer in memory it
			// will not create another issue.
			continue
		}
		fee.PaymentCompleted = true
	}
	fm.mu.Unlock()

	w.managedClearTransaction(tt)
	return nil
}

// managedDropTransaction drops a transaction from the watchdog and creates a
// persist event
func (fm *FeeManager) managedDropTransaction(tt trackedTransaction) error {
	p := fm.staticCommon.staticPersist
	w := fm.staticCommon.staticWatchdog
	txnID := tt.txn.ID()

	// Persist the drop event
	err := p.callPersistTxnDropped(tt.feeUIDs, txnID)
	if err != nil {
		return errors.AddContext(err, "unable to persist the drop event")
	}

	// Update the fees so that the process fees subsystem will
	// create a new transaction
	fm.mu.Lock()
	for _, feeUID := range tt.feeUIDs {
		fee, ok := fm.fees[feeUID]
		if !ok {
			// Fees should not be able to be cancelled once they are being tracked by
			// the watchdog.
			build.Critical(errors.AddContext(feeNotFoundError(feeUID), "fee not found after watchdog dropped transaction"))
			// Fine to continue in production as the fee is no longer in memory it
			// will not create another issue.
			continue
		}
		fee.TransactionCreated = false
	}
	fm.mu.Unlock()

	// Clear the transaction from memory
	w.managedClearTransaction(tt)
	return nil
}

// threadedMonitorTransactions checks the transactions that the watchdog is
// monitoring in a loop to see when they are confirmed in the tpool
func (fm *FeeManager) threadedMonitorTransactions() {
	// Define shorter name helpers
	fc := fm.staticCommon
	w := fm.staticCommon.staticWatchdog

	// Threadgroup Check
	err := fc.staticTG.Add()
	if err != nil {
		return
	}
	defer fc.staticTG.Done()

	// Check for Transactions in a loop
	for {
		// Make sure we are synced before checking for transactions
		fm.managedBlockUntilSynced()

		trackedTxns := w.managedTrackedTxns()
		for _, tt := range trackedTxns {
			// Since this loop submits persistence events and there could be a large
			// number of transactions, check if the FeeManager has received a close
			// signal to help with quick shutdowns.
			select {
			case <-fc.staticTG.StopChan():
				return
			default:
			}

			// Check if the transaction is confirmed
			txnID := tt.txn.ID()
			walletTxn, found, err := fc.staticWallet.Transaction(txnID)
			if err != nil {
				fc.staticLog.Println("WARN: unable to get transaction from Wallet:", err)
				continue
			}

			// Check if Transaction is found in the tpool
			if !found {
				// Try and rebroadcast the transaction
				err = fc.staticTpool.AcceptTransactionSet([]types.Transaction{tt.txn})
				if err != nil {
					fc.staticLog.Println("WARN: unable to re-broadcast the transaction:", err)
				}
				continue
			}

			// Check if the ConfirmationHeight is set
			if walletTxn.ConfirmationHeight != types.BlockHeight(math.MaxUint64) {
				// The wallet sees the transaction as confirmed. Mark the transaction as
				// confirmed.
				err = fm.managedConfirmTransaction(tt)
				if err != nil {
					fc.staticLog.Println("WARN: unable to mark transaction as confirmed:", err)
				}
				continue
			}
		}

		// Sleep until it is time for the next loop iteration
		select {
		case <-fc.staticTG.StopChan():
			return
		case <-time.After(transactionCheckInterval):
		}
	}
}
