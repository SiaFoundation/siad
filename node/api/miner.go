package api

import (
	"net/http"

	"github.com/julienschmidt/httprouter"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

type (
	// MinerGET contains the information that is returned after a GET request
	// to /miner.
	MinerGET struct {
		BlocksMined      int  `json:"blocksmined"`
		CPUHashrate      int  `json:"cpuhashrate"`
		CPUMining        bool `json:"cpumining"`
		StaleBlocksMined int  `json:"staleblocksmined"`
	}
)

// RegisterRoutesMiner is a helper function to register all miner routes.
func RegisterRoutesMiner(router *httprouter.Router, m modules.Miner, requiredPassword string) {
	router.GET("/miner", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		minerHandler(m, w, req, ps)
	})
	router.POST("/miner/block", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		minerBlockHandlerPOST(m, w, req, ps)
	}, requiredPassword))
	router.GET("/miner/header", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		minerHeaderHandlerGET(m, w, req, ps)
	}, requiredPassword))
	router.POST("/miner/header", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		minerHeaderHandlerPOST(m, w, req, ps)
	}, requiredPassword))
	router.GET("/miner/start", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		minerStartHandler(m, w, req, ps)
	}, requiredPassword))
	router.GET("/miner/stop", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		minerStopHandler(m, w, req, ps)
	}, requiredPassword))
}

// minerHandler handles the API call that queries the miner's status.
func minerHandler(miner modules.Miner, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	blocksMined, staleMined := miner.BlocksMined()
	mg := MinerGET{
		BlocksMined:      blocksMined,
		CPUHashrate:      miner.CPUHashrate(),
		CPUMining:        miner.CPUMining(),
		StaleBlocksMined: staleMined,
	}
	WriteJSON(w, mg)
}

// minerStartHandler handles the API call that starts the miner.
func minerStartHandler(miner modules.Miner, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	miner.StartCPUMining()
	WriteSuccess(w)
}

// minerStopHandler handles the API call to stop the miner.
func minerStopHandler(miner modules.Miner, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	miner.StopCPUMining()
	WriteSuccess(w)
}

// minerHeaderHandlerGET handles the API call that retrieves a block header
// for work.
func minerHeaderHandlerGET(miner modules.Miner, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	bhfw, target, err := miner.HeaderForWork()
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	w.Write(encoding.MarshalAll(target, bhfw))
}

// minerHeaderHandlerPOST handles the API call to submit a block header to the
// miner.
func minerHeaderHandlerPOST(miner modules.Miner, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var bh types.BlockHeader
	err := encoding.NewDecoder(req.Body, encoding.DefaultAllocLimit).Decode(&bh)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	err = miner.SubmitHeader(bh)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// minerBlockHandlerPOST handles the API call to submit a solved block to the
// miner.
func minerBlockHandlerPOST(miner modules.Miner, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var b types.Block
	err := encoding.NewDecoder(req.Body, encoding.DefaultAllocLimit).Decode(&b)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	err = miner.SubmitBlock(b)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
