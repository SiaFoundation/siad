package wallet

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// Alerts implements the Alerter interface for the wallet.
func (w *Wallet) Alerts() (crit, err, warn []modules.Alert) {
	return []modules.Alert{}, []modules.Alert{}, []modules.Alert{}
}
