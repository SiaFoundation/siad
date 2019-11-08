package contractor

import (
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

// contractScoreAndUtil combines a contract with its host's score and an updated
// utility.
type contractScoreAndUtil struct {
	contract modules.RenterContract
	score    types.Currency
	util     modules.ContractUtility
}

// churnLimiter keeps track of the aggregate number of bytes stored in contracts
// marked !GFR (AKA churned contracts) in the current period.
type churnLimiter struct {
	// remainingChurnBudget is the number of bytes that the churnLimiter will
	// allow to be churned in contracts at the present moment. Note that this
	// value may be negative.
	remainingChurnBudget int

	// aggregateCurrentPeriodChurn is the aggregate size of files stored in contracts
	// churned in the current period.
	aggregateCurrentPeriodChurn uint64

	// maxPeriodChurn is the maximum amount of churn allowed in a single
	// period.
	maxPeriodChurn uint64

	mu         sync.Mutex
	contractor *Contractor
}

// churnLimiterPersist is the persisted state of a churnLimiter.
type churnLimiterPersist struct {
	AggregateCurrentPeriodChurn uint64 `json:"aggregatecurrentperiodchurn"`
	RemainingChurnBudget        int    `json:"remainingchurnbudget"`
	MaxPeriodChurn              uint64 `json:"maxperiodchurn"`
}

// persistData returns the churnLimiterPersist corresponding to this
// churnLimiter's state
func (cl *churnLimiter) persistData() churnLimiterPersist {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return churnLimiterPersist{cl.aggregateCurrentPeriodChurn, cl.remainingChurnBudget, cl.maxPeriodChurn}
}

// newChurnLimiterFromPersist creates a new churnLimiter using persisted state.
func newChurnLimiterFromPersist(contractor *Contractor, persistData churnLimiterPersist) *churnLimiter {
	// If given an empty persist, initialize with the deafult max churn.
	maxChurn := persistData.MaxPeriodChurn
	if maxChurn == 0 {
		maxChurn = modules.DefaultAllowance.MaxPeriodChurn
	}

	return &churnLimiter{
		contractor:                  contractor,
		aggregateCurrentPeriodChurn: persistData.AggregateCurrentPeriodChurn,
		remainingChurnBudget:        persistData.RemainingChurnBudget,
		maxPeriodChurn:              maxChurn,
	}
}

// newChurnLimiter returns a new churnLimiter.
func newChurnLimiter(contractor *Contractor) *churnLimiter {
	return &churnLimiter{contractor: contractor, maxPeriodChurn: modules.DefaultAllowance.MaxPeriodChurn}
}

// ChurnStatus returns the current period's aggregate churn and the max churn
// per period.
func (c *Contractor) ChurnStatus() modules.ContractorChurnStatus {
	aggregateChurn, maxChurn := c.staticChurnLimiter.managedAggregateAndMaxChurn()
	return modules.ContractorChurnStatus{
		AggregateCurrentPeriodChurn: aggregateChurn,
		MaxPeriodChurn:              maxChurn,
	}
}

// callResetAggregateChurn resets the aggregate churn for this period. This
// method must be called at the beginning of every new period.
func (cl *churnLimiter) callResetAggregateChurn() {
	cl.mu.Lock()
	cl.contractor.log.Println("Aggregate Churn for last period: ", cl.aggregateCurrentPeriodChurn)
	cl.aggregateCurrentPeriodChurn = 0
	cl.mu.Unlock()
}

// callNotifyChurnedContract adds the size of this contract's files to the aggregate
// churn in this period. Must be called when contracts are marked !GFR.
func (cl *churnLimiter) callNotifyChurnedContract(contract modules.RenterContract) {
	size := contract.Transaction.FileContractRevisions[0].NewFileSize
	if size == 0 {
		return
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()

	cl.aggregateCurrentPeriodChurn += size
	cl.remainingChurnBudget -= int(size)
	cl.contractor.log.Debugf("Increasing aggregate churn by %d to %d (MaxPeriodChurn: %d)", size, cl.aggregateCurrentPeriodChurn, cl.maxPeriodChurn)
	cl.contractor.log.Debugf("Remaining churn budget: %d", cl.remainingChurnBudget)
}

// callBumpChurnBudget increases the churn budget by a fraction of the max churn
// budget per period. Used when new blocks are processed.
func (cl *churnLimiter) callBumpChurnBudget(numBlocksAdded int, period types.BlockHeight) {
	// Don't add to churn budget when there is no period, since no allowance is
	// set yet.
	if period == types.BlockHeight(0) {
		return
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Increase churn budget as a multiple of the period budget per block. This
	// let's the remainingChurnBudget increase more quickly.
	budgetIncrease := numBlocksAdded * int(cl.maxPeriodChurn/uint64(period))
	cl.remainingChurnBudget += budgetIncrease
	if cl.remainingChurnBudget > cl.maxChurnBudget() {
		cl.remainingChurnBudget = cl.maxChurnBudget()
	}
	cl.contractor.log.Debugf("Updated churn budget: %d", cl.remainingChurnBudget)
}

// callSetMaxPeriodChurn sets the max churn per period.
func (cl *churnLimiter) callSetMaxPeriodChurn(newMax uint64) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.maxPeriodChurn = newMax
}

// maxChurnBudget returns the max allowed value for remainingChurnBudget.
func (cl *churnLimiter) maxChurnBudget() int {
	// Do not let churn budget to build up to maxPeriodChurn to avoid using entire
	// period budget at once (except in special circumstances).
	return int(cl.maxPeriodChurn / 2)
}

// managedProcessSuggestedUpdates processes suggested utility updates. It prevents
// contracts from being marked as !GFR if the churn limit has been reached. The
// inputs are assumed to be contracts that have passed all critical utility
// checks.
func (cl *churnLimiter) managedProcessSuggestedUpdates(queue []contractScoreAndUtil) error {
	sort.Slice(queue, func(i, j int) bool {
		return queue[i].score.Cmp(queue[j].score) < 0
	})

	var queuedContract contractScoreAndUtil
	for len(queue) > 0 {
		queuedContract, queue = queue[0], queue[1:]

		// Churn a contract if it went from GFR in the previous util
		// (queuedContract.contract.Utility) to !GFR in the suggested util
		// (queuedContract.util) and the churnLimit has not been reached.
		turnedNotGFR := queuedContract.contract.Utility.GoodForRenew && !queuedContract.util.GoodForRenew
		churningThisContract := turnedNotGFR && cl.managedCanChurnContract(queuedContract.contract)
		if turnedNotGFR && !churningThisContract {
			cl.contractor.log.Debugln("Avoiding churn on contract: ", queuedContract.contract.ID)
			currentBudget, periodBudget := cl.managedChurnBudget()
			cl.contractor.log.Debugf("Remaining Churn Budget: %d. Remaining Period Budget: %d", currentBudget, periodBudget)
			queuedContract.util.GoodForRenew = true
		}

		if churningThisContract {
			cl.contractor.log.Println("Churning contract for bad score: ", queuedContract.contract.ID, queuedContract.score)
		}

		// Apply changes.
		err := cl.contractor.managedAcquireAndUpdateContractUtility(queuedContract.contract.ID, queuedContract.util)
		if err != nil {
			return err
		}
	}
	return nil
}

// managedChurnBudget returns the current remaining churn budget, and the remaining
// budget for the period.
func (cl *churnLimiter) managedChurnBudget() (int, int) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.remainingChurnBudget, int(cl.maxPeriodChurn) - int(cl.aggregateCurrentPeriodChurn)
}

// managedAggregateAndMaxChurn returns the aggregate churn for the current period,
// and the maximum churn allowed per period.
func (cl *churnLimiter) managedAggregateAndMaxChurn() (uint64, uint64) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.aggregateCurrentPeriodChurn, cl.maxPeriodChurn
}

// managedCanChurnContract returns true if and only if the churnLimiter can
// churn the contract right now, given its current budget.
func (cl *churnLimiter) managedCanChurnContract(contract modules.RenterContract) bool {
	size := contract.Transaction.FileContractRevisions[0].NewFileSize
	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Allow any size contract to be churned if the current budget is the max
	// budget. This allows large contracts to be churned if there is enough budget
	// remaining for the period, even if the contract is larger than the
	// maxChurnBudget.
	fitsInCurrentBudget := (cl.remainingChurnBudget-int(size) >= 0) || (cl.remainingChurnBudget == cl.maxChurnBudget())
	fitsInPeriodBudget := (int(cl.maxPeriodChurn) - int(cl.aggregateCurrentPeriodChurn) - int(size)) >= 0

	// If there has been no churn in this period, allow any size contract to be
	// churned.
	fitsInPeriodBudget = fitsInPeriodBudget || (cl.aggregateCurrentPeriodChurn == 0)

	return fitsInPeriodBudget && fitsInCurrentBudget
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
			if err = c.managedAcquireAndUpdateContractUtility(contract.ID, u); err != nil {
				return errors.AddContext(err, "unable to update utility after hostdb check")
			}
			continue
		}

		// Do critical contract checks and update the utility if any checks fail.
		u, needsUpdate = c.criticalUtilityChecks(contract, host)
		if needsUpdate {
			err = c.managedAcquireAndUpdateContractUtility(contract.ID, u)
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
			suggestedUpdateQueue = append(suggestedUpdateQueue, contractScoreAndUtil{contract, sb.Score, u})
			continue

		case necessaryUtilityUpdate:
			// Apply changes.
			err = c.managedAcquireAndUpdateContractUtility(contract.ID, u)
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
		err = c.managedAcquireAndUpdateContractUtility(contract.ID, u)
		if err != nil {
			return errors.AddContext(err, "unable to update utility after all checks passed.")
		}
	}

	// Process the suggested updates throught the churn limiter.
	err = c.staticChurnLimiter.managedProcessSuggestedUpdates(suggestedUpdateQueue)
	if err != nil {
		return errors.AddContext(err, "churnLimiter processSuggestedUpdates err")
	}

	return nil
}
