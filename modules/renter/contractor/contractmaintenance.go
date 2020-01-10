package contractor

// contractmaintenance.go handles forming and renewing contracts for the
// contractor. This includes deciding when new contracts need to be formed, when
// contracts need to be renewed, and if contracts need to be blacklisted.

import (
	"fmt"
	"math/big"
	"reflect"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// ErrInsufficientAllowance indicates that the renter's allowance is less
	// than the amount necessary to store at least one sector
	ErrInsufficientAllowance = errors.New("allowance is not large enough to cover fees of contract creation")
	errTooExpensive          = errors.New("host price was too high")
)

type (
	// fileContractRenewal is an instruction to renew a file contract.
	fileContractRenewal struct {
		id     types.FileContractID
		amount types.Currency
	}
)

// callNotifyDoubleSpend is used by the watchdog to alert the contractor
// whenever a monitored file contract input is double-spent. This function
// marks down the host score, and marks the contract as !GoodForRenew and
// !GoodForUpload.
func (c *Contractor) callNotifyDoubleSpend(fcID types.FileContractID, blockHeight types.BlockHeight) {
	c.log.Println("Watchdog found a double-spend: ", fcID, blockHeight)

	// Mark the contract as double-spent. This will cause the contract to be
	// excluded in period spending.
	c.mu.Lock()
	c.doubleSpentContracts[fcID] = blockHeight
	c.mu.Unlock()

	err := c.MarkContractBad(fcID)
	if err != nil {
		c.log.Println("callNotifyDoubleSpend error in MarkContractBad", err)
	}
}

// managedCheckForDuplicates checks for static contracts that have the same host
// key and moves the older one to old contracts.
func (c *Contractor) managedCheckForDuplicates() {
	// Build map for comparison.
	pubkeys := make(map[string]types.FileContractID)
	var newContract, oldContract modules.RenterContract
	for _, contract := range c.staticContracts.ViewAll() {
		id, exists := pubkeys[contract.HostPublicKey.String()]
		if !exists {
			pubkeys[contract.HostPublicKey.String()] = contract.ID
			continue
		}

		// Duplicate contract found, determine older contract to delete.
		if rc, ok := c.staticContracts.View(id); ok {
			if rc.StartHeight >= contract.StartHeight {
				newContract, oldContract = rc, contract
			} else {
				newContract, oldContract = contract, rc
			}
			c.log.Printf("Duplicate contract found. New contract is %x and old contract is %v", newContract.ID, oldContract.ID)

			// Get SafeContract
			oldSC, ok := c.staticContracts.Acquire(oldContract.ID)
			if !ok {
				// Update map
				pubkeys[contract.HostPublicKey.String()] = newContract.ID
				continue
			}

			// Link the contracts to each other and then store the old contract
			// in the record of historic contracts.
			c.mu.Lock()
			c.renewedFrom[newContract.ID] = oldContract.ID
			c.renewedTo[oldContract.ID] = newContract.ID
			c.oldContracts[oldContract.ID] = oldSC.Metadata()
			c.pubKeysToContractID[string(newContract.HostPublicKey.Key)] = newContract.ID

			// Save the contractor and delete the contract.
			//
			// TODO: Ideally these two things would happen atomically, but I'm
			// not completely certain that's feasible with our current
			// architecture.
			err := c.save()
			if err != nil {
				c.log.Println("Failed to save the contractor after updating renewed maps.")
			}
			c.mu.Unlock()
			c.staticContracts.Delete(oldSC)

			// Update the pubkeys map to contain the newest contract id.
			//
			// TODO: This means that if there are multiple duplicates, say 3
			// contracts that all share the same host, then the ordering may not
			// be perfect. If in reality the renewal order was A<->B<->C, it's
			// possible for the contractor to end up with A->C and B<->C in the
			// mapping.
			pubkeys[contract.HostPublicKey.String()] = newContract.ID
		}
	}
}

// managedEstimateRenewFundingRequirements estimates the amount of money that a
// contract is going to need in the next billing cycle by looking at how much
// storage is in the contract and what the historic usage pattern of the
// contract has been.
func (c *Contractor) managedEstimateRenewFundingRequirements(contract modules.RenterContract, blockHeight types.BlockHeight, allowance modules.Allowance) (types.Currency, error) {
	// Fetch the host pricing to use in the estimate.
	host, exists, err := c.hdb.Host(contract.HostPublicKey)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "error getting host from hostdb:")
	}
	if !exists {
		return types.ZeroCurrency, errors.New("could not find host in hostdb")
	}
	if host.Filtered {
		return types.ZeroCurrency, errors.New("host is blacklisted")
	}

	// Estimate the amount of money that's going to be needed for existing
	// storage.
	dataStored := contract.Transaction.FileContractRevisions[0].NewFileSize
	maintenanceCost := types.NewCurrency64(dataStored).Mul64(uint64(allowance.Period)).Mul(host.StoragePrice)

	// For the upload and download estimates, we're going to need to know the
	// amount of money that was spent on upload and download by this contract
	// line in this period. That's going to require iterating over the renew
	// history of the contract to get all the spending across any refreshes that
	// occurred this period.
	prevUploadSpending := contract.UploadSpending
	prevDownloadSpending := contract.DownloadSpending
	c.mu.Lock()
	currentID := contract.ID
	for i := 0; i < 10e3; i++ { // prevent an infinite loop if there's an [impossible] contract cycle
		// If there is no previous contract, nothing to do.
		var exists bool
		currentID, exists = c.renewedFrom[currentID]
		if !exists {
			break
		}

		// If the contract is not in oldContracts, that's probably a bug, but
		// nothing to do otherwise.
		currentContract, exists := c.oldContracts[currentID]
		if !exists {
			c.log.Println("WARN: A known previous contract is not found in c.oldContracts")
			break
		}

		// If the contract did not start in the current period, then it is not
		// relevant, and none of the previous contracts will be relevant either.
		if currentContract.StartHeight < c.currentPeriod {
			break
		}

		// Add the upload and download spending.
		prevUploadSpending = prevUploadSpending.Add(currentContract.UploadSpending)
		prevDownloadSpending = prevDownloadSpending.Add(currentContract.DownloadSpending)
	}
	c.mu.Unlock()

	// Estimate the amount of money that's going to be needed for new storage
	// based on the amount of new storage added in the previous period. Account
	// for both the storage price as well as the upload price.
	prevUploadDataEstimate := prevUploadSpending
	if !host.UploadBandwidthPrice.IsZero() {
		// TODO: Because the host upload bandwidth price can change, this is not
		// the best way to estimate the amount of data that was uploaded to this
		// contract. Better would be to look at the amount of data stored in the
		// contract from the previous cycle and use that to determine how much
		// total data.
		prevUploadDataEstimate = prevUploadDataEstimate.Div(host.UploadBandwidthPrice)
	}
	// Sanity check - the host may have changed prices, make sure we aren't
	// assuming an unreasonable amount of data.
	if types.NewCurrency64(dataStored).Cmp(prevUploadDataEstimate) < 0 {
		prevUploadDataEstimate = types.NewCurrency64(dataStored)
	}
	// The estimated cost for new upload spending is the previous upload
	// bandwidth plus the implied storage cost for all of the new data.
	newUploadsCost := prevUploadSpending.Add(prevUploadDataEstimate.Mul64(uint64(allowance.Period)).Mul(host.StoragePrice))

	// The download cost is assumed to be the same. Even if the user is
	// uploading more data, the expectation is that the download amounts will be
	// relatively constant. Add in the contract price as well.
	newDownloadsCost := prevDownloadSpending
	contractPrice := host.ContractPrice

	// Aggregate all estimates so far to compute the estimated siafunds fees.
	// The transaction fees are not included in the siafunds estimate because
	// users are not charged siafund fees on money that doesn't go into the file
	// contract (and the transaction fee goes to the miners, not the file
	// contract).
	beforeSiafundFeesEstimate := maintenanceCost.Add(newUploadsCost).Add(newDownloadsCost).Add(contractPrice)
	afterSiafundFeesEstimate := types.Tax(blockHeight, beforeSiafundFeesEstimate).Add(beforeSiafundFeesEstimate)

	// Get an estimate for how much money we will be charged before going into
	// the transaction pool.
	_, maxTxnFee := c.tpool.FeeEstimation()
	txnFees := maxTxnFee.Mul64(modules.EstimatedFileContractTransactionSetSize)

	// Add them all up and then return the estimate plus 33% for error margin
	// and just general volatility of usage pattern.
	estimatedCost := afterSiafundFeesEstimate.Add(txnFees)
	estimatedCost = estimatedCost.Add(estimatedCost.Div64(3))

	// Check for a sane minimum. The contractor should not be forming contracts
	// with less than 'fileContractMinimumFunding / (num contracts)' of the
	// value of the allowance.
	minimum := allowance.Funds.MulFloat(fileContractMinimumFunding).Div64(allowance.Hosts)
	if estimatedCost.Cmp(minimum) < 0 {
		estimatedCost = minimum
	}
	return estimatedCost, nil
}

// callInterruptContractMaintenance will issue an interrupt signal to any
// running maintenance, stopping that maintenance. If there are multiple threads
// running maintenance, they will all be stopped.
func (c *Contractor) callInterruptContractMaintenance() {
	// Spin up a thread to grab the maintenance lock. Signal that the lock was
	// acquired after the lock is acquired.
	gotLock := make(chan struct{})
	go func() {
		c.maintenanceLock.Lock()
		close(gotLock)
		c.maintenanceLock.Unlock()
	}()

	// There may be multiple threads contending for the maintenance lock. Issue
	// interrupts repeatedly until we get a signal that the maintenance lock has
	// been acquired.
	for {
		select {
		case <-gotLock:
			return
		case c.interruptMaintenance <- struct{}{}:
			c.log.Debugln("Signal sent to interrupt contract maintenance")
		}
	}
}

// managedFindMinAllowedHostScores uses a set of random hosts from the hostdb to
// calculate minimum acceptable score for a host to be marked GFR and GFU.
func (c *Contractor) managedFindMinAllowedHostScores() (error, types.Currency, types.Currency) {
	// Pull a new set of hosts from the hostdb that could be used as a new set
	// to match the allowance. The lowest scoring host of these new hosts will
	// be used as a baseline for determining whether our existing contracts are
	// worthwhile.
	c.mu.RLock()
	hostCount := int(c.allowance.Hosts)
	c.mu.RUnlock()
	hosts, err := c.hdb.RandomHosts(hostCount+randomHostsBufferForScore, nil, nil)
	if err != nil {
		return err, types.Currency{}, types.Currency{}
	}

	if len(hosts) == 0 {
		return errors.New("No hosts returned in RandomHosts"), types.Currency{}, types.Currency{}
	}

	// Find the minimum score that a host is allowed to have to be considered
	// good for upload.
	var minScoreGFR, minScoreGFU types.Currency
	sb, err := c.hdb.ScoreBreakdown(hosts[0])
	if err != nil {
		return err, types.Currency{}, types.Currency{}
	}

	lowestScore := sb.Score
	for i := 1; i < len(hosts); i++ {
		score, err := c.hdb.ScoreBreakdown(hosts[i])
		if err != nil {
			return err, types.Currency{}, types.Currency{}
		}
		if score.Score.Cmp(lowestScore) < 0 {
			lowestScore = score.Score
		}
	}
	// Set the minimum acceptable score to a factor of the lowest score.
	minScoreGFR = lowestScore.Div(scoreLeewayGoodForRenew)
	minScoreGFU = lowestScore.Div(scoreLeewayGoodForUpload)

	// Set min score to the max score seen times 2.
	if c.staticDeps.Disrupt("HighMinHostScore") {
		var maxScore types.Currency
		for i := 1; i < len(hosts); i++ {
			score, err := c.hdb.ScoreBreakdown(hosts[i])
			if err != nil {
				return err, types.Currency{}, types.Currency{}
			}
			if score.Score.Cmp(maxScore) > 0 {
				maxScore = score.Score
			}
		}
		minScoreGFR = maxScore.Mul64(2)
	}

	return nil, minScoreGFR, minScoreGFU
}

// managedNewContract negotiates an initial file contract with the specified
// host, saves it, and returns it.
func (c *Contractor) managedNewContract(host modules.HostDBEntry, contractFunding types.Currency, endHeight types.BlockHeight) (types.Currency, modules.RenterContract, error) {
	// reject hosts that are too expensive
	if host.StoragePrice.Cmp(maxStoragePrice) > 0 {
		return types.ZeroCurrency, modules.RenterContract{}, errTooExpensive
	}
	// Determine if host settings align with allowance period
	c.mu.Lock()
	if reflect.DeepEqual(c.allowance, modules.Allowance{}) {
		c.mu.Unlock()
		return types.ZeroCurrency, modules.RenterContract{}, errors.New("called managedNewContract but allowance wasn't set")
	}
	allowance := c.allowance
	hostSettings := host.HostExternalSettings
	period := c.allowance.Period
	c.mu.Unlock()

	if host.MaxDuration < period {
		err := errors.New("unable to form contract with host due to insufficient MaxDuration of host")
		return types.ZeroCurrency, modules.RenterContract{}, err
	}
	// cap host.MaxCollateral
	if host.MaxCollateral.Cmp(maxCollateral) > 0 {
		host.MaxCollateral = maxCollateral
	}

	// Check for price gouging.
	err := checkFormContractGouging(allowance, hostSettings)
	if err != nil {
		return types.ZeroCurrency, modules.RenterContract{}, errors.AddContext(err, "unable to form a contract due to price gouging detection")
	}

	// get an address to use for negotiation
	uc, err := c.wallet.NextAddress()
	if err != nil {
		return types.ZeroCurrency, modules.RenterContract{}, err
	}

	// get the wallet seed.
	seed, _, err := c.wallet.PrimarySeed()
	if err != nil {
		return types.ZeroCurrency, modules.RenterContract{}, err
	}
	// derive the renter seed and wipe it once we are done with it.
	renterSeed := proto.DeriveRenterSeed(seed)
	defer fastrand.Read(renterSeed[:])

	// create contract params
	c.mu.RLock()
	params := proto.ContractParams{
		Allowance:     c.allowance,
		Host:          host,
		Funding:       contractFunding,
		StartHeight:   c.blockHeight,
		EndHeight:     endHeight,
		RefundAddress: uc.UnlockHash(),
		RenterSeed:    renterSeed.EphemeralRenterSeed(endHeight),
	}
	c.mu.RUnlock()

	// wipe the renter seed once we are done using it.
	defer fastrand.Read(params.RenterSeed[:])

	// create transaction builder and trigger contract formation.
	txnBuilder, err := c.wallet.StartTransaction()
	if err != nil {
		return types.ZeroCurrency, modules.RenterContract{}, err
	}

	contract, formationTxnSet, sweepTxn, sweepParents, err := c.staticContracts.FormContract(params, txnBuilder, c.tpool, c.hdb, c.tg.StopChan())
	if err != nil {
		txnBuilder.Drop()
		return types.ZeroCurrency, modules.RenterContract{}, err
	}

	monitorContractArgs := monitorContractArgs{
		false,
		contract.ID,
		contract.Transaction,
		formationTxnSet,
		sweepTxn,
		sweepParents,
		params.StartHeight,
	}
	err = c.staticWatchdog.callMonitorContract(monitorContractArgs)
	if err != nil {
		return types.ZeroCurrency, modules.RenterContract{}, err
	}

	// Add a mapping from the contract's id to the public key of the host.
	c.mu.Lock()
	_, exists := c.pubKeysToContractID[contract.HostPublicKey.String()]
	if exists {
		c.mu.Unlock()
		txnBuilder.Drop()
		// We need to return a funding value because money was spent on this
		// host, even though the full process could not be completed.
		c.log.Println("WARN: Attempted to form a new contract with a host that we already have a contrat with.")
		return contractFunding, modules.RenterContract{}, fmt.Errorf("We already have a contract with host %v", contract.HostPublicKey)
	}
	c.pubKeysToContractID[contract.HostPublicKey.String()] = contract.ID
	c.mu.Unlock()

	contractValue := contract.RenterFunds
	c.log.Printf("Formed contract %v with %v for %v", contract.ID, host.NetAddress, contractValue.HumanString())
	return contractFunding, contract, nil
}

// managedPrunePubkeyMap will delete any pubkeys in the pubKeysToContractID map
// that no longer map to an active contract.
func (c *Contractor) managedPrunePubkeyMap() {
	allContracts := c.staticContracts.ViewAll()
	pks := make(map[string]struct{})
	for _, c := range allContracts {
		pks[c.HostPublicKey.String()] = struct{}{}
	}
	c.mu.Lock()
	for pk := range c.pubKeysToContractID {
		if _, exists := pks[pk]; !exists {
			delete(c.pubKeysToContractID, pk)
		}
	}
	c.mu.Unlock()
}

// managedPrunedRedundantAddressRange uses the hostdb to find hosts that
// violate the rules about address ranges and cancels them.
func (c *Contractor) managedPrunedRedundantAddressRange() {
	// Get all contracts which are not canceled.
	allContracts := c.staticContracts.ViewAll()
	var contracts []modules.RenterContract
	for _, contract := range allContracts {
		if contract.Utility.Locked && !contract.Utility.GoodForRenew && !contract.Utility.GoodForUpload {
			// contract is canceled
			continue
		}
		contracts = append(contracts, contract)
	}

	// Get all the public keys and map them to contract ids.
	pks := make([]types.SiaPublicKey, 0, len(allContracts))
	cids := make(map[string]types.FileContractID)
	for _, contract := range contracts {
		pks = append(pks, contract.HostPublicKey)
		cids[contract.HostPublicKey.String()] = contract.ID
	}

	// Let the hostdb filter out bad hosts and cancel contracts with those
	// hosts.
	badHosts, err := c.hdb.CheckForIPViolations(pks)
	if err != nil {
		c.log.Println("WARN: error checking for IP violations:", err)
		return
	}
	for _, host := range badHosts {
		if err := c.managedCancelContract(cids[host.String()]); err != nil {
			c.log.Print("WARNING: Wasn't able to cancel contract in managedPrunedRedundantAddressRange", err)
		}
	}
}

// checkFormContractGouging will check whether the pricing for forming
// this contract triggers any price gouging warnings.
func checkFormContractGouging(allowance modules.Allowance, hostSettings modules.HostExternalSettings) error {
	// Check whether the RPC base price is too high.
	if !allowance.MaxRPCPrice.IsZero() && allowance.MaxRPCPrice.Cmp(hostSettings.BaseRPCPrice) < 0 {
		return errors.New("rpc base price of host is too high - price gouging protection enabled")
	}
	// Check whether the form contract price is too high.
	if !allowance.MaxContractPrice.IsZero() && allowance.MaxContractPrice.Cmp(hostSettings.ContractPrice) < 0 {
		return errors.New("contract price of host is too high - price gouging protection enabled")
	}

	return nil
}

// managedRenew negotiates a new contract for data already stored with a host.
// It returns the new contract. This is a blocking call that performs network
// I/O.
func (c *Contractor) managedRenew(sc *proto.SafeContract, contractFunding types.Currency, newEndHeight types.BlockHeight) (modules.RenterContract, error) {
	// For convenience
	contract := sc.Metadata()
	// Sanity check - should not be renewing a bad contract.
	utility, ok := c.managedContractUtility(contract.ID)
	if !ok || !utility.GoodForRenew {
		c.log.Critical(fmt.Sprintf("Renewing a contract that has been marked as !GoodForRenew %v/%v",
			ok, utility.GoodForRenew))
	}

	// Fetch the host associated with this contract.
	host, ok, err := c.hdb.Host(contract.HostPublicKey)
	if err != nil {
		return modules.RenterContract{}, errors.AddContext(err, "error getting host from hostdb:")
	}
	c.mu.Lock()
	if reflect.DeepEqual(c.allowance, modules.Allowance{}) {
		c.mu.Unlock()
		return modules.RenterContract{}, errors.New("called managedRenew but allowance isn't set")
	}
	period := c.allowance.Period
	c.mu.Unlock()
	if !ok {
		return modules.RenterContract{}, errors.New("no record of that host")
	} else if host.Filtered {
		return modules.RenterContract{}, errors.New("host is blacklisted")
	} else if host.StoragePrice.Cmp(maxStoragePrice) > 0 {
		return modules.RenterContract{}, errTooExpensive
	} else if host.MaxDuration < period {
		return modules.RenterContract{}, errors.New("insufficient MaxDuration of host")
	}

	// cap host.MaxCollateral
	if host.MaxCollateral.Cmp(maxCollateral) > 0 {
		host.MaxCollateral = maxCollateral
	}

	// Check for price gouging on the renewal.
	err = checkFormContractGouging(c.allowance, host.HostExternalSettings)
	if err != nil {
		return modules.RenterContract{}, errors.AddContext(err, "unable to renew - price gouging protection enabled")
	}

	// get an address to use for negotiation
	uc, err := c.wallet.NextAddress()
	if err != nil {
		return modules.RenterContract{}, err
	}

	// get the wallet seed
	seed, _, err := c.wallet.PrimarySeed()
	if err != nil {
		return modules.RenterContract{}, err
	}
	// derive the renter seed and wipe it after we are done with it.
	renterSeed := proto.DeriveRenterSeed(seed)
	defer fastrand.Read(renterSeed[:])

	// create contract params
	c.mu.RLock()
	params := proto.ContractParams{
		Allowance:     c.allowance,
		Host:          host,
		Funding:       contractFunding,
		StartHeight:   c.blockHeight,
		EndHeight:     newEndHeight,
		RefundAddress: uc.UnlockHash(),
		RenterSeed:    renterSeed.EphemeralRenterSeed(newEndHeight),
	}
	c.mu.RUnlock()

	// wipe the renter seed once we are done using it.
	defer fastrand.Read(params.RenterSeed[:])

	// execute negotiation protocol
	txnBuilder, err := c.wallet.StartTransaction()
	if err != nil {
		return modules.RenterContract{}, err
	}
	newContract, formationTxnSet, sweepTxn, sweepParents, err := c.staticContracts.Renew(sc, params, txnBuilder, c.tpool, c.hdb, c.tg.StopChan())
	if err != nil {
		txnBuilder.Drop() // return unused outputs to wallet
		return modules.RenterContract{}, err
	}

	monitorContractArgs := monitorContractArgs{
		false,
		newContract.ID,
		newContract.Transaction,
		formationTxnSet,
		sweepTxn,
		sweepParents,
		params.StartHeight,
	}
	err = c.staticWatchdog.callMonitorContract(monitorContractArgs)
	if err != nil {
		return modules.RenterContract{}, err
	}

	// Add a mapping from the contract's id to the public key of the host. This
	// will destroy the previous mapping from pubKey to contract id but other
	// modules are only interested in the most recent contract anyway.
	c.mu.Lock()
	c.pubKeysToContractID[newContract.HostPublicKey.String()] = newContract.ID
	c.mu.Unlock()

	return newContract, nil
}

// managedRenewContract will use the renew instructions to renew a contract,
// returning the amount of money that was put into the contract for renewal.
func (c *Contractor) managedRenewContract(renewInstructions fileContractRenewal, currentPeriod types.BlockHeight, allowance modules.Allowance, blockHeight, endHeight types.BlockHeight) (fundsSpent types.Currency, err error) {
	// Pull the variables out of the renewal.
	id := renewInstructions.id
	amount := renewInstructions.amount

	// Mark the contract as being renewed, and defer logic to unmark it
	// once renewing is complete.
	c.log.Debugln("Marking a contract for renew:", id)
	c.mu.Lock()
	c.renewing[id] = true
	c.mu.Unlock()
	defer func() {
		c.log.Debugln("Unmarking the contract for renew", id)
		c.mu.Lock()
		delete(c.renewing, id)
		c.mu.Unlock()
	}()

	// Wait for any active editors/downloaders/sessions to finish for this
	// contract, and then grab the latest revision.
	c.mu.RLock()
	e, eok := c.editors[id]
	d, dok := c.downloaders[id]
	s, sok := c.sessions[id]
	c.mu.RUnlock()
	if eok {
		c.log.Debugln("Waiting for editor invalidation")
		e.invalidate()
		c.log.Debugln("Got editor invalidation")
	}
	if dok {
		c.log.Debugln("Waiting for downloader invalidation")
		d.invalidate()
		c.log.Debugln("Got downloader invalidation")
	}
	if sok {
		c.log.Debugln("Waiting for session invalidation")
		s.invalidate()
		c.log.Debugln("Got session invalidation")
	}

	// Fetch the contract that we are renewing.
	c.log.Debugln("Acquiring contract from the contract set", id)
	oldContract, exists := c.staticContracts.Acquire(id)
	if !exists {
		c.log.Debugln("Contract does not seem to exist")
		return types.ZeroCurrency, errors.New("contract no longer exists")
	}
	oldUtility, exists := c.managedContractUtility(id)
	if !exists {
		c.log.Printf("Contract %v slated for renew could not be found in the utility lookup", id)
		return types.ZeroCurrency, errors.New("contract utility could not be found")
	}

	// Perform the actual renew. If the renew fails, return the
	// contract. If the renew fails we check how often it has failed
	// before. Once it has failed for a certain number of blocks in a
	// row and reached its second half of the renew window, we give up
	// on renewing it and set goodForRenew to false.
	c.log.Debugln("calling managedRenew on contract", id)
	newContract, errRenew := c.managedRenew(oldContract, amount, endHeight)
	c.log.Debugln("managedRenew has returned with error:", errRenew)
	if errRenew != nil {
		// Increment the number of failed renews for the contract if it
		// was the host's fault.
		if modules.IsHostsFault(errRenew) {
			c.mu.Lock()
			c.numFailedRenews[oldContract.Metadata().ID]++
			totalFailures := c.numFailedRenews[oldContract.Metadata().ID]
			c.mu.Unlock()
			c.log.Debugln("remote host determined to be at fault, tallying up failed renews", totalFailures, id)
		}

		// Check if contract has to be replaced.
		md := oldContract.Metadata()
		c.mu.RLock()
		numRenews, failedBefore := c.numFailedRenews[md.ID]
		c.mu.RUnlock()
		secondHalfOfWindow := blockHeight+allowance.RenewWindow/2 >= md.EndHeight
		replace := numRenews >= consecutiveRenewalsBeforeReplacement
		if failedBefore && secondHalfOfWindow && replace {
			oldUtility.GoodForRenew = false
			oldUtility.GoodForUpload = false
			oldUtility.Locked = true
			err := c.callUpdateUtility(oldContract, oldUtility, true)
			if err != nil {
				c.log.Println("WARN: failed to mark contract as !goodForRenew:", err)
			}
			c.log.Printf("WARN: consistently failed to renew %v, marked as bad and locked: %v\n",
				oldContract.Metadata().HostPublicKey, errRenew)
			c.staticContracts.Return(oldContract)
			return types.ZeroCurrency, errors.AddContext(errRenew, "contract marked as bad for too many consecutive failed renew attempts")
		}

		// Seems like it doesn't have to be replaced yet. Log the
		// failure and number of renews that have failed so far.
		c.log.Printf("WARN: failed to renew contract %v [%v]: %v\n",
			oldContract.Metadata().HostPublicKey, numRenews, errRenew)
		c.staticContracts.Return(oldContract)
		return types.ZeroCurrency, errors.AddContext(errRenew, "contract renewal with host was unsuccessful")
	}
	c.log.Printf("Renewed contract %v\n", id)

	// Update the utility values for the new contract, and for the old
	// contract.
	newUtility := modules.ContractUtility{
		GoodForUpload: true,
		GoodForRenew:  true,
	}
	if err := c.managedAcquireAndUpdateContractUtility(newContract.ID, newUtility); err != nil {
		c.log.Println("Failed to update the contract utilities", err)
		c.staticContracts.Return(oldContract)
		return amount, nil // Error is not returned because the renew succeeded.
	}
	oldUtility.GoodForRenew = false
	oldUtility.GoodForUpload = false
	oldUtility.Locked = true
	if err := c.callUpdateUtility(oldContract, oldUtility, true); err != nil {
		c.log.Println("Failed to update the contract utilities", err)
		c.staticContracts.Return(oldContract)
		return amount, nil // Error is not returned because the renew succeeded.
	}

	if c.staticDeps.Disrupt("InterruptContractSaveToDiskAfterDeletion") {
		c.staticContracts.Return(oldContract)
		return amount, errors.New("InterruptContractSaveToDiskAfterDeletion disrupt")
	}
	// Lock the contractor as we update it to use the new contract
	// instead of the old contract.
	c.mu.Lock()
	// Link Contracts
	c.renewedFrom[newContract.ID] = id
	c.renewedTo[id] = newContract.ID
	// Store the contract in the record of historic contracts.
	c.oldContracts[id] = oldContract.Metadata()
	// Save the contractor.
	err = c.save()
	if err != nil {
		c.log.Println("Failed to save the contractor after creating a new contract.")
	}
	c.mu.Unlock()
	// Delete the old contract.
	c.staticContracts.Delete(oldContract)

	// Signal to the watchdog that it should immediately post the last
	// revision for this contract.
	go c.staticWatchdog.threadedSendMostRecentRevision(oldContract.Metadata())
	return amount, nil
}

// managedFindRecoverableContracts will spawn a thread to rescan parts of the
// blockchain for recoverable contracts if the wallet has been locked during the
// last scan.
func (c *Contractor) managedFindRecoverableContracts() {
	if c.staticDeps.Disrupt("disableAutomaticContractRecoveryScan") {
		return
	}
	c.mu.RLock()
	cc := c.recentRecoveryChange
	c.mu.RUnlock()
	if err := c.callInitRecoveryScan(cc); err != nil {
		c.log.Debug(err)
		return
	}
}

// managedAcquireAndUpdateContractUtility is a helper function that acquires a contract, updates
// its ContractUtility and returns the contract again.
func (c *Contractor) managedAcquireAndUpdateContractUtility(id types.FileContractID, utility modules.ContractUtility) error {
	safeContract, ok := c.staticContracts.Acquire(id)
	if !ok {
		return errors.New("failed to acquire contract for update")
	}
	defer c.staticContracts.Return(safeContract)
	return c.callUpdateUtility(safeContract, utility, false)
}

// callUpdateUtility updates the utility of a contract and notifies the
// churnLimiter of churn if necessary. This method should *always* be used as
// opposed to calling UpdateUtility directly on a safe contract from the
// contractor. Pass in renewed as true if the contract has been renewed and is
// not churn.
func (c *Contractor) callUpdateUtility(safeContract *proto.SafeContract, newUtility modules.ContractUtility, renewed bool) error {
	contract := safeContract.Metadata()

	// If the contract is going from GFR to !GFR, notify the churn limiter.
	if !renewed && contract.Utility.GoodForRenew && !newUtility.GoodForRenew {
		c.staticChurnLimiter.callNotifyChurnedContract(contract)
	}

	return safeContract.UpdateUtility(newUtility)
}

// threadedContractMaintenance checks the set of contracts that the contractor
// has against the allownace, renewing any contracts that need to be renewed,
// dropping contracts which are no longer worthwhile, and adding contracts if
// there are not enough.
//
// Between each network call, the thread checks whether a maintenance interrupt
// signal is being sent. If so, maintenance returns, yielding to whatever thread
// issued the interrupt.
func (c *Contractor) threadedContractMaintenance() {
	err := c.tg.Add()
	if err != nil {
		return
	}
	defer c.tg.Done()

	// No contract maintenance unless contractor is synced.
	if !c.managedSynced() {
		c.log.Debugln("Skipping contract maintenance since consensus isn't synced yet")
		return
	}
	c.log.Debugln("starting contract maintenance")

	// Only one instance of this thread should be running at a time. Under
	// normal conditions, fine to return early if another thread is already
	// doing maintenance. The next block will trigger another round. Under
	// testing, control is insufficient if the maintenance loop isn't guaranteed
	// to run.
	if build.Release == "testing" {
		c.maintenanceLock.Lock()
	} else if !c.maintenanceLock.TryLock() {
		c.log.Debugln("maintenance lock could not be obtained")
		return
	}
	defer c.maintenanceLock.Unlock()

	// Register the WalletLockedDuringMaintenance alert if necessary.
	var registerWalletLockedDuringMaintenance bool
	defer func() {
		if registerWalletLockedDuringMaintenance {
			c.staticAlerter.RegisterAlert(modules.AlertIDWalletLockedDuringMaintenance, AlertMSGWalletLockedDuringMaintenance, modules.ErrLockedWallet.Error(), modules.SeverityWarning)
		} else {
			c.staticAlerter.UnregisterAlert(modules.AlertIDWalletLockedDuringMaintenance)
		}
	}()

	// Perform general cleanup of the contracts. This includes recovering lost
	// contracts, archiving contracts, and other cleanup work. This should all
	// happen before the rest of the maintenance.
	c.managedFindRecoverableContracts()
	c.callRecoverContracts()
	c.managedArchiveContracts()
	c.managedCheckForDuplicates()
	c.managedPrunePubkeyMap()
	c.managedPrunedRedundantAddressRange()
	err = c.managedMarkContractsUtility()
	if err != nil {
		c.log.Debugln("Unable to mark contract utilities:", err)
		return
	}
	err = c.hdb.UpdateContracts(c.staticContracts.ViewAll())
	if err != nil {
		c.log.Debugln("Unable to update hostdb contracts:", err)
		return
	}

	// If there are no hosts requested by the allowance, there is no remaining
	// work.
	c.mu.RLock()
	wantedHosts := c.allowance.Hosts
	c.mu.RUnlock()
	if wantedHosts <= 0 {
		c.log.Debugln("Exiting contract maintenance because the number of desired hosts is <= zero.")
		return
	}

	// The rest of this function needs to know a few of the stateful variables
	// from the contractor, build those up under a lock so that the rest of the
	// function can execute without lock contention.
	c.mu.Lock()
	allowance := c.allowance
	blockHeight := c.blockHeight
	currentPeriod := c.currentPeriod
	endHeight := c.contractEndHeight()
	c.mu.Unlock()

	// Create the renewSet and refreshSet. Each is a list of contracts that need
	// to be renewed, paired with the amount of money to use in each renewal.
	//
	// The renewSet is specifically contracts which are being renewed because
	// they are about to expire. And the refreshSet is contracts that are being
	// renewed because they are out of money.
	//
	// The contractor will prioritize contracts in the renewSet over contracts
	// in the refreshSet. If the wallet does not have enough money, or if the
	// allowance does not have enough money, the contractor will prefer to save
	// data in the long term rather than renew a contract.
	var renewSet []fileContractRenewal
	var refreshSet []fileContractRenewal

	// Iterate through the contracts again, figuring out which contracts to
	// renew and how much extra funds to renew them with.
	for _, contract := range c.staticContracts.ViewAll() {
		c.log.Debugln("Examining a contract:", contract.HostPublicKey, contract.ID)
		// Skip any host that does not match our whitelist/blacklist filter
		// settings.
		host, _, err := c.hdb.Host(contract.HostPublicKey)
		if err != nil {
			c.log.Println("WARN: error getting host", err)
			continue
		}
		if host.Filtered {
			c.log.Debugln("Contract skipped because it is filtered")
			continue
		}
		// Skip hosts that can't use the current renter-host protocol.
		if build.VersionCmp(host.Version, modules.MinimumSupportedRenterHostProtocolVersion) < 0 {
			c.log.Debugln("Contract skipped because host is using an outdated version", host.Version)
		}

		// Skip any contracts which do not exist or are otherwise unworthy for
		// renewal.
		utility, ok := c.managedContractUtility(contract.ID)
		if !ok || !utility.GoodForRenew {
			if blockHeight-contract.StartHeight < types.BlocksPerWeek {
				c.log.Debugln("Contract did not last 1 week and is not being renewed", contract.ID)
			}
			c.log.Debugln("Contract skipped because it is not good for renew (utility.GoodForRenew, exists)", utility.GoodForRenew, ok)
			continue
		}

		// If the contract needs to be renewed because it is about to expire,
		// calculate a spending for the contract that is proportional to how
		// much money was spend on the contract throughout this billing cycle
		// (which is now ending).
		if blockHeight+allowance.RenewWindow >= contract.EndHeight && !c.staticDeps.Disrupt("disableRenew") {
			renewAmount, err := c.managedEstimateRenewFundingRequirements(contract, blockHeight, allowance)
			if err != nil {
				c.log.Debugln("Contract skipped because there was an error estimating renew funding requirements", renewAmount, err)
				continue
			}
			renewSet = append(renewSet, fileContractRenewal{
				id:     contract.ID,
				amount: renewAmount,
			})
			c.log.Debugln("Contract has been added to the renew set for being past the renew height")
			continue
		}

		// Check if the contract is empty. We define a contract as being empty
		// if less than 'minContractFundRenewalThreshold' funds are remaining
		// (3% at time of writing), or if there is less than 3 sectors worth of
		// storage+upload+download remaining.
		blockBytes := types.NewCurrency64(modules.SectorSize * uint64(allowance.Period))
		sectorStoragePrice := host.StoragePrice.Mul(blockBytes)
		sectorUploadBandwidthPrice := host.UploadBandwidthPrice.Mul64(modules.SectorSize)
		sectorDownloadBandwidthPrice := host.DownloadBandwidthPrice.Mul64(modules.SectorSize)
		sectorBandwidthPrice := sectorUploadBandwidthPrice.Add(sectorDownloadBandwidthPrice)
		sectorPrice := sectorStoragePrice.Add(sectorBandwidthPrice)
		percentRemaining, _ := big.NewRat(0, 1).SetFrac(contract.RenterFunds.Big(), contract.TotalCost.Big()).Float64()
		lowFundsRefresh := c.staticDeps.Disrupt("LowFundsRefresh")
		if lowFundsRefresh || ((contract.RenterFunds.Cmp(sectorPrice.Mul64(3)) < 0 || percentRemaining < MinContractFundRenewalThreshold) && !c.staticDeps.Disrupt("disableRenew")) {
			// Renew the contract with double the amount of funds that the
			// contract had previously. The reason that we double the funding
			// instead of doing anything more clever is that we don't know what
			// the usage pattern has been. The spending could have all occurred
			// in one burst recently, and the user might need a contract that
			// has substantially more money in it.
			//
			// We double so that heavily used contracts can grow in funding
			// quickly without consuming too many transaction fees, however this
			// does mean that a larger percentage of funds get locked away from
			// the user in the event that the user stops uploading immediately
			// after the renew.
			refreshSet = append(refreshSet, fileContractRenewal{
				id:     contract.ID,
				amount: contract.TotalCost.Mul64(2),
			})
			c.log.Debugln("Contract identified as needing to be added to refresh set", contract.RenterFunds, sectorPrice.Mul64(3), percentRemaining, MinContractFundRenewalThreshold)
		} else {
			c.log.Debugln("Contract did not get added to the refresh set", contract.RenterFunds, sectorPrice.Mul64(3), percentRemaining, MinContractFundRenewalThreshold)
		}
	}
	if len(renewSet) != 0 || len(refreshSet) != 0 {
		c.log.Printf("renewing %v contracts and refreshing %v contracts", len(renewSet), len(refreshSet))
	}

	// Update the failed renew map so that it only contains contracts which we
	// are currently trying to renew or refresh. The failed renew map is a map
	// that we use to track how many times consecutively we failed to renew a
	// contract with a host, so that we know if we need to abandon that host.
	c.mu.Lock()
	newFirstFailedRenew := make(map[types.FileContractID]types.BlockHeight)
	for _, r := range renewSet {
		if _, exists := c.numFailedRenews[r.id]; exists {
			newFirstFailedRenew[r.id] = c.numFailedRenews[r.id]
		}
	}
	for _, r := range refreshSet {
		if _, exists := c.numFailedRenews[r.id]; exists {
			newFirstFailedRenew[r.id] = c.numFailedRenews[r.id]
		}
	}
	c.numFailedRenews = newFirstFailedRenew
	c.mu.Unlock()

	// Depend on the PeriodSpending function to get a breakdown of spending in
	// the contractor. Then use that to determine how many funds remain
	// available in the allowance for renewals.
	spending, err := c.PeriodSpending()
	if err != nil {
		// This should only error if the contractor is shutting down
		c.log.Println("WARN: error getting period spending:", err)
		return
	}
	var fundsRemaining types.Currency
	// Check for an underflow. This can happen if the user reduced their
	// allowance at some point to less than what we've already spent.
	if spending.TotalAllocated.Cmp(allowance.Funds) < 0 {
		fundsRemaining = allowance.Funds.Sub(spending.TotalAllocated)
	}
	c.log.Debugln("Remaining funds in allowance:", fundsRemaining)

	// Register the AllowanceLowFunds alert if necessary.
	var registerLowFundsAlert bool
	defer func() {
		if registerLowFundsAlert {
			c.staticAlerter.RegisterAlert(modules.AlertIDRenterAllowanceLowFunds, AlertMSGAllowanceLowFunds, "", modules.SeverityWarning)
		} else {
			c.staticAlerter.UnregisterAlert(modules.AlertIDRenterAllowanceLowFunds)
		}
	}()
	// Go through the contracts we've assembled for renewal. Any contracts that
	// need to be renewed because they are expiring (renewSet) get priority over
	// contracts that need to be renewed because they have exhausted their funds
	// (refreshSet). If there is not enough money available, the more expensive
	// contracts will be skipped.
	for _, renewal := range renewSet {
		unlocked, err := c.wallet.Unlocked()
		if !unlocked || err != nil {
			registerWalletLockedDuringMaintenance = true
			c.log.Println("Contractor is attempting to renew contracts that are about to expire, however the wallet is locked")
			return
		}

		c.log.Println("Attempting to perform a renewal:", renewal.id)
		// Skip this renewal if we don't have enough funds remaining.
		if renewal.amount.Cmp(fundsRemaining) > 0 || c.staticDeps.Disrupt("LowFundsRenewal") {
			c.log.Println("Skipping renewal because there are not enough funds remaining in the allowance", renewal.id, renewal.amount, fundsRemaining)
			registerLowFundsAlert = true
			continue
		}

		// Renew one contract. The error is ignored because the renew function
		// already will have logged the error, and in the event of an error,
		// 'fundsSpent' will return '0'.
		fundsSpent, err := c.managedRenewContract(renewal, currentPeriod, allowance, blockHeight, endHeight)
		if err != nil {
			c.log.Println("Error renewing a contract", renewal.id, err)
		} else {
			c.log.Println("Renewal completed without error")
		}
		fundsRemaining = fundsRemaining.Sub(fundsSpent)

		// Return here if an interrupt or kill signal has been sent.
		select {
		case <-c.tg.StopChan():
			c.log.Println("returning because the renter was stopped")
			return
		case <-c.interruptMaintenance:
			c.log.Println("returning because maintenance was interrupted")
			return
		default:
		}
	}
	for _, renewal := range refreshSet {
		unlocked, err := c.wallet.Unlocked()
		if !unlocked || err != nil {
			registerWalletLockedDuringMaintenance = true
			c.log.Println("contractor is attempting to refresh contracts that have run out of funds, however the wallet is locked")
			return
		}

		// Skip this renewal if we don't have enough funds remaining.
		c.log.Debugln("Attempting to perform a contract refresh:", renewal.id)
		if renewal.amount.Cmp(fundsRemaining) > 0 || c.staticDeps.Disrupt("LowFundsRefresh") {
			c.log.Println("skipping refresh because there are not enough funds remaining in the allowance", renewal.amount, fundsRemaining)
			registerLowFundsAlert = true
			continue
		}

		// Renew one contract. The error is ignored because the renew function
		// already will have logged the error, and in the event of an error,
		// 'fundsSpent' will return '0'.
		fundsSpent, err := c.managedRenewContract(renewal, currentPeriod, allowance, blockHeight, endHeight)
		if err != nil {
			c.log.Println("Error refreshing a contract", renewal.id, err)
		}
		fundsRemaining = fundsRemaining.Sub(fundsSpent)

		// Return here if an interrupt or kill signal has been sent.
		select {
		case <-c.tg.StopChan():
			c.log.Println("returning because the renter was stopped")
			return
		case <-c.interruptMaintenance:
			c.log.Println("returning because maintenance was interrupted")
			return
		default:
		}
	}

	// Count the number of contracts which are good for uploading, and then make
	// more as needed to fill the gap.
	uploadContracts := 0
	for _, id := range c.staticContracts.IDs() {
		if cu, ok := c.managedContractUtility(id); ok && cu.GoodForUpload {
			uploadContracts++
		}
	}
	c.mu.RLock()
	neededContracts := int(c.allowance.Hosts) - uploadContracts
	c.mu.RUnlock()
	if neededContracts <= 0 {
		c.log.Debugln("do not seem to need more contracts")
		return
	}
	c.log.Println("need more contracts:", neededContracts)

	// Assemble two exclusion lists. The first one includes all hosts that we
	// already have contracts with and the second one includes all hosts we
	// have active contracts with. Then select a new batch of hosts to attempt
	// contract formation with.
	allContracts := c.staticContracts.ViewAll()
	c.mu.RLock()
	var blacklist []types.SiaPublicKey
	var addressBlacklist []types.SiaPublicKey
	for _, contract := range allContracts {
		blacklist = append(blacklist, contract.HostPublicKey)
		if !contract.Utility.Locked || contract.Utility.GoodForRenew || contract.Utility.GoodForUpload {
			addressBlacklist = append(addressBlacklist, contract.HostPublicKey)
		}
	}
	// Add the hosts we have recoverable contracts with to the blacklist to
	// avoid losing existing data by forming a new/empty contract.
	for _, contract := range c.recoverableContracts {
		blacklist = append(blacklist, contract.HostPublicKey)
	}

	// Determine the max and min initial contract funding based on the allowance
	// settings
	maxInitialContractFunds := c.allowance.Funds.Div64(c.allowance.Hosts).Mul64(MaxInitialContractFundingMulFactor).Div64(MaxInitialContractFundingDivFactor)
	minInitialContractFunds := c.allowance.Funds.Div64(c.allowance.Hosts).Div64(MinInitialContractFundingFactor)
	c.mu.RUnlock()

	// Get Hosts
	hosts, err := c.hdb.RandomHosts(neededContracts*4+randomHostsBufferForScore, blacklist, addressBlacklist)
	if err != nil {
		c.log.Println("WARN: not forming new contracts:", err)
		return
	}
	c.log.Debugln("trying to form contracts with hosts, pulled this many hosts from hostdb:", len(hosts))

	// Calculate the anticipated transaction fee.
	_, maxFee := c.tpool.FeeEstimation()
	txnFee := maxFee.Mul64(modules.EstimatedFileContractTransactionSetSize)

	// Form contracts with the hosts one at a time, until we have enough
	// contracts.
	for _, host := range hosts {
		// If no more contracts are needed, break.
		if neededContracts <= 0 {
			break
		}

		// Calculate the contract funding with host
		contractFunds := host.ContractPrice.Add(txnFee).Mul64(ContractFeeFundingFactor)

		// Check that the contract funding is reasonable compared to the max and
		// min initial funding. This is to protect against increases to
		// allowances being used up to fast and not being able to spread the
		// funds across new contracts properly, as well as protecting against
		// contracts renewing too quickly
		if contractFunds.Cmp(maxInitialContractFunds) > 0 {
			contractFunds = maxInitialContractFunds
		}
		if contractFunds.Cmp(minInitialContractFunds) < 0 {
			contractFunds = minInitialContractFunds
		}

		// Confirm the wallet is still unlocked
		unlocked, err := c.wallet.Unlocked()
		if !unlocked || err != nil {
			registerWalletLockedDuringMaintenance = true
			c.log.Println("contractor is attempting to establish new contracts with hosts, however the wallet is locked")
			return
		}

		// Determine if we have enough money to form a new contract.
		if fundsRemaining.Cmp(contractFunds) < 0 || c.staticDeps.Disrupt("LowFundsFormation") {
			registerLowFundsAlert = true
			c.log.Println("WARN: need to form new contracts, but unable to because of a low allowance")
			break
		}

		// If we are using a custom resolver we need to replace the domain name
		// with 127.0.0.1 to be able to form contracts.
		if c.staticDeps.Disrupt("customResolver") {
			port := host.NetAddress.Port()
			host.NetAddress = modules.NetAddress(fmt.Sprintf("127.0.0.1:%s", port))
		}

		// Attempt forming a contract with this host.
		start := time.Now()
		fundsSpent, newContract, err := c.managedNewContract(host, contractFunds, endHeight)
		if err != nil {
			c.log.Printf("Attempted to form a contract with %v, time spent %v, but negotiation failed: %v\n", host.NetAddress, time.Since(start).Round(time.Millisecond), err)
			continue
		}
		fundsRemaining = fundsRemaining.Sub(fundsSpent)
		neededContracts--

		sb, err := c.hdb.ScoreBreakdown(host)
		if err == nil {
			c.log.Println("A new contract has been formed with a host:", newContract.ID)
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

		// Add this contract to the contractor and save.
		err = c.managedAcquireAndUpdateContractUtility(newContract.ID, modules.ContractUtility{
			GoodForUpload: true,
			GoodForRenew:  true,
		})
		if err != nil {
			c.log.Println("Failed to update the contract utilities", err)
			return
		}
		c.mu.Lock()
		err = c.save()
		c.mu.Unlock()
		if err != nil {
			c.log.Println("Unable to save the contractor:", err)
		}

		// Soft sleep before making the next contract.
		select {
		case <-c.tg.StopChan():
			return
		case <-c.interruptMaintenance:
			return
		default:
		}
	}
}
