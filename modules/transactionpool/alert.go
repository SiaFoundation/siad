package transactionpool

import "go.sia.tech/siad/modules"

// Alerts implements the modules.Alerter interface for the transactionpool.
func (tpool *TransactionPool) Alerts() (crit, err, warn, info []modules.Alert) {
	return
}
