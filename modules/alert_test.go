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
