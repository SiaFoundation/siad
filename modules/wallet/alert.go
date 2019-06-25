package wallet

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// Alerts implements the Alerter interface for the wallet.
func (w *Wallet) Alerts() []modules.Alert {
	return []modules.Alert{}
}

// RegisterAlert implements the modules.Alerter interface for the wallet.
func (w *Wallet) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
}

// UnregisterAlert implements the modules.Alerter interface for the wallet.
func (w *Wallet) UnregisterAlert(id modules.AlertID) {
}
