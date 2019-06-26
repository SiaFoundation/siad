package contractor

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the contractor. It returns
// all alerts of the contractor.
func (c *Contractor) Alerts() []modules.Alert {
	return c.staticAlerter.Alerts()
}

// RegisterAlert implements the modules.Alerter interface for the contractor.
func (c *Contractor) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
	c.staticAlerter.RegisterAlert(id, msg, cause, severity)
}

// UnregisterAlert implements the modules.Alerter interface for the contractora. It
// will unregister the alert with the specified id from the contractor.
func (c *Contractor) UnregisterAlert(id modules.AlertID) {
	c.staticAlerter.UnregisterAlert(id)
}
