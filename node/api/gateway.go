package api

import (
	"fmt"
	"net/http"

	"gitlab.com/NebulousLabs/Sia/modules"

	"github.com/julienschmidt/httprouter"
)

// GatewayGET contains the fields returned by a GET call to "/gateway".
type GatewayGET struct {
	NetAddress modules.NetAddress `json:"netaddress"`
	Peers      []modules.Peer     `json:"peers"`

	MaxDownloadSpeed       int64 `json:"maxdownloadspeed"`
	MaxUploadSpeed         int64 `json:"maxuploadspeed"`
	MaxGlobalDownloadSpeed int64 `json:"maxglobaldownloadspeed"`
	MaxGlobalUploadSpeed   int64 `json:"maxglobaluploadspeed"`
}

// gatewayHandlerGET handles the API call asking for the gatway status.
func (api *API) gatewayHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	peers := api.gateway.Peers()
	mds, mus := api.gateway.RateLimits()
	gmds, gmus, _ := modules.GlobalRateLimits.Limits()
	// nil slices are marshalled as 'null' in JSON, whereas 0-length slices are
	// marshalled as '[]'. The latter is preferred, indicating that the value
	// exists but contains no elements.
	if peers == nil {
		peers = make([]modules.Peer, 0)
	}
	WriteJSON(w, GatewayGET{api.gateway.Address(), peers, mds, mus, gmds, gmus})
}

// gatewayHandlerPOST handles the API call changing gateway specific settings.
func (api *API) gatewayHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	maxDownloadSpeed, maxUploadSpeed := api.gateway.RateLimits()
	maxGlobalDownloadSpeed, maxGlobalUploadSpeed, _ := modules.GlobalRateLimits.Limits()
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
	// Scan the download speed limit. (optional parameter)
	if d := req.FormValue("maxglobaldownloadspeed"); d != "" {
		var downloadSpeed int64
		if _, err := fmt.Sscan(d, &downloadSpeed); err != nil {
			WriteError(w, Error{"unable to parse downloadspeed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		maxGlobalDownloadSpeed = downloadSpeed
	}
	// Scan the upload speed limit. (optional parameter)
	if u := req.FormValue("maxglobaluploadspeed"); u != "" {
		var uploadSpeed int64
		if _, err := fmt.Sscan(u, &uploadSpeed); err != nil {
			WriteError(w, Error{"unable to parse uploadspeed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		maxGlobalUploadSpeed = uploadSpeed
	}
	// Try to set the limits.
	err := api.gateway.SetRateLimits(maxDownloadSpeed, maxUploadSpeed)
	if err != nil {
		WriteError(w, Error{"failed to set new rate limit: " + err.Error()}, http.StatusBadRequest)
		return
	}
	err = api.gateway.SetGlobalRateLimits(maxGlobalDownloadSpeed, maxGlobalUploadSpeed)
	if err != nil {
		WriteError(w, Error{"failed to set new global rate limit: " + err.Error()}, http.StatusBadRequest)
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
