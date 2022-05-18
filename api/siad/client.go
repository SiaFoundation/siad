package siad

import (
	"fmt"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/wallet"
)

// A Client provides methods for interacting with a siad API server.
type Client struct {
	c api.Client
}

// ConsensusTip returns the current tip index.
func (c *Client) ConsensusTip() (resp types.ChainIndex, err error) {
	err = c.c.Get("/api/consensus/tip", &resp)
	return
}

// ConsensusState returns the consensus state at the provided index.
func (c *Client) ConsensusState(index types.ChainIndex) (resp consensus.State, err error) {
	i, _ := index.MarshalText() // index.String is lossy
	err = c.c.Get("/api/consensus/state/"+string(i), &resp)
	return
}

// ConsensusTipState returns the consensus state at the current tip.
func (c *Client) ConsensusTipState() (resp consensus.State, err error) {
	tip, err := c.ConsensusTip()
	if err != nil {
		return consensus.State{}, err
	}
	return c.ConsensusState(tip)
}

// WalletBalance returns the current wallet balance.
func (c *Client) WalletBalance() (resp WalletBalanceResponse, err error) {
	err = c.c.Get("/api/wallet/balance", &resp)
	return
}

// WalletSeedIndex returns the current wallet seed index.
func (c *Client) WalletSeedIndex() (index uint64, err error) {
	err = c.c.Get("/api/wallet/seedindex", &index)
	return
}

// WalletAddAddress adds an address to the wallet.
func (c *Client) WalletAddAddress(addr types.Address, info wallet.AddressInfo) (err error) {
	err = c.c.Post("/api/wallet/address/"+addr.String(), info, nil)
	return
}

// WalletAddressInfo returns information about an address controlled by the wallet.
func (c *Client) WalletAddressInfo(addr types.Address) (resp wallet.AddressInfo, err error) {
	err = c.c.Get("/api/wallet/address/"+addr.String(), &resp)
	return
}

// WalletAddresses returns all addresses controlled by the wallet.
func (c *Client) WalletAddresses(start, end int) (resp WalletAddressesResponse, err error) {
	err = c.c.Get(fmt.Sprintf("/api/wallet/addresses?start=%d&end=%d", start, end), &resp)
	return
}

// WalletTransactions returns all transactions relevant to the wallet.
func (c *Client) WalletTransactions(since time.Time, max int) (resp []wallet.Transaction, err error) {
	err = c.c.Get(fmt.Sprintf("/api/wallet/transactions?since=%s&max=%d", since.Format(time.RFC3339), max), &resp)
	return
}

// WalletUTXOs returns all unspent outputs controlled by the wallet.
func (c *Client) WalletUTXOs() (resp WalletUTXOsResponse, err error) {
	err = c.c.Get("/api/wallet/utxos", &resp)
	return
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

// NewClient returns a client that communicates with a siad server listening on
// the specified address.
func NewClient(addr, password string) *Client {
	return &Client{
		c: api.Client{
			BaseURL:      addr,
			AuthPassword: password,
		},
	}
}
