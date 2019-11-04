package contractor

import (
	"sort"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// DefaultMaxChurnPerPeriod is the default max churn allowed in a periood
	DefaultMaxChurnPerPeriod = 1 << 39 // 256 GiB

	// maxChurnBudget is the largest allowed churn budget.
	maxChurnBudget = 1 << 36 // 32 GiB

	// churnBudgetEarnedPerBlock is the amount of churn budget earned for each new
	// connected block.
	churnBudgetEarnedPerBlock = 1 << 33 // 4 GiB
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
	// allow to be churned in contracts at the present moment.
	remainingChurnBudget int

	// aggregateChurnThisPeriod is the aggregate size of files stored in contracts
	// churned in the current period.
	aggregateChurnThisPeriod uint64

	// maxChurnPerPeriod is the maximum amount of churn allowed in a single
	// period.
	maxChurnPerPeriod uint64

	mu         sync.Mutex
	contractor *Contractor
}

// churnLimiterPersist is the persisted state of a churnLimiter.
type churnLimiterPersist struct {
	AggregateChurnThisPeriod uint64 `json:"aggregatechurnthisperiod"`
	RemainingChurnBudget     int    `json:"remainingchurnbudget"`
	MaxChurnPerPeriod        uint64 `json:"maxchurnperperiod"`
}

// persistData returns the churnLimiterPersist corresponding to this
// churnLimiter's state
func (cl *churnLimiter) persistData() churnLimiterPersist {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return churnLimiterPersist{cl.aggregateChurnThisPeriod, cl.remainingChurnBudget, cl.maxChurnPerPeriod}
}

// newChurnLimiterFromPersist creates a new churnLimiter using persisted state.
func newChurnLimiterFromPersist(contractor *Contractor, persistData churnLimiterPersist) *churnLimiter {
	return &churnLimiter{
		contractor:               contractor,
		aggregateChurnThisPeriod: persistData.AggregateChurnThisPeriod,
		remainingChurnBudget:     persistData.RemainingChurnBudget,
		maxChurnPerPeriod:        persistData.MaxChurnPerPeriod,
	}
}

// newChurnLimiter returns a new churnLimiter.
func newChurnLimiter(contractor *Contractor) *churnLimiter {
	return &churnLimiter{contractor: contractor, maxChurnPerPeriod: DefaultMaxChurnPerPeriod}
}

// managedChurnBudget returns the current remaining churn budget, and the remaining
// budget for the period.
func (cl *churnLimiter) managedChurnBudget() (int, int) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.remainingChurnBudget, int(cl.maxChurnPerPeriod) - int(cl.aggregateChurnThisPeriod)
}

// callMaxChurnPerPeriod returns the current max churn per period.
func (cl *churnLimiter) callMaxChurnPerPeriod() uint64 {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.maxChurnPerPeriod
}

// ChurnStatus returns the current period's aggregate churn and the max churn
// per period.
func (c *Contractor) ChurnStatus() modules.ContractorChurnStatus {
	c.staticChurnLimiter.mu.Lock()
	defer c.staticChurnLimiter.mu.Unlock()
	return modules.ContractorChurnStatus{
		AggregateChurnThisPeriod: c.staticChurnLimiter.aggregateChurnThisPeriod,
		MaxChurnPerPeriod:        c.staticChurnLimiter.maxChurnPerPeriod,
	}
}

// callAggregateChurnInPeriod returns the aggregate churn for the current period.
func (cl *churnLimiter) callAggregateChurnInPeriod() uint64 {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.aggregateChurnThisPeriod
}

// callResetAggregateChurn resets the aggregate churn for this period. This
// method must be called at the beginning of every new period.
func (cl *churnLimiter) callResetAggregateChurn() {
	cl.mu.Lock()
	cl.contractor.log.Println("Aggregate Churn for last period: ", cl.aggregateChurnThisPeriod)
	cl.aggregateChurnThisPeriod = 0
	cl.mu.Unlock()
}

// callNotifyChurnedContract adds the size of this contract's files to the aggregate
// churn in this period. Must be called when contracts are marked !GFR.
func (cl *churnLimiter) callNotifyChurnedContract(contract modules.RenterContract) {
	size := contract.Transaction.FileContractRevisions[0].NewFileSize
	cl.mu.Lock()
	defer cl.mu.Unlock()

	cl.aggregateChurnThisPeriod += size
	cl.remainingChurnBudget -= int(size)
	cl.contractor.log.Debugf("Increasing aggregate churn by %d to %d (MaxChurnPerPeriod: %d)", size, cl.aggregateChurnThisPeriod, cl.maxChurnPerPeriod)
	cl.contractor.log.Debugf("Remaining churn budget: %d", cl.remainingChurnBudget)
}

// callCanChurnAmount returns true if and only if the churnLimiter can allow the
// the churn of `amount` bytes right now.
func (cl *churnLimiter) callCanChurnContract(contract modules.RenterContract) bool {
	size := contract.Transaction.FileContractRevisions[0].NewFileSize
	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Allow any size contract to be churned if the current budget is the max
	// budget. This allows large contracts to be churned if there is enough budget
	// remaining for the period, even if the contract is larger than the
	// maxChurnBudget.
	fitsInCurrentBudget := (cl.remainingChurnBudget-int(size) >= 0) || (cl.remainingChurnBudget == maxChurnBudget)
	fitsInPeriodBudget := (int(cl.maxChurnPerPeriod) - int(cl.aggregateChurnThisPeriod) - int(size)) >= 0

	return fitsInPeriodBudget && fitsInCurrentBudget
}

// callAdjustChurnBudget adjusts the churn budget. Used when new blocks are
// processed.
func (cl *churnLimiter) callAdjustChurnBudget(adjustment int) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	cl.remainingChurnBudget += adjustment
	if cl.remainingChurnBudget > maxChurnBudget {
		cl.remainingChurnBudget = maxChurnBudget
	}
	cl.contractor.log.Debugf("Updated churn budget: %d", cl.remainingChurnBudget)
}

// callSetMaxChurnPerPeriod sets the max churn per period.
func (cl *churnLimiter) callSetMaxChurnPerPeriod(newMax uint64) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.maxChurnPerPeriod = newMax
}

// SetMaxChurnPerPeriod sets the max churn per period.
func (c *Contractor) SetMaxChurnPerPeriod(newMax uint64) {
	c.staticChurnLimiter.callSetMaxChurnPerPeriod(newMax)
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
		return queue[i].score.Cmp(queue[i].score) < 0
	})

	var queuedContract contractScoreAndUtil
	for len(queue) > 0 {
		queuedContract, queue = queue[0], queue[1:]

		// Churn a contract if it went from GFR to !GFR, and the churnLimit has not
		// been reached.
		turnedNotGFR := queuedContract.contract.Utility.GoodForRenew && !queuedContract.util.GoodForRenew
		churningThisContract := turnedNotGFR && !cl.callCanChurnContract(queuedContract.contract)
		if turnedNotGFR && !churningThisContract {
			cl.contractor.log.Debugln("Avoiding churn on contract: ", queuedContract.contract.ID)
			currentBudget, periodBudget := cl.managedChurnBudget()
			cl.contractor.log.Debugf("Remaining Churn Budget: %d. Remaining Period Budget: %d", currentBudget, periodBudget)
			queuedContract.util.GoodForRenew = true
		}

		// Apply changes.
		err := cl.contractor.managedAcquireAndUpdateContractUtility(queuedContract.contract.ID, queuedContract.util)
		if err != nil {
			return err
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
	err = c.staticChurnLimiter.callProcessSuggestedUpdates(suggestedUpdateQueue)
	if err != nil {
		return errors.AddContext(err, "churnLimiter processSuggestedUpdates err")
	}

	return nil
}
