package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// alertIDUnknown is the id of an unknown alert.
	alertIDUnknown = iota
)

// alerter is a type to help implement the Alerter interface for the renter.
type (
	alerter struct {
		alerts map[alertID]modules.Alert
		mu     sync.Mutex
	}

	alertID uint64
)

// newAlerter creates a new alerter for the renter.
func newAlerter() *alerter {
	return &alerter{
		alerts: make(map[alertID]modules.Alert),
	}
}

// Alerts implements the modules.Alerter interface for the renter.
func (r *Renter) Alerts() []modules.Alert {
	return r.staticAlerter.Alerts()
}

// Alerts returns the current alerts tracked by the alerter.
func (a *alerter) Alerts() []modules.Alert {
	a.mu.Lock()
	defer a.mu.Unlock()

	alerts := make([]modules.Alert, 0, len(a.alerts))
	for _, alert := range a.alerts {
		alerts = append(alerts, alert)
	}
	return alerts
}

// RegisterAlert adds an alert to the alerter.
func (a *alerter) RegisterAlert(id alertID, msg, cause string, severity modules.AlertSeverity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts[id] = modules.Alert{
		Msg:      msg,
		Cause:    cause,
		Severity: severity,
	}
}

// UnregisterAlert removes an alert from the alerter by id.
func (a *alerter) UnregisterAlert(id alertID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.alerts, id)
}
