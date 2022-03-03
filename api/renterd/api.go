package renterd

import (
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
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

// A ContractsScanRequest contains the information of the host we are dialing
// for contract related requests.
type ContractsScanRequest struct {
	NetAddress string          `json:"netAddress"`
	HostKey    types.PublicKey `json:"hostKey"`
}

// A ContractsFormRequest requests that the host create a contract.
type ContractsFormRequest struct {
	RenterKey    types.PrivateKey `json:"renterKey"`
	NetAddress   string           `json:"netAddress"`
	HostKey      types.PublicKey  `json:"hostKey"`
	HostFunds    types.Currency   `json:"hostFunds"`
	RenterFunds  types.Currency   `json:"renterFunds"`
	EndHeight    uint64           `json:"endHeight"`
	HostSettings rhp.HostSettings `json:"hostSettings"`
}

// A ContractsFormResponse is the response to /contract/form. It contains the
// formed contract and the parent transactions.
type ContractsFormResponse struct {
	Contract rhp.Contract      `json:"contract"`
	Parent   types.Transaction `json:"parent"`
}

// A ContractsRenewResponse is the response to /contract/renew. It contains the
// formed contract and the parent transactions.
type ContractsRenewResponse struct {
	Contract rhp.Contract      `json:"contract"`
	Parent   types.Transaction `json:"parent"`
}

// A ContractsRenewRequest requests that the host renew a contract.
type ContractsRenewRequest struct {
	RenterKey      types.PrivateKey           `json:"renterKey"`
	NetAddress     string                     `json:"netAddress"`
	HostKey        types.PublicKey            `json:"hostKey"`
	RenterFunds    types.Currency             `json:"renterFunds"`
	HostCollateral types.Currency             `json:"hostCollateral"`
	Extension      uint64                     `json:"extension"`
	Contract       types.FileContractRevision `json:"contract"`
	HostSettings   rhp.HostSettings           `json:"hostSettings"`
}
