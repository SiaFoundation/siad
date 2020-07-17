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
		name               string
		queryForm          url.Values
		subfiles           modules.SkyfileSubfiles
		defaultPath        string
		disableDefaultPath bool
		err                error
	}{
		{
			name:               "single file not multipart nil",
			queryForm:          url.Values{},
			subfiles:           nil,
			defaultPath:        "",
			disableDefaultPath: false,
			err:                nil,
		},
		{
			name:        "single file not multipart empty",
			queryForm:   url.Values{modules.SkyfileDisableDefaultPathParamName: []string{"true"}},
			subfiles:    nil,
			defaultPath: "",
			err:         ErrInvalidDefaultPath,
		},
		{
			name:        "single file not multipart set",
			queryForm:   url.Values{modules.SkyfileDefaultPathParamName: []string{"about.html"}},
			subfiles:    nil,
			defaultPath: "",
			err:         ErrInvalidDefaultPath,
		},

		{
			name:        "single file multipart nil",
			queryForm:   url.Values{},
			subfiles:    modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "/about.html",
			err:         nil,
		},
		{
			name:               "single file multipart empty",
			queryForm:          url.Values{modules.SkyfileDisableDefaultPathParamName: []string{"true"}},
			subfiles:           modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath:        "",
			disableDefaultPath: true,
			err:                nil,
		},
		{
			name:        "single file multipart set to only",
			queryForm:   url.Values{modules.SkyfileDefaultPathParamName: []string{"about.html"}},
			subfiles:    modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "/about.html",
			err:         nil,
		},
		{
			name:        "single file multipart set to bad",
			queryForm:   url.Values{modules.SkyfileDefaultPathParamName: []string{"bad.html"}},
			subfiles:    modules.SkyfileSubfiles{"about.html": modules.SkyfileSubfileMetadata{}},
			defaultPath: "",
			err:         ErrInvalidDefaultPath,
		},

		{
			name:      "multi file nil has index.html",
			queryForm: url.Values{},
			subfiles: modules.SkyfileSubfiles{
				"about.html": modules.SkyfileSubfileMetadata{},
				"index.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath: "/index.html",
			err:         nil,
		},
		{
			name:      "multi file nil no index.html",
			queryForm: url.Values{},
			subfiles: modules.SkyfileSubfiles{
				"about.html": modules.SkyfileSubfileMetadata{},
				"hello.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath:        "",
			disableDefaultPath: false,
			err:                nil,
		},
		{
			name:      "multi file set to empty",
			queryForm: url.Values{modules.SkyfileDisableDefaultPathParamName: []string{"true"}},
			subfiles: modules.SkyfileSubfiles{
				"about.html": modules.SkyfileSubfileMetadata{},
				"index.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath:        "",
			disableDefaultPath: true,
			err:                nil,
		},
		{
			name:      "multi file set to existing",
			queryForm: url.Values{modules.SkyfileDefaultPathParamName: []string{"about.html"}},
			subfiles: modules.SkyfileSubfiles{
				"about.html": modules.SkyfileSubfileMetadata{},
				"index.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath: "/about.html",
			err:         nil,
		},
		{
			name:      "multi file set to not existing",
			queryForm: url.Values{modules.SkyfileDefaultPathParamName: []string{"noexist.html"}},
			subfiles: modules.SkyfileSubfiles{
				"about.html": modules.SkyfileSubfileMetadata{},
				"index.html": modules.SkyfileSubfileMetadata{},
			},
			defaultPath: "",
			err:         ErrInvalidDefaultPath,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dp, ddp, err := defaultPath(tt.queryForm, tt.subfiles)
			if (err != nil || tt.err != nil) && !errors.Contains(err, tt.err) {
				t.Fatalf("Expected error %v, got %v\n", tt.err, err)
			}
			if dp != tt.defaultPath {
				t.Fatalf("Expected defaultPath '%v', got '%v'\n", tt.defaultPath, dp)
			}
			if ddp != tt.disableDefaultPath {
				t.Fatalf("Expected disableDefaultPath '%v', got '%v'\n", tt.disableDefaultPath, ddp)
			}
		})
	}
}
