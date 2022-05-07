package explored

import (
	"encoding/json"
	"fmt"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/explorer"
)

// A Client provides methods for interacting with a explored API server.
type Client struct {
	c api.Client
}

// TxpoolBroadcast broadcasts a transaction to the network.
func (c *Client) TxpoolBroadcast(txn types.Transaction, dependsOn []types.Transaction) (err error) {
	err = c.c.Post("/api/txpool/broadcast", TxpoolBroadcastRequest{dependsOn, txn}, nil)
	return
}

// TxpoolTransactions returns all transactions in the transaction pool.
func (c *Client) TxpoolTransactions() (resp []types.Transaction, err error) {
	err = c.c.Get("/api/txpool/transactions", &resp)
	return
}

// SyncerPeers returns the current peers of the syncer.
func (c *Client) SyncerPeers() (resp []SyncerPeerResponse, err error) {
	err = c.c.Get("/api/syncer/peers", &resp)
	return
}

// SyncerConnect adds the address as a peer of the syncer.
func (c *Client) SyncerConnect(addr string) (err error) {
	err = c.c.Post("/api/syncer/connect", addr, nil)
	return
}

// ConsensusTip reports information about the current consensus state.
func (c *Client) ConsensusTip() (resp ConsensusTipResponse, err error) {
	err = c.c.Get("/api/consensus/tip", &resp)
	return
}

// ExplorerSiacoinElement gets the Siacoin element with the given ID.
func (c *Client) ExplorerSiacoinElement(id types.ElementID) (resp types.SiacoinElement, err error) {
	err = c.c.Get(fmt.Sprintf("/api/explorer/element/siacoin/%s", id.String()), &resp)
	return
}

// ExplorerSiafundElement gets the Siafund element with the given ID.
func (c *Client) ExplorerSiafundElement(id types.ElementID) (resp types.SiafundElement, err error) {
	err = c.c.Get(fmt.Sprintf("/api/explorer/element/siafund/%s", id.String()), &resp)
	return
}

// ExplorerFileContractElement gets the file contract element with the given ID.
func (c *Client) ExplorerFileContractElement(id types.ElementID) (resp types.FileContractElement, err error) {
	err = c.c.Get(fmt.Sprintf("/api/explorer/element/contract/%s", id.String()), &resp)
	return
}

// ExplorerChainStats gets stats about the block at the given index.
func (c *Client) ExplorerChainStats(index types.ChainIndex) (resp explorer.ChainStats, err error) {
	err = c.c.Get(fmt.Sprintf("/api/explorer/chain/stats/%s", index.String()), &resp)
	return
}

// ExplorerChainStatsLatest gets stats about the latest block.
func (c *Client) ExplorerChainStatsLatest() (resp explorer.ChainStats, err error) {
	err = c.c.Get("/api/explorer/chain/stats/latest", &resp)
	return
}

// ExplorerElementSearch gets information about a given element.
func (c *Client) ExplorerElementSearch(id types.ElementID) (resp ExplorerSearchResponse, err error) {
	err = c.c.Get(fmt.Sprintf("/api/explorer/element/search/%s", id.String()), &resp)
	return
}

// ExplorerAddressBalance gets the siacoin and siafund balance of an address.
func (c *Client) ExplorerAddressBalance(address types.Address) (resp ExplorerWalletBalanceResponse, err error) {
	data, err := json.Marshal(address)
	if err != nil {
		return
	}
	err = c.c.Get(fmt.Sprintf("/api/explorer/address/balance/%s", string(data)), &resp)
	return
}

// ExplorerSiacoinOutputs gets the unspent siacoin elements of an address.
func (c *Client) ExplorerSiacoinOutputs(address types.Address) (resp []types.SiacoinElement, err error) {
	data, err := json.Marshal(address)
	if err != nil {
		return
	}
	err = c.c.Get(fmt.Sprintf("/api/explorer/address/siacoins/%s", string(data)), &resp)
	return
}

// ExplorerSiafundOutputs gets the unspent siafunds elements of an address.
func (c *Client) ExplorerSiafundOutputs(address types.Address) (resp []types.SiafundElement, err error) {
	data, err := json.Marshal(address)
	if err != nil {
		return
	}
	err = c.c.Get(fmt.Sprintf("/api/explorer/address/siafunds/%s", string(data)), &resp)
	return
}

// ExplorerTransactions gets the latest transaction IDs the address was
// involved in.
func (c *Client) ExplorerTransactions(address types.Address, amount, offset int) (resp []types.ElementID, err error) {
	data, err := json.Marshal(address)
	if err != nil {
		return
	}
	err = c.c.Get(fmt.Sprintf("/api/explorer/address/transactions/%s?amount=%d&offset=%d", string(data), amount, offset), &resp)
	return
}

// ExplorerBatchBalance gets the siacoin and siafund balance of a list of addresses.
func (c *Client) ExplorerBatchBalance(addresses []types.Address) (resp []ExplorerWalletBalanceResponse, err error) {
	err = c.c.Post("/api/explorer/batch/addresses/balance", addresses, &resp)
	return
}

// ExplorerBatchSiacoins returns the unspent siacoin elements of the addresses.
func (c *Client) ExplorerBatchSiacoins(addresses []types.Address) (resp [][]types.SiacoinElement, err error) {
	err = c.c.Post("/api/explorer/batch/addresses/siacoins", addresses, &resp)
	return
}

// ExplorerBatchSiafunds returns the unspent siafund elements of the addresses.
func (c *Client) ExplorerBatchSiafunds(addresses []types.Address) (resp [][]types.SiafundElement, err error) {
	err = c.c.Post("/api/explorer/batch/addresses/siafunds", addresses, &resp)
	return
}

// ExplorerBatchTransactions returns the last n transactions of the addresses.
func (c *Client) ExplorerBatchTransactions(addresses []ExplorerTransactionsRequest) (resp [][]types.Transaction, err error) {
	err = c.c.Post("/api/explorer/batch/addresses/transactions", addresses, &resp)
	return
}

// ExplorerChainState returns the validation context at a given chain index.
func (c *Client) ExplorerChainState(index types.ChainIndex) (resp consensus.State, err error) {
	err = c.c.Get(fmt.Sprintf("/api/explorer/chain/state/%s", index.String()), &resp)
	return
}

// NewClient returns a client that communicates with a explored server listening on
// the specified address.
func NewClient(addr, password string) *Client {
	return &Client{
		c: api.Client{
			BaseURL:      addr,
			AuthPassword: password,
		},
	}
}
