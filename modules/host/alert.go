package host

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the host.
func (h *Host) Alerts() []modules.Alert {
	return append(h.staticAlerter.Alerts(), h.StorageManager.Alerts()...)
}

// TryUnregisterInsufficientCollateralBudgetAlert will be called when the host
// updates his collateral budget setting or when the locked storage collateral
// gets updated (in a way the updated storage collateral is lower).
func (h *Host) TryUnregisterInsufficientCollateralBudgetAlert() {
	// Unregister the alert if the collateral budget is enough to support cover
	// a contract's max collateral and the currently locked storage collateral
	if h.financialMetrics.LockedStorageCollateral.Add(h.settings.MaxCollateral).Cmp(h.settings.CollateralBudget) <= 0 {
		h.staticAlerter.UnregisterAlert(modules.AlertIDHostInsufficientCollateral)
	}
}
