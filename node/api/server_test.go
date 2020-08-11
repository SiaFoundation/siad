package api

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// TestExplorerPreset checks that the default configuration for the explorer is
// working correctly.
func TestExplorerPreset(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createExplorerServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Try calling a legal endpoint without a user agent.
	err = st.stdGetAPIUA("/explorer", "")
	if err != nil {
		t.Fatal(err)
	}
}

// TestReloading reloads a server and does smoke testing to see that modules
// are still working after reload.
func TestReloading(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	height := st.server.api.cs.Height()

	err = st.server.Close()
	if err != nil {
		t.Fatal(err)
	}
	rst, err := st.reloadedServerTester()
	if err != nil {
		t.Fatal(err)
	}
	defer rst.server.panicClose()
	if height != rst.server.api.cs.Height() {
		t.Error("server heights do not match")
	}

	// Mine some blocks on the reloaded server and see if any errors or panics
	// are triggered.
	for i := 0; i < 3; i++ {
		_, err := rst.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestServerTimeout verifies the server returns a Gateway Timeout if an HTTP
// call exceeds the predefined timeout period.
func TestServerTimeout(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	apiDeps := &dependencies.DependencyTimeoutOnHostGET{}
	st, err := createServerTesterWithDeps(t.Name(), modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, apiDeps)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		err = st.server.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Test that the call times out
	testGETURL := "http://" + st.server.listener.Addr().String() + "/host"
	resp, err := HttpGET(testGETURL)
	if err != nil {
		t.Fatal(err)
	}

	// Verify status code
	if resp.StatusCode != 504 {
		t.Fatal("Expected HTTP Status Code to be 504 ")
	}

	// Verify response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	var apiErr Error
	err = json.Unmarshal(body, &apiErr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apiErr.Message, "HTTP call exceeded the timeout") {
		t.Fatal("Expected response body to contain mention of the timeout")
	}
}

// TestAuthenticated tests creating a server that requires authenticated API
// calls, and then makes (un)authenticated API calls to test the
// authentication.
func TestAuthentication(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createAuthenticatedServerTester(t.Name(), "password")
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	testGETURL := "http://" + st.server.listener.Addr().String() + "/wallet/seeds"
	testPOSTURL := "http://" + st.server.listener.Addr().String() + "/host/announce"

	// Test that unauthenticated API calls fail.
	// GET
	resp, err := HttpGET(testGETURL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated API call succeeded on a server that requires authentication")
	}
	// POST
	resp, err = HttpPOST(testPOSTURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("unauthenticated API call succeeded on a server that requires authentication")
	}

	// Test that authenticated API calls with the wrong password fail.
	// GET
	resp, err = HttpGETAuthenticated(testGETURL, "wrong password")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("authenticated API call succeeded with an incorrect password")
	}
	// POST
	resp, err = HttpPOSTAuthenticated(testPOSTURL, "", "wrong password")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal("authenticated API call succeeded with an incorrect password")
	}

	// Test that authenticated API calls with the correct password succeed.
	// GET
	resp, err = HttpGETAuthenticated(testGETURL, "password")
	if err != nil {
		t.Fatal(err)
	}
	if non2xx(resp.StatusCode) {
		t.Fatal("authenticated API call failed with the correct password")
	}
	// POST
	resp, err = HttpPOSTAuthenticated(testPOSTURL, "", "password")
	if err != nil {
		t.Fatal(err)
	}
	if non2xx(resp.StatusCode) {
		t.Fatal("authenticated API call failed with the correct password")
	}
}
