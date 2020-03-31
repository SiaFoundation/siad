package feemanager

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// ProcessConsensusChange will submit a call to process fees if the consensus is
// synced
func (fm *FeeManager) ProcessConsensusChange(cc modules.ConsensusChange) {
	err := fm.staticTG.Add()
	if err != nil {
		return
	}
	defer fm.staticTG.Done()

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
	err := fm.staticTG.Add()
	if err != nil {
		return
	}
	defer fm.staticTG.Done()

	// Get the current blockheight
	bh := fm.staticCS.Height()

	// Check to see if the payoutHeight has been reached
	fm.mu.Lock()
	defer fm.mu.Unlock()
	if fm.payoutHeight == 0 {
		fm.payoutHeight = bh + PayoutInterval
	}
	if fm.payoutHeight < bh {
		// It is not time to process any fees yet
		return
	}
	fm.staticLog.Printf("Processing fees; Blockheight %v, PayoutHeight %v", bh, fm.payoutHeight)

	// Process the fees
	var processErrors error
	for _, fee := range fm.fees {
		// Process the fee
		err := fm.processFee(fee)
		if err != nil {
			fm.staticLog.Printf("WARN: unable to process fee; id %v; err: %v", fee.UID, err)
			processErrors = errors.Compose(processErrors, err)
			continue
		}

		// Remove any non-recurring fees
		if !fee.Recurring {
			delete(fm.fees, fee.UID)
		}
	}

	// Increment the payoutHeight and reset the currentPayout if we successfully
	// processed all the fees
	if processErrors == nil {
		fm.payoutHeight += PayoutInterval
		fm.currentPayout = types.ZeroCurrency
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
func (fm *FeeManager) processFee(fee *modules.AppFee) error {
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

	return nil
}
