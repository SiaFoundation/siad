package transactionpool

// shartxnsconsts.go contains the constants for the sharetxns subsystem.

import (
	"time"
)

const (
	// newPeerPollingFrequency is the amount of time that the gateway will sleep
	// between checking for new peers.
	newPeerPollingFrequency = 30 * time.Second

	// onlineSyncedLoopSleepTime is the amount of time that the tpool will
	// sleep between checks to see if it is online and synced.
	onlineSyncedLoopSleepTime = 30 * time.Second

	// newPeerBroadcastRatelimit is the amount of time per byte that we wait
	// between sending each of our transaction sets to a new peer.
	newPeerBroadcastRateLimit = 1000 * time.Nanosecond // 1000 nanoseconds per byte is 1 mbps per peer
)
