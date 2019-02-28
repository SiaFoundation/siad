package contractor

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// hasFCIdentifier checks the transaction for a ContractSignedIdentifier and
// returns the first one it finds with a bool indicating if an identifier was
// found.
func hasFCIdentifier(txn types.Transaction) (proto.ContractSignedIdentifier, crypto.Ciphertext, bool) {
	// We don't verify the host key here so we only need to make sure the
	// identifier fits into the arbitrary data.
	if len(txn.ArbitraryData) != 1 || len(txn.ArbitraryData[0]) < proto.FCSignedIdentiferSize {
		return proto.ContractSignedIdentifier{}, nil, false
	}
	// Verify the prefix.
	// TODO In the future we can remove checking for PrefixNonSia.
	var prefix types.Specifier
	copy(prefix[:], txn.ArbitraryData[0])
	if prefix != modules.PrefixNonSia &&
		prefix != modules.PrefixFileContractIdentifier {
		return proto.ContractSignedIdentifier{}, nil, false
	}
	// We found an identifier.
	var csi proto.ContractSignedIdentifier
	n := copy(csi[:], txn.ArbitraryData[0])
	hostKey := txn.ArbitraryData[0][n:]
	return csi, hostKey, true
}

// managedArchiveContracts will figure out which contracts are no longer needed
// and move them to the historic set of contracts.
func (c *Contractor) managedArchiveContracts() {
	// Determine the current block height.
	c.mu.RLock()
	currentHeight := c.blockHeight
	c.mu.RUnlock()

	// Loop through the current set of contracts and migrate any expired ones to
	// the set of old contracts.
	var expired []types.FileContractID
	for _, contract := range c.staticContracts.ViewAll() {
		// Check map of renewedTo in case renew code was interrupted before
		// archiving old contract
		c.mu.RLock()
		_, renewed := c.renewedTo[contract.ID]
		c.mu.RUnlock()
		if currentHeight > contract.EndHeight || renewed {
			id := contract.ID
			c.mu.Lock()
			c.oldContracts[id] = contract
			c.mu.Unlock()
			expired = append(expired, id)
			c.log.Println("INFO: archived expired contract", id)
		}
	}

	// Save.
	c.mu.Lock()
	c.save()
	c.mu.Unlock()

	// Delete all the expired contracts from the contract set.
	for _, id := range expired {
		if sc, ok := c.staticContracts.Acquire(id); ok {
			c.staticContracts.Delete(sc)
		}
	}
}

// ProcessConsensusChange will be called by the consensus set every time there
// is a change in the blockchain. Updates will always be called in order.
func (c *Contractor) ProcessConsensusChange(cc modules.ConsensusChange) {
	// Get the wallet's seed for contract recovery.
	s, _, err := c.wallet.PrimarySeed()
	if err != nil {
		c.log.Println("Failed to get the wallet's seed:", err)
	}
	// Get the master renter seed and wipe it once we are done with it.
	renterSeed := proto.DeriveRenterSeed(s)
	defer fastrand.Read(renterSeed[:])

	c.mu.Lock()
	for _, block := range cc.RevertedBlocks {
		if block.ID() != types.GenesisID {
			c.blockHeight--
		}
		// Remove recoverable contracts found in reverted block.
		c.removeRecoverableContracts(block)
	}
	for _, block := range cc.AppliedBlocks {
		if block.ID() != types.GenesisID {
			c.blockHeight++
		}
		// Find lost contracts for recovery.
		c.findRecoverableContracts(renterSeed, block)
	}

	// If we have entered the next period, update currentPeriod
	if c.blockHeight >= c.currentPeriod+c.allowance.Period {
		c.currentPeriod += c.allowance.Period
		// COMPATv1.0.4-lts
		// if we were storing a special metrics contract, it will be invalid
		// after we enter the next period.
		delete(c.oldContracts, metricsContractID)
	}

	c.synced = make(chan struct{})
	if cc.Synced {
		close(c.synced)
	}
	c.lastChange = cc.ID
	err = c.save()
	if err != nil {
		c.log.Println("Unable to save while processing a consensus change:", err)
	}
	c.mu.Unlock()

	// Perform contract maintenance if our blockchain is synced. Use a separate
	// goroutine so that the rest of the contractor is not blocked during
	// maintenance.
	if cc.Synced {
		go c.threadedContractMaintenance()
	}
}
