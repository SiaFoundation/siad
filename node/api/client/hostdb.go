package client

import (
	"encoding/json"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/types"
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

// HostDbFilterModeGet requests the /hostdb/filtermode GET endpoint
func (c *Client) HostDbFilterModeGet() (hdfmg api.HostdbFilterModeGET, err error) {
	err = c.get("/hostdb/filtermode", &hdfmg)
	return
}

// HostDbFilterModePost requests the /hostdb/filtermode POST endpoint
func (c *Client) HostDbFilterModePost(fm modules.FilterMode, hosts []types.SiaPublicKey, netAddresses []string) (err error) {
	filterMode := fm.String()
	hdblp := api.HostdbFilterModePOST{
		FilterMode:   filterMode,
		Hosts:        hosts,
		NetAddresses: netAddresses,
	}

	data, err := json.Marshal(hdblp)
	if err != nil {
		return err
	}
	err = c.post("/hostdb/FilterMode", string(data), nil)
	return
}

// HostDbHostsGet request the /hostdb/hosts/:pubkey endpoint's resources.
func (c *Client) HostDbHostsGet(pk types.SiaPublicKey) (hhg api.HostdbHostsGET, err error) {
	err = c.get("/hostdb/hosts/"+pk.String(), &hhg)
	return
}
