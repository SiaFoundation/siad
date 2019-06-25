package renter

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the renter.
func (r *Renter) Alerts() []modules.Alert {
	return []modules.Alert{}
}
