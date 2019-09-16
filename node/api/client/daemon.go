package client

import (
	"net/url"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/node/api"
)

// DaemonGlobalRateLimitPost uses the /daemon/settings endpoint to change the
// siad's bandwidth rate limit. downloadSpeed and uploadSpeed are interpreted
// as bytes/second.
func (c *Client) DaemonGlobalRateLimitPost(downloadSpeed, uploadSpeed int64) (err error) {
	values := url.Values{}
	values.Set("maxdownloadspeed", strconv.FormatInt(downloadSpeed, 10))
	values.Set("maxuploadspeed", strconv.FormatInt(uploadSpeed, 10))
	err = c.post("/daemon/settings", values.Encode(), nil)
	return
}

// DaemonAlertsGet requests the /daemon/alerts resource.
func (c *Client) DaemonAlertsGet() (dag api.DaemonAlertsGet, err error) {
	err = c.get("/daemon/alerts", &dag)
	return
}

// DaemonVersionGet requests the /daemon/version resource.
func (c *Client) DaemonVersionGet() (dvg api.DaemonVersionGet, err error) {
	err = c.get("/daemon/version", &dvg)
	return
}

// DaemonSettingsGet requests the /daemon/settings api resource.
func (c *Client) DaemonSettingsGet() (dsg api.DaemonSettingsGet, err error) {
	err = c.get("/daemon/settings", &dsg)
	return
}

// DaemonStopGet stops the daemon using the /daemon/stop endpoint.
func (c *Client) DaemonStopGet() (err error) {
	err = c.get("/daemon/stop", nil)
	return
}

// DaemonUpdateGet checks for an available daemon update.
func (c *Client) DaemonUpdateGet() (dig api.DaemonUpdateGet, err error) {
	err = c.get("/daemon/update", &dig)
	return
}

// DaemonUpdatePost updates the daemon.
func (c *Client) DaemonUpdatePost() (err error) {
	err = c.post("/daemon/update", "", nil)
	return
}
