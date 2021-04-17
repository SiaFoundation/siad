package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
)

const (
	// StatusModuleNotLoaded is a custom http code to indicate that a module
	// wasn't yet loaded by the Daemon and can therefore not be reached.
	StatusModuleNotLoaded = 490

	// StatusModuleDisabled is a custom http code to indicate that a module was
	// disabled by the Daemon and can therefore not be reached.
	StatusModuleDisabled = 491
)

// ErrAPICallNotRecognized is returned by API client calls made to modules that
// are not yet loaded.
var ErrAPICallNotRecognized = errors.New("API call not recognized")

// Error is a type that is encoded as JSON and returned in an API response in
// the event of an error. Only the Message field is required. More fields may
// be added to this struct in the future for better error reporting.
type Error struct {
	// Message describes the error in English. Typically it is set to
	// `err.Error()`. This field is required.
	Message string `json:"message"`

	// TODO: add a Param field with the (omitempty option in the json tag)
	// to indicate that the error was caused by an invalid, missing, or
	// incorrect parameter. This is not trivial as the API does not
	// currently do parameter validation itself. For example, the
	// /gateway/connect endpoint relies on the gateway.Connect method to
	// validate the netaddress. However, this prevents the API from knowing
	// whether an error returned by gateway.Connect is because of a
	// connection error or an invalid netaddress parameter. Validating
	// parameters in the API is not sufficient, as a parameter's value may
	// be valid or invalid depending on the current state of a module.
}

// Error implements the error interface for the Error type. It returns only the
// Message field.
func (err Error) Error() string {
	return err.Message
}

// HttpGET is a utility function for making http get requests to sia with a
// whitelisted user-agent. A non-2xx response does not return an error.
func HttpGET(url string) (resp *http.Response, err error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sia-Agent")
	return http.DefaultClient.Do(req)
}

// HttpGETAuthenticated is a utility function for making authenticated http get
// requests to sia with a whitelisted user-agent and the supplied password. A
// non-2xx response does not return an error.
func HttpGETAuthenticated(url string, password string) (resp *http.Response, err error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sia-Agent")
	req.SetBasicAuth("", password)
	return http.DefaultClient.Do(req)
}

// HttpPOST is a utility function for making post requests to sia with a
// whitelisted user-agent. A non-2xx response does not return an error.
func HttpPOST(url string, data string) (resp *http.Response, err error) {
	req, err := http.NewRequest("POST", url, strings.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sia-Agent")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return http.DefaultClient.Do(req)
}

// HttpPOSTAuthenticated is a utility function for making authenticated http
// post requests to sia with a whitelisted user-agent and the supplied
// password. A non-2xx response does not return an error.
func HttpPOSTAuthenticated(url string, data string, password string) (resp *http.Response, err error) {
	req, err := http.NewRequest("POST", url, strings.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Sia-Agent")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("", password)
	return http.DefaultClient.Do(req)
}

type (
	// API encapsulates a collection of modules and implements a http.Handler
	// to access their methods.
	API struct {
		accounting          modules.Accounting
		cs                  modules.ConsensusSet
		explorer            modules.Explorer
		gateway             modules.Gateway
		host                modules.Host
		miner               modules.Miner
		renter              modules.Renter
		tpool               modules.TransactionPool
		wallet              modules.Wallet
		staticConfigModules configModules
		modulesSet          bool

		downloadMu sync.Mutex
		downloads  map[modules.DownloadID]func()
		router     http.Handler
		routerMu   sync.RWMutex

		requiredUserAgent string
		requiredPassword  string
		Shutdown          func() error
		siadConfig        *modules.SiadConfig

		staticStartTime time.Time

		staticDeps modules.Dependencies
	}

	// configModules contains booleans that indicate if a module was part of the
	// configuration when the API was created
	configModules struct {
		Accounting      bool `json:"accounting"`
		Consensus       bool `json:"consensus"`
		Explorer        bool `json:"explorer"`
		Gateway         bool `json:"gateway"`
		Host            bool `json:"host"`
		Miner           bool `json:"miner"`
		Renter          bool `json:"renter"`
		TransactionPool bool `json:"transactionpool"`
		Wallet          bool `json:"wallet"`
	}
)

// api.ServeHTTP implements the http.Handler interface.
func (api *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	api.routerMu.RLock()
	api.router.ServeHTTP(w, r)
	api.routerMu.RUnlock()
}

// SetModules allows for replacing the modules in the API at runtime.
func (api *API) SetModules(acc modules.Accounting, cs modules.ConsensusSet, e modules.Explorer, g modules.Gateway, h modules.Host, m modules.Miner, r modules.Renter, tp modules.TransactionPool, w modules.Wallet) {
	if api.modulesSet {
		build.Critical("can't call SetModules more than once")
	}
	api.accounting = acc
	api.cs = cs
	api.explorer = e
	api.gateway = g
	api.host = h
	api.miner = m
	api.renter = r
	api.tpool = tp
	api.wallet = w
	api.staticConfigModules = configModules{
		Accounting:      api.accounting != nil,
		Consensus:       api.cs != nil,
		Explorer:        api.explorer != nil,
		Gateway:         api.gateway != nil,
		Host:            api.host != nil,
		Miner:           api.miner != nil,
		Renter:          api.renter != nil,
		TransactionPool: api.tpool != nil,
		Wallet:          api.wallet != nil,
	}
	api.modulesSet = true
	api.buildHTTPRoutes()
}

// StartTime returns the time at which the API started
func (api *API) StartTime() time.Time {
	return api.staticStartTime
}

// New creates a new Sia API from the provided modules. The API will require
// authentication using HTTP basic auth for certain endpoints of the supplied
// password is not the empty string.  Usernames are ignored for authentication.
func New(cfg *modules.SiadConfig, requiredUserAgent string, requiredPassword string, acc modules.Accounting, cs modules.ConsensusSet, e modules.Explorer, g modules.Gateway, h modules.Host, m modules.Miner, r modules.Renter, tp modules.TransactionPool, w modules.Wallet) *API {
	return NewCustom(cfg, requiredUserAgent, requiredPassword, acc, cs, e, g, h, m, r, tp, w, modules.ProdDependencies)
}

// NewCustom creates a new Sia API from the provided modules. The API will
// require authentication using HTTP basic auth for certain endpoints of the
// supplied password is not the empty string. Usernames are ignored for
// authentication. It is custom because it allows to inject custom dependencies
// into the API.
func NewCustom(cfg *modules.SiadConfig, requiredUserAgent string, requiredPassword string, acc modules.Accounting, cs modules.ConsensusSet, e modules.Explorer, g modules.Gateway, h modules.Host, m modules.Miner, r modules.Renter, tp modules.TransactionPool, w modules.Wallet, deps modules.Dependencies) *API {
	api := &API{
		accounting:        acc,
		cs:                cs,
		explorer:          e,
		gateway:           g,
		host:              h,
		miner:             m,
		renter:            r,
		tpool:             tp,
		wallet:            w,
		downloads:         make(map[modules.DownloadID]func()),
		requiredUserAgent: requiredUserAgent,
		requiredPassword:  requiredPassword,
		siadConfig:        cfg,

		staticDeps:      deps,
		staticStartTime: time.Now(),
	}

	// Register API handlers
	api.buildHTTPRoutes()

	return api
}

// UnrecognizedCallHandler handles calls to disabled/not-loaded modules.
func (api *API) UnrecognizedCallHandler(w http.ResponseWriter, _ *http.Request) {
	var errStr string
	if api.modulesSet {
		errStr = fmt.Sprintf("%d Module disabled - Refer to API.md", StatusModuleDisabled)
		WriteError(w, Error{errStr}, StatusModuleDisabled)
	} else {
		errStr = fmt.Sprintf("%d Module not loaded - Refer to API.md", StatusModuleNotLoaded)
		WriteError(w, Error{errStr}, StatusModuleNotLoaded)
	}
}

// WriteError an error to the API caller.
func WriteError(w http.ResponseWriter, err Error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	encodingErr := json.NewEncoder(w).Encode(err)
	if _, isJsonErr := encodingErr.(*json.SyntaxError); isJsonErr {
		// Marshalling should only fail in the event of a developer error.
		// Specifically, only non-marshallable types should cause an error here.
		build.Critical("failed to encode API error response:", encodingErr)
	}
}

// WriteJSON writes the object to the ResponseWriter. If the encoding fails, an
// error is written instead. The Content-Type of the response header is set
// accordingly.
func WriteJSON(w http.ResponseWriter, obj interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	err := json.NewEncoder(w).Encode(obj)
	if _, isJsonErr := err.(*json.SyntaxError); isJsonErr {
		// Marshalling should only fail in the event of a developer error.
		// Specifically, only non-marshallable types should cause an error here.
		build.Critical("failed to encode API response:", err)
	}
}

// WriteSuccess writes the HTTP header with status 204 No Content to the
// ResponseWriter. WriteSuccess should only be used to indicate that the
// requested action succeeded AND there is no data to return.
func WriteSuccess(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}
