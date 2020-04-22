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
		Standard: time.Second * 5,
		Dev:      time.Second * 3,
		Testing:  time.Second,
	}).(time.Duration)
)

// blockUntilSynced will block until the consensus is synced
func (fm *FeeManager) blockUntilSynced() {
	for {
		// Check if consensus is synced
		if fm.common.staticCS.Synced() {
			return
		}

		// Block until it is time to check again
		select {
		case <-fm.common.staticTG.StopChan():
			return
		case <-time.After(processFeesCheckInterval):
		}
	}
}

// threadedProcessFees is a background thread that handles processing the
// FeeManager's Fees
func (fm *FeeManager) threadedProcessFees() {
	err := fm.common.staticTG.Add()
	if err != nil {
		return
	}
	defer fm.common.staticTG.Done()

	for {
		// Block until synced
		fm.blockUntilSynced()

		// Grab the Current blockheight
		bh := fm.common.staticCS.Height()

		// Check if the FeeManager nextPayoutHeight has been set yet, it not,
		// update in memory. We do not need to save here as any following call
		// to save a fee to the persist file will persist the new value
		fm.common.persist.mu.Lock()
		nextPayoutHeight := fm.common.persist.nextPayoutHeight
		if nextPayoutHeight == 0 || nextPayoutHeight < bh {
			nextPayoutHeight = bh + PayoutInterval
			fm.common.persist.nextPayoutHeight = nextPayoutHeight
		}
		fm.common.persist.mu.Unlock()

		// Check for fees that need to be updated due to their PayoutHeights
		// being set to 0, and check for fees that are ready to be processed
		var feesToUpdate []modules.AppFee
		var feesToProcess []modules.AppFee
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

			// Grab any fees that need to be updated
			if fee.PayoutHeight == 0 {
				feesToUpdate = append(feesToUpdate, *fee)
				continue
			}

			// Fee needs to be processed
			feesToProcess = append(feesToProcess, *fee)
		}
		fm.mu.Unlock()

		// Add new entries for all the fees with a PayoutHeight of 0 so the new
		// PayoutHeight is reflected
		for _, fee := range feesToUpdate {
			fee.PayoutHeight = nextPayoutHeight + PayoutInterval
			err = fm.common.persist.callPersistNewFee(fee)
			if err != nil {
				fm.common.staticLog.Println("WARN: Error adding new fee with updated PayoutHeight:", err)
			}
		}

		// Process any current heights
		for _, fee := range feesToProcess {
			// Process fee
		}

		// Sleep until it is time to check the fees again
		select {
		case <-fm.common.staticTG.StopChan():
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
