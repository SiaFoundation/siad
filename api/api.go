package api

import (
	"go.sia.tech/core/types"
)

type WalletBalance struct {
	Siacoins types.Currency `json:"siacoins"`
}

type WalletAddress struct {
	Address types.Address `json:"address"`
}

type WalletAddresses []types.Address

type WalletSignRequest struct {
	ToSign      []types.ElementID `json:"toSign"`
	Transaction types.Transaction `json:"transaction"`
}

type WalletTransaction struct {
	Transaction types.Transaction `json:"transaction"`
}

type TxpoolBroadcastRequest struct {
	DependsOn   []types.Transaction `json:"dependsOn"`
	Transaction types.Transaction   `json:"transaction"`
}

type SyncerPeer struct {
	NetAddress string `json:"netAddress"`
}

type SyncerConnectRequest struct {
	NetAddress string `json:"netAddress"`
}
