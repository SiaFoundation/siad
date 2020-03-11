package modules

import (
	"encoding/json"
	"testing"
)

// TestMarshalUnmarshalAlertSeverity tests the custom marshaling/unmarshaling
// code for AlertSeverity.
func TestMarshalUnmarshalAlertSeverity(t *testing.T) {
	severityUnknown := AlertSeverity(SeverityUnknown)
	severityWarning := AlertSeverity(SeverityWarning)
	severityError := AlertSeverity(SeverityError)
	severityCritical := AlertSeverity(SeverityCritical)
	severityInvalid := AlertSeverity(42)

	var s AlertSeverity
	// Marshal/Unmarshal unknown.
	_, err := json.Marshal(severityUnknown)
	if err == nil {
		t.Fatal("Shouldn't be able to marshal unknown")
	}
	// Marshal/Unmarshal critical.
	b, err := json.Marshal(severityCritical)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s != SeverityCritical {
		t.Fatal("result not the same severity as input")
	}
	// Marshal/Unmarshal warning.
	b, err = json.Marshal(severityWarning)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s != SeverityWarning {
		t.Fatal("result not the same severity as input")
	}
	// Marshal/Unmarshal error.
	b, err = json.Marshal(severityError)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	if s != SeverityError {
		t.Fatal("result not the same severity as input")
	}
	// Marshal/Unmarshal invalid.
	_, err = json.Marshal(severityInvalid)
	if err == nil {
		t.Fatal("Shouldn't be able to marshal invalid")
	}
}

// TestAlertsSorted tests if the return values contain the right alerts.
func TestAlertsSorted(t *testing.T) {
	alerter := NewAlerter(t.Name())

	// Register some alerts in no particular order.
	alerter.RegisterAlert(AlertID("1"), "msg1", "cause1", SeverityWarning)
	alerter.RegisterAlert(AlertID("2"), "msg2", "cause2", SeverityError)
	alerter.RegisterAlert(AlertID("3"), "msg3", "cause3", SeverityCritical)
	alerter.RegisterAlert(AlertID("4"), "msg4", "cause1", SeverityWarning)
	alerter.RegisterAlert(AlertID("5"), "msg5", "cause2", SeverityError)
	alerter.RegisterAlert(AlertID("6"), "msg6", "cause3", SeverityCritical)
	alerter.RegisterAlert(AlertID("7"), "msg7", "cause1", SeverityWarning)
	alerter.RegisterAlert(AlertID("8"), "msg8", "cause2", SeverityError)
	alerter.RegisterAlert(AlertID("9"), "msg9", "cause3", SeverityCritical)

	crit, err, warn := alerter.Alerts()
	// 4 due to the already registered test alerts.
	if len(crit) != 4 || len(err) != 4 || len(warn) != 4 {
		t.Fatalf("returned slices have wrong lengths %v %v %v", len(crit), len(err), len(warn))
	}
	for _, alert := range crit {
		if alert.Severity != SeverityCritical {
			t.Fatal("alert has wrong severity")
		}
	}
	for _, alert := range err {
		if alert.Severity != SeverityError {
			t.Fatal("alert has wrong severity")
		}
	}
	for _, alert := range warn {
		if alert.Severity != SeverityWarning {
			t.Fatal("alert has wrong severity")
		}
	}
}
