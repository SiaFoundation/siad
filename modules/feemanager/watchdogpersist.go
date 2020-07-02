package feemanager

import (
	"bytes"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// callApplyTransaction will apply a transaction to the watchdog by adding the
// transaction to the transaction map if they are complete or the partial
// transaction map if the transaction is still being loaded.
func (w *watchdog) callApplyTransaction(et entryTransaction) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check to see if the transaction is already being monitored by the
	// watchdog
	_, ok := w.txns[et.TxnID]
	if ok {
		err := errors.AddContext(errTxnExists, et.TxnID.String())
		build.Critical(err)
		return err
	}

	// Start building partial transaction
	pTxn, ok := w.partialTxns[et.TxnID]
	if !ok {
		pTxn = partialTransactions{
			finalIndex: et.FinalIndex,
			txnID:      et.TxnID,
		}
	}
	pTxn.txnBytes = append(pTxn.txnBytes, et.TxnBytes[:]...)

	// Check if we have built the full transaction
	if et.Index != pTxn.finalIndex {
		// We don't have a full transaction yet
		w.partialTxns[et.TxnID] = pTxn
		return nil
	}

	// We have all the bytes of the transaction, unmarshal it and add it to the
	// watchdog
	var txn types.Transaction
	err := txn.UnmarshalSia(bytes.NewBuffer(pTxn.txnBytes))
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal transaction")
	}

	// Remove partial transaction from map and add full transaction to
	// transaction map
	delete(w.partialTxns, et.TxnID)
	w.txns[et.TxnID] = trackedTransaction{
		txn:        txn,
		createTime: time.Now(),
	}
	return nil
}

// callApplyTxnConfirmed will apply a transaction confirmed to the watchdog by
// removing it from both maps
func (w *watchdog) callApplyTxnConfirmed(txnID types.TransactionID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Grab the transaction in the watchdog
	tt, ok := w.txns[txnID]
	if !ok {
		// If the transaction is no longer in the watchdog then return. Since
		// Transaction Confirmed events can span multiple persist entries this will
		// happen but is OK. The first entry will clear the transaction from the
		// watchdog and the FeeManager persist will handle the fee updates for any
		// subsequent entries
		return
	}

	// Clear the transaction from the Watchdog
	w.clearTransaction(tt)
}

// callApplyTxnCreated will apply a transaction created to the watchdog by
// adding the fee to the tracked transaction and the feeUID and txnID to the ID
// map
func (w *watchdog) callApplyTxnCreated(feeUIDs []modules.FeeUID, txnID types.TransactionID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	tt, ok := w.txns[txnID]
	if !ok {
		build.Critical("transaction should always exist in watchdog before applying create event")
		return
	}
	for _, feeUID := range feeUIDs {
		tt.feeUIDs = append(tt.feeUIDs, feeUID)
		w.feeUIDToTxnID[feeUID] = txnID
	}
	w.txns[txnID] = tt
}

// callApplyTxnDropped will apply a transaction dropped event to the watchdog by
// dropping the tracktransaction and all associated fees
func (w *watchdog) callApplyTxnDropped(txnID types.TransactionID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Grab the transaction in the watchdog
	tt, ok := w.txns[txnID]
	if !ok {
		// If the transaction is no longer in the watchdog then return. Since
		// Transaction Dropped events and can span multiple persist entries this
		// will happen but is OK. The first entry will clear the transaction
		// from the watchdog and the FeeManager persist will handle the fee
		// updates for any subsequent entries
		return
	}

	// Clear the transaction from the Watchdog
	w.clearTransaction(tt)
}
