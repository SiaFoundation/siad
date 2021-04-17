package contractor

import (
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/proto"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

// contractEndHeight returns the height at which the Contractor's contracts
// end.
func (c *Contractor) contractEndHeight() types.BlockHeight {
	return c.currentPeriod + c.allowance.Period + c.allowance.RenewWindow
}

// managedCancelContract cancels a contract by setting its utility fields to
// false and locking the utilities. The contract can still be used for
// downloads after this but it won't be used for uploads or renewals.
func (c *Contractor) managedCancelContract(cid types.FileContractID) error {
	return c.managedAcquireAndUpdateContractUtility(cid, modules.ContractUtility{
		GoodForRenew:  false,
		GoodForUpload: false,
		Locked:        true,
	})
}

// managedContractByPublicKey returns the contract with the key specified, if
// it exists. The contract will be resolved if possible to the most recent
// child contract.
func (c *Contractor) managedContractByPublicKey(pk types.SiaPublicKey) (modules.RenterContract, bool) {
	c.mu.RLock()
	id, ok := c.pubKeysToContractID[pk.String()]
	c.mu.RUnlock()
	if !ok {
		return modules.RenterContract{}, false
	}
	return c.staticContracts.View(id)
}

// managedContractUtility returns the ContractUtility for a contract with a given id.
func (c *Contractor) managedContractUtility(id types.FileContractID) (modules.ContractUtility, bool) {
	rc, exists := c.staticContracts.View(id)
	if !exists {
		return modules.ContractUtility{}, false
	}
	return rc.Utility, true
}

// managedUpdatePubKeyToContractIDMap updates the pubkeysToContractID map
func (c *Contractor) managedUpdatePubKeyToContractIDMap() {
	// Grab the current contracts and the blockheight
	contracts := c.staticContracts.ViewAll()
	c.mu.Lock()
	c.updatePubKeyToContractIDMap(contracts)
	c.mu.Unlock()
}

// updatePubKeyToContractIDMap updates the pubkeysToContractID map
func (c *Contractor) updatePubKeyToContractIDMap(contracts []modules.RenterContract) {
	// Sanity check - there should be an equal number of GFU contracts in each
	// the ViewAll set of contracts, and also in the pubKeyToContractID map.
	uniqueGFU := make(map[string]types.FileContractID)

	// Reset the pubkey to contract id map, also create a map from each
	// contract's fcid to the contract itself, then try adding each contract to
	// the map. The most recent contract for each host will be favored as the
	// contract in the map.
	c.pubKeysToContractID = make(map[string]types.FileContractID)
	for i := 0; i < len(contracts); i++ {
		c.tryAddContractToPubKeyMap(contracts[i])

		// Fill out the uniqueGFU map, tracking every contract that is marked as
		// GoodForUpload.
		if contracts[i].Utility.GoodForUpload {
			uniqueGFU[contracts[i].HostPublicKey.String()] = contracts[i].ID
		}
	}

	// Every contract that appears in the uniqueGFU map should also appear in
	// the pubKeysToContractID map.
	for pk, fcid := range uniqueGFU {
		if c.pubKeysToContractID[pk] != fcid {
			build.Critical("Contractor is not correctly mapping from pubkey to contract id, missing GFU contracts")
		}
	}
}

// tryAddContractToPubKeyMap will try and add the contract to the
// pubKeysToContractID map. The most recent contract with the best utility for
// each pubKey will be added
func (c *Contractor) tryAddContractToPubKeyMap(newContract modules.RenterContract) {
	// Ignore any contracts that have been renewed.
	_, exists := c.renewedTo[newContract.ID]
	if exists {
		gfu, gfr := newContract.Utility.GoodForUpload, newContract.Utility.GoodForRenew
		if gfu || gfr {
			c.log.Critical("renewed contract is marked as good for upload or good for renew", gfu, gfr)
		}
		return
	}
	pk := newContract.HostPublicKey.String()

	// If there is not existing contract in the map for this pubkey, add it.
	_, exists = c.pubKeysToContractID[pk]
	if exists {
		// Sanity check - the contractor should not have multiple contract tips for the
		// same contract.
		c.log.Critical("Contractor has multiple contracts that don't form a renewedTo line for the same host")
	}
	c.pubKeysToContractID[pk] = newContract.ID
}

// ContractByPublicKey returns the contract with the key specified, if it
// exists. The contract will be resolved if possible to the most recent child
// contract.
func (c *Contractor) ContractByPublicKey(pk types.SiaPublicKey) (modules.RenterContract, bool) {
	return c.managedContractByPublicKey(pk)
}

// CancelContract cancels the Contractor's contract by marking it !GoodForRenew
// and !GoodForUpload
func (c *Contractor) CancelContract(id types.FileContractID) error {
	if err := c.tg.Add(); err != nil {
		return err
	}
	defer c.tg.Done()
	defer c.threadedContractMaintenance()
	return c.managedCancelContract(id)
}

// Contracts returns the contracts formed by the contractor in the current
// allowance period. Only contracts formed with currently online hosts are
// returned.
func (c *Contractor) Contracts() []modules.RenterContract {
	return c.staticContracts.ViewAll()
}

// ContractUtility returns the utility fields for the given contract.
func (c *Contractor) ContractUtility(pk types.SiaPublicKey) (modules.ContractUtility, bool) {
	c.mu.RLock()
	id, ok := c.pubKeysToContractID[pk.String()]
	c.mu.RUnlock()
	if !ok {
		return modules.ContractUtility{}, false
	}
	return c.managedContractUtility(id)
}

// MarkContractBad will mark a specific contract as bad.
func (c *Contractor) MarkContractBad(id types.FileContractID) error {
	if err := c.tg.Add(); err != nil {
		return err
	}
	defer c.tg.Done()

	sc, exists := c.staticContracts.Acquire(id)
	if !exists {
		return errors.New("contract not found")
	}
	defer c.staticContracts.Return(sc)
	return c.managedMarkContractBad(sc)
}

// OldContracts returns the contracts formed by the contractor that have
// expired
func (c *Contractor) OldContracts() []modules.RenterContract {
	c.mu.Lock()
	defer c.mu.Unlock()
	contracts := make([]modules.RenterContract, 0, len(c.oldContracts))
	for _, c := range c.oldContracts {
		contracts = append(contracts, c)
	}
	return contracts
}

// RecoverableContracts returns the contracts that the contractor deems
// recoverable. That means they are not expired yet and also not part of the
// active contracts. Usually this should return an empty slice unless the host
// isn't available for recovery or something went wrong.
func (c *Contractor) RecoverableContracts() []modules.RecoverableContract {
	c.mu.Lock()
	defer c.mu.Unlock()
	contracts := make([]modules.RecoverableContract, 0, len(c.recoverableContracts))
	for _, c := range c.recoverableContracts {
		contracts = append(contracts, c)
	}
	return contracts
}

// managedMarkContractBad marks an already acquired SafeContract as bad.
func (c *Contractor) managedMarkContractBad(sc *proto.SafeContract) error {
	u := sc.Utility()
	u.GoodForUpload = false
	u.GoodForRenew = false
	u.BadContract = true
	err := c.callUpdateUtility(sc, u, false)
	return errors.AddContext(err, "unable to mark contract as bad")
}
