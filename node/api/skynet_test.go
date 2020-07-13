package api

import (
	"net/url"
	"strings"
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
			name:        "noDefaultPath unparsable",
			queryForm:   url.Values{modules.SkyfileNoDefaultPathParamName: []string{"un-par-sa-ble"}},
			defaultPath: "",
			err:         ErrInvalidNoDefaultPath,
		},
		{
			name:        "noDefaultPath true + single file",
			queryForm:   url.Values{modules.SkyfileNoDefaultPathParamName: []string{"true"}},
			subfiles:    modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "",
			err:         ErrInvalidNoDefaultPath,
		},
		{
			name:      "noDefaultPath true + multi file",
			queryForm: url.Values{modules.SkyfileNoDefaultPathParamName: []string{"true"}},
			subfiles: modules.SkyfileSubfiles{
				"about.html": modules.SkyfileSubfileMetadata{},
				"hello.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath: "",
			err:         nil,
		},
		{
			name:      "default path empty, multi file, index.html present",
			queryForm: url.Values{},
			subfiles: modules.SkyfileSubfiles{
				"index.html": modules.SkyfileSubfileMetadata{},
				"about.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath: "/index.html",
		},
		{
			name:      "default path empty, multi file, index.html NOT present",
			queryForm: url.Values{},
			subfiles: modules.SkyfileSubfiles{
				"hello.html": modules.SkyfileSubfileMetadata{},
				"about.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath: "",
		},
		{
			name:        "default path empty, single file, index.html NOT present",
			queryForm:   url.Values{},
			subfiles:    modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "/about.html",
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
				t.Fatalf("Expected defaultPath '%v', got '%v'\n", tt.defaultPath, dp)
			}
		})
	}
}
