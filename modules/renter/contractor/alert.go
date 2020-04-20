package contractor

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the contractor. It returns
// all alerts of the contractor.
func (c *Contractor) Alerts() (crit, err, warn []modules.Alert) {
	return c.staticAlerter.Alerts()
}
