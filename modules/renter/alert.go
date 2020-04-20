package renter

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// Alerts implements the modules.Alerter interface for the renter. It returns
// all alerts of the renter and its submodules.
func (r *Renter) Alerts() (crit, err, warn []modules.Alert) {
	renterCrit, renterErr, renterWarn := r.staticAlerter.Alerts()
	contractorCrit, contractorErr, contractorWarn := r.hostContractor.Alerts()
	hostdbCrit, hostdbErr, hostdbWarn := r.hostDB.Alerts()
	crit = append(append(renterCrit, contractorCrit...), hostdbCrit...)
	err = append(append(renterErr, contractorErr...), hostdbErr...)
	warn = append(append(renterWarn, contractorWarn...), hostdbWarn...)
	return crit, err, warn
}
