package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// The following consts are the different types of severity levels available in
// the alert system.
const (
	// SeverityUnknown is the value of an uninitialized severity and should never
	// be used.
	SeverityUnknown = iota
	// SeverityWarning warns the user about potential issues which might require
	// preventive actions.
	SeverityWarning
	// SeverityError should be used for information about the system where
	// immediate action is recommended to avoid further issues like loss of data.
	SeverityError
	// SeverityCritical should be used for critical errors. e.g. a lack of funds
	// causing data to get lost without immediate action.
	SeverityCritical
)

// The following consts are a list of AlertIDs. All IDs used throughout Sia
// should be unique and listed here.
const (
	// alertIDUnknown is the id of an unknown alert.
	//lint:ignore U1000 keeping for safety
	alertIDUnknown = "unknown"
	// AlertIDWalletLockedDuringMaintenance is the id of the alert that is
	// registered if the wallet is locked during a contract renewal or formation.
	AlertIDWalletLockedDuringMaintenance = "wallet-locked"
	// AlertIDRenterAllowanceLowFunds is the id of the alert that is registered if at least one
	// contract failed to renew/form due to low allowance.
	AlertIDRenterAllowanceLowFunds = "low-funds"
	// AlertIDRenterContractRenewalError is the id of the alert that is
	// registered if at least once contract renewal or refresh failed
	AlertIDRenterContractRenewalError = "contract-renewal-error"
	// AlertIDGatewayOffline is the id of the alert that is registered upon a
	// call to 'gateway.Offline' if the value returned is 'false' and
	// unregistered when it returns 'true'.
	AlertIDGatewayOffline = "gateway-offline"
	// AlertIDHostDiskTrouble is the id of the alert that is registered when the
	// host is encountering problems interacting with one or more of his disks
	AlertIDHostDiskTrouble = "host-disk-trouble"
	// AlertIDHostInsufficientCollateral is the id of the alert that is
	// registered if the host has insufficient collateral budget left to form or
	// renew a contract
	AlertIDHostInsufficientCollateral = "host-insufficient-collateral"
)

// AlertIDSiafileLowRedundancy uses a Siafile's UID to create a unique AlertID
// for a low redundancy alert.
func AlertIDSiafileLowRedundancy(uid string) AlertID {
	return AlertID(fmt.Sprintf("low-redundancy:%v", uid))
}

type (
	// Alerter is the interface implemented by all top-level modules. It's an
	// interface that allows for asking a module about potential issues.
	Alerter interface {
		Alerts() []Alert
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
	AlertID string

	// AlertSeverity describes the severity of an alert.
	AlertSeverity uint64
)

// Equals returns true if x and y are identical alerts
func (x Alert) Equals(y Alert) bool {
	return x.Module == y.Module && x.Cause == y.Cause && x.Msg == y.Msg && x.Severity == y.Severity
}

// EqualsWithErrorCause returns true if x and y have the same module, message,
// and severity and if the provided error is in both of the alert's causes
func (x Alert) EqualsWithErrorCause(y Alert, causeErr string) bool {
	firstCheck := x.Module == y.Module && x.Msg == y.Msg && x.Severity == y.Severity
	causeCheck := strings.Contains(x.Cause, causeErr) && strings.Contains(y.Cause, causeErr)
	return firstCheck && causeCheck
}

// MarshalJSON defines a JSON encoding for the AlertSeverity.
func (a AlertSeverity) MarshalJSON() ([]byte, error) {
	switch a {
	case SeverityWarning:
	case SeverityError:
	case SeverityCritical:
	default:
		return nil, errors.New("unknown AlertSeverity")
	}
	return json.Marshal(a.String())
}

// UnmarshalJSON attempts to decode an AlertSeverity.
func (a *AlertSeverity) UnmarshalJSON(b []byte) error {
	var severityStr string
	if err := json.Unmarshal(b, &severityStr); err != nil {
		return err
	}
	switch severityStr {
	case "warning":
		*a = SeverityWarning
	case "error":
		*a = SeverityError
	case "critical":
		*a = SeverityCritical
	default:
		return fmt.Errorf("unknown severity '%v'", severityStr)
	}
	return nil
}

// String converts an alertSeverity to a String
func (a AlertSeverity) String() string {
	switch a {
	case SeverityWarning:
		return "warning"
	case SeverityError:
		return "error"
	case SeverityCritical:
		return "critical"
	case SeverityUnknown:
	default:
	}
	return "unknown"
}

// GenericAlerter implements the Alerter interface. It can be used as a helper
// type to implement the Alerter interface for modules and submodules.
type (
	GenericAlerter struct {
		alerts map[AlertID]Alert
		module string
		mu     sync.Mutex
	}
)

// NewAlerter creates a new alerter for the renter.
func NewAlerter(module string) *GenericAlerter {
	return &GenericAlerter{
		alerts: make(map[AlertID]Alert),
		module: module,
	}
}

// Alerts returns the current alerts tracked by the alerter.
func (a *GenericAlerter) Alerts() []Alert {
	a.mu.Lock()
	defer a.mu.Unlock()

	alerts := make([]Alert, 0, len(a.alerts))
	for _, alert := range a.alerts {
		alerts = append(alerts, alert)
	}
	return alerts
}

// RegisterAlert adds an alert to the alerter.
func (a *GenericAlerter) RegisterAlert(id AlertID, msg, cause string, severity AlertSeverity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.alerts[id] = Alert{
		Cause:    cause,
		Module:   a.module,
		Msg:      msg,
		Severity: severity,
	}
}

// UnregisterAlert removes an alert from the alerter by id.
func (a *GenericAlerter) UnregisterAlert(id AlertID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.alerts, id)
}
