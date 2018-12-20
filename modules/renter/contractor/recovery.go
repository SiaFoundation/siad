package contractor

import (
	"errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
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
func (c *Contractor) managedRecoverContract(rc modules.RecoverableContract, rs proto.RenterSeed, blockHeight types.BlockHeight) error {
	// Get the corresponding host.
	host, ok := c.hdb.Host(rc.HostPublicKey)
	if !ok {
		return errors.New("Can't recover contract with unknown host")
	}
	// Generate the secrety key for the handshake and wipe it after using it.
	sk, _ := proto.GenerateKeyPairWithOutputID(rs, rc.InputParentID)
	defer fastrand.Read(sk[:])
	// Start a new RPC sessoin.
	s, err := c.staticContracts.NewSessionWithSecret(host, rc.ID, blockHeight, c.hdb, sk, c.tg.StopChan())
	if err != nil {
		return err
	}
	// Get the most recent revision.
	rev, sigs, err := s.RecentRevision()
	if err != nil {
		return err
	}
	// Create the revision transaction.
	revTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: sigs,
	}
	// Get the merkle roots.
	// TODO this is not necessary if we stop storing the roots locally.
	// Insert the contract into the set.
	contract, err := c.staticContracts.InsertContract(revTxn, []crypto.Hash{}, sk)
	if err != nil {
		return err
	}
	// Add a mapping from the contract's id to the public key of the host.
	c.mu.Lock()
	_, exists := c.pubKeysToContractID[contract.HostPublicKey.String()]
	if exists {
		// Sanity check here since we already checked for that in
		// managedRecoverContract.
		build.Critical("Shouldn't recover contract with host that we already have a contract with")
		return errors.New("can't recover contract with a host that we already have a contract with")
	}
	c.pubKeysToContractID[contract.HostPublicKey.String()] = contract.ID
	c.mu.Unlock()

	return nil
}

// managedRecoverContracts recovers known recoverable contracts.
func (c *Contractor) managedRecoverContracts() {
	// Get the wallet seed.
	ws, _, err := c.wallet.PrimarySeed()
	if err != nil {
		c.log.Debugln("Can't recover contracts", err)
		return
	}
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
		// Get renter seed and wipe it after using it.
		ers := proto.EphemeralRenterSeed(ws, rc.WindowStart)
		defer fastrand.Read(ers[:])
		// Recover contract.
		err := c.managedRecoverContract(rc, ers, blockHeight)
		if err != nil {
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
	err = c.save()
	if err != nil {
		c.log.Println("Unable to save while recovering contracts:", err)
	}
	c.mu.Unlock()
}
