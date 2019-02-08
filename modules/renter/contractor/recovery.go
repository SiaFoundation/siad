package contractor

import (
	"errors"
	"sync"
	"sync/atomic"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TODO If we already have an active contract with a host for
// which we also have a recoverable contract, we might want to
// handle that somehow. For now we probably want to ignore a
// contract if we already have an active contract with the same
// host but there could still be files which are only
// accessible using one contract and not the other. We might
// need to somehow merge them or download all the sectors from
// the old one and upload them to the newer contract.  For now
// we ignore that contract and don't delete it. We might want
// to recover it later.

// recoveryScanner is a scanner that subscribes to the consensus set from the
// beginning and searches the blockchain for recoverable contracts. Potential
// contracts will be added to the contractor which will then periodically try
// to recover them.
type recoveryScanner struct {
	c  *Contractor
	rs proto.RenterSeed
}

// newRecoveryScanner creates a new scanner from a seed.
func (c *Contractor) newRecoveryScanner(rs proto.RenterSeed) *recoveryScanner {
	return &recoveryScanner{
		c:  c,
		rs: rs,
	}
}

// threadedScan subscribes the scanner to cs and scans the blockchain for
// filecontracts belonging to the wallet's seed. Once done, all recoverable
// contracts should be known to the contractor after which it will periodically
// try to recover them.
func (rs *recoveryScanner) threadedScan(cs consensusSet, cancel <-chan struct{}) error {
	if err := rs.c.tg.Add(); err != nil {
		return err
	}
	defer rs.c.tg.Done()
	// Subscribe to the consensus set from the beginning.
	err := cs.ConsensusSetSubscribe(rs, modules.ConsensusChangeBeginning, cancel)
	if err != nil {
		return err
	}
	// Unsubscribe once done.
	cs.Unsubscribe(rs)
	return nil
}

// ProcessConsensusChange scans the blockchain for information relevant to the
// recoveryScanner.
func (rs *recoveryScanner) ProcessConsensusChange(cc modules.ConsensusChange) {
	for _, block := range cc.AppliedBlocks {
		// Find lost contracts for recovery.
		rs.c.findRecoverableContracts(rs.rs, block)
		atomic.AddInt64(&rs.c.atomicRecoveryScanHeight, 1)
	}
	for range cc.RevertedBlocks {
		atomic.AddInt64(&rs.c.atomicRecoveryScanHeight, -1)
	}
}

// findRecoverableContracts scans the block for contracts that could
// potentially be recovered. We are not going to recover them right away though
// since many of them could already be expired. Recovery happens periodically
// in threadedContractMaintenance.
func (c *Contractor) findRecoverableContracts(renterSeed proto.RenterSeed, b types.Block) {
	for _, txn := range b.Transactions {
		// Check if the arbitrary data starts with the correct prefix.
		csi, encryptedHostKey, hasIdentifier := hasFCIdentifier(txn)
		if !hasIdentifier {
			continue
		}
		// Check if any contract should be recovered.
		for i, fc := range txn.FileContracts {
			// Create the EphemeralRenterSeed for this contract and wipe it
			// afterwards.
			rs := renterSeed.EphemeralRenterSeed(fc.WindowStart)
			defer fastrand.Read(rs[:])
			// Validate the identifier.
			hostKey, valid := csi.IsValid(rs, txn, encryptedHostKey)
			if !valid {
				continue
			}
			// Make sure the contract belongs to us by comparing the unlock
			// hash to what we would expect.
			ourSK, ourPK := proto.GenerateKeyPair(rs, txn)
			defer fastrand.Read(ourSK[:])
			uc := types.UnlockConditions{
				PublicKeys: []types.SiaPublicKey{
					types.Ed25519PublicKey(ourPK),
					hostKey,
				},
				SignaturesRequired: 2,
			}
			if fc.UnlockHash != uc.UnlockHash() {
				continue
			}
			// Make sure we don't know about that contract already.
			fcid := txn.FileContractID(uint64(i))
			_, known := c.staticContracts.View(fcid)
			if known {
				continue
			}
			// Make sure we don't already track that contract as recoverable.
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
func (c *Contractor) managedRecoverContract(rc modules.RecoverableContract, rs proto.EphemeralRenterSeed, blockHeight types.BlockHeight) error {
	// Get the corresponding host.
	host, ok := c.hdb.Host(rc.HostPublicKey)
	if !ok {
		return errors.New("Can't recover contract with unknown host")
	}
	// Generate the secrety key for the handshake and wipe it after using it.
	sk, _ := proto.GenerateKeyPairWithOutputID(rs, rc.InputParentID)
	defer fastrand.Read(sk[:])
	// Start a new RPC session.
	s, err := c.staticContracts.NewRawSession(host, blockHeight, c.hdb, c.tg.StopChan())
	if err != nil {
		return err
	}
	defer s.Close()
	// Get the most recent revision.
	rev, sigs, err := s.Lock(rc.ID, sk)
	if err != nil {
		return err
	}
	// Build a transaction for the revision.
	revTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: sigs,
	}
	// Get the merkle roots.
	var roots []crypto.Hash
	if rev.NewFileSize > 0 {
		// TODO Followup: take host max download batch size into account.
		revTxn, roots, err = s.RecoverSectorRoots(rev, sk)
		if err != nil {
			return err
		}
	}
	// Insert the contract into the set.
	contract, err := c.staticContracts.InsertContract(revTxn, roots, sk)
	if err != nil {
		return err
	}
	// Add a mapping from the contract's id to the public key of the host.
	c.mu.Lock()
	defer c.mu.Unlock()
	_, exists := c.pubKeysToContractID[contract.HostPublicKey.String()]
	if exists {
		// NOTE There is a chance that this happens if
		// c.recoverableContracts contains multiple recoverable contracts for a
		// single host. In that case we don't update the mapping and let
		// managedCheckForDuplicates handle that later.
		return errors.New("can't recover contract with a host that we already have a contract with")
	}
	c.pubKeysToContractID[contract.HostPublicKey.String()] = contract.ID
	return nil
}

// managedRecoverContracts recovers known recoverable contracts.
func (c *Contractor) managedRecoverContracts() {
	// Get the wallet seed.
	ws, _, err := c.wallet.PrimarySeed()
	if err != nil {
		c.log.Println("Can't recover contracts", err)
		return
	}
	// Get the renter seed and wipe it once we are done with it.
	renterSeed := proto.DeriveRenterSeed(ws)
	defer fastrand.Read(renterSeed[:])
	// Copy necessary fields to avoid having to hold the lock for too long.
	c.mu.RLock()
	blockHeight := c.blockHeight
	recoverableContracts := make([]modules.RecoverableContract, 0, len(c.recoverableContracts))
	for _, rc := range c.recoverableContracts {
		recoverableContracts = append(recoverableContracts, rc)
	}
	c.mu.RUnlock()

	// Remember the deleted contracts.
	deleteContract := make([]bool, len(recoverableContracts))

	// Try to recover the contracts in parallel.
	var wg sync.WaitGroup
	for i, recoverableContract := range recoverableContracts {
		wg.Add(1)
		go func(j int, rc modules.RecoverableContract) {
			defer wg.Done()
			if blockHeight >= rc.WindowEnd {
				// No need to recover a contract if we are beyond the WindowEnd.
				deleteContract[j] = true
				c.log.Printf("Not recovering contract since the current blockheight %v is >= the WindowEnd %v:",
					blockHeight, rc.WindowEnd, rc.ID)
				return
			}
			// Check if we already have an active contract with the host.
			_, exists := c.managedContractByPublicKey(rc.HostPublicKey)
			if exists {
				// TODO this is tricky. For now we probably want to ignore a
				// contract if we already have an active contract with the same
				// host but there could still be files which are only accessible
				// using one contract and not the other. We might need to somehow
				// merge them.
				// For now we ignore that contract and don't delete it. We
				// might want to recover it later.
				c.log.Println("Not recovering contract since we already have a contract with that host",
					rc.ID, rc.HostPublicKey.String())
				return
			}
			// Get the ephemeral renter seed and wipe it after using it.
			ers := renterSeed.EphemeralRenterSeed(rc.WindowStart)
			defer fastrand.Read(ers[:])
			// Recover contract.
			err := c.managedRecoverContract(rc, ers, blockHeight)
			if err != nil {
				c.log.Println("Failed to recover contract", rc.ID, err)
				return
			}
			// Recovery was successful.
			deleteContract[j] = true
			c.log.Println("Successfully recovered contract", rc.ID)
		}(i, recoverableContract)
	}

	// Wait for the recovery to be done.
	wg.Wait()

	// Delete the contracts.
	c.mu.Lock()
	for i, rc := range recoverableContracts {
		if deleteContract[i] {
			delete(c.recoverableContracts, rc.ID)
			c.log.Println("Deleted contract from recoverable contracts:", rc.ID)
		}
	}
	err = c.save()
	if err != nil {
		c.log.Println("Unable to save while recovering contracts:", err)
	}
	c.mu.Unlock()
}
