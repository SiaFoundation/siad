package renterd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/hostdb"
	"go.sia.tech/siad/v2/wallet"
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
func (c *Client) WalletAddress() (resp types.Address, err error) {
	err = c.c.Get("/api/wallet/address", &resp)
	return
}

// WalletTransactions returns all transactions relevant to the wallet.
func (c *Client) WalletTransactions(since time.Time, max int) (resp []wallet.Transaction, err error) {
	err = c.c.Get(fmt.Sprintf("/api/wallet/transactions?since=%s&max=%d", since.Format(time.RFC3339), max), &resp)
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

// RHPScan scans a host, returning its current settings.
func (c *Client) RHPScan(netAddress string, hostKey types.PublicKey) (resp rhp.HostSettings, err error) {
	err = c.c.Post("/api/rhp/scan", RHPScanRequest{netAddress, hostKey}, &resp)
	return
}

// RHPForm forms a contract with a host.
func (c *Client) RHPForm(req RHPFormRequest) (resp RHPFormResponse, err error) {
	err = c.c.Post("/api/rhp/form", req, &resp)
	return
}

// RHPRenew renews an existing contract with a host.
func (c *Client) RHPRenew(req RHPRenewRequest) (resp RHPRenewResponse, err error) {
	err = c.c.Post("/api/rhp/renew", req, &resp)
	return
}

// RHPRead invokes the Read RPC on a host, writing the response to w.
func (c *Client) RHPRead(w io.Writer, rrr RHPReadRequest) (err error) {
	js, _ := json.Marshal(rrr)
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%v%v", c.c.BaseURL, "/api/rhp/read"), bytes.NewReader(js))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("", c.c.AuthPassword)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		err, _ := ioutil.ReadAll(r.Body)
		return errors.New(string(err))
	}
	_, err = io.Copy(w, r.Body)
	return
}

// RHPAppend invokes the Write RPC on a host.
func (c *Client) RHPAppend(rar RHPAppendRequest, sector *[rhp.SectorSize]byte) (resp RHPAppendResponse, err error) {
	// build multipart form
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, _ := w.CreateFormField("meta")
	json.NewEncoder(f).Encode(rar)
	f, _ = w.CreateFormFile("sector", "sector")
	f.Write(sector[:])
	w.Close()

	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%v%v", c.c.BaseURL, "/api/rhp/append"), &buf)
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.SetBasicAuth("", c.c.AuthPassword)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return RHPAppendResponse{}, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		err, _ := ioutil.ReadAll(r.Body)
		return RHPAppendResponse{}, errors.New(string(err))
	}
	err = json.NewDecoder(r.Body).Decode(&resp)
	return
}

// HostDBHosts gets a list of hosts in the host DB.
func (c *Client) HostDBHosts() (resp []hostdb.Host, err error) {
	err = c.c.Get("/api/hostdb", &resp)
	return
}

// HostDBHost gets information about a given host.
func (c *Client) HostDBHost(hostKey types.PublicKey) (resp []hostdb.Host, err error) {
	err = c.c.Get(fmt.Sprintf("/api/hostdb/%s", hostKey), &resp)
	return
}

// HostDBScore assigns a score to a given host.
func (c *Client) HostDBScore(hostKey types.PublicKey, score float64) (err error) {
	err = c.c.Put(fmt.Sprintf("/api/hostdb/%s/score", hostKey), HostDBScoreRequest{score})
	return
}

// HostDBInteraction records an interaction with a given host.
func (c *Client) HostDBInteraction(hostKey types.PublicKey, interaction hostdb.Interaction) (err error) {
	err = c.c.Put(fmt.Sprintf("/api/hostdb/%s/interaction", hostKey), HostDBInteractionRequest{interaction})
	return
}

// NewClient returns a client that communicates with a renterd server listening
// on the specified address.
func NewClient(addr, password string) *Client {
	return &Client{
		c: api.Client{
			BaseURL:      addr,
			AuthPassword: password,
		},
	}
}
