package explorer

import "go.sia.tech/siad/modules"

// Alerts implements the modules.Alerter interface for the explorer.
func (e *Explorer) Alerts() (crit, err, warn, info []modules.Alert) {
	return
}
