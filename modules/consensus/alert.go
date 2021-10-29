package consensus

import (
	"go.sia.tech/siad/modules"
)

// Alerts implements the Alerter interface for the consensusset.
func (c *ConsensusSet) Alerts() (crit, err, warn, info []modules.Alert) {
	return
}
