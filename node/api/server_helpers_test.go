package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/threadgroup"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/consensus"
	"go.sia.tech/siad/modules/explorer"
	"go.sia.tech/siad/modules/gateway"
	"go.sia.tech/siad/modules/host"
	"go.sia.tech/siad/modules/miner"
	"go.sia.tech/siad/modules/renter"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/modules/renter/hostdb"
	"go.sia.tech/siad/modules/renter/proto"
	"go.sia.tech/siad/modules/transactionpool"
	"go.sia.tech/siad/modules/wallet"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

// A Server is a collection of siad modules that can be communicated with over
// an http api.
type Server struct {
	api               *API
	apiServer         *http.Server
	listener          net.Listener
	requiredUserAgent string
	tg                threadgroup.ThreadGroup
}

// panicClose will close a Server, panicking if there is an error upon close.
func (srv *Server) panicClose() {
	err := srv.Close()
	if err != nil {
		// Print the stack.
		debug.PrintStack()
		panic(err)
	}
}

// Close closes the Server's listener, causing the HTTP server to shut down.
func (srv *Server) Close() error {
	err := srv.listener.Close()
	err = errors.Extend(err, srv.tg.Stop())

	// Safely close each module.
	mods := []struct {
		name string
		c    io.Closer
	}{
		{"explorer", srv.api.explorer},
		{"host", srv.api.host},
		{"renter", srv.api.renter},
		{"miner", srv.api.miner},
		{"wallet", srv.api.wallet},
		{"tpool", srv.api.tpool},
		{"consensus", srv.api.cs},
		{"gateway", srv.api.gateway},
	}
	for _, mod := range mods {
		if mod.c != nil {
			if closeErr := mod.c.Close(); closeErr != nil {
				err = errors.Extend(err, fmt.Errorf("%v.Close failed: %v", mod.name, closeErr))
			}
		}
	}
	return errors.AddContext(err, "error while closing server")
}

// Serve listens for and handles API calls. It is a blocking function.
func (srv *Server) Serve() error {
	err := srv.tg.Add()
	if err != nil {
		return errors.AddContext(err, "unable to initialize server")
	}
	defer srv.tg.Done()

	// The server will run until an error is encountered or the listener is
	// closed, via either the Close method or by signal handling.  Closing the
	// listener will result in the benign error handled below.
	err = srv.apiServer.Serve(srv.listener)
	if err != nil && !strings.HasSuffix(err.Error(), "use of closed network connection") {
		return err
	}
	return nil
}

// NewServer creates a new API server from the provided modules. The API will
// require authentication using HTTP basic auth if the supplied password is not
// the empty string. Usernames are ignored for authentication. This type of
// authentication sends passwords in plaintext and should therefore only be
// used if the APIaddr is localhost.
func NewServer(dir string, APIaddr string, requiredUserAgent string, requiredPassword string, acc modules.Accounting, cs modules.ConsensusSet, e modules.Explorer, g modules.Gateway, h modules.Host, m modules.Miner, r modules.Renter, tp modules.TransactionPool, w modules.Wallet) (*Server, error) {
	return NewCustomServer(dir, APIaddr, requiredUserAgent, requiredPassword, acc, cs, e, g, h, m, r, tp, w, &modules.ProductionDependencies{})
}

// NewCustomServer creates a new API server from the provided modules. The API
// will require authentication using HTTP basic auth if the supplied password is
// not the empty string. Usernames are ignored for authentication. This type of
// authentication sends passwords in plaintext and should therefore only be used
// if the APIaddr is localhost. It is custom because it allows injecting custom
// API dependencies.
func NewCustomServer(dir string, APIaddr string, requiredUserAgent string, requiredPassword string, acc modules.Accounting, cs modules.ConsensusSet, e modules.Explorer, g modules.Gateway, h modules.Host, m modules.Miner, r modules.Renter, tp modules.TransactionPool, w modules.Wallet, apiDeps modules.Dependencies) (*Server, error) {
	listener, err := net.Listen("tcp", APIaddr)
	if err != nil {
		return nil, err
	}

	// Load the config file.
	cfg, err := modules.NewConfig(filepath.Join(dir, modules.ConfigName))
	if err != nil {
		return nil, errors.AddContext(err, "failed to load siad config")
	}

	api := NewCustom(cfg, requiredUserAgent, requiredPassword, acc, cs, e, g, h, m, r, tp, w, apiDeps)
	srv := &Server{
		api: api,
		apiServer: &http.Server{
			Handler: api,
		},
		listener:          listener,
		requiredUserAgent: requiredUserAgent,
	}
	return srv, nil
}

// serverTester contains a server and a set of channels for keeping all of the
// modules synchronized during testing.
type serverTester struct {
	cs        modules.ConsensusSet
	explorer  modules.Explorer
	gateway   modules.Gateway
	host      modules.Host
	miner     modules.TestMiner
	renter    modules.Renter
	tpool     modules.TransactionPool
	wallet    modules.Wallet
	walletKey crypto.CipherKey

	server *Server

	dir string
}

// assembleServerTesterWithDeps creates a bunch of modules with injected
// dependencies and assembles them into a server tester, without creating any
// directories or mining any blocks.
func assembleServerTesterWithDeps(key crypto.CipherKey, testdir string, gDeps, cDeps, tDeps, wDeps, hDeps, rDeps, hdbDeps, hcDeps, csDeps, apiDeps modules.Dependencies) (*serverTester, error) {
	// assembleServerTester should not get called during short tests, as it
	// takes a long time to run.
	if testing.Short() {
		panic("assembleServerTester called during short tests")
	}

	// Create the siamux
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, err
	}

	// Create the modules.
	g, err := gateway.NewCustomGateway("localhost:0", false, false, filepath.Join(testdir, modules.GatewayDir), gDeps)
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.NewCustomConsensusSet(g, false, filepath.Join(testdir, modules.ConsensusDir), cDeps)
	if err := <-errChan; err != nil {
		return nil, err
	}
	tp, err := transactionpool.NewCustomTPool(cs, g, filepath.Join(testdir, modules.TransactionPoolDir), tDeps)
	if err != nil {
		return nil, err
	}
	w, err := wallet.NewCustomWallet(cs, tp, filepath.Join(testdir, modules.WalletDir), wDeps)
	if err != nil {
		return nil, err
	}
	encrypted, err := w.Encrypted()
	if err != nil {
		return nil, err
	}
	if !encrypted {
		_, err = w.Encrypt(key)
		if err != nil {
			return nil, err
		}
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, err
	}
	h, err := host.NewCustomHost(hDeps, cs, g, tp, w, mux, "localhost:0", filepath.Join(testdir, modules.HostDir))
	if err != nil {
		return nil, err
	}
	renterPersistDir := filepath.Join(testdir, modules.RenterDir)
	hdb, errChan := hostdb.NewCustomHostDB(g, cs, tp, mux, renterPersistDir, hdbDeps)
	if err := <-errChan; err != nil {
		return nil, err
	}
	logger, err := persist.NewFileLogger(filepath.Join(renterPersistDir, "contractor.log"))
	if err != nil {
		return nil, err
	}
	renterRateLimit := ratelimit.NewRateLimit(0, 0, 0)
	contractSet, err := proto.NewContractSet(filepath.Join(renterPersistDir, "contracts"), renterRateLimit, csDeps)
	if err != nil {
		return nil, err
	}
	hc, errChan := contractor.NewCustomContractor(cs, w, tp, hdb, renterPersistDir, contractSet, logger, hcDeps)
	if err := <-errChan; err != nil {
		return nil, err
	}
	r, errChan := renter.NewCustomRenter(g, cs, tp, hdb, w, hc, mux, renterPersistDir, renterRateLimit, rDeps)
	if err := <-errChan; err != nil {
		return nil, err
	}
	srv, err := NewCustomServer(testdir, "localhost:0", "Sia-Agent", "", nil, cs, nil, g, h, m, r, tp, w, apiDeps)
	if err != nil {
		return nil, err
	}

	// Assemble the serverTester.
	st := &serverTester{
		cs:        cs,
		gateway:   g,
		host:      h,
		miner:     m,
		renter:    r,
		tpool:     tp,
		wallet:    w,
		walletKey: key,

		server: srv,

		dir: testdir,
	}

	// TODO: A more reasonable way of listening for server errors.
	go func() {
		listenErr := srv.Serve()
		if listenErr != nil && !strings.Contains(listenErr.Error(), "ThreadGroup already stopped") {
			panic(listenErr)
		}
	}()
	return st, nil
}

// assembleServerTester creates a bunch of modules and assembles them into a
// server tester, without creating any directories or mining any blocks.
func assembleServerTester(key crypto.CipherKey, testdir string) (*serverTester, error) {
	return assembleServerTesterWithDeps(key, testdir, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies)
}

// assembleAuthenticatedServerTester creates a bunch of modules and assembles
// them into a server tester that requires authentication with the given
// requiredPassword. No directories are created and no blocks are mined.
func assembleAuthenticatedServerTester(requiredPassword string, key crypto.CipherKey, testdir string) (*serverTester, error) {
	// assembleAuthenticatedServerTester should not get called during short
	// tests, as it takes a long time to run.
	if testing.Short() {
		panic("assembleServerTester called during short tests")
	}

	// Create the siamux.
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, err
	}

	// Create the modules.
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		return nil, err
	}
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, err
	}
	encrypted, err := w.Encrypted()
	if err != nil {
		return nil, err
	}
	if !encrypted {
		_, err = w.Encrypt(key)
		if err != nil {
			return nil, err
		}
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, err
	}
	h, err := host.New(cs, g, tp, w, mux, "localhost:0", filepath.Join(testdir, modules.HostDir))
	if err != nil {
		return nil, err
	}
	rl := ratelimit.NewRateLimit(0, 0, 0)
	r, errChan := renter.New(g, cs, w, tp, mux, rl, filepath.Join(testdir, modules.RenterDir))
	if err := <-errChan; err != nil {
		return nil, err
	}
	srv, err := NewServer(testdir, "localhost:0", "Sia-Agent", requiredPassword, nil, cs, nil, g, h, m, r, tp, w)
	if err != nil {
		return nil, err
	}

	// Assemble the serverTester.
	st := &serverTester{
		cs:        cs,
		gateway:   g,
		host:      h,
		miner:     m,
		renter:    r,
		tpool:     tp,
		wallet:    w,
		walletKey: key,

		server: srv,

		dir: testdir,
	}

	// TODO: A more reasonable way of listening for server errors.
	go func() {
		listenErr := srv.Serve()
		if listenErr != nil {
			panic(listenErr)
		}
	}()
	return st, nil
}

// assembleExplorerServerTester creates all the explorer dependencies and
// explorer module without creating any directories. The user agent requirement
// is disabled.
func assembleExplorerServerTester(testdir string) (*serverTester, error) {
	// assembleExplorerServerTester should not get called during short tests,
	// as it takes a long time to run.
	if testing.Short() {
		panic("assembleServerTester called during short tests")
	}

	// Create the modules.
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, err
	}
	e, err := explorer.New(cs, filepath.Join(testdir, modules.ExplorerDir))
	if err != nil {
		return nil, err
	}
	srv, err := NewServer(testdir, "localhost:0", "", "", nil, cs, e, g, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}

	// Assemble the serverTester.
	st := &serverTester{
		cs:       cs,
		explorer: e,
		gateway:  g,

		server: srv,

		dir: testdir,
	}

	// TODO: A more reasonable way of listening for server errors.
	go func() {
		listenErr := srv.Serve()
		if listenErr != nil {
			panic(listenErr)
		}
	}()
	return st, nil
}

// blankServerTester creates a server tester object that is ready for testing,
// without mining any blocks.
func blankServerTester(name string) (*serverTester, error) {
	// createServerTester is expensive, and therefore should not be called
	// during short tests.
	if testing.Short() {
		panic("blankServerTester called during short tests")
	}

	// Create the server tester with key.
	testdir := build.TempDir("api", name)
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	st, err := assembleServerTester(key, testdir)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// createServerTesterWithDeps creates a server tester object with injected
// dependencies that is ready for testing, including money in the wallet and all
// modules initialized.
func createServerTesterWithDeps(name string, gDeps, cDeps, tDeps, wDeps, hDeps, rDeps, hdbDeps, hcDeps, csDeps, apiDeps modules.Dependencies) (*serverTester, error) {
	// createServerTester is expensive, and therefore should not be called
	// during short tests.
	if testing.Short() {
		panic("createServerTester called during short tests")
	}

	// Create the testing directory.
	testdir := build.TempDir("api", name)

	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	st, err := assembleServerTesterWithDeps(key, testdir, gDeps, cDeps, tDeps, wDeps, hDeps, rDeps, hdbDeps, hcDeps, csDeps, apiDeps)
	if err != nil {
		return nil, err
	}

	// Mine blocks until the wallet has confirmed money and we are beyond the
	// tax hardfork height.
	for i := types.BlockHeight(0); i <= types.TaxHardforkHeight+types.MaturityDelay; i++ {
		_, err := st.miner.AddBlock()
		if err != nil {
			return nil, err
		}
	}

	// Wait until the desired height was reached.
	for st.cs.Height() < types.TaxHardforkHeight+types.MaturityDelay {
		time.Sleep(10 * time.Millisecond)
	}
	return st, nil
}

// createServerTester creates a server tester object that is ready for testing,
// including money in the wallet and all modules initialized.
func createServerTester(name string) (*serverTester, error) {
	return createServerTesterWithDeps(name, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies, modules.ProdDependencies)
}

// createAuthenticatedServerTester creates an authenticated server tester
// object that is ready for testing, including money in the wallet and all
// modules initialized.
func createAuthenticatedServerTester(name string, password string) (*serverTester, error) {
	// createAuthenticatedServerTester should not get called during short
	// tests, as it takes a long time to run.
	if testing.Short() {
		panic("assembleServerTester called during short tests")
	}

	// Create the testing directory.
	testdir := build.TempDir("authenticated-api", name)

	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	st, err := assembleAuthenticatedServerTester(password, key, testdir)
	if err != nil {
		return nil, err
	}

	// Mine blocks until the wallet has confirmed money.
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		_, err := st.miner.AddBlock()
		if err != nil {
			return nil, err
		}
	}

	return st, nil
}

// createExplorerServerTester creates a server tester object containing only
// the explorer and some presets that match standard explorer setups.
func createExplorerServerTester(name string) (*serverTester, error) {
	testdir := build.TempDir("api", name)
	st, err := assembleExplorerServerTester(testdir)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// decodeError returns the api.Error from a API response. This method should
// only be called if the response's status code is non-2xx. The error returned
// may not be of type api.Error in the event of an error unmarshalling the
// JSON.
func decodeError(resp *http.Response) error {
	var apiErr Error
	err := json.NewDecoder(resp.Body).Decode(&apiErr)
	if err != nil {
		return err
	}
	return apiErr
}

// non2xx returns true for non-success HTTP status codes.
func non2xx(code int) bool {
	return code < 200 || code > 299
}

// panicClose attempts to close a serverTester. If it fails, panic is called
// with the error.
func (st *serverTester) panicClose() {
	st.server.panicClose()
}

// reloadedServerTester creates a server tester where all of the persistent
// data has been copied to a new folder and all of the modules re-initialized
// on the new folder. This gives an opportunity to see how modules will behave
// when they are relying on their persistent structures.
func (st *serverTester) reloadedServerTester() (*serverTester, error) {
	// Copy the testing directory.
	copiedDir := st.dir + " - " + persist.RandomSuffix()
	err := build.CopyDir(st.dir, copiedDir)
	if err != nil {
		return nil, err
	}
	copyST, err := assembleServerTester(st.walletKey, copiedDir)
	if err != nil {
		return nil, err
	}
	return copyST, nil
}

// acceptContracts instructs the host to begin accepting contracts.
func (st *serverTester) acceptContracts() error {
	settingsValues := url.Values{}
	settingsValues.Set("acceptingcontracts", "true")
	return st.stdPostAPI("/host", settingsValues)
}

// setHostStorage adds a storage folder to the host.
func (st *serverTester) setHostStorage() error {
	values := url.Values{}
	values.Set("path", st.dir)
	values.Set("size", "1048576")
	return st.stdPostAPI("/host/storage/folders/add", values)
}

// announceHost announces the host, mines a block, and waits for the
// announcement to register.
func (st *serverTester) announceHost() error {
	// Check how many hosts there are to begin with.
	var hosts HostdbActiveGET
	err := st.getAPI("/hostdb/active", &hosts)
	if err != nil {
		return err
	}
	initialHosts := make(map[modules.NetAddress]struct{})
	// Create map of initialHosts
	for _, h := range hosts.Hosts {
		initialHosts[h.NetAddress] = struct{}{}
	}

	// Set the host to be accepting contracts.
	acceptingContractsValues := url.Values{}
	acceptingContractsValues.Set("acceptingcontracts", "true")
	err = st.stdPostAPI("/host", acceptingContractsValues)
	if err != nil {
		return build.ExtendErr("couldn't make an api call to the host:", err)
	}

	announceValues := url.Values{}
	announceValues.Set("address", string(st.host.ExternalSettings().NetAddress))
	err = st.stdPostAPI("/host/announce", announceValues)
	if err != nil {
		return err
	}
	// mine block
	_, err = st.miner.AddBlock()
	if err != nil {
		return err
	}
	// wait for announcement
	return build.Retry(100, 100*time.Millisecond, func() error {
		err = st.getAPI("/hostdb/active", &hosts)
		if err != nil {
			return err
		}
		// If we see more hosts now, then we return successful
		if len(hosts.Hosts) > len(initialHosts) {
			return nil
		}
		for _, h := range hosts.Hosts {
			_, ok := initialHosts[h.NetAddress]
			if !ok {
				// If we see a new hosts, then we return successful
				return nil
			}
		}
		// There are no new hosts.
		return errors.New("host announcement not seen")
	})
}

// getAPI makes an API call and decodes the response.
func (st *serverTester) getAPI(call string, obj interface{}) (err error) {
	resp, err := HttpGET("http://" + st.server.listener.Addr().String() + call)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, resp.Body.Close())
	}()

	if non2xx(resp.StatusCode) {
		return decodeError(resp)
	}

	// Return early because there is no content to decode.
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	// Decode the response into 'obj'.
	err = json.NewDecoder(resp.Body).Decode(obj)
	if err != nil {
		return err
	}
	return nil
}

// postAPI makes an API call and decodes the response.
func (st *serverTester) postAPI(call string, values url.Values, obj interface{}) (err error) {
	resp, err := HttpPOST("http://"+st.server.listener.Addr().String()+call, values.Encode())
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, resp.Body.Close())
	}()

	if non2xx(resp.StatusCode) {
		return decodeError(resp)
	}

	// Return early because there is no content to decode.
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}

	// Decode the response into 'obj'.
	err = json.NewDecoder(resp.Body).Decode(obj)
	if err != nil {
		return err
	}
	return nil
}

// stdGetAPI makes an API call and discards the response.
func (st *serverTester) stdGetAPI(call string) (err error) {
	resp, err := HttpGET("http://" + st.server.listener.Addr().String() + call)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, resp.Body.Close())
	}()

	if non2xx(resp.StatusCode) {
		return decodeError(resp)
	}
	return nil
}

// stdGetAPIUA makes an API call with a custom user agent.
func (st *serverTester) stdGetAPIUA(call string, userAgent string) (err error) {
	req, err := http.NewRequest("GET", "http://"+st.server.listener.Addr().String()+call, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, resp.Body.Close())
	}()

	if non2xx(resp.StatusCode) {
		return decodeError(resp)
	}
	return nil
}

// stdPostAPI makes an API call and discards the response.
func (st *serverTester) stdPostAPI(call string, values url.Values) (err error) {
	resp, err := HttpPOST("http://"+st.server.listener.Addr().String()+call, values.Encode())
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Compose(err, resp.Body.Close())
	}()

	if non2xx(resp.StatusCode) {
		return decodeError(resp)
	}
	return nil
}
