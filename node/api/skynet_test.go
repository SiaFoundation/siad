package api

import (
	"net/url"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestRedirectParameter ensures the `redirect` parameter functions correctly.
func TestRedirectParameter(t *testing.T) {
	subfilesMulti := modules.SkyfileSubfiles{
		"about.html": modules.SkyfileSubfileMetadata{},
		"hello.html": modules.SkyfileSubfileMetadata{},
	}
	subfilesSingle := modules.SkyfileSubfiles{
		"about.html": modules.SkyfileSubfileMetadata{},
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
			name:           "not allowed for single file directories",
			queryForm:      nil,
			metadata:       modules.SkyfileMetadata{Subfiles: subfilesSingle},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:      "not allowed for empty default path and nodefaultpath=true",
			queryForm: url.Values{"defaultpath": []string{""}},
			metadata: modules.SkyfileMetadata{
				Subfiles:      subfilesMulti,
				NoDefaultPath: true,
			},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:      "not allowed for valid default path and nodefaultpath=true",
			queryForm: url.Values{"defaultpath": []string{"about.html"}},
			metadata: modules.SkyfileMetadata{
				Subfiles:      subfilesMulti,
				NoDefaultPath: true,
			},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:      "not allowed for `redirect=false` parameter",
			queryForm: url.Values{"redirect": []string{"false"}},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "about.html",
			},
			expectedResult: false,
			expectedErrMsg: "",
		},
		{
			name:      "allowed for missing or empty `redirect` parameter",
			queryForm: url.Values{},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "about.html",
			},
			expectedResult: true,
			expectedErrMsg: "",
		},
		{
			name:      "allowed for `redirect=true` parameter",
			queryForm: url.Values{"redirect": []string{"true"}},
			metadata: modules.SkyfileMetadata{
				Subfiles:    subfilesMulti,
				DefaultPath: "non_about.html",
			},
			expectedResult: true,
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
