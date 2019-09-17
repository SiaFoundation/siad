package transactionpool

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the transactionpool.
func (tpool *TransactionPool) Alerts() []modules.Alert {
	return []modules.Alert{}
}
