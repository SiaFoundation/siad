package hostd

import (
	"go.sia.tech/core/types"
)

// ConsensusTipResponse is the response to /consensus/tip.
type ConsensusTipResponse struct {
	ID     types.BlockID `json:"blockID"`
	Height uint64        `json:"height"`
}

// WalletBalanceResponse is the response to /wallet/balance.
type WalletBalanceResponse struct {
	Siacoins types.Currency `json:"siacoins"`
}

// A SyncerPeerResponse is a unique peer that is being used by the syncer.
type SyncerPeerResponse struct {
	NetAddress string `json:"netAddress"`
}

// A SyncerConnectRequest requests that the syncer connect to a peer.
type SyncerConnectRequest struct {
	NetAddress string `json:"netAddress"`
}
