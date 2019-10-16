package gateway

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the gateway.
func (g *Gateway) Alerts() []modules.Alert {
	return g.staticAlerter.Alerts()
}
