package api

import (
	"go.sia.tech/core/net/gateway"

	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/wallet"
)

type WalletBalance struct {
	Siacoins types.Currency `json:"siacoins"`
}

type WalletAddress struct {
	Address types.Address `json:"address"`
}

type WalletAddresses []types.Address

type WalletSignData struct {
	ToSign      []types.ElementID `json:"toSign"`
	Transaction types.Transaction `json:"transaction"`
}

type WalletTransaction struct {
	Transaction types.Transaction `json:"transaction"`
}

type WalletTransactions []wallet.Transaction

type TxpoolTransactions []types.Transaction

type SyncerPeers []gateway.Header

type SyncerAddress struct {
	Address string `json:"address"`
}
