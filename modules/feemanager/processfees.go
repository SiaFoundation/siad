package feemanager

import (
	"fmt"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// processFeesCheckInterval is how often the FeeManager will check if fees
	// can be processed
	processFeesCheckInterval = build.Select(build.Var{
		Standard: time.Minute * 10,
		Dev:      time.Minute,
		Testing:  time.Second,
	}).(time.Duration)

	// syncCheckInterval is how often the FeeManager will check if consensus is
	// synced
	syncCheckInterval = build.Select(build.Var{
		Standard: time.Minute * 5,
		Dev:      time.Minute,
		Testing:  time.Second,
	}).(time.Duration)
)

// blockUntilSynced will block until the consensus is synced
func (fm *FeeManager) blockUntilSynced() {
	for {
		// Check if consensus is synced
		if fm.staticCommon.staticCS.Synced() {
			return
		}

		// Block until it is time to check again
		select {
		case <-fm.staticCommon.staticTG.StopChan():
			return
		case <-time.After(syncCheckInterval):
		}
	}
}

// createdAndPersistTransaction will create a transaction by sending the outputs
// to the wallet and will then persist the events
func (fm *FeeManager) createdAndPersistTransaction(feeUIDs []modules.FeeUID, outputs []types.SiacoinOutput) (err error) {
	// Submit the outputs and get the transactions
	txns, err := fm.staticCommon.staticWallet.SendSiacoinsMulti(outputs)
	if err != nil {
		return errors.AddContext(err, "unable to build transaction for fee")
	}

	// From SendSiacoinsMulti, the transaction that was just created is the
	// first transaction in the slice of transactions returned.
	txn := txns[len(txns)-1]
	txnID := txn.ID()

	// Once the transaction is created, we want to submit it to the watchdog.
	// Then the watchdog can handle any transaction related clean up.
	fm.staticCommon.staticWatchdog.callMonitorTransaction(feeUIDs, txn)

	// Handle the transaction if there is an error with persistence
	defer func() {
		if err != nil {
			err = errors.Compose(err, fm.callDropTransaction(txnID))
		}
	}()

	// Persist the transaction
	err = fm.staticCommon.staticPersist.callPersistTransaction(txn)
	if err != nil {
		return errors.AddContext(err, "unable to persist that the transaction")
	}

	// Persist the transaction created events.
	err = fm.staticCommon.staticPersist.callPersistTxnCreated(feeUIDs, txnID)
	if err != nil {
		return errors.AddContext(err, "unable to persist the transaction created event")
	}
	return nil
}

// managedProcessFees will process the fees by creating transactions to split
// the fee amount between the application developer and Nebulous and persisting
// the events.
//
// To limit the number and size of transactions the fees are processed in
// batches, meaning that all the feeUIDs submitted will be processed or non of
// them will be processed.
func (fm *FeeManager) managedProcessFees(feeUIDs []modules.FeeUID) error {
	var nebulousOutputValue types.Currency
	outputMap := make(map[types.UnlockHash]types.Currency)
	fm.mu.Lock()
	for _, feeUID := range feeUIDs {
		fee, ok := fm.fees[feeUID]
		if !ok {
			// If the fee it no longer in memory then it was probably cancelled.
			// Return an error to as we want to batch fees when creating
			// transactions and this batch of FeeUIDs is no longer
			// complete.
			fm.mu.Unlock()
			return feeNotFoundError(feeUID)
		}

		// check if the fee has already been marked as transaction created
		if fee.TransactionCreated {
			err := fmt.Errorf("fee %v is already marked as transaction created but was submitted for processing", fee.FeeUID)
			build.Critical(err)
			return err
		}

		// Mark the fee as having the transaction created in memory to prevent
		// it from being canceled by another thread.
		fee.TransactionCreated = true

		// Update the output information
		outputMap[fee.Address] = outputMap[fee.Address].Add(fee.Amount.Mul64(7).Div64(10))
		nebulousOutputValue = nebulousOutputValue.Add(fee.Amount.Mul64(3).Div64(10))
	}
	fm.mu.Unlock()

	// Create developer outputs
	var outputs []types.SiacoinOutput
	for addr, val := range outputMap {
		outputs = append(outputs, types.SiacoinOutput{
			Value:      val,
			UnlockHash: addr,
		})
	}

	// Create the Nebulous output
	outputs = append(outputs, types.SiacoinOutput{
		Value:      nebulousOutputValue,
		UnlockHash: fm.staticCommon.staticDeps.NebulousAddress(),
	})

	// Create and persist the transaction
	err := fm.createdAndPersistTransaction(feeUIDs, outputs)
	if err == nil {
		return nil
	}

	// Revert in memory changes
	fm.mu.Lock()
	defer fm.mu.Unlock()
	for _, feeUID := range feeUIDs {
		fee, ok := fm.fees[feeUID]
		if !ok {
			// Fees should not be able to be canceled with the
			// TransactionCreated Flag set to True
			build.Critical(fmt.Errorf("fee %v not found after TransactionCreate set to true", feeUID))
			continue
		}
		fee.TransactionCreated = false
	}
	return errors.AddContext(err, "unable to create and persist transaction")
}

// threadedProcessFees is a background thread that handles processing the
// FeeManager's Fees
func (fm *FeeManager) threadedProcessFees() {
	err := fm.staticCommon.staticTG.Add()
	if err != nil {
		return
	}
	defer fm.staticCommon.staticTG.Done()

	// Define shorter name helpers for staticCommon and staticPersit
	fc := fm.staticCommon
	ps := fm.staticCommon.staticPersist

	// Process Fees in a loop until the Feemanager shutsdown
	for {
		// Block until synced
		fm.blockUntilSynced()

		// Grab the Current blockheight
		bh := fc.staticCS.Height()

		// Check if the FeeManager nextPayoutHeight needs to be pushed out, if
		// so update in memory. We do not need to save here as any following
		// call to save a fee to the persist file will persist the new value.
		ps.mu.Lock()
		if ps.nextPayoutHeight <= bh {
			ps.nextPayoutHeight = bh + PayoutInterval
		}
		nextPayoutHeight := ps.nextPayoutHeight
		ps.mu.Unlock()

		// Check for fees that need to be updated due to their PayoutHeights
		// being set to 0, and check for fees that are ready to be processed
		//
		// We track lists of FeeUIDs because the fees need to be managed under
		// the FeeManager's mutex.
		var feesToProcess, feesToUpdate []modules.FeeUID
		fm.mu.Lock()
		for _, fee := range fm.fees {
			// Skip any fees that have had transactions created or payments
			// completed
			if fee.TransactionCreated || fee.PaymentCompleted {
				continue
			}

			// Skip any fees with PayoutHeights in the future
			if fee.PayoutHeight > bh {
				continue
			}

			// Grab the UIDs of any fees that need to be updated
			if fee.PayoutHeight == 0 {
				feesToUpdate = append(feesToUpdate, fee.FeeUID)
				continue
			}

			// Fee needs to be processed
			feesToProcess = append(feesToProcess, fee.FeeUID)
		}
		fm.mu.Unlock()

		// Calculate the updated payoutHeight and submit updates for the fees
		payoutHeight := nextPayoutHeight + PayoutInterval
		for _, feeUID := range feesToUpdate {
			// Make the update in memory
			fm.mu.Lock()
			fee, ok := fm.fees[feeUID]
			if !ok {
				// If the fee is no longer in memory it means it was cancelled
				// so there is nothing to do so we continue.
				fm.mu.Unlock()
				continue
			}
			originalPayoutHeight := fee.PayoutHeight
			fee.PayoutHeight = payoutHeight
			fm.mu.Unlock()

			// Persist the update
			err = ps.callPersistFeeUpdate(feeUID, payoutHeight)
			if err != nil {
				// Revert in memory change
				fm.mu.Lock()
				fee.PayoutHeight = originalPayoutHeight
				fm.mu.Unlock()
				fc.staticLog.Println("WARN: Error submitting a fee update with updated PayoutHeight:", err)
				continue
			}
		}

		// Process fees that need to be processed
		err = fm.managedProcessFees(feesToProcess)
		if err != nil {
			fc.staticLog.Println("WARN: Error processing fees:", err)
		}

		// Sleep until it is time to check the fees again
		select {
		case <-fc.staticTG.StopChan():
			return
		case <-time.After(processFeesCheckInterval):
		}
	}
}
