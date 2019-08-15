package transactionpool

// sharetxns.go is a file that ensures all of the transactions that we have are
// getting shared with all of our peers. If a new peer connects to the
// transaction pool, we should send them all of our transactions to ensure that
// they have a similar view of the network that we do. The expectation is that
// the peer will do the same, meaning that the transaction pools should quickly
// converge on having the same transactions.
//
// TODO: Add subscription support to the gateway, so that the tpool can be
// notified when peers are added and removed, instead of requiring the tpool to
// poll frequently for new peers.
//
// TODO: The transaction sets should be sent to new peers in order of their fee
// rate, instead of in the arbitrary order given by the map.

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// transactionSetSize will return the Sia encoding size of a transaction set.
func transactionSetSize(set []types.Transaction) (size int) {
	for _, txn := range set {
		size += txn.MarshalSiaSize()
	}
	return size
}

// managedBlockUntilOnlineAndSynced will block until siad is both online and
// synced. The function will return false if shutdown occurs before both
// conditions are met.
func (tp *TransactionPool) managedBlockUntilOnlineAndSynced() bool {
	// Infinite loop to keep checking online and synced status.
	for {
		// Three conditions need to be satisfied. The consensus package needs to
		// be synced, which can be checked by calling out to the consensus
		// package. We also need to check that the tpool package is synced,
		// meaning that the tpool has the most recent consensus change. And
		// finally, we need the gateway to assert that siad is online.
		consensusSynced := tp.consensusSet.Synced()
		tp.mu.Lock()
		tpoolSynced := tp.synced
		tp.mu.Unlock()
		online := tp.gateway.Online()
		if consensusSynced && tpoolSynced && online {
			return true
		}

		// Sleep for a bit before trying again, exit early if the transaction
		// pool is shutting down.
		select {
		case <-tp.tg.StopChan():
			return false
		case <-time.After(onlineSyncedLoopSleepTime):
			continue
		}
	}
}

// managedBroadcastTpoolToNewPeers takes a map of peers that have already
// received the full transaction pool and will use that to determine which peers
// have not yet received the full transaction pool. From there, the full
// transactoin pool will be broadcast to the new peers, and a new map will be
// returned which contains all of the new peers, and all of the old peers that
// had already been broadcasted to that are still online, but will not contain
// the old peers that were broadcasted to that we are no longer connected to.
func (tp *TransactionPool) managedBroadcastTpoolToNewPeers(finishedPeers map[modules.NetAddress]struct{}) map[modules.NetAddress]struct{} {
	// Get the current list of peers from the gateway. Scan through the
	// peers and determine whether it's a new unfinished peer or if it's a
	// finished peer.
	//
	// We use the map 'newFinishedPeers' so that we clear out all of the
	// peers from 'finishedPeers' that are actually no longer our peers.
	// After buildling the newFinishedPeers map, the finishedPeers map is
	// rotated out.
	currentPeers := tp.gateway.Peers()
	newFinishedPeers := make(map[modules.NetAddress]struct{})
	var unfinishedPeers []modules.Peer
	for _, peer := range currentPeers {
		_, exists := finishedPeers[peer.NetAddress]
		if exists {
			newFinishedPeers[peer.NetAddress] = struct{}{}
		} else {
			unfinishedPeers = append(unfinishedPeers, peer)
		}
	}
	finishedPeers = newFinishedPeers

	// Get the set of transactions that will be sent to the new peers.
	tp.mu.Lock()
	transactionSetIDs := make([]modules.TransactionSetID, 0, len(tp.transactionSets))
	transactionSets := make([][]types.Transaction, 0, len(tp.transactionSets))
	for id, set := range tp.transactionSets {
		transactionSetIDs = append(transactionSetIDs, id)
		transactionSets = append(transactionSets, set)
	}
	tp.mu.Unlock()

	// Broadcast each transaction set to each peer, self-ratelimiting to avoid
	// slamming our peers with transactions.
	//
	// TODO: Change the broadcast to send the sets from highest fee rate to
	// lowest fee rate.
	for i, set := range transactionSets {
		tp.mu.Lock()
		_, exists := tp.transactionSets[transactionSetIDs[i]]
		tp.mu.Unlock()
		if !exists {
			continue
		}

		// Determine the amount of time that needs to pass between each
		// broadcast according to the ratelimit.
		setSize := transactionSetSize(set)
		sleepBetweenBroadcasts := time.Duration(setSize) * newPeerBroadcastRateLimit
		nextSetChan := time.After(sleepBetweenBroadcasts)

		// Perform the broadcast and then sleep before continuing.
		tp.gateway.Broadcast("RelayTransactionSet", set, unfinishedPeers)
		select {
		case <-tp.tg.StopChan():
			return finishedPeers
		case <-nextSetChan:
		}
	}

	// Add all of the peers to the finished set of peers and return the updated
	// set.
	for _, peer := range unfinishedPeers {
		finishedPeers[peer.NetAddress] = struct{}{}
	}
	return finishedPeers
}

// threadedShareTransactions will perpetually loop, polling the gateway for new
// peers. If a new peer is found, that peer will be sent the full list of
// transactions in the tpool.
func (tp *TransactionPool) threadedShareTransactions() {
	err := tp.tg.Add()
	if err != nil {
		return
	}
	defer tp.tg.Done()

	// A map of peers that we are currently connected to and have also finished
	// syncing.
	finishedPeers := make(map[modules.NetAddress]struct{})

	// Infinite loop to keep checking for new peers.
	for {
		// Check for a stop signal.
		select {
		case <-tp.tg.StopChan():
			return
		default:
		}
		checkAgainChan := time.After(newPeerPollingFrequency)

		// Block until we are online and synced.
		if !tp.managedBlockUntilOnlineAndSynced() {
			return
		}

		// Broadcast the transaction pool contents to all new peers.
		finishedPeers = tp.managedBroadcastTpoolToNewPeers(finishedPeers)

		// Block until the timer says it is time to check again to avoid hogging
		// the CPU.
		select {
		case <-checkAgainChan:
			continue
		case <-tp.tg.StopChan():
			return
		}
	}
}
