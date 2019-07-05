package host

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the host.
func (h *Host) Alerts() []modules.Alert {
	return []modules.Alert{}
}
