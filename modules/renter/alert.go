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
		alerts map[modules.AlertID]modules.Alert
		mu     sync.Mutex
	}
)

// newAlerter creates a new alerter for the renter.
func newAlerter() *alerter {
	return &alerter{
		alerts: make(map[modules.AlertID]modules.Alert),
	}
}

// Alerts implements the modules.Alerter interface for the renter.
func (r *Renter) Alerts() []modules.Alert {
	return r.staticAlerter.Alerts()
}

// RegisterAlert implements the modules.Alerter interface for the renter.
func (r *Renter) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
	r.staticAlerter.RegisterAlert(id, msg, cause, severity)
}

// UnregisterAlert implements the modules.Alerter interface for the renter.
func (r *Renter) UnregisterAlert(id modules.AlertID) {
	r.staticAlerter.UnregisterAlert(id)
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
func (a *alerter) RegisterAlert(id modules.AlertID, msg, cause string, severity modules.AlertSeverity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts[id] = modules.Alert{
		Cause:    cause,
		Module:   "renter",
		Msg:      msg,
		Severity: severity,
	}
}

// UnregisterAlert removes an alert from the alerter by id.
func (a *alerter) UnregisterAlert(id modules.AlertID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.alerts, id)
}
