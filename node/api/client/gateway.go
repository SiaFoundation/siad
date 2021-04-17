package client

import (
	"encoding/json"
	"net/url"
	"strconv"

	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api"
)

var (
	// ErrPeerExists indicates that two peers are already connected. The string
	// of this error needs to be updated if the string of errPeerExists in the
	// gateway package is changed.
	ErrPeerExists = errors.New("already connected to this peer")
)

// GatewayBandwidthGet requests the /gateway/bandwidth api resource
func (c *Client) GatewayBandwidthGet() (gbg api.GatewayBandwidthGET, err error) {
	err = c.get("/gateway/bandwidth", &gbg)
	return
}

// GatewayConnectPost uses the /gateway/connect/:address endpoint to connect to
// the gateway at address
func (c *Client) GatewayConnectPost(address modules.NetAddress) (err error) {
	err = c.post("/gateway/connect/"+string(address), "", nil)
	if err != nil && errors.Contains(err, ErrPeerExists) {
		err = ErrPeerExists
	}
	return
}

// GatewayDisconnectPost uses the /gateway/disconnect/:address endpoint to
// disconnect the gateway from a peer.
func (c *Client) GatewayDisconnectPost(address modules.NetAddress) (err error) {
	err = c.post("/gateway/disconnect/"+string(address), "", nil)
	return
}

// GatewayGet requests the /gateway api resource
func (c *Client) GatewayGet() (gwg api.GatewayGET, err error) {
	err = c.get("/gateway", &gwg)
	return
}

// GatewayRateLimitPost uses the /gateway endpoint to change the gateway's
// bandwidth rate limit. downloadSpeed and uploadSpeed are interpreted as
// bytes/second.
func (c *Client) GatewayRateLimitPost(downloadSpeed, uploadSpeed int64) (err error) {
	values := url.Values{}
	values.Set("maxdownloadspeed", strconv.FormatInt(downloadSpeed, 10))
	values.Set("maxuploadspeed", strconv.FormatInt(uploadSpeed, 10))
	err = c.post("/gateway", values.Encode(), nil)
	return
}

// GatewayBlocklistGet uses the /gateway/blocklist endpoint to request the
// Gateway's blocklist
func (c *Client) GatewayBlocklistGet() (gbg api.GatewayBlocklistGET, err error) {
	err = c.get("/gateway/blocklist", &gbg)
	return
}

// GatewayAppendBlocklistPost uses the /gateway/blocklist endpoint to append
// addresses to the Gateway's blocklist
func (c *Client) GatewayAppendBlocklistPost(addresses []string) (err error) {
	gbp := api.GatewayBlocklistPOST{
		Action:    "append",
		Addresses: addresses,
	}
	data, err := json.Marshal(gbp)
	if err != nil {
		return err
	}
	err = c.post("/gateway/blocklist", string(data), nil)
	return
}

// GatewayRemoveBlocklistPost uses the /gateway/blocklist endpoint to remove
// addresses from the Gateway's blocklist
func (c *Client) GatewayRemoveBlocklistPost(addresses []string) (err error) {
	gbp := api.GatewayBlocklistPOST{
		Action:    "remove",
		Addresses: addresses,
	}
	data, err := json.Marshal(gbp)
	if err != nil {
		return err
	}
	err = c.post("/gateway/blocklist", string(data), nil)
	return
}

// GatewaySetBlocklistPost uses the /gateway/blocklist endpoint to set the
// Gateway's blocklist
func (c *Client) GatewaySetBlocklistPost(addresses []string) (err error) {
	gbp := api.GatewayBlocklistPOST{
		Action:    "set",
		Addresses: addresses,
	}
	data, err := json.Marshal(gbp)
	if err != nil {
		return err
	}
	err = c.post("/gateway/blocklist", string(data), nil)
	return
}
