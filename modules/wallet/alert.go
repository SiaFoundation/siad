package wallet

import (
	"go.sia.tech/siad/modules"
)

// Alerts implements the Alerter interface for the wallet.
func (w *Wallet) Alerts() (crit, err, warn, info []modules.Alert) {
	return
}
