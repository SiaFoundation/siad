package api

import (
	"net/url"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestAllowRedirect ensures defaultPath functions correctly.
func TestAllowRedirect(t *testing.T) {
	subfilesMulti := modules.SkyfileSubfiles{
		"about.html": modules.SkyfileSubfileMetadata{},
		"hello.html": modules.SkyfileSubfileMetadata{},
	}
	tests := []struct {
		name           string
		queryForm      url.Values
		metadata       modules.SkyfileMetadata
		expectedResult bool
		expectedErrMsg string
	}{
		{
			name:           "not allowed for single files",
			queryForm:      nil,
			metadata:       modules.SkyfileMetadata{},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:           "not allowed for empty (but present) default path",
			queryForm:      url.Values{"defaultpath": []string{""}},
			metadata:       modules.SkyfileMetadata{Subfiles: subfilesMulti},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:      "allowed for missing or empty `redirect` parameter",
			queryForm: url.Values{},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "non_empty",
			},
			expectedResult: true,
			expectedErrMsg: "",
		},
		{
			name:      "allowed for `redirect=true` parameter",
			queryForm: url.Values{"redirect": []string{"true"}},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "non_empty",
			},
			expectedResult: true,
			expectedErrMsg: "",
		},
		{
			name:      "not allowed for `redirect=false` parameter",
			queryForm: url.Values{"redirect": []string{"false"}},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "non_empty",
			},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:      "error on unparsable `redirect` parameter",
			queryForm: url.Values{"redirect": []string{"un_par_sa_ble!"}},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "non_empty",
			},
			expectedResult: false,
			expectedErrMsg: "unable to parse 'redirect' parameter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := useDefaultPath(tt.queryForm, tt.metadata)
			if (tt.expectedErrMsg == "" && err != nil) ||
				(tt.expectedErrMsg != "" && err == nil) ||
				(tt.expectedErrMsg != "" && err != nil && !strings.Contains(err.Error(), tt.expectedErrMsg)) {
				t.Fatalf("Expected error message %v, got %v\n", tt.expectedErrMsg, err)
			}
			if res != tt.expectedResult {
				t.Fatalf("Expected result %t, got %t\n", tt.expectedResult, res)
			}
		})
	}
}

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
