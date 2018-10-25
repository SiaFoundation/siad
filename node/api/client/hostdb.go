package client

import (
	"encoding/json"

	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

// HostDbGet requests the /hostdb endpoint's resources.
func (c *Client) HostDbGet() (hdg api.HostdbGet, err error) {
	err = c.get("/hostdb", &hdg)
	return
}

// HostDbActiveGet requests the /hostdb/active endpoint's resources.
func (c *Client) HostDbActiveGet() (hdag api.HostdbActiveGET, err error) {
	err = c.get("/hostdb/active", &hdag)
	return
}

// HostDbAllGet requests the /hostdb/all endpoint's resources.
func (c *Client) HostDbAllGet() (hdag api.HostdbAllGET, err error) {
	err = c.get("/hostdb/all", &hdag)
	return
}

// HostDbListModePost requests the /hostdb/listmode endpoint
func (c *Client) HostDbListModePost(hosts []types.SiaPublicKey, mode string) (err error) {
	hdblp := api.HostdbListModePOST{
		Mode:  mode,
		Hosts: hosts,
	}

	data, err := json.Marshal(hdblp)
	if err != nil {
		return err
	}
	err = c.post("/hostdb/listmode", string(data), nil)
	return
}

// HostDbHostsGet request the /hostdb/hosts/:pubkey endpoint's resources.
func (c *Client) HostDbHostsGet(pk types.SiaPublicKey) (hhg api.HostdbHostsGET, err error) {
	err = c.get("/hostdb/hosts/"+pk.String(), &hhg)
	return
}
