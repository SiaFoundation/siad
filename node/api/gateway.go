package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"

	"go.sia.tech/siad/modules"
)

type (
	// GatewayGET contains the fields returned by a GET call to "/gateway".
	GatewayGET struct {
		NetAddress modules.NetAddress `json:"netaddress"`
		Peers      []modules.Peer     `json:"peers"`
		Online     bool               `json:"online"`

		MaxDownloadSpeed int64 `json:"maxdownloadspeed"`
		MaxUploadSpeed   int64 `json:"maxuploadspeed"`
	}

	// GatewayBandwidthGET contains the bandwidth usage of the gateway
	GatewayBandwidthGET struct {
		Download  uint64    `json:"download"`
		Upload    uint64    `json:"upload"`
		StartTime time.Time `json:"starttime"`
	}

	// GatewayBlocklistPOST contains the information needed to set the Blocklist
	// of the gateway
	GatewayBlocklistPOST struct {
		Action    string   `json:"action"`
		Addresses []string `json:"addresses"`
	}

	// GatewayBlocklistGET contains the Blocklist of the gateway
	GatewayBlocklistGET struct {
		Blacklist []string `json:"blacklist"` // deprecated, kept for backwards compatibility
		Blocklist []string `json:"blocklist"`
	}
)

// RegisterRoutesGateway is a helper function to register all gateway routes.
func RegisterRoutesGateway(router *httprouter.Router, g modules.Gateway, requiredPassword string) {
	router.GET("/gateway", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayHandlerGET(g, w, req, ps)
	})
	router.POST("/gateway", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayHandlerPOST(g, w, req, ps)
	})
	router.GET("/gateway/bandwidth", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayBandwidthHandlerGET(g, w, req, ps)
	})
	router.POST("/gateway/connect/:netaddress", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayConnectHandler(g, w, req, ps)
	}, requiredPassword))
	router.POST("/gateway/disconnect/:netaddress", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayDisconnectHandler(g, w, req, ps)
	}, requiredPassword))
	router.GET("/gateway/blocklist", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayBlocklistHandlerGET(g, w, req, ps)
	})
	router.POST("/gateway/blocklist", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayBlocklistHandlerPOST(g, w, req, ps)
	}, requiredPassword))

	// Deprecated fields
	router.GET("/gateway/blacklist", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayBlocklistHandlerGET(g, w, req, ps)
	})
	router.POST("/gateway/blacklist", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		gatewayBlocklistHandlerPOST(g, w, req, ps)
	}, requiredPassword))
}

// gatewayHandlerGET handles the API call asking for the gateway status.
func gatewayHandlerGET(gateway modules.Gateway, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	peers := gateway.Peers()
	mds, mus := gateway.RateLimits()
	// nil slices are marshalled as 'null' in JSON, whereas 0-length slices are
	// marshalled as '[]'. The latter is preferred, indicating that the value
	// exists but contains no elements.
	if peers == nil {
		peers = make([]modules.Peer, 0)
	}
	WriteJSON(w, GatewayGET{gateway.Address(), peers, gateway.Online(), mds, mus})
}

// gatewayHandlerPOST handles the API call changing gateway specific settings.
func gatewayHandlerPOST(gateway modules.Gateway, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	maxDownloadSpeed, maxUploadSpeed := gateway.RateLimits()
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
	err := gateway.SetRateLimits(maxDownloadSpeed, maxUploadSpeed)
	if err != nil {
		WriteError(w, Error{"failed to set new rate limit: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// gatewayBandwidthHandlerGET handles the API call asking for the gateway's
// bandwidth usage.
func gatewayBandwidthHandlerGET(gateway modules.Gateway, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	upload, download, startTime, err := gateway.BandwidthCounters()
	if err != nil {
		WriteError(w, Error{"failed to get gateway's bandwidth usage: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, GatewayBandwidthGET{
		Download:  download,
		Upload:    upload,
		StartTime: startTime,
	})
}

// gatewayConnectHandler handles the API call to add a peer to the gateway.
func gatewayConnectHandler(gateway modules.Gateway, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	addr := modules.NetAddress(ps.ByName("netaddress"))
	err := gateway.ConnectManual(addr)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// gatewayDisconnectHandler handles the API call to remove a peer from the gateway.
func gatewayDisconnectHandler(gateway modules.Gateway, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	addr := modules.NetAddress(ps.ByName("netaddress"))
	err := gateway.DisconnectManual(addr)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// gatewayBlocklistHandlerGET handles the API call to get the gateway's
// blocklist
func gatewayBlocklistHandlerGET(gateway modules.Gateway, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get Blocklist
	blocklist, err := gateway.Blocklist()
	if err != nil {
		WriteError(w, Error{"unable to get blocklist mode: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, GatewayBlocklistGET{
		Blacklist: blocklist, // Returned for backwards compatibility
		Blocklist: blocklist,
	})
}

// gatewayBlocklistHandlerPOST handles the API call to modify the gateway's
// blocklist
//
// Addresses will be passed in as an array of strings, comma separated net
// addresses
func gatewayBlocklistHandlerPOST(gateway modules.Gateway, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters
	var params GatewayBlocklistPOST
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}

	switch params.Action {
	case "append":
		// Check that addresses where submitted
		if len(params.Addresses) == 0 {
			WriteError(w, Error{"no addresses submitted to append or remove"}, http.StatusBadRequest)
			return
		}
		// Add addresses to Blocklist
		if err := gateway.AddToBlocklist(params.Addresses); err != nil {
			WriteError(w, Error{"failed to add addresses to the blocklist: " + err.Error()}, http.StatusBadRequest)
			return
		}
	case "remove":
		// Check that addresses where submitted
		if len(params.Addresses) == 0 {
			WriteError(w, Error{"no addresses submitted to append or remove"}, http.StatusBadRequest)
			return
		}
		// Remove addresses from the Blocklist
		if err := gateway.RemoveFromBlocklist(params.Addresses); err != nil {
			WriteError(w, Error{"failed to remove addresses from the blocklist: " + err.Error()}, http.StatusBadRequest)
			return
		}
	case "set":
		// Set Blocklist
		if err := gateway.SetBlocklist(params.Addresses); err != nil {
			WriteError(w, Error{"failed to set the blocklist: " + err.Error()}, http.StatusBadRequest)
			return
		}
	default:
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}
