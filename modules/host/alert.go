package host

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the host.
func (h *Host) Alerts() []modules.Alert {
	return []modules.Alert{}
}

// RegisterAlert implements the modules.Alerter interface for the host.
func (h *Host) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
}

// UnregisterAlert implements the modules.Alerter interface for the host.
func (h *Host) UnregisterAlert(id modules.AlertID) {
}
