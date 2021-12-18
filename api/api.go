package api

import (
	"go.sia.tech/core/types"
)

// WalletBalance is the response to /wallet/balance. It contains the confirmed
// Siacoin and Siafund balance of the wallet.
type WalletBalance struct {
	Siacoins types.Currency `json:"siacoins"`
}

// WalletAddress is the response to /wallet/address.
type WalletAddress struct {
	Address types.Address `json:"address"`
}

// WalletAddresses is a list of addresses owned by the wallet.
type WalletAddresses []types.Address

// A WalletSignRequest is sent to the /wallet/sign endpoint to sign a transaction.
type WalletSignRequest struct {
	ToSign      []types.ElementID `json:"toSign"`
	Transaction types.Transaction `json:"transaction"`
}

// A WalletTransaction is a transaction in the wallet.
type WalletTransaction struct {
	Transaction types.Transaction `json:"transaction"`
}

// TxpoolBroadcastRequest is the request for the /txpool/broadcast endpoint.
// It contains the transaction to broadcast and the transactions that it
// depends on.
type TxpoolBroadcastRequest struct {
	DependsOn   []types.Transaction `json:"dependsOn"`
	Transaction types.Transaction   `json:"transaction"`
}

// A SyncerPeer is a unique peer that is being used by the syncer.
type SyncerPeer struct {
	NetAddress string `json:"netAddress"`
}

// A SyncerConnectRequest requests that the syncer connect to a peer.
type SyncerConnectRequest struct {
	NetAddress string `json:"netAddress"`
}
