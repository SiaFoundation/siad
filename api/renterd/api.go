package renterd

import "go.sia.tech/core/types"

// WalletBalanceResponse is the response to /wallet/balance. It contains the confirmed
// Siacoin and Siafund balance of the wallet.
type WalletBalanceResponse struct {
	Siacoins types.Currency `json:"siacoins"`
	Siafunds uint64         `json:"siafunds"`
}

// WalletAddressResponse is the response to /wallet/address.
type WalletAddressResponse struct {
	Address types.Address `json:"address"`
}

// WalletAddressesResponse is a list of addresses owned by the wallet.
type WalletAddressesResponse []types.Address

// A WalletSignRequest is sent to the /wallet/sign endpoint to sign a transaction.
type WalletSignRequest struct {
	ToSign      []types.ElementID `json:"toSign"`
	Transaction types.Transaction `json:"transaction"`
}

// A WalletTransactionResponse is a transaction in the wallet.
type WalletTransactionResponse struct {
	Transaction types.Transaction `json:"transaction"`
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
