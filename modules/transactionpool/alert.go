package transactionpool

import "gitlab.com/NebulousLabs/Sia/modules"

// Alerts implements the modules.Alerter interface for the transactionpool.
func (tpool *TransactionPool) Alerts() []modules.Alert {
	return []modules.Alert{}
}

// RegisterAlert implements the modules.Alerter interface for the transactionpool.
func (tpool *TransactionPool) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
}

// UnregisterAlert implements the modules.Alerter interface for the transactionpool.
func (tpool *TransactionPool) UnregisterAlert(id modules.AlertID) {
}
