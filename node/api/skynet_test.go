package api

import (
	"net/url"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestDefaultPath ensures defaultPath functions correctly.
func TestDefaultPath(t *testing.T) {
	tests := []struct {
		name        string
		queryForm   url.Values
		subfiles    modules.SkyfileSubfiles
		defaultPath string
		err         error
	}{
		{
			name:        "explicitly disabled default path",
			queryForm:   url.Values{modules.SkyfileDefaultPathParamName: []string{""}},
			defaultPath: "",
		},
		{
			name:        "default path not specified, index.html present",
			queryForm:   url.Values{},
			subfiles:    modules.SkyfileSubfiles{"index.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "/index.html",
		},
		{
			name:        "default path not specified, index.html NOT present",
			queryForm:   url.Values{},
			subfiles:    modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "",
		},
		{
			name:        "default path specified, file exists",
			queryForm:   url.Values{modules.SkyfileDefaultPathParamName: []string{"index.js"}},
			subfiles:    modules.SkyfileSubfiles{"index.js": modules.SkyfileSubfileMetadata{}},
			defaultPath: "/index.js",
		},
		{
			name:        "default path specified, file does not exist",
			queryForm:   url.Values{modules.SkyfileDefaultPathParamName: []string{"index.js"}},
			defaultPath: "",
			err:         ErrInvalidDefaultPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, err := defaultPath(tt.queryForm, tt.subfiles)
			if errors.Contains(tt.err, err) {
				t.Fatalf("Expected error %v, got %v\n", tt.err, err)
			}
			if dp != tt.defaultPath {
				t.Fatalf("Expected defaultPath %v, got %v\n", tt.defaultPath, dp)
			}
		})
	}
}
