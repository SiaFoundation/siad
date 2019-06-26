package hostdb

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the hostdb. It returns
// all alerts of the hostdb.
func (hdb *HostDB) Alerts() []modules.Alert {
	return hdb.staticAlerter.Alerts()
}

// RegisterAlert implements the modules.Alerter interface for the hostdb.
func (hdb *HostDB) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
	hdb.staticAlerter.RegisterAlert(id, msg, cause, severity)
}

// UnregisterAlert implements the modules.Alerter interface for the hostdb. It
// will unregister the alert with the specified id from the hostdb.
func (hdb *HostDB) UnregisterAlert(id modules.AlertID) {
	hdb.staticAlerter.UnregisterAlert(id)
}
