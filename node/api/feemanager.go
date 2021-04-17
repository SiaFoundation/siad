package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

type (
	// FeeManagerGET is the object returned as a response to a GET request to
	// /feemanager
	FeeManagerGET struct {
		PayoutHeight types.BlockHeight `json:"payoutheight"`
	}

	// FeeManagerAddFeePOST is the object returned as a response to a POST
	// request to /feemanager/add
	FeeManagerAddFeePOST struct {
		// FeeUID is the UID of the Fee that was just added to the FeeManager
		FeeUID modules.FeeUID `json:"feeuid"`
	}

	// FeeManagerPaidFeesGET is the object returned as a response to a GET
	// request to /feemanager/paidfees
	FeeManagerPaidFeesGET struct {
		// This is a full historical list of Fees that have been Paid
		PaidFees []modules.AppFee `json:"paidfees"`
	}

	// FeeManagerPendingFeesGET is the object returned as a response to a GET
	// request to /feemanager/pendingfees
	FeeManagerPendingFeesGET struct {
		// This is the list of current pending Fees
		PendingFees []modules.AppFee `json:"pendingfees"`
	}
)

// RegisterRoutesFeeManager is a helper function to register all feemanager
// routes.
func RegisterRoutesFeeManager(router *httprouter.Router, fm modules.FeeManager, requiredPassword string) {
	router.GET("/feemanager", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		feemanagerHandlerGET(fm, w, req, ps)
	})
	router.POST("/feemanager/add", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		feemanagerAddHandlerPOST(fm, w, req, ps)
	}, requiredPassword))
	router.POST("/feemanager/cancel", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		feemanagerCancelHandlerPOST(fm, w, req, ps)
	}, requiredPassword))
	router.GET("/feemanager/paidfees", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		feemanagerPaidFeesHandlerGET(fm, w, req, ps)
	})
	router.GET("/feemanager/pendingfees", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		feemanagerPendingFeesHandlerGET(fm, w, req, ps)
	})
}

// feemanagerHandlerGET handles API calls to /feemanager
func feemanagerHandlerGET(feemanager modules.FeeManager, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	payoutheight, err := feemanager.PayoutHeight()
	if err != nil {
		WriteError(w, Error{"could not get the payoutHeight of the FeeManager: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, FeeManagerGET{
		PayoutHeight: payoutheight,
	})
}

// feemanagerAddHandlerPOST handles API calls to /feemanager/add
func feemanagerAddHandlerPOST(feemanager modules.FeeManager, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
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

	// Scan for recurring - OPTIONAL
	var recurring bool
	if r := req.FormValue("recurring"); r != "" {
		recurring, err = scanBool(r)
		if err != nil {
			WriteError(w, Error{"could not read recurring: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Add the fee
	feeUID, err := feemanager.AddFee(address, amount, modules.AppUID(appUIDstr), recurring)
	if err != nil {
		WriteError(w, Error{"could not set the fee: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	// Return the feeUID of the fee that was just added
	WriteJSON(w, FeeManagerAddFeePOST{
		FeeUID: feeUID,
	})
}

// feemanagerCancelHandlerPOST handles API calls to /feemanager/cancel
func feemanagerCancelHandlerPOST(feemanager modules.FeeManager, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Scan for feeuid - REQUIRED
	feeUID := req.FormValue("feeuid")
	if feeUID == "" {
		WriteError(w, Error{"feeuid cannot be blank"}, http.StatusBadRequest)
		return
	}

	// Cancel the fee
	err := feemanager.CancelFee(modules.FeeUID(feeUID))
	if err != nil {
		WriteError(w, Error{"could not cancel the fee: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	// Return successful
	WriteSuccess(w)
}

// feemanagerPaidFeesHandlerGET handles API calls to /feemanager/paidfees
func feemanagerPaidFeesHandlerGET(feemanager modules.FeeManager, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	paidFees, err := feemanager.PaidFees()
	if err != nil {
		WriteError(w, Error{"could not get the paid fees of the FeeManager: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, FeeManagerPaidFeesGET{
		PaidFees: paidFees,
	})
}

// feemanagerPendingFeesHandlerGET handles API calls to /feemanager/pendingfees
func feemanagerPendingFeesHandlerGET(feemanager modules.FeeManager, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	pendingFees, err := feemanager.PendingFees()
	if err != nil {
		WriteError(w, Error{"could not get the pending fees of the FeeManager: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, FeeManagerPendingFeesGET{
		PendingFees: pendingFees,
	})
}
