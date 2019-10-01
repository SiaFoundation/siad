package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"

	"gitlab.com/NebulousLabs/Sia/modules"
)

type (
	// GatewayGET contains the fields returned by a GET call to "/gateway".
	GatewayGET struct {
		NetAddress modules.NetAddress `json:"netaddress"`
		Peers      []modules.Peer     `json:"peers"`

		MaxDownloadSpeed int64 `json:"maxdownloadspeed"`
		MaxUploadSpeed   int64 `json:"maxuploadspeed"`
	}

	// GatewayBlacklistPOST contains the information needed to set the Blacklist
	// of the gateway
	GatewayBlacklistPOST struct {
		Action    string               `json:"action"`
		Addresses []modules.NetAddress `json:"addresses"`
	}

	// GatewayBlacklistGET contains the Blacklist of the gateway
	GatewayBlacklistGET struct {
		Blacklist []string `json:"blacklist"`
	}
)

// gatewayHandlerGET handles the API call asking for the gatway status.
func (api *API) gatewayHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	peers := api.gateway.Peers()
	mds, mus := api.gateway.RateLimits()
	// nil slices are marshalled as 'null' in JSON, whereas 0-length slices are
	// marshalled as '[]'. The latter is preferred, indicating that the value
	// exists but contains no elements.
	if peers == nil {
		peers = make([]modules.Peer, 0)
	}
	WriteJSON(w, GatewayGET{api.gateway.Address(), peers, mds, mus})
}

// gatewayHandlerPOST handles the API call changing gateway specific settings.
func (api *API) gatewayHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	maxDownloadSpeed, maxUploadSpeed := api.gateway.RateLimits()
	// Scan the download speed limit. (optional parameter)
	if d := req.FormValue("maxdownloadspeed"); d != "" {
		var downloadSpeed int64
		if _, err := fmt.Sscan(d, &downloadSpeed); err != nil {
			WriteError(w, Error{"unable to parse downloadspeed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		maxDownloadSpeed = downloadSpeed
	}
	// Scan the upload speed limit. (optional parameter)
	if u := req.FormValue("maxuploadspeed"); u != "" {
		var uploadSpeed int64
		if _, err := fmt.Sscan(u, &uploadSpeed); err != nil {
			WriteError(w, Error{"unable to parse uploadspeed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		maxUploadSpeed = uploadSpeed
	}
	// Try to set the limits.
	err := api.gateway.SetRateLimits(maxDownloadSpeed, maxUploadSpeed)
	if err != nil {
		WriteError(w, Error{"failed to set new rate limit: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// gatewayConnectHandler handles the API call to add a peer to the gateway.
func (api *API) gatewayConnectHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	addr := modules.NetAddress(ps.ByName("netaddress"))
	err := api.gateway.ConnectManual(addr)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// gatewayDisconnectHandler handles the API call to remove a peer from the gateway.
func (api *API) gatewayDisconnectHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	addr := modules.NetAddress(ps.ByName("netaddress"))
	err := api.gateway.DisconnectManual(addr)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// gatewayBlacklistHandlerGET handles the API call to get the gateway's
// blacklist
func (api *API) gatewayBlacklistHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get Blacklist
	blacklist, err := api.gateway.Blacklist()
	if err != nil {
		WriteError(w, Error{"unable to get blacklist mode: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, GatewayBlacklistGET{
		Blacklist: blacklist,
	})
}

// gatewayBlacklistHandlerPOST handles the API call to set the gateway's filter
//
// Addresses will be passed in as an array of strings, comma separated net
// addresses
func (api *API) gatewayBlacklistHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters
	var params GatewayBlacklistPOST
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}
	var gbo modules.GatewayBlacklistOp
	if err = gbo.FromString(params.Action); err != nil {
		WriteError(w, Error{"unable to load filter mode from string: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Check that addresses where submitted if the action is append or remove
	switch gbo {
	case modules.GatewayAppendToBlacklist:
		fallthrough
	case modules.GatewayRemoveFromBlacklist:
		if len(params.Addresses) == 0 {
			WriteError(w, Error{"no addresses submitted to append or remove"}, http.StatusBadRequest)
			return
		}
	default:
	}

	// Set Blacklist
	if err := api.gateway.SetBlacklist(gbo, params.Addresses); err != nil {
		WriteError(w, Error{"failed to set the blacklist: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
