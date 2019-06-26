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

// RegisterAlert implements the modules.Alerter interface for the renter.
func (r *Renter) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
	r.staticAlerter.RegisterAlert(id, msg, cause, severity)
}

// UnregisterAlert implements the modules.Alerter interface for the renter. It
// will unregister the alert with the specified id from the module or
// submodules.
func (r *Renter) UnregisterAlert(id modules.AlertID) {
	r.staticAlerter.UnregisterAlert(id)
	r.hostContractor.UnregisterAlert(id)
	r.hostDB.UnregisterAlert(id)
}
