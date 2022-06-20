package explored

import (
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
)

// A Client provides methods for interacting with a explored API server.
type Client struct {
	c api.Client
}

// TxpoolBroadcast broadcasts a transaction to the network.
func (c *Client) TxpoolBroadcast(txn types.Transaction, dependsOn []types.Transaction) (err error) {
	err = c.c.Post("/txpool/broadcast", TxpoolBroadcastRequest{dependsOn, txn}, nil)
	return
}

// TxpoolTransactions returns all transactions in the transaction pool.
func (c *Client) TxpoolTransactions() (resp []types.Transaction, err error) {
	err = c.c.Get("/txpool/transactions", &resp)
	return
}

// SyncerPeers returns the current peers of the syncer.
func (c *Client) SyncerPeers() (resp []SyncerPeerResponse, err error) {
	err = c.c.Get("/syncer/peers", &resp)
	return
}

// SyncerConnect adds the address as a peer of the syncer.
func (c *Client) SyncerConnect(addr string) (err error) {
	err = c.c.Post("/syncer/connect", addr, nil)
	return
}

// ConsensusTip reports information about the current consensus state.
func (c *Client) ConsensusTip() (resp ConsensusTipResponse, err error) {
	err = c.c.Get("/consensus/tip", &resp)
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
