package renterd

import (
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/hostdb"
)

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

// An RHPScanRequest contains the address and pubkey of the host to scan.
type RHPScanRequest struct {
	NetAddress string          `json:"netAddress"`
	HostKey    types.PublicKey `json:"hostKey"`
}

// An RHPFormRequest requests that the host create a contract.
type RHPFormRequest struct {
	RenterKey    types.PrivateKey `json:"renterKey"`
	NetAddress   string           `json:"netAddress"`
	HostKey      types.PublicKey  `json:"hostKey"`
	HostFunds    types.Currency   `json:"hostFunds"`
	RenterFunds  types.Currency   `json:"renterFunds"`
	EndHeight    uint64           `json:"endHeight"`
	HostSettings rhp.HostSettings `json:"hostSettings"`
}

// An RHPFormResponse is the response to /rhp/form. It contains the formed
// contract and the parent transactions.
type RHPFormResponse struct {
	Contract rhp.Contract      `json:"contract"`
	Parent   types.Transaction `json:"parent"`
}

// An RHPRenewRequest requests that the host renew a contract.
type RHPRenewRequest struct {
	RenterKey      types.PrivateKey           `json:"renterKey"`
	NetAddress     string                     `json:"netAddress"`
	HostKey        types.PublicKey            `json:"hostKey"`
	RenterFunds    types.Currency             `json:"renterFunds"`
	HostCollateral types.Currency             `json:"hostCollateral"`
	Extension      uint64                     `json:"extension"`
	Contract       types.FileContractRevision `json:"contract"`
	HostSettings   rhp.HostSettings           `json:"hostSettings"`
}

// An RHPRenewResponse is the response to /rhp/renew. It contains the formed
// contract and the parent transaction.
type RHPRenewResponse struct {
	Contract rhp.Contract      `json:"contract"`
	Parent   types.Transaction `json:"parent"`
}

// An RHPReadRequest invokes the Read RPC on a host. The raw response is
// streamed to the client.
type RHPReadRequest struct {
	HostKey    types.PublicKey             `json:"hostKey"`
	NetAddress string                      `json:"netAddress"`
	ContractID types.ElementID             `json:"contractID"`
	RenterKey  types.PrivateKey            `json:"renterKey"`
	MaxPrice   types.Currency              `json:"maxPrice"`
	Sections   []rhp.RPCReadRequestSection `json:"sections"`
}

// An RHPAppendRequest invokes the Write RPC on a host, appending one sector to
// the contract. The sector data should be included alongside this request
// object as a multipart form.
type RHPAppendRequest struct {
	HostKey    types.PublicKey  `json:"hostKey"`
	NetAddress string           `json:"netAddress"`
	ContractID types.ElementID  `json:"contractID"`
	RenterKey  types.PrivateKey `json:"renterKey"`
	MaxPrice   types.Currency   `json:"maxPrice"`
}

// An RHPAppendResponse is the response to /rhp/append. It contains the Merkle
// root of the appended sector.
type RHPAppendResponse struct {
	SectorRoot types.Hash256 `json:"sectorRoot"`
}

// A HostDBScoreRequest sets the score of a host.
type HostDBScoreRequest struct {
	Score float64 `json:"score"`
}

// A HostDBInteractionRequest records interactions with a host.
type HostDBInteractionRequest struct {
	Interactions []hostdb.Interaction `json:"interaction"`
}
