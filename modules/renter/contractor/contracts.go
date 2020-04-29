package contractor

import (
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

// contractEndHeight returns the height at which the Contractor's contracts
// end. If there are no contracts, it returns zero.
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
	bh := c.cs.Height()

	c.mu.Lock()
	c.updatePubKeyToContractIDMap(contracts, bh)
	c.mu.Unlock()

	// Count the GFU contracts in the thing.
	totalGFU := 0
	for pk, _ := range c.pubKeysToContractID {
		var hpk types.SiaPublicKey
		err := hpk.LoadString(pk)
		if err != nil {
			build.Critical("ur dumb")
		}
		utility, exists := c.ContractUtility(hpk)
		if exists && utility.GoodForUpload {
			totalGFU++
		}
	}
	c.log.Printf("Contractor is reporting %v total GFU when using the ContractUtility lookup", totalGFU)
}

// updatePubKeyToContractIDMap updates the pubkeysToContractID map
func (c *Contractor) updatePubKeyToContractIDMap(contracts []modules.RenterContract, bh types.BlockHeight) {
	// Sanity check - figure out how many unique GFU contracts exist in this
	// list.
	uniqueGFU := make(map[string]struct{})
	contractMap := make(map[string]modules.RenterContract)
	for i := 0; i < len(contracts); i++ {
		contractMap[contracts[i].ID.String()] = contracts[i]
		if contracts[i].Utility.GoodForUpload {
			uniqueGFU[contracts[i].HostPublicKey.String()] = struct{}{}
		}
	}
	c.log.Printf("Contractor has %v unique GFU contracts found", len(uniqueGFU))
	c.log.Printf("Contractor has %v unique GFU contracts found", len(uniqueGFU))
	c.log.Printf("Contractor has %v unique GFU contracts found", len(uniqueGFU))
	c.log.Printf("Contractor has %v unique GFU contracts found", len(uniqueGFU))
	c.log.Printf("Contractor has %v unique GFU contracts found", len(uniqueGFU))
	c.log.Printf("Contractor has %v unique GFU contracts found", len(uniqueGFU))

	// Reset the map, also handles initialization
	c.pubKeysToContractID = make(map[string]types.FileContractID)

	// Try adding each contract to the map.
	for i := 0; i < len(contracts); i++ {
		c.tryAddContractToPubKeyMap(contracts[i], contractMap)
	}

	// Count the GFU contracts in the thing.
	for pk, _ := range c.pubKeysToContractID {
		_, exists := uniqueGFU[pk]
		contract, _ := contractMap[pk]
		if exists && !contract.Utility.GoodForUpload || !exists && contract.Utility.GoodForUpload {
			c.log.Printf("the GFU thing has a mismatch... right in the builder function")
		}
	}
}

// tryAddContractToPubKeyMap will try and add the contract to the
// pubKeysToContractID map. The most recent contract with the best utility for
// each pubKey will be added
func (c *Contractor) tryAddContractToPubKeyMap(contract modules.RenterContract, contractMap map[string]modules.RenterContract) {
	pk := contract.HostPublicKey.String()
	fcid, ok := c.pubKeysToContractID[pk]
	if !ok || contract.Utility.GoodForUpload {
		// If the pubkey isn't in the map yet then add it
		c.pubKeysToContractID[pk] = contract.ID
		return
	}

	/*
		// Compare the new contract to the existing contract, keep the better
		// contract.
		fc, ok := contractMap[fcid.String()]
		if !ok {
			build.Critical("Developer error, contract ID not found in contractMap")
			return
		}

		// Compare the utility of the new contract to the existing contract.
		if contract.Utility.GoodForUpload {
			c.pubKeysToContractID[pk] = contract.ID
		}

			// If the end height of the contract in the map is greater than the
			// current contract then the map has the right contract
			if fc.EndHeight > contract.EndHeight {
				return
			} else if contract.EndHeight > fc.EndHeight {
				// If the end height of the current contract is greater than the
				// contract in the map then we want to update the contractID
				c.pubKeysToContractID[pk] = contract.ID
				contractMap[contract.ID] = contract
				return
			}

			// If the end height are the same then we want to go with the contract
			// with the best utility
			if contract.Utility.Cmp(fc.Utility) != 1 {
				// The contract in the make has an equal or better utility so just leave
				// it
				return
			}
			c.pubKeysToContractID[pk] = contract.ID
			contractMap[contract.ID] = contract
	*/
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
	u := sc.Utility()
	u.GoodForUpload = false
	u.GoodForRenew = false
	u.BadContract = true
	err := c.callUpdateUtility(sc, u, false)
	c.staticContracts.Return(sc)
	return errors.AddContext(err, "unable to mark contract as bad")
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
