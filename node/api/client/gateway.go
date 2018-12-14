package client

import (
	"net/url"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrPeerExists indicates that two peers are already connected. The string
	// of this error needs to be updated if the string of errPeerExists in the
	// gateway package is changed.
	ErrPeerExists = errors.New("already connected to this peer")
)

// GatewayConnectPost uses the /gateway/connect/:address endpoint to connect to
// the gateway at address
func (c *Client) GatewayConnectPost(address modules.NetAddress) (err error) {
	err = c.post("/gateway/connect/"+string(address), "", nil)
	if err != nil && err.Error() == ErrPeerExists.Error() {
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
