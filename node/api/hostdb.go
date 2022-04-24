package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/julienschmidt/httprouter"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

type (
	// ExtendedHostDBEntry is an extension to modules.HostDBEntry that includes
	// the string representation of the public key, otherwise presented as two
	// fields, a string and a base64 encoded byte slice.
	ExtendedHostDBEntry struct {
		modules.HostDBEntry
		PublicKeyString string `json:"publickeystring"`
	}

	// HostdbActiveGET lists active hosts on the network.
	HostdbActiveGET struct {
		Hosts []ExtendedHostDBEntry `json:"hosts"`
	}

	// HostdbAllGET lists all hosts that the renter is aware of.
	HostdbAllGET struct {
		Hosts []ExtendedHostDBEntry `json:"hosts"`
	}

	// HostdbHostsGET lists detailed statistics for a particular host, selected
	// by pubkey.
	HostdbHostsGET struct {
		Entry          ExtendedHostDBEntry        `json:"entry"`
		ScoreBreakdown modules.HostScoreBreakdown `json:"scorebreakdown"`
	}

	// HostdbGet holds information about the hostdb.
	HostdbGet struct {
		InitialScanComplete bool `json:"initialscancomplete"`
	}

	// HostdbFilterModeGET contains the information about the HostDB's
	// filtermode
	HostdbFilterModeGET struct {
		FilterMode   string   `json:"filtermode"`
		Hosts        []string `json:"hosts"`
		NetAddresses []string `json:"netaddresses"`
	}

	// HostdbFilterModePOST contains the information needed to set the the
	// FilterMode of the hostDB
	HostdbFilterModePOST struct {
		FilterMode   string               `json:"filtermode"`
		Hosts        []types.SiaPublicKey `json:"hosts"`
		NetAddresses []string             `json:"netaddresses"`
	}
)

// hostdbHandler handles the API call asking for the list of active
// hosts.
func (api *API) hostdbHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	isc, err := api.renter.InitialScanComplete()
	if err != nil {
		WriteError(w, Error{"Failed to get initial scan status: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, HostdbGet{
		InitialScanComplete: isc,
	})
}

// hostdbActiveHandler handles the API call asking for the list of active
// hosts.
func (api *API) hostdbActiveHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var numHosts uint64
	hosts, err := api.renter.ActiveHosts()
	if err != nil {
		WriteError(w, Error{"unable to get active hosts: " + err.Error()}, http.StatusBadRequest)
		return
	}

	if req.FormValue("numhosts") == "" {
		// Default value for 'numhosts' is all of them.
		numHosts = uint64(len(hosts))
	} else {
		// Parse the value for 'numhosts'.
		_, err := fmt.Sscan(req.FormValue("numhosts"), &numHosts)
		if err != nil {
			WriteError(w, Error{"unable to parse numhosts: " + err.Error()}, http.StatusBadRequest)
			return
		}

		// Catch any boundary errors.
		if numHosts > uint64(len(hosts)) {
			numHosts = uint64(len(hosts))
		}
	}

	// Convert the entries into extended entries.
	var extendedHosts []ExtendedHostDBEntry
	for _, host := range hosts {
		extendedHosts = append(extendedHosts, ExtendedHostDBEntry{
			HostDBEntry:     host,
			PublicKeyString: host.PublicKey.String(),
		})
	}

	WriteJSON(w, HostdbActiveGET{
		Hosts: extendedHosts[:numHosts],
	})
}

// hostdbAllHandler handles the API call asking for the list of all hosts.
func (api *API) hostdbAllHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get the set of all hosts and convert them into extended hosts.
	hosts, err := api.renter.AllHosts()
	if err != nil {
		WriteError(w, Error{"unable to get all hosts: " + err.Error()}, http.StatusBadRequest)
		return
	}
	var extendedHosts []ExtendedHostDBEntry
	for _, host := range hosts {
		extendedHosts = append(extendedHosts, ExtendedHostDBEntry{
			HostDBEntry:     host,
			PublicKeyString: host.PublicKey.String(),
		})
	}

	WriteJSON(w, HostdbAllGET{
		Hosts: extendedHosts,
	})
}

// hostdbHostsHandler handles the API call asking for a specific host,
// returning detailed information about that host.
func (api *API) hostdbHostsHandler(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	var pk types.SiaPublicKey
	pk.LoadString(ps.ByName("pubkey"))

	entry, exists, err := api.renter.Host(pk)
	if err != nil {
		WriteError(w, Error{"unable to get host: " + err.Error()}, http.StatusBadRequest)
		return
	}
	if !exists {
		WriteError(w, Error{"requested host does not exist"}, http.StatusBadRequest)
		return
	}
	breakdown, err := api.renter.ScoreBreakdown(entry)
	if err != nil {
		WriteError(w, Error{"error calculating score breakdown: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	// Extend the hostdb entry  to have the public key string.
	extendedEntry := ExtendedHostDBEntry{
		HostDBEntry:     entry,
		PublicKeyString: entry.PublicKey.String(),
	}
	WriteJSON(w, HostdbHostsGET{
		Entry:          extendedEntry,
		ScoreBreakdown: breakdown,
	})
}

// hostdbFilterModeHandlerGET handles the API call to get the hostdb's filter
// mode
func (api *API) hostdbFilterModeHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get FilterMode
	fm, hostMap, netAddresses, err := api.renter.Filter()
	if err != nil {
		WriteError(w, Error{"unable to get filter mode: " + err.Error()}, http.StatusBadRequest)
		return
	}
	// Build Slice of PubKeys
	var hosts []string
	for key := range hostMap {
		hosts = append(hosts, key)
	}
	WriteJSON(w, HostdbFilterModeGET{
		FilterMode:   fm.String(),
		Hosts:        hosts,
		NetAddresses: netAddresses,
	})
}

// hostdbFilterModeHandlerPOST handles the API call to set the hostdb's filter
// mode
func (api *API) hostdbFilterModeHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters
	var params HostdbFilterModePOST
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}

	var fm modules.FilterMode
	if err = fm.FromString(params.FilterMode); err != nil {
		WriteError(w, Error{"unable to load filter mode from string: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Set list mode
	if err := api.renter.SetFilterMode(fm, params.Hosts, params.NetAddresses); err != nil {
		WriteError(w, Error{"failed to set the list mode: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
