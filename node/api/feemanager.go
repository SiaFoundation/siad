package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
	"gitlab.com/NebulousLabs/Sia/modules"
)

type (
	// FeeManagerGET is the object returned as a response to a GET request to
	// /feemanager.
	FeeManagerGET struct {
		// Settings are the current settings of the FeeManager
		Settings modules.FeeManagerSettings `json:"settings"`

		// This is the list of current pending Fees
		PendingFees []modules.AppFee `json:"pendingfees"`

		// This is a full historical list of Fees that have been Paid
		PaidFees []modules.AppFee `json:"paidfees"`
	}
)

// feemanagerHandlerGET handles API calls to /feemanager
func (api *API) feemanagerHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	settings, err := api.feemanager.Settings()
	if err != nil {
		WriteError(w, Error{"could not get the settings of the FeeManager: " + err.Error()}, http.StatusBadRequest)
		return
	}
	pending, paid, err := api.feemanager.Fees()
	if err != nil {
		WriteError(w, Error{"could not get the fees of the FeeManager: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, FeeManagerGET{
		Settings:    settings,
		PendingFees: pending,
		PaidFees:    paid,
	})
}

// feemanagerCancelHandlerPOST handles API calls to /feemanager/cancel
func (api *API) feemanagerCancelHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Scan for feeuid - REQUIRED
	feeUID := req.FormValue("feeuid")
	if feeUID == "" {
		WriteError(w, Error{"feeuid cannot be blank"}, http.StatusBadRequest)
		return
	}

	// Cancel the fee
	err := api.feemanager.CancelFee(modules.FeeUID(feeUID))
	if err != nil {
		WriteError(w, Error{"could not cancel the fee: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Return successful
	WriteSuccess(w)
}

// feemanagerSetHandlerPOST handles API calls to /feemanager/set
func (api *API) feemanagerSetHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Scan for amount - REQUIRED
	if req.FormValue("amount") == "" {
		WriteError(w, Error{"amount cannot be blank"}, http.StatusBadRequest)
		return
	}
	amount, ok := scanAmount(req.FormValue("amount"))
	if !ok {
		WriteError(w, Error{"could not read amount"}, http.StatusBadRequest)
		return
	}

	// Scan for address - REQUIRED
	if req.FormValue("address") == "" {
		WriteError(w, Error{"address cannot be blank"}, http.StatusBadRequest)
		return
	}
	address, err := scanAddress(req.FormValue("address"))
	if err != nil {
		WriteError(w, Error{"could not read address: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Scan for appuid - REQUIRED
	appUIDstr := req.FormValue("appuid")
	if appUIDstr == "" {
		WriteError(w, Error{"appuid cannot be blank"}, http.StatusBadRequest)
		return
	}

	// Scan for reoccuring - OPTIONAL
	var reoccuring bool
	if r := req.FormValue("reoccuring"); r != "" {
		reoccuring, err = scanBool(r)
		if err != nil {
			WriteError(w, Error{"could not read reoccuring: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Set the fee
	err = api.feemanager.SetFee(address, amount, modules.AppUID(appUIDstr), reoccuring)
	if err != nil {
		WriteError(w, Error{"could not set the fee: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Return successful
	WriteSuccess(w)
}
