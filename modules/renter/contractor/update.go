package contractor

import (
	"fmt"

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
	// The length of the arbitrary data should be 2. One slice for the
	// identifier and one for the host's public key.
	if len(txn.ArbitraryData) != 2 {
		return proto.ContractSignedIdentifier{}, nil, false
	}
	identifier := txn.ArbitraryData[0]
	hostKey := txn.ArbitraryData[1]
	// Verify the length of the identifier.
	if len(identifier) != proto.FCSignedIdentiferSize {
		return proto.ContractSignedIdentifier{}, nil, false
	}
	// Verify the prefix.
	// TODO In the future we can remove checking for PrefixNonSia.
	var prefix types.Specifier
	copy(prefix[:], identifier)
	if prefix != modules.PrefixNonSia &&
		prefix != modules.PrefixFileContractIdentifier {
		return proto.ContractSignedIdentifier{}, nil, false
	}
	// We found an identifier.
	var csi proto.ContractSignedIdentifier
	copy(csi[:], identifier)
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

// recoverContract recovers a FileContract from the host that it was formed
// with.
func (c *Contractor) recoverContract(fc types.FileContract, hostKey types.SiaPublicKey) {
	fmt.Println("recovering contract", hostKey.String())
	// Get the host.
	//	c.hdb.Host(fc.
	//	panic("not implemented")
	//	var sk crypto.SecretKey
	//	s, err := c.staticContracts.NewSessionWithSecret(
}

// recoverContracts recovers previously formed, contracts from a block.
func (c *Contractor) recoverContracts(walletSeed modules.Seed, b types.Block) {
	for _, txn := range b.Transactions {
		// Check if the arbitrary data starts with the correct prefix.
		csi, encryptedHostKey, hasIdentifier := hasFCIdentifier(txn)
		if !hasIdentifier {
			continue
		}
		// Check if any contract should be recovered.
		for i, fc := range txn.FileContracts {
			// Create the RenterSeed for this contract.
			rs := proto.EphemeralRenterSeed(walletSeed, fc.WindowStart)
			defer fastrand.Read(rs[:])
			// Validate it.
			hostKey, valid := csi.IsValid(rs, txn, encryptedHostKey)
			if !valid {
				continue
			}
			// The contract shouldn't be expired.
			if c.blockHeight >= fc.WindowEnd {
				continue
			}
			// Make sure we don't know about that contract already.
			_, known := c.staticContracts.View(txn.FileContractID(uint64(i)))
			if known {
				continue
			}
			// Recover the contract.
			c.recoverContract(fc, hostKey)
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
	c.mu.Lock()
	for _, block := range cc.RevertedBlocks {
		if block.ID() != types.GenesisID {
			c.blockHeight--
		}
		// TODO: Should we delete contracts that got reverted?
	}
	for _, block := range cc.AppliedBlocks {
		if block.ID() != types.GenesisID {
			c.blockHeight++
		}
		// Recover
		c.recoverContracts(s, block)
	}

	// If we have entered the next period, update currentPeriod
	if c.blockHeight >= c.currentPeriod+c.allowance.Period {
		c.currentPeriod += c.allowance.Period
		// COMPATv1.0.4-lts
		// if we were storing a special metrics contract, it will be invalid
		// after we enter the next period.
		delete(c.oldContracts, metricsContractID)
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
