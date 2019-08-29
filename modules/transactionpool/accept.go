package transactionpool

// TODO: It seems like the transaction pool is not properly detecting conflicts
// between a file contract revision and a file contract.

import (
	"errors"
	"fmt"
	"math"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	errEmptySet            = errors.New("transaction set is empty")
	errFullTransactionPool = errors.New("transaction pool cannot accept more transactions")
	errLowMinerFees        = errors.New("transaction set needs more miner fees to be accepted")
	errObjectConflict      = errors.New("transaction set conflicts with an existing transaction set")
)

// relatedObjectIDs determines all of the object ids related to a transaction.
func relatedObjectIDs(ts []types.Transaction) []ObjectID {
	oidMap := make(map[ObjectID]struct{})
	for _, t := range ts {
		for _, sci := range t.SiacoinInputs {
			oidMap[ObjectID(sci.ParentID)] = struct{}{}
		}
		for i := range t.SiacoinOutputs {
			oidMap[ObjectID(t.SiacoinOutputID(uint64(i)))] = struct{}{}
		}
		for i := range t.FileContracts {
			oidMap[ObjectID(t.FileContractID(uint64(i)))] = struct{}{}
		}
		for _, fcr := range t.FileContractRevisions {
			oidMap[ObjectID(fcr.ParentID)] = struct{}{}
		}
		for _, sp := range t.StorageProofs {
			oidMap[ObjectID(sp.ParentID)] = struct{}{}
		}
		for _, sfi := range t.SiafundInputs {
			oidMap[ObjectID(sfi.ParentID)] = struct{}{}
		}
		for i := range t.SiafundOutputs {
			oidMap[ObjectID(t.SiafundOutputID(uint64(i)))] = struct{}{}
		}
	}

	var oids []ObjectID
	for oid := range oidMap {
		oids = append(oids, oid)
	}
	return oids
}

// requiredFeesToExtendTpool returns the amount of fees required to extend the
// transaction pool to fit another transaction set. The amount returned has the
// unit 'currency per byte'.
func (tp *TransactionPool) requiredFeesToExtendTpool() types.Currency {
	// If the transaction pool is nearly empty, it can be extended even if there
	// are no fees.
	if tp.transactionListSize < TransactionPoolSizeForFee {
		return types.ZeroCurrency
	}

	// Calculate the fee required to bump out the size of the transaction pool.
	ratioToTarget := float64(tp.transactionListSize) / TransactionPoolSizeTarget
	feeFactor := math.Pow(ratioToTarget, TransactionPoolExponentiation)
	return types.SiacoinPrecision.MulFloat(feeFactor).Div64(1000) // Divide by 1000 to get SC / kb
}

// checkTransactionSetComposition checks if the transaction set is valid given
// the state of the pool. It does not check that each individual transaction
// would be legal in the next block, but does check things like miner fees and
// IsStandard.
func (tp *TransactionPool) checkTransactionSetComposition(ts []types.Transaction) (uint64, error) {
	// Check that the transaction set is not already known.
	setID := TransactionSetID(crypto.HashObject(ts))
	_, exists := tp.transactionSets[setID]
	if exists {
		return 0, modules.ErrDuplicateTransactionSet
	}

	// All checks after this are expensive.
	//
	// TODO: There is no DoS prevention mechanism in place to prevent repeated
	// expensive verifications of invalid transactions that are created on the
	// fly.

	// Check that all transactions follow 'Standard.md' guidelines.
	setSize, err := isStandardTransactionSet(ts)
	if err != nil {
		return 0, err
	}

	return setSize, nil
}

// handleConflicts detects whether the conflicts in the transaction pool are
// legal children of the new transaction pool set or not.
func (tp *TransactionPool) handleConflicts(ts []types.Transaction, conflicts []TransactionSetID, txnFn func([]types.Transaction) (modules.ConsensusChange, error)) error {
	// Create a list of all the transaction ids that compose the set of
	// conflicts.
	conflictMap := make(map[types.TransactionID]TransactionSetID)
	for _, conflict := range conflicts {
		conflictSet := tp.transactionSets[conflict]
		for _, conflictTxn := range conflictSet {
			conflictMap[conflictTxn.ID()] = conflict
		}
	}

	// Discard all duplicate transactions from the input transaction set.
	var dedupSet []types.Transaction
	for _, t := range ts {
		_, exists := conflictMap[t.ID()]
		if exists {
			continue
		}
		dedupSet = append(dedupSet, t)
	}
	if len(dedupSet) == 0 {
		return modules.ErrDuplicateTransactionSet
	}
	// If transactions were pruned, it's possible that the set of
	// dependencies/conflicts has also reduced. To minimize computational load
	// on the consensus set, we want to prune out all of the conflicts that are
	// no longer relevant. As an example, consider the transaction set {A}, the
	// set {B}, and the new set {A, C}, where C is dependent on B. {A} and {B}
	// are both conflicts, but after deduplication {A} is no longer a conflict.
	// This is recursive, but it is guaranteed to run only once as the first
	// deduplication is guaranteed to be complete.
	if len(dedupSet) < len(ts) {
		oids := relatedObjectIDs(dedupSet)
		var conflicts []TransactionSetID
		for _, oid := range oids {
			conflict, exists := tp.knownObjects[oid]
			if exists {
				conflicts = append(conflicts, conflict)
			}
		}
		return tp.handleConflicts(dedupSet, conflicts, txnFn)
	}

	// Merge all of the conflict sets with the input set (input set goes last
	// to preserve dependency ordering), and see if the set as a whole is both
	// small enough to be legal and valid as a set. If no, return an error. If
	// yes, add the new set to the pool, and eliminate the old set. The output
	// diff objects can be repeated, (no need to remove those). Just need to
	// remove the conflicts from tp.transactionSets.
	var superset []types.Transaction
	supersetMap := make(map[TransactionSetID]struct{})
	for _, conflict := range conflictMap {
		supersetMap[conflict] = struct{}{}
	}
	for conflict := range supersetMap {
		superset = append(superset, tp.transactionSets[conflict]...)
	}
	superset = append(superset, dedupSet...)

	// Check the composition of the transaction set, including fees and
	// IsStandard rules (this is a new set, the rules must be rechecked).
	setSize, err := tp.checkTransactionSetComposition(superset)
	if err != nil {
		return err
	}

	// Check that the transaction set has enough fees to justify adding it to
	// the transaction list.
	requiredFees := tp.requiredFeesToExtendTpool().Mul64(setSize)
	var setFees types.Currency
	for _, txn := range superset {
		for _, fee := range txn.MinerFees {
			setFees = setFees.Add(fee)
		}
	}
	if requiredFees.Cmp(setFees) > 0 {
		// TODO: check if there is an existing set with lower fees that we can
		// kick out.
		return errLowMinerFees
	}

	// Check that the transaction set is valid.
	cc, err := txnFn(superset)
	if err != nil {
		return modules.NewConsensusConflict("provided transaction set has prereqs, but is still invalid: " + err.Error())
	}

	// Remove the conflicts from the transaction pool.
	for conflict := range supersetMap {
		conflictSet := tp.transactionSets[conflict]
		tp.transactionListSize -= len(encoding.Marshal(conflictSet))
		delete(tp.transactionSets, conflict)
		delete(tp.transactionSetDiffs, conflict)
	}

	// Add the transaction set to the pool.
	setID := TransactionSetID(crypto.HashObject(superset))
	tp.transactionSets[setID] = superset
	for _, diff := range cc.SiacoinOutputDiffs {
		tp.knownObjects[ObjectID(diff.ID)] = setID
	}
	for _, diff := range cc.FileContractDiffs {
		tp.knownObjects[ObjectID(diff.ID)] = setID
	}
	for _, diff := range cc.SiafundOutputDiffs {
		tp.knownObjects[ObjectID(diff.ID)] = setID
	}
	tp.transactionSetDiffs[setID] = &cc
	tsetSize := len(encoding.Marshal(superset))
	tp.transactionListSize += tsetSize

	// debug logging
	if build.DEBUG {
		txLogs := ""
		for i, t := range superset {
			txLogs += fmt.Sprintf("superset transaction %v size: %vB\n", i, len(encoding.Marshal(t)))
		}
		tp.log.Debugf("accepted transaction superset %v, size: %vB\ntpool size is %vB after accpeting transaction superset\ntransactions: \n%v\n", setID, tsetSize, tp.transactionListSize, txLogs)
	}

	return nil
}

// acceptTransactionSet verifies that a transaction set is allowed to be in the
// transaction pool, and then adds it to the transaction pool.
func (tp *TransactionPool) acceptTransactionSet(ts []types.Transaction, txnFn func([]types.Transaction) (modules.ConsensusChange, error)) error {
	if len(ts) == 0 {
		return errEmptySet
	}

	// Remove all transactions that have been confirmed in the transaction set.
	oldTS := ts
	ts = []types.Transaction{}
	for _, txn := range oldTS {
		if !tp.transactionConfirmed(tp.dbTx, txn.ID()) {
			ts = append(ts, txn)
		}
	}
	// If no transactions remain, return a dublicate error.
	if len(ts) == 0 {
		return modules.ErrDuplicateTransactionSet
	}

	// Check the composition of the transaction set.
	setSize, err := tp.checkTransactionSetComposition(ts)
	if err != nil {
		return err
	}

	// Check that the transaction set has enough fees to justify adding it to
	// the transaction list.
	requiredFees := tp.requiredFeesToExtendTpool().Mul64(setSize)
	var setFees types.Currency
	for _, txn := range ts {
		for _, fee := range txn.MinerFees {
			setFees = setFees.Add(fee)
		}
	}
	if requiredFees.Cmp(setFees) > 0 {
		// TODO: check if there is an existing set with lower fees that we can
		// kick out.
		tp.log.Debugln("Incoming transaction was rejected for having low fees", requiredFees, setFees)
		for _, txn := range ts {
			tp.log.Debugln(txn.ID())
		}
		return errLowMinerFees
	}

	// Check for conflicts with other transactions, which would indicate a
	// double-spend. Legal children of a transaction set will also trigger the
	// conflict-detector.
	oids := relatedObjectIDs(ts)
	var conflicts []TransactionSetID
	for _, oid := range oids {
		conflict, exists := tp.knownObjects[oid]
		if exists {
			conflicts = append(conflicts, conflict)
		}
	}
	if len(conflicts) > 0 {
		return tp.handleConflicts(ts, conflicts, txnFn)
	}
	cc, err := txnFn(ts)
	if err != nil {
		return modules.NewConsensusConflict("provided transaction set is standalone and invalid: " + err.Error())
	}

	// Add the transaction set to the pool.
	setID := TransactionSetID(crypto.HashObject(ts))
	tp.transactionSets[setID] = ts
	for _, oid := range oids {
		tp.knownObjects[oid] = setID
	}
	tp.transactionSetDiffs[setID] = &cc
	tsetSize := len(encoding.Marshal(ts))
	tp.transactionListSize += tsetSize
	for _, txn := range ts {
		if _, exists := tp.transactionHeights[txn.ID()]; !exists {
			tp.transactionHeights[txn.ID()] = tp.blockHeight
		}
	}

	// debug logging
	if build.DEBUG {
		txLogs := ""
		for i, t := range ts {
			txLogs += fmt.Sprintf("transaction %v size: %vB\n", i, len(encoding.Marshal(t)))
		}
		tp.log.Debugf("accepted transaction set %v, size: %vB\ntpool size is %vB after accpeting transaction set\ntransactions: \n%v\n", setID, tsetSize, tp.transactionListSize, txLogs)
	}
	return nil
}

// AcceptTransactionSet adds a transaction to the unconfirmed set of
// transactions. If the transaction is accepted, it will be relayed to
// connected peers.
//
// TODO: Break into component sets when the set gets accepted.
func (tp *TransactionPool) AcceptTransactionSet(ts []types.Transaction) error {
	// assert on consensus set to get special method
	cs, ok := tp.consensusSet.(interface {
		LockedTryTransactionSet(fn func(func(txns []types.Transaction) (modules.ConsensusChange, error)) error) error
	})
	if !ok {
		return errors.New("consensus set does not support LockedTryTransactionSet method")
	}

	return cs.LockedTryTransactionSet(func(txnFn func(txns []types.Transaction) (modules.ConsensusChange, error)) error {
		tp.log.Debugln("Beginning broadcast of transaction set")
		tp.mu.Lock()
		defer tp.mu.Unlock()
		err := tp.acceptTransactionSet(ts, txnFn)
		if err == modules.ErrDuplicateTransactionSet {
			tp.log.Debugln("Transaction set is a duplicate:", err)
			return err
		}
		if err != nil {
			tp.log.Debugln("Transaction set will not be broadcast due to an error:", err)
			return err
		}
		go tp.gateway.Broadcast("RelayTransactionSet", ts, tp.gateway.Peers())
		// Notify subscribers of an accepted transaction set
		tp.updateSubscribersTransactions()
		tp.log.Debugln("Transaction set broadcast appears to have succeeded")
		return nil
	})
}

// relayTransactionSet is an RPC that accepts a transaction set from a peer. If
// the accept is successful, the transaction will be relayed to the gateway's
// other peers.
func (tp *TransactionPool) relayTransactionSet(conn modules.PeerConn) error {
	if err := tp.tg.Add(); err != nil {
		return err
	}
	defer tp.tg.Done()
	err := conn.SetDeadline(time.Now().Add(relayTransactionSetTimeout))
	if err != nil {
		return err
	}
	// Automatically close the channel when tg.Stop() is called.
	finishedChan := make(chan struct{})
	defer close(finishedChan)
	go func() {
		select {
		case <-tp.tg.StopChan():
		case <-finishedChan:
		}
		conn.Close()
	}()

	var ts []types.Transaction
	err = encoding.ReadObject(conn, &ts, types.BlockSizeLimit)
	if err != nil {
		return err
	}

	return tp.AcceptTransactionSet(ts)
}

// MinimumCombinedSet takes two transaction sets as input and returns a combined
// transaction set. The first input is the set of required transactions, which
// the caller is indicating must all be a part of the final set. The second
// input is a set of related transactions that the caller believes may contain
// parent transactions of the required transactions. MinimumCombinedSet will
// scan through the related transactions and pull in any which are required
// parents of the required transactions, returning the final result.
//
// The final transaction set which gets returned will contain all of the
// required transactions, and will contain any of the related transactions which
// are necessary for the required transactions to be confirmed.
//
// NOTE: Both of the inputs are proper transaction sets. A proper transaction
// set is already sorted so that no parent comes after a child in the array and
// has no missing dependencies. It is okay if the input sets are overlapping.
func MinimumCombinedSet(requiredTxns []types.Transaction, relatedTxns []types.Transaction) ([]types.Transaction, error) {
	err := tp.tg.Add()
	if err != nil {
		return err
	}
	defer tp.tg.Done()

	// Track which transactions have already been scanned and added to the final
	// set of required transactions.
	includedTxns := make(map[types.TransactionID]struct{})

	// Determine what the required inputs are for the provided transaction.
	requiredInputs := make(map[ObjectID]struct{})
	for _, txn := range requiredTxns {
		for _, sci := range txn.SiacoinInputs {
			oid := ObjectID(sci.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		for _, fcr := range txn.FileContractRevisions {
			oid := ObjectID(fcr.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		for _, sp := range txn.StorageProofs {
			oid := ObjectID(sp.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		for _, sfi := range txn.SiafundInputs {
			oid := ObjectID(sfi.ParentID)
			requiredInputs[oid] = struct{}{}
		}
		includedTxns[txn.ID()] = struct{}{}
	}

	// Create a list of which related transactions create which outputs.
	potenialSources := make(map[ObjectID]*types.Transaction)
	for i := 0; i < len(relatedTxns); i++ {
		for j := range relatedTxns[i].SiacoinOutputs {
			potentialSources[ObjectID(relatedTxns[i].SiacoinOutputID(uint64(j)))] = &relatedTxns[i]
		}
		for j := range relatedTxns[i].FileContracts {
			potentialSources[ObjectID(relatedTxns[i].FileContractID(uint64(j)))] = &relatedTxns[i]
		}
		for j := range relatedTxns[i].SiafundOutputs {
			potentialSources[ObjectID(relatedTxns[i].SiafundOutputID(uint64(j)))] = &relatedTxns[i]
		}
	}

	// Cycle through all of the required inputs and find the transactions that
	// contain required inputs to the provided transaction. Do so in a loop that
	// will keep checking for more required inputs
	visitedInputs := make(map[ObjectID]struct{})
	var requiredParents []types.Transaction
	for len(requiredInputs) > 0 {
		newRequiredInputs := make(map[ObjectID]struct{})
		for ri := range requiredInputs {
			// First check whether we've scanned this input for required parents
			// before. If so, there is no need to scan again. This clause will
			// guarantee eventual termination.
			_, exists := visitedInputs[ri]
			if exists {
				continue
			}
			visitedInputs[ri] = struct{}{}

			// Check if this input is available at all in the potential sources.
			// If not, that means this input may already be confirmed on the
			// blockchain.
			txn, exists := potentialSources[ri]
			if !exists {
				continue
			}

			// Check if this transcation has already been scanned and added as a
			// requirement.
			_, exists := includedTxns[txn.ID()]
			if exists {
				continue
			}

			// If the input does have a source in the list of related
			// transactions, the source also needs to have its inputs checked
			// for any requirements.
			requiredParents = append(requiredParents, *txn)
			for _, sci := range txn.SiacoinInputs {
				oid := ObjectID(sci.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
			for _, fcr := range txn.FileContractRevisions {
				oid := ObjectID(fcr.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
			for _, sp := range txn.StorageProofs {
				oid := ObjectID(sp.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
			for _, sfi := range txn.SiafundInputs {
				oid := ObjectID(sfi.ParentID)
				newRequiredInputs[oid] = struct{}{}
			}
		}

		// All previously required inputs have been visited, but new required
		// inputs may have been picked up. Now need to scan those new required
		// inputs.
		requiredInputs = newRequiredInputs
	}

	// Build the final set. The requiredTxns are already sorted to be in the
	// correct order (per the input requirements) but the required parents were
	// constructed in reverse order, and therefore need to be reversed as they
	// are appended.
	for i := len(requiredParents)-1; i >= 0; i-- {
		requiredTxns = append(requiredTxns, requiredParents[i])
	}
	return requiredTxns, nil
}
