package modules

import (
	"encoding/json"
	"strconv"
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

	// Register some alerts
	for i := 0; i < 20; i++ {
		id := strconv.Itoa(i)
		alerter.RegisterAlert(AlertID(id), "msg"+id, "cause"+id, AlertSeverity(i%4+1))
	}

	crit, err, warn, info := alerter.Alerts()
	// 5 due to the already registered test alerts.
	if len(crit) != 5 || len(err) != 5 || len(warn) != 5 || len(info) != 5 {
		t.Fatalf("returned slices have wrong lengths %v %v %v %v", len(crit), len(err), len(warn), len(info))
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
	for _, alert := range info {
		if alert.Severity != SeverityInfo {
			t.Fatal("alert has wrong severity")
		}
	}
}
