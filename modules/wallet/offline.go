package wallet

import (
	"bytes"
	"errors"
	"math"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// UnspentOutputs returns the unspent outputs tracked by the wallet.
func (w *Wallet) UnspentOutputs() ([]modules.UnspentOutput, error) {
	if err := w.tg.Add(); err != nil {
		return nil, err
	}
	defer w.tg.Done()
	w.mu.Lock()
	defer w.mu.Unlock()

	// ensure durability of reported outputs
	if err := w.syncDB(); err != nil {
		return nil, err
	}

	// build initial list of confirmed outputs
	var outputs []modules.UnspentOutput
	dbForEachSiacoinOutput(w.dbTx, func(scoid types.SiacoinOutputID, sco types.SiacoinOutput) {
		outputs = append(outputs, modules.UnspentOutput{
			FundType:   types.SpecifierSiacoinOutput,
			ID:         types.OutputID(scoid),
			UnlockHash: sco.UnlockHash,
			Value:      sco.Value,
		})
	})
	dbForEachSiafundOutput(w.dbTx, func(sfoid types.SiafundOutputID, sfo types.SiafundOutput) {
		outputs = append(outputs, modules.UnspentOutput{
			FundType:   types.SpecifierSiafundOutput,
			ID:         types.OutputID(sfoid),
			UnlockHash: sfo.UnlockHash,
			Value:      sfo.Value,
		})
	})

	// don't include outputs marked as spent in pending transactions
	pending := make(map[types.OutputID]struct{})
	for _, pt := range w.unconfirmedProcessedTransactions {
		for _, input := range pt.Inputs {
			if input.WalletAddress {
				pending[input.ParentID] = struct{}{}
			}
		}
	}
	filtered := outputs[:0]
	for _, o := range outputs {
		if _, ok := pending[o.ID]; !ok {
			filtered = append(filtered, o)
		}
	}
	outputs = filtered

	// set the confirmation height for each output
outer:
	for i, o := range outputs {
		txnIndices, err := dbGetAddrTransactions(w.dbTx, o.UnlockHash)
		if err != nil {
			return nil, err
		}
		for _, j := range txnIndices {
			pt, err := dbGetProcessedTransaction(w.dbTx, j)
			if err != nil {
				return nil, err
			}
			for _, sco := range pt.Outputs {
				if sco.ID == o.ID {
					outputs[i].ConfirmationHeight = pt.ConfirmationHeight
					continue outer
				}
			}
		}
	}

	// add unconfirmed outputs, except those that are spent in pending
	// transactions
	for _, pt := range w.unconfirmedProcessedTransactions {
		for _, o := range pt.Outputs {
			if _, ok := pending[o.ID]; !ok && o.WalletAddress {
				outputs = append(outputs, modules.UnspentOutput{
					FundType:           types.SpecifierSiacoinOutput,
					ID:                 o.ID,
					UnlockHash:         o.RelatedAddress,
					Value:              o.Value,
					ConfirmationHeight: types.BlockHeight(math.MaxUint64), // unconfirmed
				})
			}
		}
	}

	// mark the watch-only outputs
	for i, o := range outputs {
		_, ok := w.watchedAddrs[o.UnlockHash]
		outputs[i].IsWatchOnly = ok
	}

	return outputs, nil
}

// UnlockConditions returns the UnlockConditions for the specified address, if
// they are known to the wallet.
func (w *Wallet) UnlockConditions(addr types.UnlockHash) (uc types.UnlockConditions, err error) {
	if err := w.tg.Add(); err != nil {
		return types.UnlockConditions{}, err
	}
	defer w.tg.Done()
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.unlocked {
		return types.UnlockConditions{}, modules.ErrLockedWallet
	}
	if sk, ok := w.keys[addr]; ok {
		uc = sk.UnlockConditions
	} else {
		// not in memory; try database
		uc, err = dbGetUnlockConditions(w.dbTx, addr)
		if err != nil {
			return types.UnlockConditions{}, errors.New("no record of UnlockConditions for that UnlockHash")
		}
	}
	// make a copy of the public key slice; otherwise the caller can modify it
	uc.PublicKeys = append([]types.SiaPublicKey(nil), uc.PublicKeys...)
	return uc, nil
}

// AddUnlockConditions adds a set of UnlockConditions to the wallet database.
func (w *Wallet) AddUnlockConditions(uc types.UnlockConditions) error {
	if err := w.tg.Add(); err != nil {
		return err
	}
	defer w.tg.Done()
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.unlocked {
		return modules.ErrLockedWallet
	}
	return dbPutUnlockConditions(w.dbTx, uc)
}

// SignTransaction signs txn using secret keys known to the wallet. The
// transaction should be complete with the exception of the Signature fields
// of each TransactionSignature referenced by toSign. For convenience, if
// toSign is empty, SignTransaction signs everything that it can.
func (w *Wallet) SignTransaction(txn *types.Transaction, toSign []crypto.Hash) error {
	if err := w.tg.Add(); err != nil {
		return err
	}
	defer w.tg.Done()

	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.unlocked {
		return modules.ErrLockedWallet
	}
	consensusHeight, err := dbGetConsensusHeight(w.dbTx)
	if err != nil {
		return err
	}

	// if toSign is empty, sign all inputs that we have keys for
	if len(toSign) == 0 {
		for _, sci := range txn.SiacoinInputs {
			if _, ok := w.keys[sci.UnlockConditions.UnlockHash()]; ok {
				toSign = append(toSign, crypto.Hash(sci.ParentID))
			}
		}
		for _, sfi := range txn.SiafundInputs {
			if _, ok := w.keys[sfi.UnlockConditions.UnlockHash()]; ok {
				toSign = append(toSign, crypto.Hash(sfi.ParentID))
			}
		}
	}
	return signTransaction(txn, w.keys, toSign, consensusHeight)
}

// SignTransaction signs txn using secret keys derived from seed. The
// transaction should be complete with the exception of the Signature fields
// of each TransactionSignature referenced by toSign, which must not be empty.
//
// SignTransaction must derive all of the keys from scratch, so it is
// appreciably slower than calling the Wallet.SignTransaction method. Only the
// first 1 million keys are derived.
func SignTransaction(txn *types.Transaction, seed modules.Seed, toSign []crypto.Hash, height types.BlockHeight) error {
	if len(toSign) == 0 {
		// unlike the wallet method, we can't simply "sign all inputs we have
		// keys for," because without generating all of the keys up front, we
		// don't know how many inputs we actually have keys for.
		return errors.New("toSign cannot be empty")
	}
	// generate keys in batches up to 1e6 before giving up
	keys := make(map[types.UnlockHash]spendableKey, 1e6)
	var keyIndex uint64
	const keysPerBatch = 1000
	for len(keys) < 1e6 {
		for _, sk := range generateKeys(seed, keyIndex, keyIndex+keysPerBatch, false) {
			keys[sk.UnlockConditions.UnlockHash()] = sk
		}
		keyIndex += keysPerBatch
		if err := signTransaction(txn, keys, toSign, height); err == nil {
			return nil
		}
	}
	return signTransaction(txn, keys, toSign, height)
}

// signTransaction signs the specified inputs of txn using the specified keys.
// It returns an error if any of the specified inputs cannot be signed.
func signTransaction(txn *types.Transaction, keys map[types.UnlockHash]spendableKey, toSign []crypto.Hash, height types.BlockHeight) error {
	// helper function to lookup unlock conditions in the txn associated with
	// a transaction signature's ParentID
	findUnlockConditions := func(id crypto.Hash) (types.UnlockConditions, bool) {
		for _, sci := range txn.SiacoinInputs {
			if crypto.Hash(sci.ParentID) == id {
				return sci.UnlockConditions, true
			}
		}
		for _, sfi := range txn.SiafundInputs {
			if crypto.Hash(sfi.ParentID) == id {
				return sfi.UnlockConditions, true
			}
		}
		return types.UnlockConditions{}, false
	}
	// helper function to lookup the secret key that can sign
	findSigningKey := func(uc types.UnlockConditions, pubkeyIndex uint64) (crypto.SecretKey, bool) {
		if pubkeyIndex >= uint64(len(uc.PublicKeys)) {
			return crypto.SecretKey{}, false
		}
		pk := uc.PublicKeys[pubkeyIndex]
		sk, ok := keys[uc.UnlockHash()]
		if !ok {
			return crypto.SecretKey{}, false
		}
		for _, key := range sk.SecretKeys {
			pubKey := key.PublicKey()
			if bytes.Equal(pk.Key, pubKey[:]) {
				return key, true
			}
		}
		return crypto.SecretKey{}, false
	}

	for _, id := range toSign {
		// find associated txn signature
		sigIndex := -1
		for i, sig := range txn.TransactionSignatures {
			if sig.ParentID == id {
				sigIndex = i
				break
			}
		}
		if sigIndex == -1 {
			return errors.New("toSign references signatures not present in transaction")
		}
		// find associated input
		uc, ok := findUnlockConditions(id)
		if !ok {
			return errors.New("toSign references IDs not present in transaction")
		}
		// lookup the signing key
		sk, ok := findSigningKey(uc, txn.TransactionSignatures[sigIndex].PublicKeyIndex)
		if !ok {
			return errors.New("could not locate signing key for " + id.String())
		}
		// add signature
		//
		// NOTE: it's possible that the Signature field will already be filled
		// out. Although we could save a bit of work by not signing it, in
		// practice it's probably best to overwrite any existing signatures,
		// since we know that ours will be valid.
		sigHash := txn.SigHash(sigIndex, height)
		encodedSig := crypto.SignHash(sigHash, sk)
		txn.TransactionSignatures[sigIndex].Signature = encodedSig[:]
	}

	return nil
}

// AddWatchAddresses instructs the wallet to begin tracking a set of
// addresses, in addition to the addresses it was previously tracking. If none
// of the addresses have appeared in the blockchain, the unused flag may be
// set to true. Otherwise, the wallet must rescan the blockchain to search for
// transactions containing the addresses.
func (w *Wallet) AddWatchAddresses(addrs []types.UnlockHash, unused bool) error {
	if err := w.tg.Add(); err != nil {
		return modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	err := func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.unlocked {
			return modules.ErrLockedWallet
		}

		// update in-memory map
		for _, addr := range addrs {
			w.watchedAddrs[addr] = struct{}{}
		}
		// update db
		alladdrs := make([]types.UnlockHash, 0, len(w.watchedAddrs))
		for addr := range w.watchedAddrs {
			alladdrs = append(alladdrs, addr)
		}
		if err := dbPutWatchedAddresses(w.dbTx, alladdrs); err != nil {
			return err
		}

		if !unused {
			// prepare to rescan
			if err := w.dbTx.DeleteBucket(bucketProcessedTransactions); err != nil {
				return err
			}
			if _, err := w.dbTx.CreateBucket(bucketProcessedTransactions); err != nil {
				return err
			}
			w.unconfirmedProcessedTransactions = nil
			if err := dbPutConsensusChangeID(w.dbTx, modules.ConsensusChangeBeginning); err != nil {
				return err
			}
			if err := dbPutConsensusHeight(w.dbTx, 0); err != nil {
				return err
			}
		}
		return w.syncDB()
	}()
	if err != nil {
		return err
	}

	if !unused {
		// rescan the blockchain
		w.cs.Unsubscribe(w)
		w.tpool.Unsubscribe(w)

		done := make(chan struct{})
		go w.rescanMessage(done)
		defer close(done)
		if err := w.cs.ConsensusSetSubscribe(w, modules.ConsensusChangeBeginning, w.tg.StopChan()); err != nil {
			return err
		}
		w.tpool.TransactionPoolSubscribe(w)
	}

	return nil
}

// RemoveWatchAddresses instructs the wallet to stop tracking a set of
// addresses and delete their associated transactions. If none of the
// addresses have appeared in the blockchain, the unused flag may be set to
// true. Otherwise, the wallet must rescan the blockchain to rebuild its
// transaction history.
func (w *Wallet) RemoveWatchAddresses(addrs []types.UnlockHash, unused bool) error {
	if err := w.tg.Add(); err != nil {
		return modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	err := func() error {
		w.mu.Lock()
		defer w.mu.Unlock()
		if !w.unlocked {
			return modules.ErrLockedWallet
		}

		// update in-memory map
		for _, addr := range addrs {
			delete(w.watchedAddrs, addr)
		}
		// update db
		alladdrs := make([]types.UnlockHash, 0, len(w.watchedAddrs))
		for addr := range w.watchedAddrs {
			alladdrs = append(alladdrs, addr)
		}
		if err := dbPutWatchedAddresses(w.dbTx, alladdrs); err != nil {
			return err
		}
		if !unused {
			// outputs associated with the addresses may be present in the
			// SiacoinOutputs bucket. Iterate through the bucket and remove
			// any outputs that we are no longer watching.
			var outputIDs []types.SiacoinOutputID
			dbForEachSiacoinOutput(w.dbTx, func(scoid types.SiacoinOutputID, sco types.SiacoinOutput) {
				if !w.isWalletAddress(sco.UnlockHash) {
					outputIDs = append(outputIDs, scoid)
				}
			})
			for _, scoid := range outputIDs {
				if err := dbDeleteSiacoinOutput(w.dbTx, scoid); err != nil {
					return err
				}
			}

			// prepare to rescan
			if err := w.dbTx.DeleteBucket(bucketProcessedTransactions); err != nil {
				return err
			}
			if _, err := w.dbTx.CreateBucket(bucketProcessedTransactions); err != nil {
				return err
			}
			w.unconfirmedProcessedTransactions = nil
			if err := dbPutConsensusChangeID(w.dbTx, modules.ConsensusChangeBeginning); err != nil {
				return err
			}
			if err := dbPutConsensusHeight(w.dbTx, 0); err != nil {
				return err
			}
		}
		return w.syncDB()
	}()
	if err != nil {
		return err
	}

	if !unused {
		// rescan the blockchain
		w.cs.Unsubscribe(w)
		w.tpool.Unsubscribe(w)

		done := make(chan struct{})
		go w.rescanMessage(done)
		defer close(done)
		if err := w.cs.ConsensusSetSubscribe(w, modules.ConsensusChangeBeginning, w.tg.StopChan()); err != nil {
			return err
		}
		w.tpool.TransactionPoolSubscribe(w)
	}

	return nil
}

// WatchAddresses returns the set of addresses that the wallet is currently
// watching.
func (w *Wallet) WatchAddresses() ([]types.UnlockHash, error) {
	if err := w.tg.Add(); err != nil {
		return nil, modules.ErrWalletShutdown
	}
	defer w.tg.Done()

	w.mu.RLock()
	defer w.mu.RUnlock()

	addrs := make([]types.UnlockHash, 0, len(w.watchedAddrs))
	for addr := range w.watchedAddrs {
		addrs = append(addrs, addr)
	}
	return addrs, nil
}
