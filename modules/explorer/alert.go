package explorer

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the explorer.
func (e *Explorer) Alerts() []modules.Alert {
	return []modules.Alert{}
}
