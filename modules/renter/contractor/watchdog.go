package contractor

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// Key Assumptions:
//
// Contracts are removed from watchdog state after their storage proof window
// has closed. The assumption here is that contracts are generally formed such
// that by the time the window has closed, it is both extremely unlikely that
// the original file contract transaction or any revisions will be re-orged out
// and it is also irrelevant to the renter by that point in time because they
// will already have started treating the contract as expired. We also note that
// the watchdog does *not* check if storage proofs are re-orged out.  If a host
// has ever submitted a valid storage proof, then from the renter's point of
// view they have fulfilled their obligation for the contract.
//
// TODOs:
// - Perform action when storage proof is found and when missing at the end of
//   the window.
//
// - When creating sweep transaction, add parent transactions if the renter's
//   own dependencies are causing this to be triggered.

type watchdog struct {
	// contracts stores all contract metadata used by the watchdog for any
	// contract it is watching. Any contract that is being watched must have data
	// in here.
	contracts map[types.FileContractID]*fileContractStatus

	// outputDependencies maps Siacoin outputs to the file contracts that are
	// dependent on them. When a contract is first submitted to the watchdog to be
	// monitored, the outputDependencies created for that contract are the
	// confirmed Siacoin outputs used to create the file contract transaction set.
	// The inverse mapping can be created on demand for any given file contract
	// using the corresponding (updated)formationTxnSet. This map is used to check
	// for double-spends on inputs used to form a contract.
	outputDependencies map[types.SiacoinOutputID]map[types.FileContractID]struct{}

	// The watchdog uses the renewWindow to compute the first blockheight (start
	// of storage proof window minus renewWindow) at which the watchdog will
	// broadcast the most recent revision if it hasn't seen it on chain yet.
	renewWindow types.BlockHeight
	blockHeight types.BlockHeight

	tpool      transactionPool
	staticDeps modules.Dependencies
	contractor *Contractor

	mu sync.Mutex
}

// fileContractStatus holds all the metadata the watchdog needs to monitor a file
// contract.
type fileContractStatus struct {
	// formationSweepHeight is the blockheight by which the watchdog expects to
	// find the contract on-chain. Up until this height, if contract is not yet
	// found the watchdog will rebbroadcast the formationTxnSet. After this height
	// the watchdog will attempt to sweep its inputs and abandon this contract
	formationSweepHeight types.BlockHeight
	contractFound        bool
	revisionFound        uint64            // store the revision number found
	storageProofFound    types.BlockHeight // store the blockheight at which the proof was found.

	// While watching for contract formation, the watchdog may periodically
	// rebroadcast the initial file contract transaction and unconfirmed parent
	// transactions. Any transactions in the original txn set that have been found
	// on-chain are removed from this set. If a Siacoin output that this file
	// contract depends on is re-orged out, then the transaction that creates that
	// output is added to the set.
	formationTxnSet []types.Transaction

	// parentOutputs stores SiacoinOutputID of any output which this file contract
	// was ever dependent on. It is initialized with the parent outputs from the
	// formationTxnSet but may grow as transactions are added or pruned from the
	// formationTxnSet.
	parentOutputs map[types.SiacoinOutputID]struct{}

	// In case its been too long since the contract was supposed to form and the
	// initial set has yet to appear on-chain, the watchdog is also prepared to
	// double spend the inputs used by the contractor to create the contract with
	// a higher fee-rate if necessary. It does so by extending the sweepTxn.
	sweepTxn     types.Transaction
	sweepParents []types.Transaction

	// Store the storage proof window start and end heights.
	windowStart types.BlockHeight
	windowEnd   types.BlockHeight
}

// monitorContractArgs defines the arguments passed to callMonitorContract.
type monitorContractArgs struct {
	// recovered indicates that the contract has been recovered, and that it
	// doesn't need to be monitored for formation.
	recovered bool

	fcID            types.FileContractID
	revisionTxn     types.Transaction
	formationTxnSet []types.Transaction
	sweepTxn        types.Transaction
	sweepParents    []types.Transaction
	blockHeight     types.BlockHeight
}

// newWatchdog creates a new watchdog.
func newWatchdog(contractor *Contractor) *watchdog {
	renewWindow := contractor.Allowance().RenewWindow
	return &watchdog{
		contracts:          make(map[types.FileContractID]*fileContractStatus),
		outputDependencies: make(map[types.SiacoinOutputID]map[types.FileContractID]struct{}),

		renewWindow: renewWindow,
		blockHeight: contractor.blockHeight,

		tpool:      contractor.tpool,
		staticDeps: contractor.staticDeps,
		contractor: contractor,
	}
}

// ContractStatus returns the status of a contract in the watchdog. If the
// contract has been double-spent, the fields other than DoubleSpendHeight are
// not up-to-date.
func (c *Contractor) ContractStatus(fcID types.FileContractID) (modules.ContractWatchStatus, bool) {
	c.mu.RLock()
	height, doubleSpent := c.doubleSpentContracts[fcID]
	c.mu.RUnlock()

	// double-spent contracts are no longer accounted for in the watchdog's
	// internal state so the only meaningful information we can give is that it
	// was double-spent.
	if doubleSpent {
		return modules.ContractWatchStatus{
			DoubleSpendHeight: height,
		}, true
	}
	return c.staticWatchdog.managedContractStatus(fcID)
}

// callAllowanceUpdated informs the watchdog of an allowance change.
func (w *watchdog) callAllowanceUpdated(a modules.Allowance) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Set the new renewWindow.
	w.renewWindow = a.RenewWindow
}

// callMonitorContract tells the watchdog to monitor the blockchain for data
// relevant to the given contract.
func (w *watchdog) callMonitorContract(args monitorContractArgs) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.contractor.log.Debugln("callMonitorContract", args.fcID)

	if _, ok := w.contracts[args.fcID]; ok {
		w.contractor.log.Println("watchdog asked to watch contract it already knowns: ", args.fcID)
		return errAlreadyWatchingContract
	}

	if len(args.revisionTxn.FileContractRevisions) == 0 {
		w.contractor.log.Println("No revisions in revisiontxn", args)
		return errors.New("no revision in moinitor contract args")
	}

	// Sanity check on non-recovery monitoring.
	if !args.recovered {
		saneInputs := len(args.formationTxnSet) != 0
		saneInputs = saneInputs && len(args.sweepTxn.SiacoinInputs) != 0
		saneInputs = saneInputs && args.blockHeight != 0
		if !saneInputs {
			w.contractor.log.Critical("Bad args given for contract: ", args)
			return errors.New("bad args for non recovered contract")
		}
	}

	fileContractStatus := &fileContractStatus{
		// this height is meaningless for a recovered contract, but will be set to
		// something reasonable if the formation transaction is reorged out.
		formationSweepHeight: args.blockHeight + waitTime,
		formationTxnSet:      args.formationTxnSet,
		contractFound:        args.recovered,
		parentOutputs:        make(map[types.SiacoinOutputID]struct{}),
		sweepTxn:             args.sweepTxn,
		sweepParents:         args.sweepParents,
		windowStart:          args.revisionTxn.FileContractRevisions[0].NewWindowStart,
		windowEnd:            args.revisionTxn.FileContractRevisions[0].NewWindowEnd,
	}
	w.contracts[args.fcID] = fileContractStatus

	// Watch the parent outputs of this set.
	outputDependencies := getParentOutputIDs(args.formationTxnSet)
	for _, oid := range outputDependencies {
		w.addOutputDependency(oid, args.fcID)
	}

	w.contractor.log.Debugln("Monitoring contract: ", args.fcID)
	return nil
}

// callSendMostRecentRevision sends the most recent revision transaction out.
// Should be called whenever a contract is no longer going to be used.
func (w *watchdog) callSendMostRecentRevision(metadata modules.RenterContract) {
	fcID := metadata.ID
	lastRevisionTxn := metadata.Transaction
	lastRevNum := lastRevisionTxn.FileContractRevisions[0].NewRevisionNumber

	debugStr := fmt.Sprintf("sending most recent revision txn for contract with id: %s revNum: %d", fcID.String(), lastRevNum)
	w.sendTxnSet([]types.Transaction{lastRevisionTxn}, debugStr)
}

// callScanConsensusChange scans applied and reverted blocks, updating the
// watchdog's state with all information relevant to monitored contracts.
func (w *watchdog) callScanConsensusChange(cc modules.ConsensusChange) {
	for _, block := range cc.RevertedBlocks {
		w.blockHeight--
		w.managedScanRevertedBlock(block)
	}

	for _, block := range cc.AppliedBlocks {
		w.blockHeight++
		w.managedScanAppliedBlock(block)
	}
}

// sendTxnSet broadcasts a transaction set and logs errors that are not
// duplicate transaction errors. (This is because the watchdog may be
// overzealous in sending out transactions).
func (w *watchdog) sendTxnSet(txnSet []types.Transaction, reason string) {
	w.contractor.log.Debugln("Sending txn set to tpool", reason)
	if w.staticDeps.Disrupt("DisableWatchdogBroadcast") {
		w.contractor.log.Debugln("(Watchdog Broadcast Disrupted)")
		return
	}

	// Send the transaction set in a go-routine to avoid deadlock when this
	// sendTxnSet is called within ProcessConsensusChange.
	go func() {
		err := w.tpool.AcceptTransactionSet(txnSet)
		if err != nil && err != modules.ErrDuplicateTransactionSet {
			w.contractor.log.Println("watchdog send transaction error: "+reason, err)
		}
	}()
}

// deleteContract removes all data about this contract from the watchdog.
func (w *watchdog) deleteContract(fcID types.FileContractID) {
	w.contractor.log.Debugln("Deleting contract: ", fcID)
	contractData, ok := w.contracts[fcID]
	if ok {
		for oid := range contractData.parentOutputs {
			w.removeOutputDependency(oid, fcID)
		}
	}
	delete(w.contracts, fcID)
}

// addOutputDependency marks the contract with fcID as dependent on this Siacoin
// output.
func (w *watchdog) addOutputDependency(outputID types.SiacoinOutputID, fcID types.FileContractID) {
	dependentFCs, ok := w.outputDependencies[outputID]
	if !ok {
		dependentFCs = make(map[types.FileContractID]struct{})
	}
	dependentFCs[fcID] = struct{}{}
	w.outputDependencies[outputID] = dependentFCs

	// Add the dependencies into the contract metadata also.
	contractData := w.contracts[fcID]
	contractData.parentOutputs[outputID] = struct{}{}
}

// removeOutputDependency removes the given SiacoinOutput from the dependencies
// of this file contract.
func (w *watchdog) removeOutputDependency(outputID types.SiacoinOutputID, fcID types.FileContractID) {
	dependentFCs, ok := w.outputDependencies[outputID]
	if !ok {
		w.contractor.log.Debugf("unable to remove output dependency: outputID not found in outputDependencies: outputID: %s", crypto.Hash(outputID).String())
		return
	}

	_, foundContract := dependentFCs[fcID]
	if !foundContract {
		w.contractor.log.Debugf("unable to remove output dependency: FileContract not marked in outputDependencies: fcID: %s, outputID: %s", crypto.Hash(fcID).String(), crypto.Hash(outputID).String())
		return
	}

	if len(dependentFCs) == 1 {
		// If this is the only file contract dependent on that output, delete the
		// whole set.
		delete(w.outputDependencies, outputID)
	} else {
		delete(dependentFCs, fcID)
	}
}

// getParentOutputIDs returns the IDs of the parent SiacoinOutputs used in the
// transaction set. That is, it returns the SiacoinOutputs that this transaction
// set is dependent on.
func getParentOutputIDs(txnSet []types.Transaction) []types.SiacoinOutputID {
	// Create a map of created and spent outputs. The parent outputs are those
	// that are spent but not created in this set.
	createdOutputs := make(map[types.SiacoinOutputID]bool)
	spentOutputs := make(map[types.SiacoinOutputID]bool)
	for _, txn := range txnSet {
		for _, scInput := range txn.SiacoinInputs {
			spentOutputs[scInput.ParentID] = true
		}
		for i := range txn.SiacoinOutputs {
			createdOutputs[txn.SiacoinOutputID(uint64(i))] = true
		}
	}

	// Remove all intermediary outputs that were created in the set from the set
	// of spentOutputs.
	parentOutputs := make([]types.SiacoinOutputID, 0)
	for id := range spentOutputs {
		if !createdOutputs[id] {
			parentOutputs = append(parentOutputs, id)
		}
	}

	return parentOutputs
}

// removeTxnFromSet is a helper function used to create a standalone-valid
// transaction set by removing confirmed transactions from a transaction set. If
// the transaction meant to be removed is not present in the set and error is
// returned, otherwise a new transaction set is returned that no longer contains
// that transaction.
func removeTxnFromSet(txn types.Transaction, txnSet []types.Transaction) ([]types.Transaction, error) {
	txnID := txn.ID()

	for i, txnFromSet := range txnSet {
		if txnFromSet.ID() == txnID {
			// Create the new set without the txn.
			newSet := append(txnSet[:i], txnSet[i+1:]...)
			return newSet, nil
		}
	}

	// Since this function is called when some parent inputs of the txnSet are
	// spent, this error indicates that the txn given double-spends a txn from
	// the set.
	return nil, errTxnNotInSet
}

// managedScanAppliedBlock updates the watchdog's state with data from a newly
// connected block. It checks for contracts, revisions, and proofs of monitored
// contracts and also for double-spends of any outputs which monitored contracts
// depend on.
func (w *watchdog) managedScanAppliedBlock(block types.Block) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, txn := range block.Transactions {
		for i := range txn.FileContracts {
			fcID := txn.FileContractID(uint64(i))
			if contractData, ok := w.contracts[fcID]; ok {
				contractData.contractFound = true
				w.contractor.log.Debugln("Found contract: ", fcID)
			}
		}

		for _, rev := range txn.FileContractRevisions {
			if contractData, ok := w.contracts[rev.ParentID]; ok {
				contractData.revisionFound = rev.NewRevisionNumber
				w.contractor.log.Debugln("Found revision for: ", rev.ParentID, rev.NewRevisionNumber)
			}
		}

		for _, storageProof := range txn.StorageProofs {
			if contractData, ok := w.contracts[storageProof.ParentID]; ok {
				contractData.storageProofFound = w.blockHeight
				w.contractor.log.Debugln("Found storage proof: ", storageProof.ParentID)
			}
		}
		// Check the transaction for spends of any inputs a monitored file contract
		// depends on.
		w.findDependencySpends(txn)
	}
}

// findDependencySpends checks the transactions from a newly applied block to
// see if it spends outputs which monitored contracts are dependent on. If so,
// the function prunes the formationTxnSet for that contract and updates its
// dependencies.
func (w *watchdog) findDependencySpends(txn types.Transaction) {
	// This transaction could be double-spending inputs used across multiple
	// file contracts. Keep track of those here.
	inputsSpent := make(map[types.FileContractID]struct{})
	spendsMonitoredOutput := false
	for _, scInput := range txn.SiacoinInputs {
		// Check if the output spent here is a parent of any contract being
		// monitored.
		fcIDs, watching := w.outputDependencies[scInput.ParentID]
		if !watching {
			continue
		}

		for fcID := range fcIDs {
			// If we found the contract already, then this output must be spent in the
			// formation txn set. Otherwise we must check if this transaction
			// double-spends any inputs for the formation transaction set.
			_, ok := w.contracts[fcID]
			if !ok {
				w.contractor.log.Critical("Found dependency on un-monitored formation")
				continue
			}
			spendsMonitoredOutput = true
			inputsSpent[fcID] = struct{}{}
		}
	}

	if !spendsMonitoredOutput {
		return
	}

	// Try removing this transaction from the formation transaction set, and
	// check if the pruned formation transaction set is still considered valid.
	for fcID := range inputsSpent {
		// Some transactions from this contract's formation set may have already
		// been pruned. If so, use the most recent set.
		contractData := w.contracts[fcID]
		txnSet := contractData.formationTxnSet

		// Try removing this transaction from the set.
		prunedFormationTxnSet, err := removeTxnFromSet(txn, txnSet)
		if err != nil {
			w.contractor.log.Debugln("Error removing txn from set, inputs were double-spent:", err, fcID)

			//  Signal to the contractor that this contract's inputs were
			//  double-spent and that it should be removed.
			w.deleteContract(fcID)
			go w.contractor.callNotifyDoubleSpend(fcID, w.blockHeight)
		} else {
			w.contractor.log.Debugln("Removed transaction from set for: ", fcID, len(prunedFormationTxnSet))
			contractData.formationTxnSet = prunedFormationTxnSet

			// Add the outputs created by the pruned transaction as dependencies.
			for i := range txn.SiacoinOutputs {
				w.addOutputDependency(txn.SiacoinOutputID(uint64(i)), fcID)
			}
			// Remove inputs used by the pruned transaction as dependencies. They are
			// no longer needed because the outputs we just added will be sufficient
			// for tracking the file contract's dependency on this transaction.
			for _, input := range txn.SiacoinInputs {
				w.removeOutputDependency(input.ParentID, fcID)
			}
		}
	}
}

// managedScanRevertedBlock updates the watchdog's state with data from a newly
// reverted block. It checks for the removal of contracts, revisions, and proofs
// of monitored contracts and also for the creation of any new dependencies for
// monitored formation transaction sets.
func (w *watchdog) managedScanRevertedBlock(block types.Block) {
	w.contractor.log.Debugln("processing reverted block at height: ", w.blockHeight)
	w.mu.Lock()
	defer w.mu.Unlock()

	outputsCreatedInBlock := make(map[types.SiacoinOutputID]*types.Transaction)
	for i := 0; i < len(block.Transactions); i++ {
		txn := &block.Transactions[i]
		for i := range txn.SiacoinOutputs {
			outputsCreatedInBlock[txn.SiacoinOutputID(uint64(i))] = txn
		}

		for i := range txn.FileContracts {
			fcID := txn.FileContractID(uint64(i))
			// After a blockchain reorg, it's possible that a contract that used to be on
			// the active chain is no longer in the new active chain. To make sure all
			// active contracts are actually committed to on-chain, the watchdog keeps track
			// of any contracts removed during a reorg. If they have not re-appeared and the
			// contractor is synced then the watchdog must re-broadcast the file contract's
			// transaction.
			contractData, ok := w.contracts[fcID]
			if !ok {
				continue
			}

			w.contractor.log.Println("Contract formation txn reverted: ", fcID)
			contractData.contractFound = false

			// Set watchheight to max(current watch height,  current height + leeway)
			if contractData.formationSweepHeight < w.blockHeight+reorgLeeway {
				contractData.formationSweepHeight = w.blockHeight + reorgLeeway
			}

			// Sanity check: if the contract was previously confirmed, it should have
			// been removed from the formationTxnSet.
			if len(contractData.formationTxnSet) != 0 {
				w.contractor.log.Critical("found reverted contract with non-empty formationTxnSet in watchdog", fcID)
			}

			// Re-add the file contract transaction to the formationTxnSet.
			contractData.formationTxnSet = []types.Transaction{*txn}
			outputDependencies := getParentOutputIDs(contractData.formationTxnSet)
			for _, oid := range outputDependencies {
				w.addOutputDependency(oid, fcID)
			}
		}

		for _, rev := range txn.FileContractRevisions {
			if contractData, ok := w.contracts[rev.ParentID]; ok {
				w.contractor.log.Debugln("Revision for monitored contract reverted: ", rev.ParentID, rev.NewRevisionNumber)
				contractData.revisionFound = 0 // There are no zero revisions.
			}
		}

		for _, storageProof := range txn.StorageProofs {
			if contractData, ok := w.contracts[storageProof.ParentID]; ok {
				w.contractor.log.Debugln("Storage proof for monitored contract reverted: ", storageProof.ParentID)
				contractData.storageProofFound = 0
			}
		}
	}
	w.updateDependenciesFromRevertedBlock(outputsCreatedInBlock)
}

// updateDependenciesFromRevertedBlock checks all created outputs in a reverted
// block to see if any monitored formation transactions are dependent on them.
// If so, the watchdog adds the reverted transaction creating that output as a
// dependency for that file contract.
func (w *watchdog) updateDependenciesFromRevertedBlock(createdOutputs map[types.SiacoinOutputID]*types.Transaction) {
	// Create a queue of outputs to check.
	outputQueue := make([]types.SiacoinOutputID, 0)
	outputsInQueue := make(map[types.SiacoinOutputID]struct{})

	// Helper function that adds outputs spent in this transaction to the queue,
	// if they are not already in it.
	addParentOutputsToQueue := func(txn *types.Transaction) {
		for _, scInput := range txn.SiacoinInputs {
			_, outputCreatedInBlock := createdOutputs[scInput.ParentID]
			_, inQueue := outputsInQueue[scInput.ParentID]
			if !inQueue && outputCreatedInBlock {
				outputQueue = append(outputQueue, scInput.ParentID)
				outputsInQueue[scInput.ParentID] = struct{}{}
			}
		}
	}

	// Populate the queue first by checking all outputs once.
	for outputID, txn := range createdOutputs {
		// Check if any file contracts being monitored by the watchdog have this
		// output as a dependency.
		dependentFCs, watching := w.outputDependencies[outputID]
		if !watching {
			continue
		}
		// Add the new dependencies to file contracts dependent on this output.
		for fcID := range dependentFCs {
			w.contractor.log.Debugln("Adding dependency to file contract:", fcID, txn.ID())
			w.addDependencyToContractFormationSet(fcID, *txn)
		}
		// Queue up the parent outputs so that we can check if they are adding new
		// dependencies as well.
		addParentOutputsToQueue(txn)
	}

	// Go through all the new dependencies in the queue.
	var outputID types.SiacoinOutputID
	for len(outputQueue) > 0 {
		// Pop on output ID off the queue.
		outputID, outputQueue = outputQueue[0], outputQueue[1:]
		txn := createdOutputs[outputID]

		// Check if any file contracts being monitored by the watchdog have this
		// output as a dependency.
		dependentFCs, watching := w.outputDependencies[outputID]
		if !watching {
			continue
		}
		// Add the new dependencies to file contracts dependent on this output.
		for fcID := range dependentFCs {
			w.contractor.log.Debugln("Adding dependency to file contract:", fcID, txn.ID())
			w.addDependencyToContractFormationSet(fcID, *txn)
		}
		// Queue up the parent outputs so that we can check if they are adding new
		// dependencies as well.
		addParentOutputsToQueue(txn)

		// Remove from outputsInQueue map at end of function, in order to not re-add
		// the same output.
		delete(outputsInQueue, outputID)
	}
}

// addDependencyToContractFormationSet adds a tranasaction to a contract's
// formationTransactionSet, if it is not already in that set. It also adds
// the outputs spent in that transaction as dependencies for this file contract.
func (w *watchdog) addDependencyToContractFormationSet(fcID types.FileContractID, txn types.Transaction) {
	contractData := w.contracts[fcID]
	txnSet := contractData.formationTxnSet

	txnID := txn.ID()
	for _, existingTxn := range contractData.formationTxnSet {
		// Don't add duplicate transactions.
		if txnID == existingTxn.ID() {
			return
		}
	}

	// Add this transaction as a new dependency.
	// NOTE: Dependencies must be prepended to maintain correct ordering.
	txnSet = append([]types.Transaction{txn}, txnSet...)
	contractData.formationTxnSet = txnSet

	// Add outputs as dependencies to this file contract.
	for _, scInput := range txn.SiacoinInputs {
		w.addOutputDependency(scInput.ParentID, fcID)
	}
}

// managedCheckContracts checks if the watchdog needs to take any actions for
// any contracts its watching at this blockHeight.  For newly formed contracts,
// it checks if a contract has been seen on-chain yet, if not the watchdog will
// re-broadcast the initial transaction. If enough time has elapsed the watchdog
// will double-spend the inputs used to create that file contract.
//
// The watchdog also checks if the latest revision for a file contract has been
// posted yet. If not, the watchdog will also re-broadcast that transaction.
//
// Finally, the watchdog checks if hosts' storage proofs made it on chain within
// their expiration window, and notifies the contractor of the storage proof
// status.
func (w *watchdog) managedCheckContracts() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.contractor.log.Debugln("Checking contracts", w.blockHeight)

	for fcID, contractData := range w.contracts {
		if !contractData.contractFound {
			w.checkUnconfirmedContract(fcID, contractData)
		}

		if (w.blockHeight >= contractData.windowStart-w.renewWindow) && (contractData.revisionFound != 0) {
			// Check if the most recent revision has appeared on-chain. If not send it
			// ourselves. Called in a go-routine because the contractor may be in
			// maintenance which can cause a deadlock because this function Acquires a
			// lock using the contractset.
			w.contractor.log.Debugln("Checking revision for monitored contract: ", fcID)
			go w.managedCheckMonitoredRevision(fcID, w.blockHeight)
		}

		if w.blockHeight >= contractData.windowEnd {
			if contractData.storageProofFound == 0 {
				// TODO: penalize host / send signal back to watchee
				w.contractor.log.Debugln("didn't find proof", fcID)
			} else {
				// TODO: ++ host / send signal back to watchee
				w.contractor.log.Debugln("did find proof", fcID)
			}
			w.deleteContract(fcID)
		}
	}
}

// checkUnconfirmedContract re-broadcasts the file contract formation
// transaction or sweeps the inputs used by the renter, depending on whether or
// not the transaction set has too many added dependencies or if the
// formationSweepHeight has been reached.
func (w *watchdog) checkUnconfirmedContract(fcID types.FileContractID, contractData *fileContractStatus) {
	// Check that the size of the formationTxnSet has not gone beyond the
	// standardness limit. If it has, then we won't be able to propagate it
	// anymore.
	var setSize int
	for _, txn := range contractData.formationTxnSet {
		setSize += txn.MarshalSiaSize()
	}
	if setSize > modules.TransactionSetSizeLimit {
		w.contractor.log.Println("UpdatedFormationTxnSet beyond set size limit", fcID)
	}

	if (w.blockHeight >= contractData.formationSweepHeight) || (setSize > modules.TransactionSetSizeLimit) {
		w.contractor.log.Debugln("Sweeping inputs: ", w.blockHeight, contractData.formationSweepHeight)
		// TODO: Add parent transactions if the renter's own dependencies are
		// causing this to be triggered.
		w.sweepContractInputs(fcID, contractData)
	} else {
		// Try to broadcast the transaction set again.
		debugStr := fmt.Sprintf("sending formation txn for contract with id: %s at h=%d wh=%d", fcID.String(), w.blockHeight, contractData.formationSweepHeight)
		w.contractor.log.Debugln(debugStr)
		w.sendTxnSet(contractData.formationTxnSet, debugStr)
	}
}

// managedCheckMonitoredRevision checks if the given FileContract has it latest
// revision posted on-chain. If not, the watchdog broadcasts the latest revision
// transaction itself.
func (w *watchdog) managedCheckMonitoredRevision(fcID types.FileContractID, height types.BlockHeight) {
	// Get the highest revision number seen by the watchdog for this FC.
	var revNumFound uint64
	w.mu.Lock()
	if contractData, ok := w.contracts[fcID]; ok {
		revNumFound = contractData.revisionFound
	}
	w.mu.Unlock()

	// Get the last revision transaction from the contractset / oldcontracts.
	var lastRevisionTxn types.Transaction
	contract, ok := w.contractor.staticContracts.Acquire(fcID)
	if ok {
		lastRevisionTxn = contract.Metadata().Transaction
		w.contractor.staticContracts.Return(contract)
	} else {
		w.contractor.log.Debugln("Unable to Acquire monitored contract from contractset", fcID)
		// Try old contracts. If the contract was renewed already it won't be in the
		// contractset.
		w.contractor.mu.RLock()
		contract, ok := w.contractor.oldContracts[fcID]
		if !ok {
			w.contractor.log.Debugln("Unable to Acquire monitored contract from oldContracts", fcID)
			w.contractor.mu.RUnlock()
			return
		}
		lastRevisionTxn = contract.Transaction
		w.contractor.mu.RUnlock()
	}

	lastRevNum := lastRevisionTxn.FileContractRevisions[0].NewRevisionNumber
	if lastRevNum > revNumFound {
		// NOTE: fee-bumping via CPFP (the watchdog will do this every block
		// until it sees the revision or the window has closed.)
		debugStr := fmt.Sprintf("sending revision txn for contract with id: %s revNum: %d", fcID.String(), lastRevNum)
		w.contractor.log.Debugln(debugStr)
		w.sendTxnSet([]types.Transaction{lastRevisionTxn}, debugStr)
	}
}

// sweepContractInputs spends the inputs used initially by the contractor
// for creating a file contract, and sends them to an address owned by
// this wallet.  This is done only if a file contract has not appeared on-chain
// in time.
// TODO: this function fails if the wallet is locked. since an alert is already
// broadcast to the user, it might be useful to retry a sweep once the wallet
// is unlocked.
func (w *watchdog) sweepContractInputs(fcID types.FileContractID, contractData *fileContractStatus) {
	sweepBuilder, err := w.contractor.wallet.RegisterTransaction(contractData.sweepTxn, contractData.sweepParents)
	if err != nil {
		w.contractor.log.Println("Unable to register sweep transaction")
		return
	}
	markedInputs := sweepBuilder.MarkWalletInputs()
	if !markedInputs {
		w.contractor.log.Println("sweepBuilder did not mark any owned inputs")
	}

	// Get the size of the transaction set so far for fee calculation.
	setSize := contractData.sweepTxn.MarshalSiaSize()
	for _, sweepParent := range contractData.sweepParents {
		setSize += sweepParent.MarshalSiaSize()
	}

	// Estimate a transaction fee and add it to the txn.
	_, maxFee := w.tpool.FeeEstimation()
	txnFee := maxFee.Mul64(uint64(setSize)) // Estimated transaction size in bytes
	sweepBuilder.AddMinerFee(txnFee)

	txn, _ := sweepBuilder.View()
	// There can be refund outputs, but the last output is the one that is used to
	// sweep.
	numOuts := len(txn.SiacoinOutputs)
	if numOuts == 0 {
		w.contractor.log.Println("expected at least 1 output in sweepTxn", len(txn.SiacoinOutputs))
		return
	}
	replacementOutput := types.SiacoinOutput{
		Value:      txn.SiacoinOutputs[numOuts-1].Value.Sub(txnFee),
		UnlockHash: txn.SiacoinOutputs[numOuts-1].UnlockHash,
	}
	err = sweepBuilder.ReplaceSiacoinOutput(uint64(numOuts-1), replacementOutput)
	if err != nil {
		w.contractor.log.Println("error replacing output in sweep")
		return
	}

	signedTxnSet, err := sweepBuilder.Sign(true)
	if err != nil {
		w.contractor.log.Println("unable to sign sweep txn", fcID)
		return
	}

	debugStr := fmt.Sprintf("SweepTxn for contract with id: %s", fcID.String())
	w.sendTxnSet(signedTxnSet, debugStr)
}

// managedContractStatus returns the status of a contract in the watchdog if it
// exists.
func (w *watchdog) managedContractStatus(fcID types.FileContractID) (contractStatus modules.ContractWatchStatus, ok bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	contractData, ok := w.contracts[fcID]
	if !ok {
		return contractStatus, false
	}

	contractStatus.FormationSweepHeight = contractData.formationSweepHeight
	contractStatus.LatestRevisionFound = contractData.revisionFound
	contractStatus.ContractFound = contractData.contractFound
	contractStatus.StorageProofFoundAtHeight = contractData.storageProofFound

	return contractStatus, true
}
