package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// Alerts implements the modules.Alerter interface for the renter. It returns
// all alerts of the renter and its submodules.
func (r *Renter) Alerts() []modules.Alert {
	renterAlerts := r.staticAlerter.Alerts()
	contractorAlerts := r.hostContractor.Alerts()
	hostdbAlerts := r.hostDB.Alerts()
	return append(append(renterAlerts, contractorAlerts...), hostdbAlerts...)
}
