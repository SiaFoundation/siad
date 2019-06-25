package modules

import (
	"encoding/json"
	"errors"
	"fmt"
)

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

type (
	// Alerter is the interface implemented by all top-level modules. It's a very
	// simple interface that allows for asking a module about potential issues.
	Alerter interface {
		Alerts() []Alert
	}

	// Alert is a type that contains essential information about an alert.
	Alert struct {
		// Msg is the message the Alert is meant to convey to the user.
		// e.g. "Contractor can't form new contrats"
		Msg string `json:"msg"`
		// Cause is the cause for the Alert.
		// e.g. "Wallet is locked"
		Cause string `json:"cause"`
		// Severity categorizes the Alerts to allow for an easy way to filter them.
		Severity AlertSeverity `json:"severity"`
	}

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
