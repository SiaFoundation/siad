package renterd

import (
	"fmt"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
)

// A Client provides methods for interacting with a renterd API server
type Client struct {
	c api.Client
}

// WalletBalance returns the current wallet balance.
func (c *Client) WalletBalance() (resp WalletBalanceResponse, err error) {
	err = c.c.Get("/api/wallet/balance", &resp)
	return
}

// WalletAddress returns an address controlled by the wallet.
func (c *Client) WalletAddress() (resp WalletAddressResponse, err error) {
	err = c.c.Get("/api/wallet/address", &resp)
	return
}

// WalletAddresses returns all addresses controlled by the wallet.
func (c *Client) WalletAddresses(start, end int) (resp WalletAddressesResponse, err error) {
	err = c.c.Get(fmt.Sprintf("/api/wallet/addresses?start=%d&end=%d", start, end), &resp)
	return
}

// WalletTransactions returns all transactions relevant to the wallet.
func (c *Client) WalletTransactions(since time.Time, max int) (resp []WalletTransactionResponse, err error) {
	err = c.c.Get(fmt.Sprintf("/api/wallet/transactions?since=%s&max=%d", since.Format(time.RFC3339), max), &resp)
	return
}

// WalletSign signs a transaction.
func (c *Client) WalletSign(txn *types.Transaction, toSign []types.ElementID) (err error) {
	err = c.c.Post("/api/wallet/sign", WalletSignRequest{toSign, *txn}, txn)
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

// NewClient returns a client that communicates with a renterd server listening on
// the specified address.
func NewClient(addr, password string) *Client {
	return &Client{
		c: api.Client{
			BaseURL:      addr,
			AuthPassword: password,
		},
	}
}
