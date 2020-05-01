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

	// errTxnNotExists is the error returned if a transaction is not being
	// monitored by the watchdog
	errTxnNotExists = errors.New("transaction does not exists in the watchdog")

	// recreateTransactionInterval is how long the watchdog will wait before it
	// will try and recreate the the transaction
	recreateTransactionInterval = build.Select(build.Var{
		Standard: time.Hour * 24,
		Dev:      time.Hour,
		Testing:  time.Second,
	}).(time.Duration)

	// transactionCheckInterval is how ofter the watchdog will check the
	// transactions
	transactionCheckInterval = build.Select(build.Var{
		Standard: time.Minute * 10,
		Dev:      time.Minute,
		Testing:  time.Second,
	}).(time.Duration)
)

type (
	// partialTransactions contains information about a transaction that is
	// partially loaded from disk
	partialTransactions struct {
		finalIndex int
		txnBytes   []byte
		txnID      types.TransactionID
	}

	// trackedTransaction contains information about a transaction that the
	// watchdog is tracking
	trackedTransaction struct {
		createTime time.Time
		feeUIDs    []modules.FeeUID
		txn        types.Transaction
	}

	// watchdog monitors transactions for the FeeManager to ensure that
	// transactions for Fees are being confirmed on chain
	watchdog struct {
		// feeUIDToTxnID maps fee UIDs to transaction IDs
		feeUIDToTxnID map[modules.FeeUID]types.TransactionID

		// partialTxns is a map used to help load transactions from disk
		partialTxns map[types.TransactionID]partialTransactions

		// Transactions that the watchdog is monitoring
		txns map[types.TransactionID]trackedTransaction

		// Utilities
		staticCommon *feeManagerCommon
		mu           sync.Mutex
	}
)

// callFeeTracked returns whether or not the fee is being tracked by the
// watchdog and the transaction ID is it associated with
func (w *watchdog) callFeeTracked(feeUID modules.FeeUID) (types.TransactionID, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	txnID, ok := w.feeUIDToTxnID[feeUID]
	return txnID, ok
}

// callMonitorTransaction adds a transaction to the watchdog to monitor with the
// associated fees.
func (w *watchdog) callMonitorTransaction(feeUIDs []modules.FeeUID, txn types.Transaction) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check to see if the transaction is already being monitored by the
	// watchdog
	txnID := txn.ID()
	_, ok := w.txns[txnID]
	if ok {
		// This is a developer error, we should only be calling this method
		// after creating a new transaction
		build.Critical(errors.AddContext(errTxnExists, txnID.String()))
	}

	// Add transaction to the watchdog
	w.txns[txnID] = trackedTransaction{
		feeUIDs:    feeUIDs,
		createTime: time.Now(),
		txn:        txn,
	}

	// Add Fees to watchdog
	for _, feeUID := range feeUIDs {
		w.feeUIDToTxnID[feeUID] = txnID
	}
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
		return errTxnNotExists
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
			// Fees should not be able to be cancelled once then
			// they are being tracked by the watchdog.
			build.Critical(errors.AddContext(feeNotFoundError(feeUID), "fee not found after watchdog dropped transaction"))
			// Fine to continue in production as the fee is no
			// longer in memory it will not create another issue.
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

	// TODO - sweep the transaction to invalidate it

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
			// Fees should not be able to be cancelled once then
			// they are being tracked by the watchdog.
			build.Critical(errors.AddContext(feeNotFoundError(feeUID), "fee not found after watchdog dropped transaction"))
			// Fine to continue in production as the fee is no
			// longer in memory it will not create another issue.
			continue
		}
		fee.TransactionCreated = false
	}
	fm.mu.Unlock()

	// Clear the transaction from memory
	w.managedClearTransaction(tt)
	return nil
}

// threadedCheckTransactions checks the transactions that the watchdog is
// monitoring in a loop to see when they are confirmed in the wallet
func (fm *FeeManager) threadedCheckTransactions() {
	// Define shorter name helpers
	fc := fm.staticCommon
	w := fm.staticCommon.staticWatchdog

	// Threadgroup Check
	err := fc.staticTG.Add()
	if err != nil {
		return
	}
	defer fc.staticTG.Done()

	// Make sure we are synced before checking for transactions
	fm.blockUntilSynced()

	// Check for Transactions in a loop
	for {
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

			// Check if Transaction is found in the wallet
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
				// Transaction is confirmed in the wallet. Mark the transaction as
				// confirmed.
				err = fm.managedConfirmTransaction(tt)
				if err != nil {
					fc.staticLog.Println("WARN: unable to mark transaction as confirmed:", err)
				}
				continue
			}

			// Transaction is still unconfirmed, check if it is time to drop
			// the transaction
			if time.Since(tt.createTime) < recreateTransactionInterval {
				continue
			}

			// Assume the transaction won't ever go through and drop the
			// transaction
			fc.staticLog.Debugln("Watchdog is dropping transaction", txnID)
			err = fm.managedDropTransaction(tt)
			if err != nil {
				fc.staticLog.Println("WARN: error dropping transaction:", err)
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
