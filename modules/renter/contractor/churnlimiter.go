package contractor

import (
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
)

const maxStorageChurnPerPeriod = 2 << 10

// contractScoreAndUtil combines a contract with its host's score and an updated
// utility.
type contractScoreAndUtil struct {
	modules.RenterContract
	sb   modules.HostScoreBreakdown
	util modules.ContractUtility
}

// churnLimiter keeps track of the aggregate number of bytes stored in contracts
// marked !GFR (AKA churned contracts) in the current period. It also
type churnLimiter struct {
	mu         sync.Mutex
	contractor *Contractor

	// aggregateChurnThisPeriod is the aggregate size of files stored under
	// contract churned in the current period.
	aggregateChurnThisPeriod uint64
}

// newChurnLimiter returns a new churnLimiter.
func newChurnLimiter() *churnLimiter {
	return &churnLimiter{}
}

// callResetAggregateChurn resets the aggregate churn for this period. Should be
// called at the beginning of every new period.
func (cl *churnLimiter) callResetAggregateChurn() {
	cl.mu.Lock()
	cl.contractor.log.Println("Aggregate Churn for last period: ", cl.aggregateChurnThisPeriod)
	cl.aggregateChurnThisPeriod = 0
	cl.mu.Unlock()
}

// callNotifyChurnedContract adds the size of this contract's files to the aggregate
// churn in this period. Should be called when contracts are marked !GFR.
func (cl *churnLimiter) callNotifyChurnedContract(contract modules.RenterContract) {
	size := contract.Transaction.FileContractRevisions[0].NewFileSize
	cl.mu.Lock()
	cl.aggregateChurnThisPeriod += size
	cl.contractor.log.Debugf("Increasing aggregate churn by %d to %d", size, cl.aggregateChurnThisPeriod)
	cl.mu.Unlock()
}

// callReachedChurnLimit returns true if and only if the aggregate churn for
// this period exceeds the maximum allowed churn.
func (cl *churnLimiter) callReachedChurnLimit() bool {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.aggregateChurnThisPeriod >= maxStorageChurnPerPeriod
}

// callProcessSuggestedUpdates processes suggested utility updates. It prevents
// contracts from being marked as !GFR if the churn limit has been reached. The
// inputs are assumed to be contracts that have passed all critical utility
// checks.
func (cl *churnLimiter) callProcessSuggestedUpdates(queue []contractScoreAndUtil) error {
	if len(queue) == 0 {
		return nil
	}
	sort.Slice(queue, func(i, j int) bool {
		return queue[i].sb.Score.Cmp(queue[i].sb.Score) < 0
	})

	var contract contractScoreAndUtil
	for len(queue) > 0 {
		contract, queue = queue[0], queue[1:]

		// Mark contracts as GFR if the churn limit has been reached, and if it was
		// queued as !GFR
		churningThisContract := !contract.util.GoodForRenew && !cl.callReachedChurnLimit()
		if !churningThisContract {
			cl.contractor.log.Debugln("Avoiding churn on contract: ", contract.ID, cl.callReachedChurnLimit())
			contract.util.GoodForRenew = true
		}

		// Apply changes.
		err := cl.contractor.managedUpdateContractUtility(contract.ID, contract.util)
		if err != nil {
			return err
		}
		if churningThisContract {
			cl.callNotifyChurnedContract(contract.RenterContract)
		}
	}
	return nil
}

// managedMarkContractsUtility checks every active contract in the contractor and
// figures out whether the contract is useful for uploading, and whether the
// contract should be renewed.
func (c *Contractor) managedMarkContractsUtility() error {
	err, minScoreGFR, minScoreGFU := c.managedFindMinAllowedHostScores()
	if err != nil {
		return err
	}

	// Queue for possible contracts to churn. Passed to churnLimiter for final
	// judgment.
	suggestedUpdateQueue := make([]contractScoreAndUtil, 0)

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

		// suggestedUtilityUpdates are applied selectively by the churnLimiter.
		// These are contracts with acceptable, but not very good host scores.
		case suggestedUtilityUpdate:
			c.log.Debugln("Queueing utility update", contract.ID, sb.Score)
			suggestedUpdateQueue = append(suggestedUpdateQueue, contractScoreAndUtil{contract, sb, u})
			continue

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

	// Process the suggested updates throught the churn limiter.
	err = c.staticChurnLimiter.callProcessSuggestedUpdates(suggestedUpdateQueue)
	if err != nil {
		return errors.AddContext(err, "churnLimiter processSuggestedUpdates err")
	}

	return nil
}
