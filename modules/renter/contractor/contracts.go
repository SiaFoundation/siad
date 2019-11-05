package contractor

import (
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
