package consensus

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

// Alerts implements the Alerter interface for the consensusset.
func (c *ConsensusSet) Alerts() []modules.Alert {
	return []modules.Alert{}
}

// RegisterAlert implements the modules.Alerter interface for the consensusset.
func (c *ConsensusSet) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
}

// UnregisterAlert implements the modules.Alerter interface for the consensusset.
func (c *ConsensusSet) UnregisterAlert(id modules.AlertID) {
}
