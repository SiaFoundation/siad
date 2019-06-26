package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// The following consts are the different types of severity levels available in
// the alert system.
const (
	// SeverityUnknown is the value of an uninitialized severity and should never
	// be used.
	SeverityUnknown = iota
	// SeverityInfo should be used for information about the system which doesn't
	// require user interaction.
	SeverityInfo
	// SeverityWarning warns the user about potential issues which might require
	// preventive actions.
	SeverityWarning
	// SeverityError should be used for information about the system where
	// immediate action is recommended to avoid further issues like loss of data.
	SeverityError
)

// The following consts are a list of AlertIDs. All IDs used throughout Sia
// should be unique and listed here.
const (
	// alertIDUnknown is the id of an unknown alert.
	alertIDUnknown = iota
	// AlertIDIncompleteMaintenace is the id of the alert that is registered if the
	// wallet is locked during a contract maintenance.
	AlertIDIncompleteMaintenance
)

type (
	// Alerter is the interface implemented by all top-level modules. It's a very
	// simple interface that allows for asking a module about potential issues.
	Alerter interface {
		Alerts() []Alert
		RegisterAlert(id AlertID, msg, cause string, severity AlertSeverity)
		UnregisterAlert(id AlertID)
	}

	// Alert is a type that contains essential information about an alert.
	Alert struct {
		// Cause is the cause for the Alert.
		// e.g. "Wallet is locked"
		Cause string `json:"cause"`
		// Msg is the message the Alert is meant to convey to the user.
		// e.g. "Contractor can't form new contrats"
		Msg string `json:"msg"`
		// Module contains information about what module the alert originated from.
		Module string `json:"module"`
		// Severity categorizes the Alerts to allow for an easy way to filter them.
		Severity AlertSeverity `json:"severity"`
	}

	// AlertID is a helper type for an Alert's ID.
	AlertID uint64

	// AlertSeverity describes the severity of an alert.
	AlertSeverity uint8
)

// MarshalJSON defines a JSON encoding for the AlertSeverity.
func (a AlertSeverity) MarshalJSON() ([]byte, error) {
	switch a {
	case SeverityInfo:
		return json.Marshal("info")
	case SeverityWarning:
		return json.Marshal("warning")
	case SeverityError:
		return json.Marshal("error")
	case SeverityUnknown:
	default:
	}
	return nil, errors.New("unknown AlertSeverity")
}

// UnmarshalJSON attempts to decode an AlertSeverity.
func (a *AlertSeverity) UnmarshalJSON(b []byte) error {
	var severityStr string
	if err := json.Unmarshal(b, &severityStr); err != nil {
		return err
	}
	switch severityStr {
	case "info":
		*a = SeverityInfo
	case "warning":
		*a = SeverityWarning
	case "error":
		*a = SeverityError
	default:
		return fmt.Errorf("unknown severity '%v'", severityStr)
	}
	return nil
}

// alerter implements the Alerter interface. It can be used as a helper type to
// implement the Alerter interface for modules and submodules.
type (
	alerter struct {
		alerts map[AlertID]Alert
		mu     sync.Mutex
	}
)

// NewAlerter creates a new alerter for the renter.
func NewAlerter() Alerter {
	return &alerter{
		alerts: make(map[AlertID]Alert),
	}
}

// Alerts returns the current alerts tracked by the alerter.
func (a *alerter) Alerts() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()

	alerts := make([]Alert, 0, len(a.alerts))
	for _, alert := range a.alerts {
		alerts = append(alerts, alert)
	}
	return alerts
}

// RegisterAlert adds an alert to the alerter.
func (a *alerter) RegisterAlert(id AlertID, msg, cause string, severity AlertSeverity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts[id] = Alert{
		Cause:    cause,
		Module:   "renter",
		Msg:      msg,
		Severity: severity,
	}
}

// UnregisterAlert removes an alert from the alerter by id.
func (a *alerter) UnregisterAlert(id AlertID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.alerts, id)
}
