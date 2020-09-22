package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/julienschmidt/httprouter"
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/skykey"
)

// TestSkynetHelpers is a convenience function that wraps all of the Skynet
// helper tests, this ensures these tests are ran when supplying `-run
// TestSkynet` from the command line.
func TestSkynetHelpers(t *testing.T) {
	t.Run("ParseSkylinkURL", testParseSkylinkURL)
	t.Run("ParseUploadRequestParameters", testParseUploadRequestParameters)
	t.Run("ValidDefaultPath", testValidDefaultPath)
}

// testParseSkylinkURL is a table test for the parseSkylinkUrl function.
func testParseSkylinkURL(t *testing.T) {
	tests := []struct {
		name                 string
		strToParse           string
		skylink              string
		skylinkStringNoQuery string
		path                 string
		errMsg               string
	}{
		{
			name:                 "no path",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			path:                 "/",
			errMsg:               "",
		},
		{
			name:                 "no path with query",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w?foo=bar",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			path:                 "/",
			errMsg:               "",
		},
		{
			name:                 "with path to file",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar.baz",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar.baz",
			path:                 "/foo/bar.baz",
			errMsg:               "",
		},
		{
			name:                 "with path to dir with trailing slash",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar/",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar/",
			path:                 "/foo/bar/",
			errMsg:               "",
		},
		{
			name:                 "with path to dir without trailing slash",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar",
			path:                 "/foo/bar",
			errMsg:               "",
		},
		{
			name:                 "with path to file with query",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar.baz?foobar=nope",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar.baz",
			path:                 "/foo/bar.baz",
			errMsg:               "",
		},
		{
			name:                 "with path to dir with query with trailing slash",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar/?foobar=nope",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar/",
			path:                 "/foo/bar/",
			errMsg:               "",
		},
		{
			name:                 "with path to dir with query without trailing slash",
			strToParse:           "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar?foobar=nope",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo/bar",
			path:                 "/foo/bar",
			errMsg:               "",
		},
		{
			// Test URL-decoding the path.
			name:                 "with path to dir containing both a query and an encoded '?'",
			strToParse:           "/IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo%3Fbar?foobar=nope",
			skylink:              "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w",
			skylinkStringNoQuery: "IAC6CkhNYuWZqMVr1gob1B6tPg4MrBGRzTaDvAIAeu9A9w/foo%3Fbar",
			path:                 "/foo?bar",
			errMsg:               "",
		},
		{
			name:                 "invalid skylink",
			strToParse:           "invalid_skylink/foo/bar?foobar=nope",
			skylink:              "",
			skylinkStringNoQuery: "",
			path:                 "",
			errMsg:               modules.ErrSkylinkIncorrectSize.Error(),
		},
		{
			name:                 "empty input",
			strToParse:           "",
			skylink:              "",
			skylinkStringNoQuery: "",
			path:                 "",
			errMsg:               modules.ErrSkylinkIncorrectSize.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skylink, skylinkStringNoQuery, path, err := parseSkylinkURL(tt.strToParse)
			// Is there an actual or expected error?
			if err != nil || tt.errMsg != "" {
				// Actual err should contain expected err.
				if err == nil || !strings.Contains(err.Error(), tt.errMsg) {
					t.Fatalf("Expected error '%s', got %v\n", tt.errMsg, err)
				} else {
					// The errors match, so the test case passes.
					return
				}
			}
			if skylink.String() != tt.skylink {
				t.Fatalf("Expected skylink '%v', got '%v'\n", tt.skylink, skylink)
			}
			if skylinkStringNoQuery != tt.skylinkStringNoQuery {
				t.Fatalf("Expected skylinkStringNoQuery '%v', got '%v'\n", tt.skylinkStringNoQuery, skylinkStringNoQuery)
			}
			if path != tt.path {
				t.Fatalf("Expected path '%v', got '%v'\n", tt.path, path)
			}
		})
	}
}

// dict is a small helper type that represents a dictionary of key,value pairs
type dict map[string]string

// testParseUploadRequestParameters verifies the functionality of
// 'parseUploadHeadersAndRequestParameters'.
func testParseUploadRequestParameters(t *testing.T) {
	t.Parallel()

	// create a siapath
	siapath, err := modules.NewSiaPath(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// buildRequest is a helper function that creates a request object
	buildRequest := func(query, headers dict) *http.Request {
		values := url.Values{}
		for k, v := range query {
			values.Set(k, v)
		}
		resource := fmt.Sprintf("/skynet/skyfile/%s?%s", siapath.String(), values.Encode())
		req, err := http.NewRequest("POST", resource, nil)
		if err != nil {
			t.Fatal("Could not create request", err)
		}
		for k, v := range headers {
			values.Set(k, v)
			req.Header.Set(k, v)
		}
		return req
	}

	// parseRequest simply wraps 'parseUploadHeadersAndRequestParameters' to avoid
	// handling the error for every case
	parseRequest := func(req *http.Request, ps httprouter.Params) (*skyfileUploadHeaders, *skyfileUploadParams) {
		// if content type is not set, default to a binary stream
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/octet-stream")
		}
		headers, params, err := parseUploadHeadersAndRequestParameters(req, ps)
		if err != nil {
			t.Fatal("Unexpected error", err)
		}
		return headers, params
	}

	// create empty router params
	param := httprouter.Param{Key: "siapath", Value: siapath.String()}
	defaultParams := httprouter.Params{param}
	none := make(dict)
	yes := fmt.Sprintf("%t", true)

	// verify 'Skynet-Disable-Force'
	hdrs := dict{"Skynet-Disable-Force": yes}
	req := buildRequest(make(dict), hdrs)
	headers, _ := parseRequest(req, defaultParams)
	if !headers.disableForce {
		t.Fatal("Unexpected")
	}

	// verify 'Skynet-Disable-Force' - combo with 'force'
	req = buildRequest(dict{"force": yes}, hdrs)
	_, _, err = parseUploadHeadersAndRequestParameters(req, defaultParams)
	if err == nil {
		t.Fatal("Unexpected")
	}

	// verify 'Content-Type'
	req = buildRequest(make(dict), dict{"Content-Type": "text/html"})
	headers, _ = parseRequest(req, defaultParams)
	if headers.mediaType != "text/html" {
		t.Fatal("Unexpected")
	}

	// verify 'basechunkredundancy'
	req = buildRequest(dict{"basechunkredundancy": fmt.Sprintf("%v", 2)}, none)
	_, params := parseRequest(req, defaultParams)
	if params.baseChunkRedundancy != uint8(2) {
		t.Fatal("Unexpected")
	}

	// verify 'convertpath'
	req = buildRequest(dict{"convertpath": "/foo/bar"}, none)
	_, params = parseRequest(req, defaultParams)
	if params.convertpath != "/foo/bar" {
		t.Fatal("Unexpected")
	}

	// verify 'convertpath' - combo with 'filename
	req = buildRequest(dict{"convertpath": "/foo/bar", "filename": "foo.txt"}, none)
	_, _, err = parseUploadHeadersAndRequestParameters(req, defaultParams)
	if err == nil {
		t.Fatal("Unexpected")
	}

	// verify 'defaultpath'
	req = buildRequest(dict{"defaultpath": "/foo/bar.txt"}, none)
	_, params = parseRequest(req, defaultParams)
	if params.defaultPath != "/foo/bar.txt" {
		t.Fatal("Unexpected")
	}

	// verify 'disabledefaultpath'
	req = buildRequest(dict{"disabledefaultpath": yes}, none)
	_, params = parseRequest(req, defaultParams)
	if !params.disableDefaultPath {
		t.Fatal("Unexpected")
	}

	// verify 'disabledefaultpath' - combo with 'defaultpath'
	req = buildRequest(dict{"defaultpath": "/foo/bar.txt", "disabledefaultpath": yes}, none)
	_, _, err = parseUploadHeadersAndRequestParameters(req, defaultParams)
	if err == nil {
		t.Fatal("Unexpected")
	}

	// verify 'dryrun'
	req = buildRequest(dict{"dryrun": yes}, none)
	_, params = parseRequest(req, defaultParams)
	if !params.dryRun {
		t.Fatal("Unexpected")
	}

	// verify 'filename'
	req = buildRequest(dict{"filename": "foo.txt"}, none)
	_, params = parseRequest(req, defaultParams)
	if params.filename != "foo.txt" {
		t.Fatal("Unexpected")
	}

	// verify 'force'
	req = buildRequest(dict{"force": yes}, none)
	_, params = parseRequest(req, defaultParams)
	if !params.force {
		t.Fatal("Unexpected")
	}

	// verify 'force' - combo with 'dryrun
	req = buildRequest(dict{"force": yes, "dryrun": yes}, none)
	_, _, err = parseUploadHeadersAndRequestParameters(req, defaultParams)
	if err == nil {
		t.Fatal("Unexpected")
	}

	// verify 'mode'
	req = buildRequest(dict{"mode": fmt.Sprintf("%o", os.FileMode(0644))}, none)
	_, params = parseRequest(req, defaultParams)
	if params.mode != os.FileMode(0644) {
		t.Fatal("Unexpected")
	}

	// verify 'root'
	req = buildRequest(dict{"root": yes}, none)
	_, params = parseRequest(req, defaultParams)
	if !params.root {
		t.Fatal("Unexpected")
	}

	// verify 'siapath' (no root)
	req = buildRequest(none, none)
	_, params = parseRequest(req, defaultParams)
	expected, err := modules.SkynetFolder.Join(siapath.String())
	if err != nil || params.siaPath != expected {
		t.Fatal("Unexpected", err)
	}

	// verify 'siapath' (at root)
	req = buildRequest(dict{"root": yes}, none)
	_, params = parseRequest(req, defaultParams)
	if params.siaPath != siapath {
		t.Fatal("Unexpected")
	}

	// create a test skykey
	km, err := skykey.NewSkykeyManager(build.TempDir("skykey", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	key, err := km.CreateKey("testkey", skykey.TypePublicID)
	if err != nil {
		t.Fatal(err)
	}
	keyIdStr := key.ID().ToString()

	// verify 'skykeyname'
	req = buildRequest(dict{"skykeyname": key.Name}, none)
	_, params = parseRequest(req, defaultParams)
	if params.skyKeyName != key.Name {
		t.Fatal("Unexpected")
	}

	// verify 'skykeyid'
	req = buildRequest(dict{"skykeyid": keyIdStr}, none)
	_, params = parseRequest(req, defaultParams)
	if params.skyKeyId.ToString() != keyIdStr {
		t.Fatal("Unexpected")
	}

	// verify 'skykeyid' - combo with 'skykeyname'
	req = buildRequest(dict{"skykeyname": key.Name, "skykeyid": key.ID().ToString()}, none)
	_, _, err = parseUploadHeadersAndRequestParameters(req, defaultParams)
	if err == nil {
		t.Fatal("Unexpected")
	}
}

// testValidDefaultPath ensures the functionality of 'validDefaultPath'
func testValidDefaultPath(t *testing.T) {
	t.Parallel()

	subfiles := func(filenames ...string) modules.SkyfileSubfiles {
		md := make(modules.SkyfileSubfiles)
		for _, fn := range filenames {
			md[fn] = modules.SkyfileSubfileMetadata{Filename: fn}
		}
		return md
	}

	tests := []struct {
		name       string
		dpQuery    string
		dpExpected string
		subfiles   modules.SkyfileSubfiles
		err        error
	}{
		{
			name:       "empty default path - no files",
			subfiles:   nil,
			dpQuery:    "",
			dpExpected: "",
			err:        nil,
		},
		{
			name:       "no default path - files",
			subfiles:   subfiles("a.html"),
			dpQuery:    "",
			dpExpected: "",
			err:        nil,
		},
		{
			name:       "existing default path",
			subfiles:   subfiles("a.html"),
			dpQuery:    "/a.html",
			dpExpected: "/a.html",
			err:        nil,
		},
		{
			name:       "existing default path - multiple subfiles",
			subfiles:   subfiles("a.html", "b.html"),
			dpQuery:    "/a.html",
			dpExpected: "/a.html",
			err:        nil,
		},
		{
			name:       "existing default path - ensure leading slash",
			subfiles:   subfiles("a.html"),
			dpQuery:    "a.html",
			dpExpected: "/a.html",
			err:        nil,
		},
		{
			name:       "non existing default path",
			subfiles:   subfiles("b.html"),
			dpQuery:    "a.html",
			dpExpected: "",
			err:        ErrInvalidDefaultPath,
		},
		{
			name:       "non html default path",
			subfiles:   subfiles("a.txt"),
			dpQuery:    "a.txt",
			dpExpected: "",
			err:        ErrInvalidDefaultPath,
		},
		{
			name:       "HTML file with extension 'htm' as default path",
			subfiles:   subfiles("a.htm"),
			dpQuery:    "a.htm",
			dpExpected: "/a.htm",
			err:        nil,
		},
		{
			name:       "default path not at root",
			subfiles:   subfiles("a/b/c.html"),
			dpQuery:    "a/b/c.html",
			dpExpected: "",
			err:        ErrInvalidDefaultPath,
		},
	}

	for _, subtest := range tests {
		t.Run(subtest.name, func(t *testing.T) {
			dp, err := validDefaultPath(subtest.dpQuery, subtest.subfiles)
			if subtest.err != nil && !errors.Contains(err, subtest.err) {
				t.Fatal("Unexpected error")
			}
			if subtest.err == nil && err != nil {
				t.Fatal("Unexpected error", err)
			}
			if dp != subtest.dpExpected {
				t.Fatal("Unexpected default path")
			}
		})
	}
}
