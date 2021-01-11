package profile

import "testing"

// TestProcessProfileFlags probes the ProcessProfileFlags function
func TestProcessProfileFlags(t *testing.T) {
	t.Parallel()
	// Create testing inputs
	var tests = []struct {
		in    string
		out   string
		valid bool
	}{
		// Single flag cases
		{"c", "c", true},
		{"C", "c", true},
		{"m", "m", true},
		{"M", "m", true},
		{"t", "t", true},
		{"T", "t", true},

		// Multi flag cases
		{"cmt", "cmt", true},
		{"cmT", "cmt", true},
		{"cMt", "cmt", true},
		{"Cmt", "cmt", true},
		{"CMt", "cmt", true},
		{"CmT", "cmt", true},
		{"cMT", "cmt", true},
		{"CMT", "cmt", true},

		// Check spaces
		{" ", " ", true},
		{" CMT", " cmt", true},
		{"CMT ", "cmt ", true},

		// Error Cases
		{"", "", false},
		{" CMT ", "", false},
		{" cmt ", "", false},
		{"x", "", false},
		{"at", "", false},
		{"abdfijklnopqsuvxyz", "", false},
		{"cghmrtwez", "", false},
		{"cc", "", false},
		{"ccm", "", false},
		{"ccmm", "", false},
		{"cmm", "", false},
		{"cghmrtwe", "", false},
		{"CGHMRTWE", "", false},
	}

	// Run Tests
	for _, test := range tests {
		output, err := ProcessProfileFlags(test.in)
		if err != nil && test.valid {
			t.Log("Test:", test)
			t.Error("valid test returned an error:", err)
		}
		if err == nil && !test.valid {
			t.Log("Test:", test)
			t.Error("invalid test did not return an error")
		}
		if output != test.out && test.valid {
			t.Log("Test:", test)
			t.Error("valid test returned unexpected output:", output)
		}
	}
}
