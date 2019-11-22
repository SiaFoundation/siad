package contractor

import (
	"math/big"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

type utilityUpdateStatus int

const (
	_ = iota
	noUpdate
	suggestedUtilityUpdate
	necessaryUtilityUpdate
)

// checkHostScore checks host scorebreakdown against minimum accepted scores.
// forceUpdate is true if the utility change must be taken.
func (c *Contractor) checkHostScore(contract modules.RenterContract, sb modules.HostScoreBreakdown, minScoreGFR, minScoreGFU types.Currency) (modules.ContractUtility, utilityUpdateStatus) {
	u := contract.Utility

	// Contract has no utility if the score is poor.
	if !minScoreGFR.IsZero() && sb.Score.Cmp(minScoreGFR) < 0 {
		// Log if the utility has changed.
		if u.GoodForUpload || u.GoodForRenew {
			c.log.Printf("Marking contract as having no utility because of host score: %v", contract.ID)
			c.log.Println("Min Score:", minScoreGFR)
			c.log.Println("Score:    ", sb.Score)
			c.log.Println("Age Adjustment:        ", sb.AgeAdjustment)
			c.log.Println("Burn Adjustment:       ", sb.BurnAdjustment)
			c.log.Println("Collateral Adjustment: ", sb.CollateralAdjustment)
			c.log.Println("Duration Adjustment:   ", sb.DurationAdjustment)
			c.log.Println("Interaction Adjustment:", sb.InteractionAdjustment)
			c.log.Println("Price Adjustment:      ", sb.PriceAdjustment)
			c.log.Println("Storage Adjustment:    ", sb.StorageRemainingAdjustment)
			c.log.Println("Uptime Adjustment:     ", sb.UptimeAdjustment)
			c.log.Println("Version Adjustment:    ", sb.VersionAdjustment)
		}
		u.GoodForUpload = false
		u.GoodForRenew = false

		// Only force utility updates if the score is the min possible score.
		// Otherwise defer update decision for low-score contracts to the
		// churnLimiter.
		if sb.Score.Cmp(types.NewCurrency64(1)) <= 0 {
			return u, necessaryUtilityUpdate
		}
		c.log.Println("Adding contract utility update to churnLimiter queue")
		return u, suggestedUtilityUpdate
	}

	// Contract should not be used for uplodaing if the score is poor.
	if !minScoreGFU.IsZero() && sb.Score.Cmp(minScoreGFU) < 0 {
		if u.GoodForUpload {
			c.log.Printf("Marking contract as not good for upload because of a poor score: %v", contract.ID)
			c.log.Println("Min Score:", minScoreGFU)
			c.log.Println("Score:    ", sb.Score)
			c.log.Println("Age Adjustment:        ", sb.AgeAdjustment)
			c.log.Println("Burn Adjustment:       ", sb.BurnAdjustment)
			c.log.Println("Collateral Adjustment: ", sb.CollateralAdjustment)
			c.log.Println("Duration Adjustment:   ", sb.DurationAdjustment)
			c.log.Println("Interaction Adjustment:", sb.InteractionAdjustment)
			c.log.Println("Price Adjustment:      ", sb.PriceAdjustment)
			c.log.Println("Storage Adjustment:    ", sb.StorageRemainingAdjustment)
			c.log.Println("Uptime Adjustment:     ", sb.UptimeAdjustment)
			c.log.Println("Version Adjustment:    ", sb.VersionAdjustment)

		}
		if !u.GoodForRenew {
			c.log.Println("Marking contract as being good for renew", contract.ID)
		}
		u.GoodForUpload = false
		u.GoodForRenew = true
		return u, necessaryUtilityUpdate
	}

	return u, noUpdate
}

// criticalUtilityChecks performs critical checks on a contract that would
// require, with no exceptions, marking the contract as !GFR and/or !GFU.
// Returns true if and only if and of the checks passed and require the utility
// to be updated.
func (c *Contractor) criticalUtilityChecks(contract modules.RenterContract, host modules.HostDBEntry) (modules.ContractUtility, bool) {
	c.mu.RLock()
	blockHeight := c.blockHeight
	renewWindow := c.allowance.RenewWindow
	period := c.allowance.Period
	c.mu.RUnlock()

	u, needsUpdate := c.offlineCheck(contract, host)
	if needsUpdate {
		return u, needsUpdate
	}

	u, needsUpdate = c.upForRenewalCheck(contract, renewWindow, blockHeight)
	if needsUpdate {
		return u, needsUpdate
	}

	u, needsUpdate = c.sufficientFundsCheck(contract, host, period)
	if needsUpdate {
		return u, needsUpdate
	}

	u, needsUpdate = c.outOfStorageCheck(contract, blockHeight)
	if needsUpdate {
		return u, needsUpdate
	}

	return contract.Utility, false
}

// hostInHostDBCheck checks if the host is in the hostdb and not filtered.
// Returns true if a check fails and the utility returned must be used to update
// the contract state.
func (c *Contractor) hostInHostDBCheck(contract modules.RenterContract) (modules.HostDBEntry, modules.ContractUtility, bool) {
	u := contract.Utility
	host, exists, err := c.hdb.Host(contract.HostPublicKey)
	// Contract has no utility if the host is not in the database. Or is
	// filtered by the blacklist or whitelist. Or if there was an error
	if !exists || host.Filtered || err != nil {
		// Log if the utility has changed.
		if u.GoodForUpload || u.GoodForRenew {
			c.log.Printf("Marking contract as having no utility because found in hostDB: %v, or host is Filtered: %v - %v", exists, host.Filtered, contract.ID)
		}
		u.GoodForUpload = false
		u.GoodForRenew = false
		return host, u, true
	}
	return host, u, false
}

// offLineCheck checks if the host for this contract is offline.
// Returns true if a check fails and the utility returned must be used to update
// the contract state.
func (c *Contractor) offlineCheck(contract modules.RenterContract, host modules.HostDBEntry) (modules.ContractUtility, bool) {
	u := contract.Utility
	// Contract has no utility if the host is offline.
	if isOffline(host) {
		// Log if the utility has changed.
		if u.GoodForUpload || u.GoodForRenew {
			c.log.Println("Marking contract as having no utility because of host being offline", contract.ID)
		}
		u.GoodForUpload = false
		u.GoodForRenew = false
		return u, true
	}
	return u, false
}

// upForRenewalCheck checks if this contract is up for renewal.
// Returns true if a check fails and the utility returned must be used to update
// the contract state.
func (c *Contractor) upForRenewalCheck(contract modules.RenterContract, renewWindow, blockHeight types.BlockHeight) (modules.ContractUtility, bool) {
	u := contract.Utility
	// Contract should not be used for uploading if the time has come to
	// renew the contract.
	if blockHeight+renewWindow >= contract.EndHeight {
		if u.GoodForUpload {
			c.log.Println("Marking contract as not good for upload because it is time to renew the contract", contract.ID)
		}
		if !u.GoodForRenew {
			c.log.Println("Marking contract as being good for renew:", contract.ID)
		}
		u.GoodForUpload = false
		u.GoodForRenew = true
		return u, true
	}
	return u, false
}

// sufficientFundsCheck checks if there are enough funds left in the contract
// for uploads.
// Returns true if a check fails and the utility returned must be used to update
// the contract state.
func (c *Contractor) sufficientFundsCheck(contract modules.RenterContract, host modules.HostDBEntry, period types.BlockHeight) (modules.ContractUtility, bool) {
	u := contract.Utility

	// Contract should not be used for uploading if the contract does
	// not have enough money remaining to perform the upload.
	blockBytes := types.NewCurrency64(modules.SectorSize * uint64(period))
	sectorStoragePrice := host.StoragePrice.Mul(blockBytes)
	sectorUploadBandwidthPrice := host.UploadBandwidthPrice.Mul64(modules.SectorSize)
	sectorDownloadBandwidthPrice := host.DownloadBandwidthPrice.Mul64(modules.SectorSize)
	sectorBandwidthPrice := sectorUploadBandwidthPrice.Add(sectorDownloadBandwidthPrice)
	sectorPrice := sectorStoragePrice.Add(sectorBandwidthPrice)
	percentRemaining, _ := big.NewRat(0, 1).SetFrac(contract.RenterFunds.Big(), contract.TotalCost.Big()).Float64()
	if contract.RenterFunds.Cmp(sectorPrice.Mul64(3)) < 0 || percentRemaining < MinContractFundUploadThreshold {
		if u.GoodForUpload {
			c.log.Printf("Marking contract as not good for upload because of insufficient funds: %v vs. %v - %v", contract.RenterFunds.Cmp(sectorPrice.Mul64(3)) < 0, percentRemaining, contract.ID)
		}
		if !u.GoodForRenew {
			c.log.Println("Marking contract as being good for renew:", contract.ID)
		}
		u.GoodForUpload = false
		u.GoodForRenew = true
		return u, true
	}
	return u, false
}

// outOfStorageCheck checks if the host is running out of storage.
// Returns true if a check fails and the utility returned must be used to update
// the contract state.
func (c *Contractor) outOfStorageCheck(contract modules.RenterContract, blockHeight types.BlockHeight) (modules.ContractUtility, bool) {
	u := contract.Utility
	// Contract should not be used for uploading if the host is out of storage.
	if blockHeight-u.LastOOSErr <= oosRetryInterval {
		if u.GoodForUpload {
			c.log.Println("Marking contract as not being good for upload due to the host running out of storage:", contract.ID)
		}
		if !u.GoodForRenew {
			c.log.Println("Marking contract as being good for renew:", contract.ID)
		}
		u.GoodForUpload = false
		u.GoodForRenew = true
		return u, true
	}
	return u, false
}
