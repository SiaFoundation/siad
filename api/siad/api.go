package siad

import (
	"go.sia.tech/core/types"
)

// WalletBalanceResponse is the response to /wallet/balance. It contains the
// confirmed Siacoin and Siafund balance of the wallet.
type WalletBalanceResponse struct {
	Siacoins types.Currency `json:"siacoins"`
	Siafunds uint64         `json:"siafunds"`
}

// WalletAddressesResponse is the response to /wallet/addresses.
type WalletAddressesResponse []types.Address

// WalletUTXOsResponse is the set of unspent outputs owned by the wallet.
type WalletUTXOsResponse struct {
	Siacoins []types.SiacoinElement `json:"siacoins"`
	Siafunds []types.SiafundElement `json:"siafunds"`
}

// TxpoolBroadcastRequest is the request for the /txpool/broadcast endpoint.
// It contains the transaction to broadcast and the transactions that it
// depends on.
type TxpoolBroadcastRequest struct {
	DependsOn   []types.Transaction `json:"dependsOn"`
	Transaction types.Transaction   `json:"transaction"`
}

// A SyncerPeerResponse is a unique peer that is being used by the syncer.
type SyncerPeerResponse struct {
	NetAddress string `json:"netAddress"`
}

// A SyncerConnectRequest requests that the syncer connect to a peer.
type SyncerConnectRequest struct {
	NetAddress string `json:"netAddress"`
}
