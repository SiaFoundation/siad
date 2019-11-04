package contractor

import (
	"gitlab.com/NebulousLabs/errors"
)

// managedMarkContractsUtility checks every active contract in the contractor and
// figures out whether the contract is useful for uploading, and whether the
// contract should be renewed.
func (c *Contractor) managedMarkContractsUtility() error {
	err, minScoreGFR, minScoreGFU := c.managedFindMinAllowedHostScores()
	if err != nil {
		return err
	}

	// Update utility fields for each contract.
	for _, contract := range c.staticContracts.ViewAll() {
		u := contract.Utility

		// If the utility is locked, do nothing.
		if u.Locked {
			continue
		}

		// Get host from hostdb and check that it's not filtered.
		host, u, needsUpdate := c.hostInHostDBCheck(contract)
		if needsUpdate {
			if err = c.managedUpdateContractUtility(contract.ID, u); err != nil {
				return errors.AddContext(err, "unable to update utility after hostdb check")
			}
			continue
		}

		// Do critical contract checks and update the utility if any checks fail.
		u, needsUpdate = c.criticalUtilityChecks(contract, host)
		if needsUpdate {
			err = c.managedUpdateContractUtility(contract.ID, u)
			if err != nil {
				return errors.AddContext(err, "unable to update utility after criticalUtilityChecks")
			}
			continue
		}

		sb, err := c.hdb.ScoreBreakdown(host)
		if err != nil {
			return err
		}

		// Check the host scorebreakdown against the minimum accepted scores.
		u, utilityUpdateStatus := c.checkHostScore(contract, sb, minScoreGFR, minScoreGFU)
		switch utilityUpdateStatus {
		case noUpdate:

		case suggestedUtilityUpdate:

		case necessaryUtilityUpdate:
			// Apply changes.
			err = c.managedUpdateContractUtility(contract.ID, u)
			if err != nil {
				return errors.AddContext(err, "unable to update utility after checkHostScore")
			}
			continue

		default:
			c.log.Critical("Undefined checkHostScore utilityUpdateStatus", utilityUpdateStatus, contract.ID)
		}

		// All checks passed, marking contract as GFU and GFR.
		if !u.GoodForUpload || !u.GoodForRenew {
			c.log.Println("Marking contract as being both GoodForUpload and GoodForRenew", u.GoodForUpload, u.GoodForRenew, contract.ID)
		}
		u.GoodForUpload = true
		u.GoodForRenew = true
		// Apply changes.
		err = c.managedUpdateContractUtility(contract.ID, u)
		if err != nil {
			return errors.AddContext(err, "unable to update utility after all checks passed.")
		}
	}
	return nil
}
