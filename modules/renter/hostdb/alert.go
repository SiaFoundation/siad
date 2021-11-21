package hostdb

import "go.sia.tech/siad/modules"

// Alerts implements the modules.Alerter interface for the hostdb. It returns
// all alerts of the hostdb.
func (hdb *HostDB) Alerts() (crit, err, warn, info []modules.Alert) {
	return hdb.staticAlerter.Alerts()
}
