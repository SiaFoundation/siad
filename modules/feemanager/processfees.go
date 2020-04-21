package feemanager

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// ProcessConsensusChange will submit a call to process fees if the consensus is
// synced
func (fm *FeeManager) ProcessConsensusChange(cc modules.ConsensusChange) {
	err := fm.common.staticTG.Add()
	if err != nil {
		return
	}
	defer fm.common.staticTG.Done()

	// Check to see if Consensus is synced
	if !cc.Synced {
		return
	}

	// Process fees
	go fm.threadedProcessFees()
	return
}

// threadedProcessFees loops over the FeeManager's fees and processes fees based
// on payOutHeight
func (fm *FeeManager) threadedProcessFees() {
	err := fm.common.staticTG.Add()
	if err != nil {
		return
	}
	defer fm.common.staticTG.Done()

	// Get the current blockheight
	bh := fm.common.staticCS.Height()

	// Check if there are no fees, bump out the payout height if so.
	fm.mu.Lock()
	numFees := len(fm.fees)
	fm.mu.Unlock()
	if numFees == 0 {
		fm.common.persist.mu.Lock()
		fm.common.persist.nextPayoutHeight = bh + PayoutInterval
		fm.common.persist.mu.Unlock()
		fm.common.persist.syncCoordinator.managedSyncPersist()
	}

	fm.mu.Lock()
	defer fm.mu.Unlock()
	// If there are no fees, bump out the payoutHeight
	if len(fm.fees) == 0 {
		fm.payoutHeight = bh + PayoutInterval
	}
	// Check to see if the payoutHeight has been reached.
	if fm.payoutHeight > bh {
		return
	}
	fm.staticLog.Printf("Processing fees; Blockheight %v, PayoutHeight %v", bh, fm.payoutHeight)

	// Process the fees
	var processErrors error
	for _, fee := range fm.fees {
		// Check for any recurring fees that have already been paid for this period
		if fee.Recurring && fee.PayoutHeight > bh {
			continue
		}

		// Process the fee.
		err := fm.processFee(fee)
		if err != nil {
			fm.staticLog.Printf("WARN: unable to process fee; id %v; err: %v", fee.UID, err)
			processErrors = errors.Compose(processErrors, err)
			continue
		}
		delete(fm.fees, fee.UID)
	}

	// Increment the payoutHeight.
	if processErrors == nil {
		fm.payoutHeight += PayoutInterval
		fm.staticLog.Println("All fees processed, new PayoutHeight is", fm.payoutHeight)
	}

	// Save the FeeManager
	err = fm.save()
	if err != nil {
		fm.staticLog.Println("WARN: error saving FeeManager after processing fees:", err)
	}
	return
}

// processFee will submit txns to split the PayOut between the application
// developer and Nebulous
func (fm *FeeManager) processFee(fee *appFee) error {
	if fm.staticDeps.Disrupt("ProcessFeeFail") {
		return errors.New("processFee failed due to dependency")
	}

	// Split PayOut between Application Developer Address and Nebulous Address
	appDevFeePayOut := fee.Amount.Mul64(7).Div64(10)
	nebulousFeePayOut := fee.Amount.Mul64(3).Div64(10)
	appDevFee := types.SiacoinOutput{
		Value:      appDevFeePayOut,
		UnlockHash: fee.Address,
	}
	nebulousFee := types.SiacoinOutput{
		Value:      nebulousFeePayOut,
		UnlockHash: nebAddress,
	}
	outputs := []types.SiacoinOutput{appDevFee, nebulousFee}
	_, err := fm.staticWallet.SendSiacoinsMulti(outputs)
	if err != nil {
		return errors.AddContext(err, "unable to send siacoin outputs")
	}

	// TODO: Once a fee is confirmed on-chain, add an entry with a timestamp to
	// the append-only log that says the fee is now available on chain.
	//
	// TODO: We will probably also need to make an update when the transaction
	// is posted which contains the transaction. This is a bit tricky because
	// the transaction will need to be split across multiple entries, which will
	// make both encoding and decoding a bit annoying.

	return nil
}
