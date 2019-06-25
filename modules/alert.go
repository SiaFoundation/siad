package modules

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
		Msg      string
		Cause    string
		Severity uint8
	}
)
