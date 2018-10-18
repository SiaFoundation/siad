package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"github.com/julienschmidt/httprouter"
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

	// HostdbListmodePOST contains the information needed to set the the
	// listmode of the hostDB
	HostdbListmodePOST struct {
		Mode  string               `json:"mode"`
		Hosts []types.SiaPublicKey `json:"hosts"`
	}
)

// hostdbHandler handles the API call asking for the list of active
// hosts.
func (api *API) hostdbHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	isc, err := api.renter.InitialScanComplete()
	if err != nil {
		WriteError(w, Error{"Failed to get initial scan status" + err.Error()}, http.StatusInternalServerError)
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
	hosts := api.renter.ActiveHosts()

	if req.FormValue("numhosts") == "" {
		// Default value for 'numhosts' is all of them.
		numHosts = uint64(len(hosts))
	} else {
		// Parse the value for 'numhosts'.
		_, err := fmt.Sscan(req.FormValue("numhosts"), &numHosts)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
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
func (api *API) hostdbAllHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Get the set of all hosts and convert them into extended hosts.
	hosts := api.renter.AllHosts()
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
func (api *API) hostdbHostsHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	var pk types.SiaPublicKey
	pk.LoadString(ps.ByName("pubkey"))

	entry, exists := api.renter.Host(pk, false)
	if !exists {
		WriteError(w, Error{"requested host does not exist"}, http.StatusBadRequest)
		return
	}
	breakdown := api.renter.ScoreBreakdown(entry)

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

// hostdbListModeHandlerPOST handles the API call to set the hostdb to whitelist
// of blacklist mode
func (api *API) hostdbListModeHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters
	var params HostdbListmodePOST
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Determine mode
	var whitelist bool
	if params.Mode == "whitelist" {
		whitelist = true
	}
	if !whitelist && params.Mode != "blacklist" && params.Mode != "disable" {
		WriteError(w, Error{"mode unrecognized, must be either whitelist, blacklist, or disable"}, http.StatusBadRequest)
		return
	}

	if err := api.renter.SetListMode(whitelist, params.Hosts); err != nil {
		WriteError(w, Error{"failed to set the list mode: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
