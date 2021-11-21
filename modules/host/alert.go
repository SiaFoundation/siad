package host

import "go.sia.tech/siad/modules"

// Alerts implements the modules.Alerter interface for the host.
func (h *Host) Alerts() (crit, err, warn, info []modules.Alert) {
	hostCrit, hostErr, hostWarn, hostInfo := h.staticAlerter.Alerts()
	smCrit, smErr, smWarn, smInfo := h.StorageManager.Alerts()
	crit = append(hostCrit, smCrit...)
	err = append(hostErr, smErr...)
	warn = append(hostWarn, smWarn...)
	info = append(hostInfo, smInfo...)
	return
}

// tryUnregisterInsufficientCollateralBudgetAlert will be called when the host
// updates his collateral budget setting or when the locked storage collateral
// gets updated (in a way the updated storage collateral is lower).
func (h *Host) tryUnregisterInsufficientCollateralBudgetAlert() {
	// Unregister the alert if the collateral budget is enough to support cover
	// a contract's max collateral and the currently locked storage collateral
	if h.financialMetrics.LockedStorageCollateral.Add(h.settings.MaxCollateral).Cmp(h.settings.CollateralBudget) <= 0 {
		h.staticAlerter.UnregisterAlert(modules.AlertIDHostInsufficientCollateral)
	}
}
