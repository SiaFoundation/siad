package transactionpool

import (
	"errors"
	"fmt"
	"strings"
	"time"

	bolt "github.com/coreos/bbolt"
	"gitlab.com/NebulousLabs/demotemutex"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/sync"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/Sia/types/typesutil"
)

var (
	errNilCS      = errors.New("transaction pool cannot initialize with a nil consensus set")
	errNilGateway = errors.New("transaction pool cannot initialize with a nil gateway")
)

type (
	// ObjectID is the ID of an object such as siacoin output and file
	// contracts, and is used to see if there is are conflicts or overlaps within
	// the transaction pool.
	ObjectID crypto.Hash

	// The TransactionPool tracks incoming transactions, accepting them or
	// rejecting them based on internal criteria such as fees and unconfirmed
	// double spends.
	TransactionPool struct {
		// Dependencies of the transaction pool.
		consensusSet modules.ConsensusSet
		gateway      modules.Gateway

		// To prevent double spends in the unconfirmed transaction set, the
		// transaction pool keeps a list of all objects that have either been
		// created or consumed by the current unconfirmed transaction pool. All
		// transactions with overlaps are rejected. This model is
		// over-aggressive - one transaction set may create an object that
		// another transaction set spends. This is done to minimize the
		// computation and memory load on the transaction pool. Dependent
		// transactions should be lumped into a single transaction set.
		//
		// transactionSetDiffs map form a transaction set id to the set of
		// diffs that resulted from the transaction set.
		knownObjects        map[ObjectID]modules.TransactionSetID
		subscriberSets      map[modules.TransactionSetID]*modules.UnconfirmedTransactionSet
		transactionHeights  map[types.TransactionID]types.BlockHeight
		transactionSets     map[modules.TransactionSetID][]types.Transaction
		transactionSetDiffs map[modules.TransactionSetID]*modules.ConsensusChange
		transactionListSize int

		// Variables related to the blockchain.
		blockHeight     types.BlockHeight
		recentMedians   []types.Currency
		recentMedianFee types.Currency // SC per byte

		// The consensus change index tracks how many consensus changes have
		// been sent to the transaction pool. When a new subscriber joins the
		// transaction pool, all prior consensus changes are sent to the new
		// subscriber.
		subscribers []modules.TransactionPoolSubscriber

		// Utilities.
		db         *persist.BoltDatabase
		dbTx       *bolt.Tx
		log        *persist.Logger
		mu         demotemutex.DemoteMutex
		tg         sync.ThreadGroup
		persistDir string
	}
)

// New creates a transaction pool that is ready to receive transactions.
func New(cs modules.ConsensusSet, g modules.Gateway, persistDir string) (*TransactionPool, error) {
	// Check that the input modules are non-nil.
	if cs == nil {
		return nil, errNilCS
	}
	if g == nil {
		return nil, errNilGateway
	}

	// Initialize a transaction pool.
	tp := &TransactionPool{
		consensusSet: cs,
		gateway:      g,

		knownObjects:        make(map[ObjectID]modules.TransactionSetID),
		subscriberSets:      make(map[modules.TransactionSetID]*modules.UnconfirmedTransactionSet),
		transactionHeights:  make(map[types.TransactionID]types.BlockHeight),
		transactionSets:     make(map[modules.TransactionSetID][]types.Transaction),
		transactionSetDiffs: make(map[modules.TransactionSetID]*modules.ConsensusChange),

		persistDir: persistDir,
	}

	// Open the tpool database.
	err := tp.initPersist()
	if err != nil {
		return nil, err
	}

	// Register RPCs
	g.RegisterRPC("RelayTransactionSet", tp.relayTransactionSet)
	tp.tg.OnStop(func() {
		tp.gateway.UnregisterRPC("RelayTransactionSet")
	})

	// Spin up a thread to periodically dump the tpool size. (debug mode)
	if build.DEBUG {
		go tp.threadedLogListSize()
	}

	return tp, nil
}

// Close releases any resources held by the transaction pool, stopping all of
// its worker threads.
func (tp *TransactionPool) Close() error {
	return tp.tg.Stop()
}

// FeeEstimation returns an estimation for what fee should be applied to
// transactions. It returns a minimum and maximum estimated fee per transaction
// byte.
func (tp *TransactionPool) FeeEstimation() (min, max types.Currency) {
	err := tp.tg.Add()
	if err != nil {
		return
	}
	defer tp.tg.Done()
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Use three methods to determine an acceptable fee. The first method looks
	// at what fee is required to get into a block on the blockchain based on
	// the actual fees of transactions confirmed in recent blocks. The second
	// method looks at the current tpool and performs fee estimation based on
	// the other transactions in the tpool. The third method is an absolute
	// minimum.

	// First method: use the median fees calculated while looking at
	// transactions that have been confirmed in the recent blocks.
	feeByBlockchain := tp.recentMedianFee

	// Second method: use the median fees calculated while looking at the
	// current size of the transaction pool. For the min fee, use a size that's
	// a fixed size larger than the current pool, and then also add some
	// proportional padding. The fixed size handles cases where the tpool is
	// really small, and a low number of transactions can move the fee
	// substantially. The proportional padding is for when the tpool is large
	// and there is a lot of activity which is adding to the tpool.
	//
	// The sizes for proportional and constant are computed independently, and
	// then the max is taken of the two.
	sizeAfterConstantPadding := tp.transactionListSize + feeEstimationConstantPadding
	sizeAfterProportionalPadding := int(float64(tp.transactionListSize) * float64(feeEstimationProportionalPadding))
	var feeByCurrentTpoolSize types.Currency
	if sizeAfterConstantPadding > sizeAfterProportionalPadding {
		feeByCurrentTpoolSize = requiredFeesToExtendTpoolAtSize(sizeAfterConstantPadding)
	} else {
		feeByCurrentTpoolSize = requiredFeesToExtendTpoolAtSize(sizeAfterProportionalPadding)
	}

	// Pick the larger of the first two methods to be compared with the third
	// method.
	if feeByBlockchain.Cmp(feeByCurrentTpoolSize) > 0 {
		min = feeByBlockchain
	} else {
		min = feeByCurrentTpoolSize
	}

	// Third method: ensure the fee is above an absolute minimum.
	if min.Cmp(minEstimation) < 0 {
		min = minEstimation
	}
	max = min.Mul64(maxMultiplier)
	return
}

// TransactionList returns a list of all transactions in the transaction pool.
// The transactions are provided in an order that can acceptably be put into a
// block.
func (tp *TransactionPool) TransactionList() []types.Transaction {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	var txns []types.Transaction
	for _, tSet := range tp.transactionSets {
		txns = append(txns, tSet...)
	}
	return txns
}

// Transaction returns the transaction with the provided txid, its parents, and
// a bool indicating if it exists in the transaction pool.
func (tp *TransactionPool) Transaction(id types.TransactionID) (types.Transaction, []types.Transaction, bool) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// find the transaction
	exists := false
	var txn types.Transaction
	var allParents []types.Transaction
	for _, tSet := range tp.transactionSets {
		for i, t := range tSet {
			if t.ID() == id {
				txn = t
				allParents = tSet[:i]
				exists = true
				break
			}
		}
	}

	// prune unneeded parents
	parentIDs := make(map[types.OutputID]struct{})
	addOutputIDs := func(txn types.Transaction) {
		for _, input := range txn.SiacoinInputs {
			parentIDs[types.OutputID(input.ParentID)] = struct{}{}
		}
		for _, fcr := range txn.FileContractRevisions {
			parentIDs[types.OutputID(fcr.ParentID)] = struct{}{}
		}
		for _, input := range txn.SiafundInputs {
			parentIDs[types.OutputID(input.ParentID)] = struct{}{}
		}
		for _, proof := range txn.StorageProofs {
			parentIDs[types.OutputID(proof.ParentID)] = struct{}{}
		}
		for _, sig := range txn.TransactionSignatures {
			parentIDs[types.OutputID(sig.ParentID)] = struct{}{}
		}
	}
	isParent := func(t types.Transaction) bool {
		for i := range t.SiacoinOutputs {
			if _, exists := parentIDs[types.OutputID(t.SiacoinOutputID(uint64(i)))]; exists {
				return true
			}
		}
		for i := range t.FileContracts {
			if _, exists := parentIDs[types.OutputID(t.SiacoinOutputID(uint64(i)))]; exists {
				return true
			}
		}
		for i := range t.SiafundOutputs {
			if _, exists := parentIDs[types.OutputID(t.SiacoinOutputID(uint64(i)))]; exists {
				return true
			}
		}
		return false
	}

	addOutputIDs(txn)
	var necessaryParents []types.Transaction
	for i := len(allParents) - 1; i >= 0; i-- {
		parent := allParents[i]

		if isParent(parent) {
			necessaryParents = append([]types.Transaction{parent}, necessaryParents...)
			addOutputIDs(parent)
		}
	}

	return txn, necessaryParents, exists
}

// Transactions returns the transactions of the transaction pool
func (tp *TransactionPool) Transactions() []types.Transaction {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	var txns []types.Transaction
	for _, set := range tp.transactionSets {
		txns = append(txns, set...)
	}
	return txns
}

// TransactionSet returns the transaction set the provided object appears in.
func (tp *TransactionPool) TransactionSet(oid crypto.Hash) []types.Transaction {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	// Define txns as to not use the memory that stores the actual map
	var txns []types.Transaction
	tSetID, exists := tp.knownObjects[ObjectID(oid)]
	if !exists {
		return nil
	}
	tSet, exists := tp.transactionSets[tSetID]
	if !exists {
		return nil
	}
	txns = append(txns, tSet...)
	return txns
}

// Broadcast broadcasts a transaction set to all of the transaction pool's
// peers.
func (tp *TransactionPool) Broadcast(ts []types.Transaction) {
	go tp.gateway.Broadcast("RelayTransactionSet", ts, tp.gateway.Peers())
}

// threadedLogListSize will periodically log the current size of the transaction
// pool.
func (tp *TransactionPool) threadedLogListSize() {
	err := tp.tg.Add()
	if err != nil {
		return
	}
	defer tp.tg.Done()

	// Infinite loop to keep printing the size of the tpool.
	for {
		// Soft sleep for 5 minutes between each log line.
		select {
		case <-tp.tg.StopChan():
			return
		case <-time.After(logSizeFrequency):
		}
		tp.mu.Lock()
		tp.log.Debugln("Current tpool size:", tp.transactionListSize)
		tp.mu.Unlock()
	}
}

// printConflicts prints the rejected transaction set and the transaction sets
// in the TransactionPool that it conflicts with using human-readable
// strings for each transaction.
func (tp *TransactionPool) printConflicts(ts []types.Transaction) {
	relatedObjects := relatedObjectIDs(ts)
	conflictSets := make(map[modules.TransactionSetID]bool)
	for _, oid := range relatedObjects {
		conflict, exists := tp.knownObjects[oid]
		if exists {
			conflictSets[conflict] = true
		}
	}

	latestBlock, _ := tp.consensusSet.BlockAtHeight(tp.blockHeight)
	logStr := fmt.Sprintf("Rejected transaction set with conflicts.\nBlockHeight: %d BlockID: %s\n", tp.blockHeight, latestBlock.ID())
	for _, txn := range ts {
		logStr += typesutil.SprintTxnWithObjectIDs(txn)
	}

	logStr += "\nPrinting conflict transaction sets:\n\n"
	for conflictSetID := range conflictSets {
		logStr += "ConflictSetID: " + crypto.Hash(conflictSetID).String()
		for _, txn := range tp.transactionSets[conflictSetID] {
			// Add an extra level of indentation to conflict set transactions.
			logStr += strings.Replace(typesutil.SprintTxnWithObjectIDs(txn), "\n", "\n\t", -1)
		}
	}
	tp.log.Println(logStr)
}
