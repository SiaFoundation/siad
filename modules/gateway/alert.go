package gateway

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the gateway.
func (g *Gateway) Alerts() []modules.Alert {
	return []modules.Alert{}
}

// RegisterAlert implements the modules.Alerter interface for the gateway.
func (g *Gateway) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
}

// UnregisterAlert implements the modules.Alerter interface for the gateway.
func (g *Gateway) UnregisterAlert(id modules.AlertID) {
}
