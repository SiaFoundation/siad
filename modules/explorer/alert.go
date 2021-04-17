package explorer

import "go.sia.tech/siad/modules"

// Alerts implements the modules.Alerter interface for the explorer.
func (e *Explorer) Alerts() (crit, err, warn []modules.Alert) {
	return []modules.Alert{}, []modules.Alert{}, []modules.Alert{}
}
