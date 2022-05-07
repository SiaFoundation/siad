package explored

import (
	"time"

	"go.sia.tech/core/types"
)

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

// ConsensusTipResponse contains information about the current consensus state.
type ConsensusTipResponse struct {
	Index types.ChainIndex `json:"index"`

	TotalWork  types.Work    `json:"totalWork"`
	Difficulty types.Work    `json:"difficulty"`
	OakWork    types.Work    `json:"oakWork"`
	OakTime    time.Duration `json:"oakTime"`

	SiafundPool       types.Currency `json:"siafundPool"`
	FoundationAddress types.Address  `json:"foundationAddress"`
}

// A ExplorerSearchResponse contains information about an element.
type ExplorerSearchResponse struct {
	Type                string                    `json:"type"`
	SiacoinElement      types.SiacoinElement      `json:"siacoinElement"`
	SiafundElement      types.SiafundElement      `json:"siafundElement"`
	FileContractElement types.FileContractElement `json:"fileContractElement"`
}

// A ExplorerWalletBalanceResponse contains the confirmed Siacoin and Siafund balance of
// the wallet.
type ExplorerWalletBalanceResponse struct {
	Siacoins types.Currency `json:"siacoins"`
	Siafunds uint64         `json:"siafunds"`
}

// A ExplorerTransactionsRequest contains an address and the amount of
// transactions involving the address to request.
type ExplorerTransactionsRequest struct {
	Address types.Address `json:"address"`
	Amount  int           `json:"amount"`
	Offset  int           `json:"offset"`
}
