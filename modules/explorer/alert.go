package explorer

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the explorer.
func (e *Explorer) Alerts() []modules.Alert {
	return []modules.Alert{}
}

// RegisterAlert implements the modules.Alerter interface for the explorer.
func (e *Explorer) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
}

// UnregisterAlert implements the modules.Alerter interface for the explorer.
func (e *Explorer) UnregisterAlert(id modules.AlertID) {
}
