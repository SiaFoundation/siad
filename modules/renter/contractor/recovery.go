package contractor

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// findRecoverableContracts scans the block for contracts that could
// potentially be recovered. We are not going to recover them right away though
// since many of them could already be expired. Recovery happens periodically
// in threadedContractMaintenance.
func (c *Contractor) findRecoverableContracts(walletSeed modules.Seed, b types.Block) {
	for _, txn := range b.Transactions {
		// Check if the arbitrary data starts with the correct prefix.
		csi, encryptedHostKey, hasIdentifier := hasFCIdentifier(txn)
		if !hasIdentifier {
			continue
		}
		// Check if any contract should be recovered.
		for i, fc := range txn.FileContracts {
			// Create the RenterSeed for this contract and wipe it afterwards.
			rs := proto.EphemeralRenterSeed(walletSeed, fc.WindowStart)
			defer fastrand.Read(rs[:])
			// Validate it.
			hostKey, valid := csi.IsValid(rs, txn, encryptedHostKey)
			if !valid {
				continue
			}
			// Make sure we don't know about that contract already.
			fcid := txn.FileContractID(uint64(i))
			_, known := c.staticContracts.View(fcid)
			if known {
				continue
			}
			// Make sure we don't track that contract already as recoverable.
			_, known = c.recoverableContracts[fcid]
			if known {
				continue
			}

			// Mark the contract for recovery.
			c.recoverableContracts[fcid] = modules.RecoverableContract{
				FileContract:  fc,
				ID:            fcid,
				HostPublicKey: hostKey,
				InputParentID: txn.SiacoinInputs[0].ParentID,
			}
		}
	}
}

// managedRecoverContract recovers a single contract by contacting the host it
// was formed with and retrieving the latest revision and sector roots.
func (c *Contractor) managedRecoverContract(rc modules.RecoverableContract) error {
	panic("not implemented yet")
}

// managedRecoverContracts recovers known recoverable contracts.
func (c *Contractor) managedRecoverContracts() {
	// Copy necessary fields to avoid having to hold the lock for too long.
	c.mu.RLock()
	blockHeight := c.blockHeight
	recoverableContracts := make([]modules.RecoverableContract, 0, len(c.recoverableContracts))
	for _, rc := range c.recoverableContracts {
		recoverableContracts = append(recoverableContracts, rc)
	}
	c.mu.RUnlock()

	// Remember the deleted contracts.
	var deletedContracts []types.FileContractID

	// Try to recover one contract after another.
	// TODO this loop can probably be multithreaded to speed things up a
	// little.
	for _, rc := range recoverableContracts {
		if blockHeight >= rc.WindowEnd {
			// No need to recover a contract if we are beyond the WindowEnd.
			deletedContracts = append(deletedContracts, rc.ID)
			continue
		}
		// Check if we already have an active contract with the host.
		_, exists := c.managedContractByPublicKey(rc.HostPublicKey)
		if exists {
			// TODO this is tricky. For now we probably want to ignore a
			// contract if we already have an active contract with the same
			// host but there could still be files which are only accessible
			// using one contract and not the other. We might need to somehow
			// merge them.
			deletedContracts = append(deletedContracts, rc.ID)
			continue
		}
		// Recover contract.
		if err := c.managedRecoverContract(rc); err != nil {
			c.log.Debugln("Failed to recover contract", rc.ID, err)
		}
		// Recovery was successful.
		deletedContracts = append(deletedContracts, rc.ID)
		c.log.Debugln("Successfully recovered contract", rc.ID)
	}

	// Delete the contracts.
	c.mu.Lock()
	for _, fcid := range deletedContracts {
		delete(c.recoverableContracts, fcid)
	}
	err := c.save()
	if err != nil {
		c.log.Println("Unable to save while recovering contracts:", err)
	}
	c.mu.Unlock()
}
