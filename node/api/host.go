package api

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/julienschmidt/httprouter"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	// errNoPath is returned when a call fails to provide a nonempty string
	// for the path parameter.
	errNoPath = Error{"path parameter is required"}

	// errStorageFolderNotFound is returned if a call is made looking for a
	// storage folder which does not appear to exist within the storage
	// manager.
	errStorageFolderNotFound = errors.New("storage folder with the provided path could not be found")

	// ErrInvalidRPCDownloadRatio is returned if the user tries to set a value
	// for the download price or the base RPC Price that violates the maximum
	// ratio
	ErrInvalidRPCDownloadRatio = errors.New("invalid ratio between the download price and the base RPC price, base cost of 100M request should be cheaper than downloading 4TB")

	// ErrInvalidSectorAccessDownloadRatio is returned if the user tries to set
	// a value for the download price or the Sector Access Price that violates
	// the maximum ratio
	ErrInvalidSectorAccessDownloadRatio = errors.New("invalid ratio between the download price and the sector access price, base cost of 10M accesses should be cheaper than downloading 4TB")
)

type (
	// ContractInfoGET contains the information that is returned after a GET request
	// to /host/contracts - information for the host about stored obligations.
	ContractInfoGET struct {
		Contracts []modules.StorageObligation `json:"contracts"`
	}

	// HostContractGET contains information about the storage contract returned
	// by a GET request to /host/contracts/:id
	HostContractGET struct {
		Contract modules.StorageObligation `json:"contract"`
	}

	// HostGET contains the information that is returned after a GET request to
	// /host - a bunch of information about the status of the host.
	HostGET struct {
		ConnectabilityStatus modules.HostConnectabilityStatus `json:"connectabilitystatus"`
		ExternalSettings     modules.HostExternalSettings     `json:"externalsettings"`
		FinancialMetrics     modules.HostFinancialMetrics     `json:"financialmetrics"`
		InternalSettings     modules.HostInternalSettings     `json:"internalsettings"`
		NetworkMetrics       modules.HostNetworkMetrics       `json:"networkmetrics"`
		PriceTable           modules.RPCPriceTable            `json:"pricetable"`
		PublicKey            types.SiaPublicKey               `json:"publickey"`
		WorkingStatus        modules.HostWorkingStatus        `json:"workingstatus"`
	}

	// HostEstimateScoreGET contains the information that is returned from a
	// /host/estimatescore call.
	HostEstimateScoreGET struct {
		EstimatedScore types.Currency `json:"estimatedscore"`
		ConversionRate float64        `json:"conversionrate"`
	}

	// StorageGET contains the information that is returned after a GET request
	// to /host/storage - a bunch of information about the status of storage
	// management on the host.
	StorageGET struct {
		Folders []modules.StorageFolderMetadata `json:"folders"`
	}
)

// RegisterRoutesHost is a helper function to register all host routes.
func RegisterRoutesHost(router *httprouter.Router, h modules.Host, deps modules.Dependencies, requiredPassword string) {
	// Calls directly pertaining to the host.
	router.GET("/host", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		hostHandlerGET(h, w, deps, req, ps)
	})
	router.POST("/host", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		hostHandlerPOST(h, w, req, ps)
	}, requiredPassword))
	router.POST("/host/announce", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		hostAnnounceHandler(h, w, req, ps)
	}, requiredPassword))
	router.GET("/host/contracts", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		hostContractInfoHandler(h, w, req, ps)
	})
	router.GET("/host/contracts/:contractID", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		hostContractGetHandler(h, w, req, ps)
	})
	router.GET("/host/bandwidth", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		hostBandwidthHandlerGET(h, w, req, ps)
	})

	// Calls pertaining to the storage manager that the host uses.
	router.GET("/host/storage", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		storageHandler(h, w, req, ps)
	})
	router.POST("/host/storage/folders/add", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		storageFoldersAddHandler(h, w, req, ps)
	}, requiredPassword))
	router.POST("/host/storage/folders/remove", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		storageFoldersRemoveHandler(h, w, req, ps)
	}, requiredPassword))
	router.POST("/host/storage/folders/resize", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		storageFoldersResizeHandler(h, w, req, ps)
	}, requiredPassword))
	router.POST("/host/storage/sectors/delete/:merkleroot", RequirePassword(func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		storageSectorsDeleteHandler(h, w, req, ps)
	}, requiredPassword))
}

// folderIndex determines the index of the storage folder with the provided
// path.
func folderIndex(folderPath string, storageFolders []modules.StorageFolderMetadata) (int, error) {
	for _, sf := range storageFolders {
		if sf.Path == folderPath {
			return int(sf.Index), nil
		}
	}
	return -1, errStorageFolderNotFound
}

// hostContractGetHandler handles the API call to get information about a contract.
func hostContractGetHandler(host modules.Host, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	var obligationID types.FileContractID
	contractIDStr := ps.ByName("contractID")

	buf, err := hex.DecodeString(contractIDStr)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("error parsing storage contract id: %v", err)}, http.StatusBadRequest)
		return
	}

	copy(obligationID[:], buf)

	contract, err := host.StorageObligation(obligationID)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("error get storage contract: %v", err)}, http.StatusNotFound)
		return
	}

	WriteJSON(w, HostContractGET{
		Contract: contract,
	})
}

// hostContractInfoHandler handles the API call to get the contract information of the host.
// Information is retrieved via the storage obligations from the host database.
func hostContractInfoHandler(host modules.Host, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	cg := ContractInfoGET{
		Contracts: host.StorageObligations(),
	}
	WriteJSON(w, cg)
}

// hostHandlerGET handles GET requests to the /host API endpoint, returning key
// information about the host.
func hostHandlerGET(host modules.Host, w http.ResponseWriter, deps modules.Dependencies, _ *http.Request, _ httprouter.Params) {
	es := host.ExternalSettings()
	fm := host.FinancialMetrics()
	is := host.InternalSettings()
	nm := host.NetworkMetrics()
	cs := host.ConnectabilityStatus()
	ws := host.WorkingStatus()
	pk := host.PublicKey()
	pt := host.PriceTable()
	hg := HostGET{
		ConnectabilityStatus: cs,
		ExternalSettings:     es,
		FinancialMetrics:     fm,
		InternalSettings:     is,
		NetworkMetrics:       nm,
		PriceTable:           pt,
		PublicKey:            pk,
		WorkingStatus:        ws,
	}

	if deps.Disrupt("TimeoutOnHostGET") {
		time.Sleep(httpServerTimeout + 5*time.Second)
	}

	WriteJSON(w, hg)
}

// hostsBandwidthHandlerGET handles GET requests to the /host/bandwidth API endpoint,
// returning bandwidth usage data from the host module
func hostBandwidthHandlerGET(host modules.Host, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	sent, receive, startTime, err := host.BandwidthCounters()
	if err != nil {
		WriteError(w, Error{"failed to get hosts's bandwidth usage: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, GatewayBandwidthGET{
		Download:  receive,
		Upload:    sent,
		StartTime: startTime,
	})
}

// parseHostSettings a request's query strings and returns a
// modules.HostInternalSettings configured with the request's query string
// parameters.
func parseHostSettings(host modules.Host, req *http.Request) (modules.HostInternalSettings, error) {
	settings := host.InternalSettings()

	if req.FormValue("acceptingcontracts") != "" {
		var x bool
		_, err := fmt.Sscan(req.FormValue("acceptingcontracts"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.AcceptingContracts = x
	}
	if req.FormValue("maxdownloadbatchsize") != "" {
		var x uint64
		_, err := fmt.Sscan(req.FormValue("maxdownloadbatchsize"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MaxDownloadBatchSize = x
	}
	if req.FormValue("maxduration") != "" {
		var x types.BlockHeight
		_, err := fmt.Sscan(req.FormValue("maxduration"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MaxDuration = x
	}
	if req.FormValue("maxrevisebatchsize") != "" {
		var x uint64
		_, err := fmt.Sscan(req.FormValue("maxrevisebatchsize"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MaxReviseBatchSize = x
	}
	if req.FormValue("netaddress") != "" {
		var x modules.NetAddress
		_, err := fmt.Sscan(req.FormValue("netaddress"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.NetAddress = x
	}
	if req.FormValue("windowsize") != "" {
		var x types.BlockHeight
		_, err := fmt.Sscan(req.FormValue("windowsize"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.WindowSize = x
	}

	if req.FormValue("collateral") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("collateral"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.Collateral = x
	}
	if req.FormValue("collateralbudget") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("collateralbudget"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.CollateralBudget = x
	}
	if req.FormValue("maxcollateral") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("maxcollateral"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MaxCollateral = x
	}

	if req.FormValue("minbaserpcprice") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("minbaserpcprice"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MinBaseRPCPrice = x
	}
	if req.FormValue("mincontractprice") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("mincontractprice"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MinContractPrice = x
	}
	if req.FormValue("mindownloadbandwidthprice") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("mindownloadbandwidthprice"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MinDownloadBandwidthPrice = x
	}
	if req.FormValue("minsectoraccessprice") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("minsectoraccessprice"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MinSectorAccessPrice = x
	}
	if req.FormValue("minstorageprice") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("minstorageprice"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MinStoragePrice = x
	}
	if req.FormValue("minuploadbandwidthprice") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("minuploadbandwidthprice"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MinUploadBandwidthPrice = x
	}
	if req.FormValue("ephemeralaccountexpiry") != "" {
		var x uint64
		_, err := fmt.Sscan(req.FormValue("ephemeralaccountexpiry"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.EphemeralAccountExpiry = time.Duration(x) * time.Second
	}
	if req.FormValue("maxephemeralaccountbalance") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("maxephemeralaccountbalance"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MaxEphemeralAccountBalance = x
	}
	if req.FormValue("maxephemeralaccountrisk") != "" {
		var x types.Currency
		_, err := fmt.Sscan(req.FormValue("maxephemeralaccountrisk"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.MaxEphemeralAccountRisk = x
	}
	if req.FormValue("registrysize") != "" {
		var x uint64
		_, err := fmt.Sscan(req.FormValue("registrysize"), &x)
		if err != nil {
			return modules.HostInternalSettings{}, err
		}
		settings.RegistrySize = x
	}
	if req.FormValue("customregistrypath") != "" {
		settings.CustomRegistryPath = req.FormValue("customregistrypath")
	}

	// Validate the RPC, Sector Access, and Download Prices
	minBaseRPCPrice := settings.MinBaseRPCPrice
	maxBaseRPCPrice := settings.MaxBaseRPCPrice()
	if minBaseRPCPrice.Cmp(maxBaseRPCPrice) > 0 {
		return modules.HostInternalSettings{}, ErrInvalidRPCDownloadRatio
	}
	minSectorAccessPrice := settings.MinSectorAccessPrice
	maxSectorAccessPrice := settings.MaxSectorAccessPrice()
	if minSectorAccessPrice.Cmp(maxSectorAccessPrice) > 0 {
		return modules.HostInternalSettings{}, ErrInvalidSectorAccessDownloadRatio
	}

	return settings, nil
}

// hostEstimateScoreGET handles the POST request to /host/estimatescore and
// computes an estimated HostDB score for the provided settings.
func hostEstimateScoreGET(host modules.Host, renter modules.Renter, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// This call requires a renter, check that it is present.
	if renter == nil {
		WriteError(w, Error{"cannot call /host/estimatescore without the renter module"}, http.StatusBadRequest)
		return
	}

	settings, err := parseHostSettings(host, req)
	if err != nil {
		WriteError(w, Error{"error parsing host settings: " + err.Error()}, http.StatusBadRequest)
		return
	}
	var totalStorage, remainingStorage uint64
	for _, sf := range host.StorageFolders() {
		totalStorage += sf.Capacity
		remainingStorage += sf.CapacityRemaining
	}
	mergedSettings := modules.HostExternalSettings{
		AcceptingContracts:   settings.AcceptingContracts,
		MaxDownloadBatchSize: settings.MaxDownloadBatchSize,
		MaxDuration:          settings.MaxDuration,
		MaxReviseBatchSize:   settings.MaxReviseBatchSize,
		RemainingStorage:     remainingStorage,
		SectorSize:           modules.SectorSize,
		TotalStorage:         totalStorage,
		WindowSize:           settings.WindowSize,

		Collateral:    settings.Collateral,
		MaxCollateral: settings.MaxCollateral,

		ContractPrice:          settings.MinContractPrice,
		DownloadBandwidthPrice: settings.MinDownloadBandwidthPrice,
		StoragePrice:           settings.MinStoragePrice,
		UploadBandwidthPrice:   settings.MinUploadBandwidthPrice,

		EphemeralAccountExpiry:     settings.EphemeralAccountExpiry,
		MaxEphemeralAccountBalance: settings.MaxEphemeralAccountBalance,

		Version: modules.RHPVersion,
	}
	entry := modules.HostDBEntry{}
	entry.PublicKey = host.PublicKey()
	entry.HostExternalSettings = mergedSettings
	// Use the default allowance for now, since we do not know what sort of
	// allowance the renters may use to attempt to access this host.
	estimatedScoreBreakdown, err := renter.EstimateHostScore(entry, modules.DefaultAllowance)
	if err != nil {
		WriteError(w, Error{"error estimating host score: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	e := HostEstimateScoreGET{
		EstimatedScore: estimatedScoreBreakdown.Score,
		ConversionRate: estimatedScoreBreakdown.ConversionRate,
	}
	WriteJSON(w, e)
}

// hostHandlerPOST handles POST request to the /host API endpoint, which sets
// the internal settings of the host.
func hostHandlerPOST(host modules.Host, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	settings, err := parseHostSettings(host, req)
	if err != nil {
		WriteError(w, Error{"error parsing host settings: " + err.Error()}, http.StatusBadRequest)
		return
	}

	err = host.SetInternalSettings(settings)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// hostAnnounceHandler handles the API call to get the host to announce itself
// to the network.
func hostAnnounceHandler(host modules.Host, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var err error
	if addr := req.FormValue("netaddress"); addr != "" {
		err = host.AnnounceAddress(modules.NetAddress(addr))
	} else {
		err = host.Announce()
	}
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// storageHandler returns a bunch of information about storage management on
// the host.
func storageHandler(host modules.Host, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	WriteJSON(w, StorageGET{
		Folders: host.StorageFolders(),
	})
}

// storageFoldersAddHandler adds a storage folder to the storage manager.
func storageFoldersAddHandler(host modules.Host, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	folderPath := req.FormValue("path")
	var folderSize uint64
	_, err := fmt.Sscan(req.FormValue("size"), &folderSize)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	err = host.AddStorageFolder(folderPath, folderSize)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// storageFoldersResizeHandler resizes a storage folder in the storage manager.
func storageFoldersResizeHandler(host modules.Host, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	folderPath := req.FormValue("path")
	if folderPath == "" {
		WriteError(w, Error{"path parameter is required"}, http.StatusBadRequest)
		return
	}

	storageFolders := host.StorageFolders()
	folderIndex, err := folderIndex(folderPath, storageFolders)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	var newSize uint64
	_, err = fmt.Sscan(req.FormValue("newsize"), &newSize)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	err = host.ResizeStorageFolder(uint16(folderIndex), newSize, false)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// storageFoldersRemoveHandler removes a storage folder from the storage
// manager.
func storageFoldersRemoveHandler(host modules.Host, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	folderPath := req.FormValue("path")
	if folderPath == "" {
		WriteError(w, Error{"path parameter is required"}, http.StatusBadRequest)
		return
	}

	storageFolders := host.StorageFolders()
	folderIndex, err := folderIndex(folderPath, storageFolders)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	force := req.FormValue("force") == "true"
	err = host.RemoveStorageFolder(uint16(folderIndex), force)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// storageSectorsDeleteHandler handles the call to delete a sector from the
// storage manager.
func storageSectorsDeleteHandler(host modules.Host, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	sectorRoot, err := scanHash(ps.ByName("merkleroot"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	err = host.DeleteSector(sectorRoot)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
