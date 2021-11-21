package renter

import (
	"go.sia.tech/siad/modules"
)

// Alerts implements the modules.Alerter interface for the renter. It returns
// all alerts of the renter and its submodules.
func (r *Renter) Alerts() (crit, err, warn, info []modules.Alert) {
	renterCrit, renterErr, renterWarn, renterInfo := r.staticAlerter.Alerts()
	contractorCrit, contractorErr, contractorWarn, contractorInfo := r.hostContractor.Alerts()
	hostdbCrit, hostdbErr, hostdbWarn, hostdbInfo := r.hostDB.Alerts()
	crit = append(append(renterCrit, contractorCrit...), hostdbCrit...)
	err = append(append(renterErr, contractorErr...), hostdbErr...)
	warn = append(append(renterWarn, contractorWarn...), hostdbWarn...)
	info = append(append(renterInfo, contractorInfo...), hostdbInfo...)
	return
}
