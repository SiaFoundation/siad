package feemanager

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
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
		var feesToUpdate []modules.FeeUID
		var feesToProcess []*modules.AppFee
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
			feesToProcess = append(feesToProcess, fee)
		}
		fm.mu.Unlock()

		// Calculate the updated payoutHeight and submit updates for the fees
		payoutHeight := nextPayoutHeight + PayoutInterval
		for _, feeUID := range feesToUpdate {
			err = ps.callPersistFeeUpdate(feeUID, payoutHeight)
			if err != nil {
				fc.staticLog.Println("WARN: Error submitting a fee update with updated PayoutHeight:", err)
			}
		}

		// Process any current heights
		// for _, fee := range feesToProcess {
		// 	// Process fee
		// }

		// Sleep until it is time to check the fees again
		select {
		case <-fc.staticTG.StopChan():
			return
		case <-time.After(processFeesCheckInterval):
		}
	}
}

// processFee will create a txn to split the fee amount between the application
// developer and Nebulous
func (fm *FeeManager) processFee(fee *modules.AppFee) error {
	// TODO: Once a fee is confirmed on-chain, add an entry with a timestamp to
	// the append-only log that says the fee is now available on chain.
	//
	// TODO: We will probably also need to make an update when the transaction
	// is posted which contains the transaction. This is a bit tricky because
	// the transaction will need to be split across multiple entries, which will
	// make both encoding and decoding a bit annoying.

	// Create transaction for the fee and track it

	// Transaction should be 70% to the developer, 30% to Nebulous

	// Mark that the transaction for the fee was created
	return nil
}
