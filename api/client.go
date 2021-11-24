package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"

	"go.sia.tech/core/types"
)

// A Client communicates with a siad server.
type Client struct {
	addr string
}

func (c *Client) req(method string, route string, data, resp interface{}) error {
	var body io.Reader
	if data != nil {
		js, _ := json.Marshal(data)
		body = bytes.NewReader(js)
	}
	req, err := http.NewRequest(method, fmt.Sprintf("%v%v", c.addr, route), body)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer io.Copy(ioutil.Discard, r.Body)
	defer r.Body.Close()
	if r.StatusCode != 200 {
		err, _ := ioutil.ReadAll(r.Body)
		return errors.New(string(err))
	}
	if resp == nil {
		return nil
	}
	return json.NewDecoder(r.Body).Decode(resp)
}

func (c *Client) get(route string, r interface{}) error     { return c.req("GET", route, nil, r) }
func (c *Client) post(route string, d, r interface{}) error { return c.req("POST", route, d, r) }
func (c *Client) put(route string, d interface{}) error     { return c.req("PUT", route, d, nil) }
func (c *Client) delete(route string) error                 { return c.req("DELETE", route, nil, nil) }

// WalletBalance returns the current wallet balance.
func (c *Client) WalletBalance() (resp WalletBalance, err error) {
	err = c.get("/wallet/balance", &resp)
	return
}

// WalletAddress returns an address controlled by the wallet.
func (c *Client) WalletAddress() (resp WalletAddress, err error) {
	err = c.get("/wallet/address", &resp)
	return
}

// WalletAddresses returns all addresses controlled by the wallet.
func (c *Client) WalletAddresses() (resp WalletAddresses, err error) {
	err = c.get("/wallet/addresses", &resp)
	return
}

// WalletTransactions returns all transactions relevant to the wallet, ordered
// oldest-to-newest.
func (c *Client) WalletTransactions() (resp WalletTransactions, err error) {
	err = c.get("/wallet/transactions", &resp)
	return
}

// WalletSign signs a transaction.
func (c *Client) WalletSign(txn types.Transaction, toSign []types.ElementID) (resp WalletTransaction, err error) {
	err = c.post("/wallet/sign", WalletSignData{toSign, txn}, &resp)
	return
}

// WalletBroadcastTransaction broadcasts a transaction to the network.
func (c *Client) WalletBroadcastTransaction(txn types.Transaction) (err error) {
	err = c.post("/wallet/broadcast", txn, nil)
	return
}

// TxpoolTransactions returns all transactions in the transaction pool.
func (c *Client) TxpoolTransactions() (resp TxpoolTransactions, err error) {
	err = c.get("/txpool/transactions", &resp)
	return
}

// SyncerPeers returns all peers of the syncer.
func (c *Client) SyncerPeers() (resp SyncerPeers, err error) {
	err = c.get("/syncer/peers", &resp)
	return
}

// SyncerAddress returns the syncer's listening address.
func (c *Client) SyncerAddress() (resp SyncerAddress, err error) {
	err = c.get("/syncer/address", &resp)
	return
}

// SyncerConnect adds the address as a peer of the syncer.
func (c *Client) SyncerConnect(addr string) (err error) {
	err = c.post("/syncer/connect", addr, nil)
	return
}

// NewClient returns a client that communicates with a siad server listening on
// the specified address.
func NewClient(addr string) *Client {
	return &Client{addr}
}
