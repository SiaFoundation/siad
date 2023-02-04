package api

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

var (
	// requiredHosts specifies the minimum number of hosts that must be set in
	// the renter settings for the renter settings to be valid. This minimum is
	// there to prevent users from shooting themselves in the foot.
	requiredHosts = build.Select(build.Var{
		Standard: uint64(20),
		Testnet:  uint64(20),
		Dev:      uint64(1),
		Testing:  uint64(1),
	}).(uint64)

	// requiredParityPieces specifies the minimum number of parity pieces that
	// must be used when uploading a file. This minimum exists to prevent users
	// from shooting themselves in the foot.
	requiredParityPieces = build.Select(build.Var{
		Standard: int(9),
		Testnet:  int(9),
		Dev:      int(0),
		Testing:  int(0),
	}).(int)

	// requiredRedundancy specifies the minimum redundancy that will be
	// accepted by the renter when uploading a file. This minimum exists to
	// prevent users from shooting themselves in the foot.
	requiredRedundancy = build.Select(build.Var{
		Standard: float64(2),
		Testnet:  float64(2),
		Dev:      float64(1),
		Testing:  float64(1),
	}).(float64)

	// requiredRenewWindow establishes the minimum allowed renew window for the
	// renter settings. This minimum is here to prevent users from shooting
	// themselves in the foot.
	requiredRenewWindow = build.Select(build.Var{
		Standard: types.BlockHeight(288),
		Testnet:  types.BlockHeight(288),
		Dev:      types.BlockHeight(1),
		Testing:  types.BlockHeight(1),
	}).(types.BlockHeight)

	// errNeedBothDataAndParityPieces is the error returned when only one of the
	// erasure coding parameters is set
	errNeedBothDataAndParityPieces = errors.New("must provide both the datapieces parameter and the paritypieces parameter if specifying erasure coding parameters")

	// ErrFundsNeedToBeSet is the error returned when the funds are not set for
	// the allowance
	ErrFundsNeedToBeSet = errors.New("funds needs to be set if it hasn't been set before")

	// ErrPeriodNeedToBeSet is the error returned when the period is not set for
	// the allowance
	ErrPeriodNeedToBeSet = errors.New("period needs to be set if it hasn't been set before")
)

type (
	// RenterGET contains various renter metrics.
	RenterGET struct {
		Settings         modules.RenterSettings     `json:"settings"`
		FinancialMetrics modules.ContractorSpending `json:"financialmetrics"`
		CurrentPeriod    types.BlockHeight          `json:"currentperiod"`
		NextPeriod       types.BlockHeight          `json:"nextperiod"`

		MemoryStatus modules.MemoryStatus `json:"memorystatus"`
	}

	// RenterContract represents a contract formed by the renter.
	RenterContract struct {
		// Amount of contract funds that have been spent on downloads.
		DownloadSpending types.Currency `json:"downloadspending"`
		// Block height that the file contract ends on.
		EndHeight types.BlockHeight `json:"endheight"`
		// Fees paid in order to form the file contract.
		Fees types.Currency `json:"fees"`
		// Amount of contract funds that have been spent on funding an ephemeral
		// account on the host.
		FundAccountSpending types.Currency `json:"fundaccountspending"`
		// Public key of the host the contract was formed with.
		HostPublicKey types.SiaPublicKey `json:"hostpublickey"`
		// HostVersion is the version of Sia that the host is running
		HostVersion string `json:"hostversion"`
		// ID of the file contract.
		ID types.FileContractID `json:"id"`
		// A signed transaction containing the most recent contract revision.
		LastTransaction types.Transaction `json:"lasttransaction"`
		// Amount of contract funds that have been spent on maintenance tasks
		// such as updating the price table or syncing the ephemeral account
		// balance.
		MaintenanceSpending modules.MaintenanceSpending `json:"maintenancespending"`
		// Address of the host the file contract was formed with.
		NetAddress modules.NetAddress `json:"netaddress"`
		// Remaining funds left for the renter to spend on uploads & downloads.
		RenterFunds types.Currency `json:"renterfunds"`
		// Size of the file contract, which is typically equal to the number of
		// bytes that have been uploaded to the host.
		Size uint64 `json:"size"`
		// Block height that the file contract began on.
		StartHeight types.BlockHeight `json:"startheight"`
		// Amount of contract funds that have been spent on storage.
		StorageSpending types.Currency `json:"storagespending"`
		// DEPRECATED: This is the exact same value as StorageSpending, but it has
		// incorrect capitalization. This was fixed in 1.3.2, but this field is kept
		// to preserve backwards compatibility on clients who depend on the
		// incorrect capitalization. This field will be removed in the future, so
		// clients should switch to the StorageSpending field (above) with the
		// correct lowercase name.
		StorageSpendingDeprecated types.Currency `json:"StorageSpending,siamismatch"`
		// Total cost to the wallet of forming the file contract.
		TotalCost types.Currency `json:"totalcost"`
		// Amount of contract funds that have been spent on uploads.
		UploadSpending types.Currency `json:"uploadspending"`
		// Signals if contract is good for uploading data
		GoodForUpload bool `json:"goodforupload"`
		// Signals if contract is good for a renewal
		GoodForRenew bool `json:"goodforrenew"`
		// Signals if a contract has been marked as bad
		BadContract bool `json:"badcontract"`
	}

	// RenterContracts contains the renter's contracts.
	RenterContracts struct {
		// Compatibility Fields
		Contracts         []RenterContract `json:"contracts"`
		InactiveContracts []RenterContract `json:"inactivecontracts"`

		// Current Fields
		ActiveContracts           []RenterContract              `json:"activecontracts"`
		PassiveContracts          []RenterContract              `json:"passivecontracts"`
		RefreshedContracts        []RenterContract              `json:"refreshedcontracts"`
		DisabledContracts         []RenterContract              `json:"disabledcontracts"`
		ExpiredContracts          []RenterContract              `json:"expiredcontracts"`
		ExpiredRefreshedContracts []RenterContract              `json:"expiredrefreshedcontracts"`
		RecoverableContracts      []modules.RecoverableContract `json:"recoverablecontracts"`
	}

	// RenterDirectory lists the files and directories contained in the queried
	// directory
	RenterDirectory struct {
		Directories []modules.DirectoryInfo `json:"directories"`
		Files       []modules.FileInfo      `json:"files"`
	}

	// RenterDownloadQueue contains the renter's download queue.
	RenterDownloadQueue struct {
		Downloads []DownloadInfo `json:"downloads"`
	}

	// RenterFile lists the file queried.
	RenterFile struct {
		File modules.FileInfo `json:"file"`
	}

	// RenterFiles lists the files known to the renter.
	RenterFiles struct {
		Files []modules.FileInfo `json:"files"`
	}

	// RenterFuseInfo contains information about mounted fuse filesystems.
	RenterFuseInfo struct {
		MountPoints []modules.MountInfo `json:"mountpoints"`
	}

	// RenterLoad lists files that were loaded into the renter.
	RenterLoad struct {
		FilesAdded []string `json:"filesadded"`
	}

	// RenterPricesGET lists the data that is returned when a GET call is made
	// to /renter/prices.
	RenterPricesGET struct {
		modules.RenterPriceEstimation
		modules.Allowance
	}
	// RenterRecoveryStatusGET returns information about potential contract
	// recovery scans.
	RenterRecoveryStatusGET struct {
		ScanInProgress bool              `json:"scaninprogress"`
		ScannedHeight  types.BlockHeight `json:"scannedheight"`
	}
	// RenterShareASCII contains an ASCII-encoded .sia file.
	RenterShareASCII struct {
		ASCIIsia string `json:"asciisia"`
	}

	// RenterUploadedBackup describes an uploaded backup.
	RenterUploadedBackup struct {
		Name           string          `json:"name"`
		CreationDate   types.Timestamp `json:"creationdate"`
		Size           uint64          `json:"size"`
		UploadProgress float64         `json:"uploadprogress"`
	}

	// RenterBackupsGET lists the renter's uploaded backups, as well as the
	// set of contracts storing all known backups.
	RenterBackupsGET struct {
		Backups       []RenterUploadedBackup `json:"backups"`
		SyncedHosts   []types.SiaPublicKey   `json:"syncedhosts"`
		UnsyncedHosts []types.SiaPublicKey   `json:"unsyncedhosts"`
	}

	// RenterUploadReadyGet lists the upload ready status of the renter
	RenterUploadReadyGet struct {
		// Ready indicates whether of not the renter is ready to successfully
		// upload to full redundancy based on the erasure coding provided and
		// the number of contracts
		Ready bool `json:"ready"`

		// Contract information
		ContractsNeeded    int `json:"contractsneeded"`
		NumActiveContracts int `json:"numactivecontracts"`

		// Erasure Coding information
		DataPieces   int `json:"datapieces"`
		ParityPieces int `json:"paritypieces"`
	}

	// DownloadInfo contains all client-facing information of a file.
	DownloadInfo struct {
		Destination     string          `json:"destination"`     // The destination of the download.
		DestinationType string          `json:"destinationtype"` // Can be "file", "memory buffer", or "http stream".
		Filesize        uint64          `json:"filesize"`        // DEPRECATED. Same as 'Length'.
		Length          uint64          `json:"length"`          // The length requested for the download.
		Offset          uint64          `json:"offset"`          // The offset within the siafile requested for the download.
		SiaPath         modules.SiaPath `json:"siapath"`         // The siapath of the file used for the download.

		Completed            bool      `json:"completed"`            // Whether or not the download has completed.
		EndTime              time.Time `json:"endtime"`              // The time when the download fully completed.
		Error                string    `json:"error"`                // Will be the empty string unless there was an error.
		Received             uint64    `json:"received"`             // Amount of data confirmed and decoded.
		StartTime            time.Time `json:"starttime"`            // The time when the download was started.
		StartTimeUnix        int64     `json:"starttimeunix"`        // The time when the download was started in unix format.
		TotalDataTransferred uint64    `json:"totaldatatransferred"` // The total amount of data transferred, including negotiation, overdrive etc.
	}
)

// Returns the boolean value of the 'root' parameter of req or an error if
// it exists but is not parsable as bool.
func isCalledWithRootFlag(req *http.Request) (bool, error) {
	rootStr := req.FormValue("root")
	if rootStr == "" {
		return false, nil
	}
	root, err := strconv.ParseBool(rootStr)
	if err != nil {
		return false, errors.New("unable to parse 'root' arg: " + err.Error())
	}
	return root, nil
}

// rebaseInputSiaPath rebases the SiaPath provided by the user to one that is
// prefixed by the user's home directory.
func rebaseInputSiaPath(siaPath modules.SiaPath) (modules.SiaPath, error) {
	// Prepend the provided siapath with the /home/siafiles dir.
	if siaPath.IsRoot() {
		return modules.UserFolder, nil
	}
	return modules.UserFolder.Join(siaPath.String())
}

// trimSiaDirFolder is a helper method to trim /home/siafiles off of the
// siapaths of the dirinfos since the user expects a path relative to
// /home/siafiles and not relative to root.
func trimSiaDirFolder(dis ...modules.DirectoryInfo) (_ []modules.DirectoryInfo, err error) {
	for i := range dis {
		dis[i].SiaPath, err = dis[i].SiaPath.Rebase(modules.UserFolder, modules.RootSiaPath())
		if err != nil {
			return nil, err
		}
	}
	return dis, nil
}

// trimSiaDirFolderOnFiles is a helper method to trim /home/siafiles off of the
// siapaths of the fileinfos since the user expects a path relative to
// /home/siafiles and not relative to root.
func trimSiaDirFolderOnFiles(fis ...modules.FileInfo) (_ []modules.FileInfo, err error) {
	for i := range fis {
		fis[i].SiaPath, err = fis[i].SiaPath.Rebase(modules.UserFolder, modules.RootSiaPath())
		if err != nil {
			return nil, errors.AddContext(err, "unable to trim the user sia path from a provided fileinfo")
		}
	}
	return fis, nil
}

// trimSiaDirInfo is a helper method to trim /home/siafiles off of the
// siapaths of the fileinfos since the user expects a path relative to
// /home/siafiles and not relative to root.
func trimDownloadInfo(dis ...modules.DownloadInfo) (_ []modules.DownloadInfo, err error) {
	for i := range dis {
		dis[i].SiaPath, err = dis[i].SiaPath.Rebase(modules.UserFolder, modules.RootSiaPath())
		if err != nil {
			return nil, err
		}
	}
	return dis, nil
}

// renterBubbleHandlerPOST handles the API calls to /renter/bubble.
func (api *API) renterBubbleHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse the 'rootsiapath' parameter
	rootSiaPath := false
	var err error
	if r := req.FormValue("rootsiapath"); r != "" {
		rootSiaPath, err = scanBool(r)
		if err != nil {
			WriteError(w, Error{"unable to parse 'rootsiapath' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse the siaPath
	var siaPath modules.SiaPath
	s := req.FormValue("siapath")
	if rootSiaPath && s != "" {
		WriteError(w, Error{"rootsiapath and non empty siapath cannot both be used"}, http.StatusBadRequest)
		return
	}
	if !rootSiaPath && s == "" {
		WriteError(w, Error{"rootsiapath should be true if no siapath is provided"}, http.StatusBadRequest)
		return
	}
	if rootSiaPath {
		siaPath = modules.RootSiaPath()
	} else {
		err = siaPath.LoadString(s)
		if err != nil {
			WriteError(w, Error{"unable to parse siapath: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse the 'force' parameter
	force := false
	if f := req.FormValue("force"); f != "" {
		force, err = scanBool(f)
		if err != nil {
			WriteError(w, Error{"unable to parse 'force' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	// Parse the 'recursive' parameter
	recursive := false
	if r := req.FormValue("recursive"); r != "" {
		recursive, err = strconv.ParseBool(r)
		if err != nil {
			WriteError(w, Error{"unable to parse 'recursive' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Call bubble
	err = api.renter.BubbleMetadata(siaPath, force, recursive)
	if err != nil {
		WriteError(w, Error{"unable to bubble directory: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteSuccess(w)
	return
}

// renterBackupsHandlerGET handles the API calls to /renter/backups.
func (api *API) renterBackupsHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	backups, syncedHosts, err := api.renter.UploadedBackups()
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	var unsyncedHosts []types.SiaPublicKey
outer:
	for _, c := range api.renter.Contracts() {
		for _, h := range syncedHosts {
			if c.HostPublicKey.Equals(h) {
				continue outer
			}
		}
		unsyncedHosts = append(unsyncedHosts, c.HostPublicKey)
	}

	// if requested, fetch the backups stored on a specific host
	if req.FormValue("host") != "" {
		var hostKey types.SiaPublicKey
		hostKey.LoadString(req.FormValue("host"))
		if hostKey.Key == nil {
			WriteError(w, Error{"invalid host public key"}, http.StatusBadRequest)
			return
		}
		backups, err = api.renter.BackupsOnHost(hostKey)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	rups := make([]RenterUploadedBackup, len(backups))
	for i, b := range backups {
		rups[i] = RenterUploadedBackup{
			Name:           b.Name,
			CreationDate:   b.CreationDate,
			Size:           b.Size,
			UploadProgress: b.UploadProgress,
		}
	}
	WriteJSON(w, RenterBackupsGET{
		Backups:       rups,
		SyncedHosts:   syncedHosts,
		UnsyncedHosts: unsyncedHosts,
	})
}

// renterBackupsCreateHandlerPOST handles the API calls to /renter/backups/create
func (api *API) renterBackupsCreateHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Check that a name was specified.
	name := req.FormValue("name")
	if name == "" {
		WriteError(w, Error{"name not specified"}, http.StatusBadRequest)
		return
	}

	// Write the backup to a temporary file and delete it after uploading.
	tmpDir, err := ioutil.TempDir("", "sia-backup")
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	randomSuffix := persist.RandomSuffix()
	backupPath := filepath.Join(tmpDir, fmt.Sprintf("%v-%v.bak", name, randomSuffix))
	defer func() {
		// At this point we have already responded so we can't write a potential
		// error here.
		_ = os.RemoveAll(backupPath)
	}()

	// Get the wallet seed.
	ws, _, err := api.wallet.PrimarySeed()
	if err != nil {
		WriteError(w, Error{"failed to get wallet's primary seed"}, http.StatusInternalServerError)
		return
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, modules.BackupKeySpecifier)
	defer fastrand.Read(secret[:])
	// Create the backup.
	if err := api.renter.CreateBackup(backupPath, secret[:32]); err != nil {
		WriteError(w, Error{"failed to create backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	// Upload the backup.
	if err := api.renter.UploadBackup(backupPath, name); err != nil {
		WriteError(w, Error{"failed to upload backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterBackupsRestoreHandlerGET handles the API calls to /renter/backups/restore
func (api *API) renterBackupsRestoreHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Check that a name was specified.
	name := req.FormValue("name")
	if name == "" {
		WriteError(w, Error{"name not specified"}, http.StatusBadRequest)
		return
	}
	// Write the backup to a temporary file and delete it after loading.
	tmpDir, err := ioutil.TempDir("", "sia-backup")
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()
	backupPath := filepath.Join(tmpDir, name)
	if err := api.renter.DownloadBackup(backupPath, name); err != nil {
		WriteError(w, Error{"failed to download backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	// Get the wallet seed.
	ws, _, err := api.wallet.PrimarySeed()
	if err != nil {
		WriteError(w, Error{"failed to get wallet's primary seed"}, http.StatusInternalServerError)
		return
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, modules.BackupKeySpecifier)
	defer fastrand.Read(secret[:])
	// Load the backup.
	if err := api.renter.LoadBackup(backupPath, secret[:32]); err != nil {
		WriteError(w, Error{"failed to load backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterBackupHandlerPOST handles the API calls to /renter/backup
func (api *API) renterBackupHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Check that destination was specified.
	dst := req.FormValue("destination")
	if dst == "" {
		WriteError(w, Error{"destination not specified"}, http.StatusBadRequest)
		return
	}
	// The destination needs to be an absolute path.
	if !filepath.IsAbs(dst) {
		WriteError(w, Error{"destination must be an absolute path"}, http.StatusBadRequest)
		return
	}
	// Get the wallet seed.
	ws, _, err := api.wallet.PrimarySeed()
	if err != nil {
		WriteError(w, Error{"failed to get wallet's primary seed"}, http.StatusInternalServerError)
		return
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, modules.BackupKeySpecifier)
	defer fastrand.Read(secret[:])
	// Create the backup.
	if err := api.renter.CreateBackup(dst, secret[:32]); err != nil {
		WriteError(w, Error{"failed to create backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterBackupHandlerPOST handles the API calls to /renter/recoverbackup
func (api *API) renterLoadBackupHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Check that source was specified.
	src := req.FormValue("source")
	if src == "" {
		WriteError(w, Error{"source not specified"}, http.StatusBadRequest)
		return
	}
	// The source needs to be an absolute path.
	if !filepath.IsAbs(src) {
		WriteError(w, Error{"source must be an absolute path"}, http.StatusBadRequest)
		return
	}
	// Get the wallet seed.
	ws, _, err := api.wallet.PrimarySeed()
	if err != nil {
		WriteError(w, Error{"failed to get wallet's primary seed"}, http.StatusInternalServerError)
		return
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, modules.BackupKeySpecifier)
	defer fastrand.Read(secret[:])
	// Load the backup.
	if err := api.renter.LoadBackup(src, secret[:32]); err != nil {
		WriteError(w, Error{"failed to load backup: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// parseErasureCodingParameters parses the supplied string values and creates
// an erasure coder. If values haven't been supplied it will fill in sane
// defaults.
func parseErasureCodingParameters(strDataPieces, strParityPieces string) (modules.ErasureCoder, error) {
	// Parse data and parity pieces
	dataPieces, parityPieces, err := ParseDataAndParityPieces(strDataPieces, strParityPieces)
	if err != nil {
		return nil, err
	}

	// Check if data and parity pieces were set
	if dataPieces == 0 && parityPieces == 0 {
		return nil, nil
	}

	// Verify that sane values for parityPieces and redundancy are being
	// supplied.
	if parityPieces < requiredParityPieces {
		err := fmt.Errorf("a minimum of %v parity pieces is required, but %v parity pieces requested", requiredParityPieces, parityPieces)
		return nil, err
	}
	redundancy := float64(dataPieces+parityPieces) / float64(dataPieces)
	if float64(dataPieces+parityPieces)/float64(dataPieces) < requiredRedundancy {
		err := fmt.Errorf("a redundancy of %.2f is required, but redundancy of %.2f supplied", requiredRedundancy, redundancy)
		return nil, err
	}

	// Create the erasure coder.
	return modules.NewRSSubCode(dataPieces, parityPieces, crypto.SegmentSize)
}

// ParseDataAndParityPieces parse the numeric values for dataPieces and
// parityPieces from the input strings
func ParseDataAndParityPieces(strDataPieces, strParityPieces string) (dataPieces, parityPieces int, err error) {
	// Check that both values have been supplied.
	if (strDataPieces == "") != (strParityPieces == "") {
		return 0, 0, errNeedBothDataAndParityPieces
	}

	// Check for blank strings.
	if strDataPieces == "" && strParityPieces == "" {
		return 0, 0, nil
	}

	// Parse dataPieces and Parity Pieces.
	_, err = fmt.Sscan(strDataPieces, &dataPieces)
	if err != nil {
		err = errors.AddContext(err, "unable to read parameter 'datapieces'")
		return 0, 0, err
	}
	_, err = fmt.Sscan(strParityPieces, &parityPieces)
	if err != nil {
		err = errors.AddContext(err, "unable to read parameter 'paritypieces'")
		return 0, 0, err
	}

	// Check that either both values are zero or neither are zero
	if (dataPieces == 0) != (parityPieces == 0) {
		return 0, 0, errNeedBothDataAndParityPieces
	}

	return dataPieces, parityPieces, nil
}

// renterHandlerGET handles the API call to /renter.
func (api *API) renterHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	settings, err := api.renter.Settings()
	if err != nil {
		WriteError(w, Error{"unable able to get renter settings: " + err.Error()}, http.StatusBadRequest)
		return
	}
	spending, err := api.renter.PeriodSpending()
	if err != nil {
		WriteError(w, Error{"unable to get Period Spending: " + err.Error()}, http.StatusBadRequest)
		return
	}
	currentPeriod := api.renter.CurrentPeriod()
	nextPeriod := currentPeriod + settings.Allowance.Period
	memoryStatus, err := api.renter.MemoryStatus()
	if err != nil {
		WriteError(w, Error{"unable to get renter memory information: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, RenterGET{
		Settings:         settings,
		FinancialMetrics: spending,
		CurrentPeriod:    currentPeriod,
		NextPeriod:       nextPeriod,

		MemoryStatus: memoryStatus,
	})
}

// renterHandlerPOST handles the API call to set the Renter's settings. This API
// call handles multiple settings and so each setting is optional on it's own.
// Groups of settings, such as the allowance, have certain requirements if they
// are being set in which case certain fields are no longer optional.
func (api *API) renterHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Get the existing settings
	settings, err := api.renter.Settings()
	if err != nil {
		WriteError(w, Error{"unable able to get renter settings: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Scan for all allowance fields
	var hostsSet, renewWindowSet, expectedStorageSet,
		expectedUploadSet, expectedDownloadSet, expectedRedundancySet, maxPeriodChurnSet bool
	if f := req.FormValue("funds"); f != "" {
		funds, ok := scanAmount(f)
		if !ok {
			WriteError(w, Error{"unable to parse funds"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.Funds = funds
	}
	if h := req.FormValue("hosts"); h != "" {
		var hosts uint64
		if _, err := fmt.Sscan(h, &hosts); err != nil {
			WriteError(w, Error{"unable to parse hosts: " + err.Error()}, http.StatusBadRequest)
			return
		} else if hosts != 0 && hosts < requiredHosts {
			WriteError(w, Error{fmt.Sprintf("insufficient number of hosts, need at least %v but have %v", requiredHosts, hosts)}, http.StatusBadRequest)
			return
		}
		settings.Allowance.Hosts = hosts
		hostsSet = true
	}
	if p := req.FormValue("period"); p != "" {
		var period types.BlockHeight
		if _, err := fmt.Sscan(p, &period); err != nil {
			WriteError(w, Error{"unable to parse period: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.Allowance.Period = types.BlockHeight(period)
	}
	if rw := req.FormValue("renewwindow"); rw != "" {
		var renewWindow types.BlockHeight
		if _, err := fmt.Sscan(rw, &renewWindow); err != nil {
			WriteError(w, Error{"unable to parse renewwindow: " + err.Error()}, http.StatusBadRequest)
			return
		} else if renewWindow != 0 && types.BlockHeight(renewWindow) < requiredRenewWindow {
			WriteError(w, Error{fmt.Sprintf("renew window is too small, must be at least %v blocks but have %v blocks", requiredRenewWindow, renewWindow)}, http.StatusBadRequest)
			return
		}
		settings.Allowance.RenewWindow = types.BlockHeight(renewWindow)
		renewWindowSet = true
	}
	if es := req.FormValue("expectedstorage"); es != "" {
		var expectedStorage uint64
		if _, err := fmt.Sscan(es, &expectedStorage); err != nil {
			WriteError(w, Error{"unable to parse expectedStorage: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.Allowance.ExpectedStorage = expectedStorage
		expectedStorageSet = true
	}
	if euf := req.FormValue("expectedupload"); euf != "" {
		var expectedUpload uint64
		if _, err := fmt.Sscan(euf, &expectedUpload); err != nil {
			WriteError(w, Error{"unable to parse expectedUpload: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.Allowance.ExpectedUpload = expectedUpload
		expectedUploadSet = true
	}
	if edf := req.FormValue("expecteddownload"); edf != "" {
		var expectedDownload uint64
		if _, err := fmt.Sscan(edf, &expectedDownload); err != nil {
			WriteError(w, Error{"unable to parse expectedDownload: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.Allowance.ExpectedDownload = expectedDownload
		expectedDownloadSet = true
	}
	if er := req.FormValue("expectedredundancy"); er != "" {
		var expectedRedundancy float64
		if _, err := fmt.Sscan(er, &expectedRedundancy); err != nil {
			WriteError(w, Error{"unable to parse expectedRedundancy: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.Allowance.ExpectedRedundancy = expectedRedundancy
		expectedRedundancySet = true
	}
	if mpc := req.FormValue("maxperiodchurn"); mpc != "" {
		var maxPeriodChurn uint64
		if _, err := fmt.Sscan(mpc, &maxPeriodChurn); err != nil {
			WriteError(w, Error{"unable to parse new max churn per period: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxPeriodChurn = maxPeriodChurn
		maxPeriodChurnSet = true
	}
	if str := req.FormValue("maxrpcprice"); str != "" {
		price, ok := scanAmount(str)
		if !ok {
			WriteError(w, Error{"unable to parse maxrpcprice"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxRPCPrice = price
	}
	if str := req.FormValue("maxcontractprice"); str != "" {
		price, ok := scanAmount(str)
		if !ok {
			WriteError(w, Error{"unable to parse maxcontractprice"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxContractPrice = price
	}
	if str := req.FormValue("maxdownloadbandwidthprice"); str != "" {
		price, ok := scanAmount(str)
		if !ok {
			WriteError(w, Error{"unable to parse maxdownloadbandwidthprice"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxDownloadBandwidthPrice = price
	}
	if str := req.FormValue("maxsectoraccessprice"); str != "" {
		price, ok := scanAmount(str)
		if !ok {
			WriteError(w, Error{"unable to parse maxsectoraccessprice"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxSectorAccessPrice = price
	}
	if str := req.FormValue("maxstorageprice"); str != "" {
		price, ok := scanAmount(str)
		if !ok {
			WriteError(w, Error{"unable to parse maxstorageprice"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxStoragePrice = price
	}
	if str := req.FormValue("maxuploadbandwidthprice"); str != "" {
		price, ok := scanAmount(str)
		if !ok {
			WriteError(w, Error{"unable to parse maxuploadbandwidthprice"}, http.StatusBadRequest)
			return
		}
		settings.Allowance.MaxUploadBandwidthPrice = price
	}

	// Validate any allowance changes. Funds and Period are the only required
	// fields.
	zeroFunds := settings.Allowance.Funds.Cmp(types.ZeroCurrency) == 0
	zeroPeriod := settings.Allowance.Period == 0
	if zeroFunds && zeroPeriod {
		// If both the funds and period are zero then the allowance should be
		// cancelled. Make sure that the rest of the fields are zeroed out
		settings.Allowance = modules.Allowance{}
	} else if !reflect.DeepEqual(settings.Allowance, modules.Allowance{}) {
		// Allowance has been set at least partially. Validate that all fields
		// are set correctly

		// If Funds is still 0 return an error since we need the user to set the
		// period initially
		if zeroFunds {
			WriteError(w, Error{ErrFundsNeedToBeSet.Error()}, http.StatusBadRequest)
			return
		}

		// If Period is still 0 return an error since we need the user to set
		// the period initially
		if zeroPeriod {
			WriteError(w, Error{ErrPeriodNeedToBeSet.Error()}, http.StatusBadRequest)
			return
		}

		// If the user set Hosts to 0 return an error, otherwise if Hosts was
		// not set by the user then set it to the sane default
		if settings.Allowance.Hosts == 0 && hostsSet {
			WriteError(w, Error{contractor.ErrAllowanceNoHosts.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.Hosts == 0 {
			settings.Allowance.Hosts = modules.DefaultAllowance.Hosts
		}

		// If the user set the Renew Window to 0 return an error, otherwise if
		// the Renew Window was not set by the user then set it to the sane
		// default
		if settings.Allowance.RenewWindow == 0 && renewWindowSet {
			WriteError(w, Error{contractor.ErrAllowanceZeroWindow.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.RenewWindow == 0 {
			settings.Allowance.RenewWindow = settings.Allowance.Period / 2
		}

		// If the user set ExpectedStorage to 0 return an error, otherwise if
		// ExpectedStorage was not set by the user then set it to the sane
		// default
		if settings.Allowance.ExpectedStorage == 0 && expectedStorageSet {
			WriteError(w, Error{contractor.ErrAllowanceZeroExpectedStorage.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.ExpectedStorage == 0 {
			settings.Allowance.ExpectedStorage = modules.DefaultAllowance.ExpectedStorage
		}

		// If the user set ExpectedUpload to 0 return an error, otherwise if
		// ExpectedUpload was not set by the user then set it to the sane
		// default
		if settings.Allowance.ExpectedUpload == 0 && expectedUploadSet {
			WriteError(w, Error{contractor.ErrAllowanceZeroExpectedUpload.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.ExpectedUpload == 0 {
			settings.Allowance.ExpectedUpload = modules.DefaultAllowance.ExpectedUpload
		}

		// If the user set ExpectedDownload to 0 return an error, otherwise if
		// ExpectedDownload was not set by the user then set it to the sane
		// default
		if settings.Allowance.ExpectedDownload == 0 && expectedDownloadSet {
			WriteError(w, Error{contractor.ErrAllowanceZeroExpectedDownload.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.ExpectedDownload == 0 {
			settings.Allowance.ExpectedDownload = modules.DefaultAllowance.ExpectedDownload
		}

		// If the user set ExpectedRedundancy to 0 return an error, otherwise if
		// ExpectedRedundancy was not set by the user then set it to the sane
		// default
		if settings.Allowance.ExpectedRedundancy == 0 && expectedRedundancySet {
			WriteError(w, Error{contractor.ErrAllowanceZeroExpectedRedundancy.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.ExpectedRedundancy == 0 {
			settings.Allowance.ExpectedRedundancy = modules.DefaultAllowance.ExpectedRedundancy
		}

		// If the user set MaxPeriodChurn to 0 return an error, otherwise if
		// MaxPeriodChurn was not set by the user then set it to the sane
		// default
		if settings.Allowance.MaxPeriodChurn == 0 && maxPeriodChurnSet {
			WriteError(w, Error{contractor.ErrAllowanceZeroMaxPeriodChurn.Error()}, http.StatusBadRequest)
			return
		} else if settings.Allowance.MaxPeriodChurn == 0 {
			settings.Allowance.MaxPeriodChurn = modules.DefaultAllowance.MaxPeriodChurn
		}
	}

	// Scan the download speed limit. (optional parameter)
	if d := req.FormValue("maxdownloadspeed"); d != "" {
		var downloadSpeed int64
		if _, err := fmt.Sscan(d, &downloadSpeed); err != nil {
			WriteError(w, Error{"unable to parse downloadspeed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.MaxDownloadSpeed = downloadSpeed
	}
	// Scan the upload speed limit. (optional parameter)
	if u := req.FormValue("maxuploadspeed"); u != "" {
		var uploadSpeed int64
		if _, err := fmt.Sscan(u, &uploadSpeed); err != nil {
			WriteError(w, Error{"unable to parse uploadspeed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.MaxUploadSpeed = uploadSpeed
	}

	// Scan the checkforipviolation flag.
	if ipc := req.FormValue("checkforipviolation"); ipc != "" {
		var ipviolationcheck bool
		if _, err := fmt.Sscan(ipc, &ipviolationcheck); err != nil {
			WriteError(w, Error{"unable to parse ipviolationcheck: " + err.Error()}, http.StatusBadRequest)
			return
		}
		settings.IPViolationCheck = ipviolationcheck
	}

	// Set the settings in the renter.
	err = api.renter.SetSettings(settings)
	if err != nil {
		WriteError(w, Error{"unable to set renter settings: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterAllowanceCancelHandlerPOST handles the API call to cancel the Renter's
// allowance
func (api *API) renterAllowanceCancelHandlerPOST(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get the existing settings
	settings, err := api.renter.Settings()
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Set the allownace to nil
	settings.Allowance = modules.Allowance{}

	// Set the settings in the renter.
	err = api.renter.SetSettings(settings)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterCleanHandlerPOST handles the API call to clean lost files from a Renter.
func (api *API) renterCleanHandlerPOST(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	var deleteErrs error
	cleanFunc := func(fi modules.FileInfo) {
		if fi.OnDisk || fi.Redundancy >= 1 {
			return
		}
		deleteErrs = errors.Compose(deleteErrs, api.renter.DeleteFile(fi.SiaPath))
	}
	err := api.renter.FileList(modules.RootSiaPath(), true, false, cleanFunc)
	err = errors.Compose(err, deleteErrs)
	if err != nil {
		WriteError(w, Error{"unable to clear lost files: " + err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// renterContractCancelHandler handles the API call to cancel a specific Renter contract.
func (api *API) renterContractCancelHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var fcid types.FileContractID
	if err := fcid.LoadString(req.FormValue("id")); err != nil {
		WriteError(w, Error{"unable to parse id: " + err.Error()}, http.StatusBadRequest)
		return
	}
	err := api.renter.CancelContract(fcid)
	if err != nil {
		WriteError(w, Error{"unable to cancel contract: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterContractsHandler handles the API call to request the Renter's
// contracts. Active and renewed contracts are returned by default
//
// Contracts are returned for Compatibility and are the contracts returned from
// renter.Contracts()
//
// Inactive contracts are contracts that are not currently being used by the
// renter because they are !goodForRenew, but have endheights that are in the
// future so could potentially become active again
//
// Active contracts are contracts that the renter is actively using to store
// data and can upload, download, and renew. These contracts are GoodForUpload
// and GoodForRenew
//
// Refreshed contracts are contracts that are in the current period and were
// refreshed due to running out of funds. A new contract that replaced a
// refreshed contract can either be in Active or Disabled contracts. These
// contracts are broken out as to not double count the data recorded in the
// contract.
//
// Disabled Contracts are contracts that are no longer active as there are Not
// GoodForUpload and Not GoodForRenew but still have endheights in the current
// period.
//
// Expired contracts are contracts who's endheights are in the past.
//
// ExpiredRefreshed contracts are refreshed contracts who's endheights are in
// the past.
//
// Recoverable contracts are contracts of the renter that are recovered from the
// blockchain by using the renter's seed.
func (api *API) renterContractsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse flags
	var disabled, inactive, expired, recoverable bool
	var err error
	if s := req.FormValue("disabled"); s != "" {
		disabled, err = scanBool(s)
		if err != nil {
			WriteError(w, Error{"unable to parse disabled: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	if s := req.FormValue("inactive"); s != "" {
		inactive, err = scanBool(s)
		if err != nil {
			WriteError(w, Error{"unable to parse inactive: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	if s := req.FormValue("expired"); s != "" {
		expired, err = scanBool(s)
		if err != nil {
			WriteError(w, Error{"unable to parse expired: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	if s := req.FormValue("recoverable"); s != "" {
		recoverable, err = scanBool(s)
		if err != nil {
			WriteError(w, Error{"unable to parse recoverable: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse the renter's contracts into their appropriate categories
	contracts := api.parseRenterContracts(disabled, inactive, expired)

	// Get recoverable contracts
	var recoverableContracts []modules.RecoverableContract
	if recoverable {
		recoverableContracts = api.renter.RecoverableContracts()
	}
	contracts.RecoverableContracts = recoverableContracts

	WriteJSON(w, contracts)
}

// parseRenterContracts categorized the Renter's contracts from Contracts() and
// OldContracts().
func (api *API) parseRenterContracts(disabled, inactive, expired bool) RenterContracts {
	var rc RenterContracts
	currentBlockHeight := api.cs.Height()
	for _, c := range api.renter.Contracts() {
		// Fetch host address
		var netAddress modules.NetAddress
		hdbe, exists, _ := api.renter.Host(c.HostPublicKey)
		if exists {
			netAddress = hdbe.NetAddress
		}

		// Build the contract.
		contract := RenterContract{
			BadContract:               c.Utility.BadContract,
			DownloadSpending:          c.DownloadSpending,
			EndHeight:                 c.EndHeight,
			Fees:                      c.TxnFee.Add(c.SiafundFee).Add(c.ContractFee),
			FundAccountSpending:       c.FundAccountSpending,
			GoodForUpload:             c.Utility.GoodForUpload,
			GoodForRenew:              c.Utility.GoodForRenew,
			HostPublicKey:             c.HostPublicKey,
			HostVersion:               hdbe.Version,
			ID:                        c.ID,
			LastTransaction:           c.Transaction,
			NetAddress:                netAddress,
			MaintenanceSpending:       c.MaintenanceSpending,
			RenterFunds:               c.RenterFunds,
			Size:                      c.Size(),
			StartHeight:               c.StartHeight,
			StorageSpending:           c.StorageSpending,
			StorageSpendingDeprecated: c.StorageSpending,
			TotalCost:                 c.TotalCost,
			UploadSpending:            c.UploadSpending,
		}

		// Determine contract status
		refreshed := api.renter.RefreshedContract(c.ID)
		active := c.Utility.GoodForUpload && c.Utility.GoodForRenew && !refreshed
		passive := !c.Utility.GoodForUpload && c.Utility.GoodForRenew && !refreshed
		disabledContract := disabled && !active && !passive && !refreshed

		// A contract can either be active, passive, refreshed, or disabled
		statusErr := active && passive && refreshed || active && refreshed || active && passive || passive && refreshed
		if statusErr {
			build.Critical("Contract has multiple status types, this should never happen")
		} else if active {
			rc.ActiveContracts = append(rc.ActiveContracts, contract)
		} else if passive {
			rc.PassiveContracts = append(rc.PassiveContracts, contract)
		} else if refreshed {
			rc.RefreshedContracts = append(rc.RefreshedContracts, contract)
		} else if disabledContract {
			rc.DisabledContracts = append(rc.DisabledContracts, contract)
		}

		// Record InactiveContracts and Contracts for compatibility
		if !active && inactive {
			rc.InactiveContracts = append(rc.InactiveContracts, contract)
		}
		rc.Contracts = append(rc.Contracts, contract)
	}

	// Get current block height for reference
	currentPeriod := api.renter.CurrentPeriod()
	for _, c := range api.renter.OldContracts() {
		var size uint64
		if len(c.Transaction.FileContractRevisions) != 0 {
			size = c.Transaction.FileContractRevisions[0].NewFileSize
		}

		// Fetch host address
		var netAddress modules.NetAddress
		hdbe, exists, _ := api.renter.Host(c.HostPublicKey)
		if exists {
			netAddress = hdbe.NetAddress
		}

		// Build contract
		contract := RenterContract{
			BadContract:               c.Utility.BadContract,
			DownloadSpending:          c.DownloadSpending,
			EndHeight:                 c.EndHeight,
			Fees:                      c.TxnFee.Add(c.SiafundFee).Add(c.ContractFee),
			FundAccountSpending:       c.FundAccountSpending,
			GoodForUpload:             c.Utility.GoodForUpload,
			GoodForRenew:              c.Utility.GoodForRenew,
			HostPublicKey:             c.HostPublicKey,
			HostVersion:               hdbe.Version,
			ID:                        c.ID,
			LastTransaction:           c.Transaction,
			MaintenanceSpending:       c.MaintenanceSpending,
			NetAddress:                netAddress,
			RenterFunds:               c.RenterFunds,
			Size:                      size,
			StartHeight:               c.StartHeight,
			StorageSpending:           c.StorageSpending,
			StorageSpendingDeprecated: c.StorageSpending,
			TotalCost:                 c.TotalCost,
			UploadSpending:            c.UploadSpending,
		}

		// Determine contract status
		refreshed := api.renter.RefreshedContract(c.ID)
		endHeightInPast := c.EndHeight < currentBlockHeight || c.StartHeight < currentPeriod
		expiredContract := expired && endHeightInPast && !refreshed
		expiredRefreshed := expired && endHeightInPast && refreshed
		refreshedContract := refreshed && !endHeightInPast
		disabledContract := disabled && !refreshed && !endHeightInPast

		// A contract can only be refreshed, disabled, expired, or expired refreshed
		if expiredContract {
			rc.ExpiredContracts = append(rc.ExpiredContracts, contract)
		} else if expiredRefreshed {
			rc.ExpiredRefreshedContracts = append(rc.ExpiredRefreshedContracts, contract)
		} else if refreshedContract {
			rc.RefreshedContracts = append(rc.RefreshedContracts, contract)
		} else if disabledContract {
			rc.DisabledContracts = append(rc.DisabledContracts, contract)
		}

		// Record inactive contracts for compatibility
		if inactive && !endHeightInPast {
			rc.InactiveContracts = append(rc.InactiveContracts, contract)
		}
	}

	return rc
}

// renterClearDownloadsHandler handles the API call to request to clear the download queue.
func (api *API) renterClearDownloadsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var afterTime time.Time
	beforeTime := types.EndOfTime
	beforeStr, afterStr := req.FormValue("before"), req.FormValue("after")
	if beforeStr != "" {
		beforeInt, err := strconv.ParseInt(beforeStr, 10, 64)
		if err != nil {
			WriteError(w, Error{"parsing integer value for parameter `before` failed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		beforeTime = time.Unix(0, beforeInt)
	}
	if afterStr != "" {
		afterInt, err := strconv.ParseInt(afterStr, 10, 64)
		if err != nil {
			WriteError(w, Error{"parsing integer value for parameter `after` failed: " + err.Error()}, http.StatusBadRequest)
			return
		}
		afterTime = time.Unix(0, afterInt)
	}

	err := api.renter.ClearDownloadHistory(afterTime, beforeTime)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterContractorChurnStatus handles the API call to request the churn status
// from the renter's contractor.
func (api *API) renterContractorChurnStatus(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	WriteJSON(w, api.renter.ContractorChurnStatus())
}

// renterDownloadsHandler handles the API call to request the download queue.
func (api *API) renterDownloadsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var downloads []DownloadInfo
	var err error
	dis := api.renter.DownloadHistory()
	root, err := scanBool(req.FormValue("root"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	if !root {
		dis, err = trimDownloadInfo(dis...)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusInternalServerError)
			return
		}
	}
	for _, di := range dis {
		downloads = append(downloads, DownloadInfo{
			Destination:     di.Destination,
			DestinationType: di.DestinationType,
			Filesize:        di.Length,
			Length:          di.Length,
			Offset:          di.Offset,
			SiaPath:         di.SiaPath,

			Completed:            di.Completed,
			EndTime:              di.EndTime,
			Error:                di.Error,
			Received:             di.Received,
			StartTime:            di.StartTime,
			StartTimeUnix:        di.StartTimeUnix,
			TotalDataTransferred: di.TotalDataTransferred,
		})
	}
	WriteJSON(w, RenterDownloadQueue{
		Downloads: downloads,
	})
}

// renterDownloadByUIDHandlerGET handles the API call to /renter/downloadinfo.
func (api *API) renterDownloadByUIDHandlerGET(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	uid := strings.TrimPrefix(ps.ByName("uid"), "/")
	di, exists := api.renter.DownloadByUID(modules.DownloadID(uid))
	if !exists {
		WriteError(w, Error{fmt.Sprintf("Download with id '%v' doesn't exist", string(uid))}, http.StatusBadRequest)
		return
	}
	dis, err := trimDownloadInfo(di)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusInternalServerError)
		return
	}
	di = dis[0]
	WriteJSON(w, DownloadInfo{
		Destination:     di.Destination,
		DestinationType: di.DestinationType,
		Filesize:        di.Length,
		Length:          di.Length,
		Offset:          di.Offset,
		SiaPath:         di.SiaPath,

		Completed:            di.Completed,
		EndTime:              di.EndTime,
		Error:                di.Error,
		Received:             di.Received,
		StartTime:            di.StartTime,
		StartTimeUnix:        di.StartTimeUnix,
		TotalDataTransferred: di.TotalDataTransferred,
	})
}

// renterFuseHandlerGET handles the API call to /renter/fuse.
func (api *API) renterFuseHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	rfi := RenterFuseInfo{
		MountPoints: api.renter.MountInfo(),
	}
	for i := 0; i < len(rfi.MountPoints); i++ {
		rebased, err := rfi.MountPoints[i].SiaPath.Rebase(modules.UserFolder, modules.RootSiaPath())
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
		rfi.MountPoints[i].SiaPath = rebased
	}

	WriteJSON(w, rfi)
}

// renterFuseMountHandlerPOST handles the API call to /renter/fuse/mount.
func (api *API) renterFuseMountHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var siaPath modules.SiaPath
	var err error
	spfv := req.FormValue("siapath")
	if spfv == "" {
		siaPath = modules.RootSiaPath()
	} else {
		siaPath, err = modules.NewSiaPath(spfv)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}
	siaPath, err = rebaseInputSiaPath(siaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	mount := req.FormValue("mount")
	var opts modules.MountOptions
	if req.FormValue("readonly") != "" {
		readOnly, err := scanBool(req.FormValue("readonly"))
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
		opts.ReadOnly = readOnly
	}
	if req.FormValue("allowother") != "" {
		allowOther, err := scanBool(req.FormValue("allowother"))
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
		opts.AllowOther = allowOther
	}
	if err := api.renter.Mount(mount, siaPath, opts); err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterFuseUnmountHandlerPOST handles the API call to /renter/fuse/unmount.
func (api *API) renterFuseUnmountHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	err := api.renter.Unmount(req.FormValue("mount"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterRecoveryScanHandlerPOST handles the API call to /renter/recoveryscan.
func (api *API) renterRecoveryScanHandlerPOST(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	if err := api.renter.InitRecoveryScan(); err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterRecoveryScanHandlerGET handles the API call to /renter/recoveryscan.
func (api *API) renterRecoveryScanHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	scanInProgress, height := api.renter.RecoveryScanStatus()
	WriteJSON(w, RenterRecoveryStatusGET{
		ScanInProgress: scanInProgress,
		ScannedHeight:  height,
	})
}

// renterRenameHandler handles the API call to rename a file entry in the
// renter.
func (api *API) renterRenameHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse the siaPath and the newSiaPath
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	newSiaPath, err := modules.NewSiaPath(req.FormValue("newsiapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Determine whether the user is requesting a user siapath, or a root siapath.
	root, err := isCalledWithRootFlag(req)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	// Rebase the user's input to the user folder if the user is requesting a user siapath.
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
		newSiaPath, err = rebaseInputSiaPath(newSiaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}
	err = api.renter.RenameFile(siaPath, newSiaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterFileHandler handles GET requests to the /renter/file/:siapath API endpoint.
func (api *API) renterFileHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Determine the siapath that the user wants to get the file from.
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Determine whether the user is requesting a user siapath, or a root siapath.
	root, err := isCalledWithRootFlag(req)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	// Rebase the user's input to the user folder if the user is requesting a user siapath.
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Fetch the file.
	file, err := api.renter.File(siaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// If the user requested the user siapath, trim the dir folder so that the
	// output is all centered around the user's folder.
	if !root {
		files, err := trimSiaDirFolderOnFiles(file)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
		file = files[0]
	}

	WriteJSON(w, RenterFile{
		File: file,
	})
}

// renterFileHandler handles POST requests to the /renter/file/:siapath API endpoint.
func (api *API) renterFileHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	newTrackingPath := req.FormValue("trackingpath")
	stuck := req.FormValue("stuck")
	root, err := scanBool(req.FormValue("root"))
	if err != nil {
		WriteError(w, Error{"unable to parse root flag: " + err.Error()}, http.StatusBadRequest)
		return
	}
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{"unable to parse siapath: " + err.Error()}, http.StatusBadRequest)
		return
	}
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}
	// Handle changing the tracking path of a file.
	if newTrackingPath != "" {
		if err := api.renter.SetFileTrackingPath(siaPath, newTrackingPath); err != nil {
			WriteError(w, Error{fmt.Sprintf("unable set tracking path: %v", err)}, http.StatusBadRequest)
			return
		}
	}
	// Handle changing the 'stuck' status of a file.
	if stuck != "" {
		s, err := strconv.ParseBool(stuck)
		if err != nil {
			WriteError(w, Error{"unable to parse 'stuck' arg"}, http.StatusBadRequest)
			return
		}
		if err := api.renter.SetFileStuck(siaPath, s); err != nil {
			WriteError(w, Error{"failed to change file 'stuck' status: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	WriteSuccess(w)
}

// renterFilesHandler handles the API call to list all of the files.
func (api *API) renterFilesHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var c bool
	var err error
	if cached := req.FormValue("cached"); cached != "" {
		c, err = strconv.ParseBool(cached)
		if err != nil {
			WriteError(w, Error{"unable to parse 'cached' arg"}, http.StatusBadRequest)
			return
		}
	}
	var files []modules.FileInfo
	var mu sync.Mutex
	err = api.renter.FileList(modules.UserFolder, true, c, func(fi modules.FileInfo) {
		mu.Lock()
		files = append(files, fi)
		mu.Unlock()
	})
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	// Sort slices by SiaPath.
	sort.Slice(files, func(i, j int) bool {
		return files[i].SiaPath.String() < files[j].SiaPath.String()
	})
	files, err = trimSiaDirFolderOnFiles(files...)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, RenterFiles{
		Files: files,
	})
}

// renterPricesHandler reports the expected costs of various actions given the
// renter settings and the set of available hosts.
func (api *API) renterPricesHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	allowance := modules.Allowance{}
	// Scan the allowance amount. (optional parameter)
	if f := req.FormValue("funds"); f != "" {
		funds, ok := scanAmount(f)
		if !ok {
			WriteError(w, Error{"unable to parse funds"}, http.StatusBadRequest)
			return
		}
		allowance.Funds = funds
	}
	// Scan the number of hosts to use. (optional parameter)
	if h := req.FormValue("hosts"); h != "" {
		var hosts uint64
		if _, err := fmt.Sscan(h, &hosts); err != nil {
			WriteError(w, Error{"unable to parse hosts: " + err.Error()}, http.StatusBadRequest)
			return
		} else if hosts != 0 && hosts < requiredHosts {
			WriteError(w, Error{fmt.Sprintf("insufficient number of hosts, need at least %v but have %v", modules.DefaultAllowance.Hosts, hosts)}, http.StatusBadRequest)
			return
		} else {
			allowance.Hosts = hosts
		}
	}
	// Scan the period. (optional parameter)
	if p := req.FormValue("period"); p != "" {
		var period types.BlockHeight
		if _, err := fmt.Sscan(p, &period); err != nil {
			WriteError(w, Error{"unable to parse period: " + err.Error()}, http.StatusBadRequest)
			return
		}
		allowance.Period = types.BlockHeight(period)
	}
	// Scan the renew window. (optional parameter)
	if rw := req.FormValue("renewwindow"); rw != "" {
		var renewWindow types.BlockHeight
		if _, err := fmt.Sscan(rw, &renewWindow); err != nil {
			WriteError(w, Error{"unable to parse renewwindow: " + err.Error()}, http.StatusBadRequest)
			return
		} else if renewWindow != 0 && types.BlockHeight(renewWindow) < requiredRenewWindow {
			WriteError(w, Error{fmt.Sprintf("renew window is too small, must be at least %v blocks but have %v blocks", requiredRenewWindow, renewWindow)}, http.StatusBadRequest)
			return
		} else {
			allowance.RenewWindow = types.BlockHeight(renewWindow)
		}
	}

	// Check for partially set allowance, which can happen since hosts and renew
	// window can be optional fields. Checking here instead of assigning values
	// above so that an empty allowance can still be submitted
	if !reflect.DeepEqual(allowance, modules.Allowance{}) {
		if allowance.Funds.Cmp(types.ZeroCurrency) == 0 {
			WriteError(w, Error{fmt.Sprint("Allowance not set correctly, `funds` parameter left empty")}, http.StatusBadRequest)
			return
		}
		if allowance.Period == 0 {
			WriteError(w, Error{fmt.Sprint("Allowance not set correctly, `period` parameter left empty")}, http.StatusBadRequest)
			return
		}
		if allowance.Hosts == 0 {
			WriteError(w, Error{fmt.Sprint("Allowance not set correctly, `hosts` parameter left empty")}, http.StatusBadRequest)
			return
		}
		if allowance.RenewWindow == 0 {
			WriteError(w, Error{fmt.Sprint("Allowance not set correctly, `renewwindow` parameter left empty")}, http.StatusBadRequest)
			return
		}
	}

	estimate, a, err := api.renter.PriceEstimation(allowance)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, RenterPricesGET{
		RenterPriceEstimation: estimate,
		Allowance:             a,
	})
}

// renterDeleteHandler handles the API call to delete a file entry from the
// renter.
func (api *API) renterDeleteHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Determine whether the user is requesting a user siapath, or a root siapath.
	root, err := isCalledWithRootFlag(req)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	// Rebase the user's input to the user folder if the user is requesting a user siapath.
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	err = api.renter.DeleteFile(siaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// renterCancelDownloadHandler handles the API call to cancel a download.
func (api *API) renterCancelDownloadHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Get the id.
	id := modules.DownloadID(req.FormValue("id"))
	if id == "" {
		WriteError(w, Error{"id not specified"}, http.StatusBadRequest)
		return
	}
	// Get the download from the map and delete it.
	api.downloadMu.Lock()
	cancel, ok := api.downloads[id]
	delete(api.downloads, id)
	api.downloadMu.Unlock()
	if !ok {
		WriteError(w, Error{"download for id not found"}, http.StatusBadRequest)
		return
	}
	// Cancel download and delete it from the map.
	cancel()
	WriteSuccess(w)
}

// renterDownloadHandler handles the API call to download a file.
func (api *API) renterDownloadHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	params, err := parseDownloadParameters(w, req, ps)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	var id modules.DownloadID
	var start func() error
	if params.Async {
		var cancel func()
		id, start, cancel, err = api.renter.DownloadAsync(params, func(_ error) error {
			api.downloadMu.Lock()
			delete(api.downloads, id)
			api.downloadMu.Unlock()
			return nil
		})
		// Add download to API's map for cancellation.
		if err == nil {
			api.downloadMu.Lock()
			api.downloads[id] = cancel
			api.downloadMu.Unlock()
		}
	} else {
		id, start, err = api.renter.Download(params)
	}
	if err != nil {
		WriteError(w, Error{"download creation failed: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	// Set ID before starting download.
	w.Header().Set("ID", string(id))
	// Start download.
	if err := start(); err != nil {
		WriteError(w, Error{"download failed: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	if params.Httpwriter == nil {
		// `httpresp=true` causes writes to w before this line is run, automatically
		// adding `200 Status OK` code to response. Calling this results in a
		// multiple calls to WriteHeaders() errors.
		WriteSuccess(w)
		return
	}
}

// renterDownloadAsyncHandler handles the API call to download a file asynchronously.
func (api *API) renterDownloadAsyncHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	req.ParseForm()
	req.Form.Set("async", "true")
	api.renterDownloadHandler(w, req, ps)
}

// parseDownloadParameters parses the download parameters passed to the
// /renter/download endpoint. Validation of these parameters is done by the
// renter.
func parseDownloadParameters(w http.ResponseWriter, req *http.Request, ps httprouter.Params) (modules.RenterDownloadParameters, error) {
	destination := req.FormValue("destination")

	// The offset and length in bytes.
	offsetparam := req.FormValue("offset")
	lengthparam := req.FormValue("length")

	// Determines whether the response is written to response body.
	httprespparam := req.FormValue("httpresp")

	// Determines whether to return on completion of download or straight away.
	// If httprespparam is present, this parameter is ignored.
	asyncparam := req.FormValue("async")

	// Determines whether to interpret the siapath as a root path or originating
	// from /home/user.
	rootparam := req.FormValue("root")

	// disablelocalfetchparam determines whether downloads will be fetched from
	// disk if available.
	disablelocalfetchparam := req.FormValue("disablelocalfetch")

	// Parse the offset and length parameters.
	var offset, length uint64
	if len(offsetparam) > 0 {
		_, err := fmt.Sscan(offsetparam, &offset)
		if err != nil {
			return modules.RenterDownloadParameters{}, errors.AddContext(err, "could not decode the offset as uint64")
		}
	}
	if len(lengthparam) > 0 {
		_, err := fmt.Sscan(lengthparam, &length)
		if err != nil {
			return modules.RenterDownloadParameters{}, errors.AddContext(err, "could not decode the offset as uint64")
		}
	}

	// Parse the httpresp parameter.
	httpresp, err := scanBool(httprespparam)
	if err != nil {
		return modules.RenterDownloadParameters{}, errors.AddContext(err, "httpresp parameter could not be parsed")
	}

	// Parse the async parameter.
	async, err := scanBool(asyncparam)
	if err != nil {
		return modules.RenterDownloadParameters{}, errors.AddContext(err, "async parameter could not be parsed")
	}

	// Parse the root parameter. If it is set we rebase the siapath.
	root, err := scanBool(rootparam)
	if err != nil {
		return modules.RenterDownloadParameters{}, errors.AddContext(err, "root parameter could not be parsed")
	}

	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		return modules.RenterDownloadParameters{}, errors.AddContext(err, "error parsing the siapath")
	}

	// If root is not set we need to rebase the siapath.
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			return modules.RenterDownloadParameters{}, err
		}
	}

	var disableLocalFetch bool
	if disablelocalfetchparam != "" {
		disableLocalFetch, err = scanBool(disablelocalfetchparam)
		if err != nil {
			return modules.RenterDownloadParameters{}, errors.AddContext(err, "error parsing the disablelocalfetch flag")
		}
	}

	dp := modules.RenterDownloadParameters{
		Destination:      destination,
		DisableDiskFetch: disableLocalFetch,
		Async:            async,
		Length:           length,
		Offset:           offset,
		SiaPath:          siaPath,
	}
	if httpresp {
		dp.Httpwriter = w
	}

	return dp, nil
}

// renterStreamHandler handles downloads from the /renter/stream endpoint
func (api *API) renterStreamHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	root, err := scanBool(req.FormValue("root"))
	if err != nil {
		err = errors.AddContext(err, "error parsing the root flag")
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}
	disablelocalfetchparam := req.FormValue("disablelocalfetch")
	var disableLocalFetch bool
	if disablelocalfetchparam != "" {
		disableLocalFetch, err = scanBool(disablelocalfetchparam)
		if err != nil {
			err = errors.AddContext(err, "error parsing the disablelocalfetch flag")
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}
	fileName, streamer, err := api.renter.Streamer(siaPath, disableLocalFetch)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to create download streamer: %v", err)},
			http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = streamer.Close()
	}()
	http.ServeContent(w, req, fileName, time.Time{}, streamer)
}

// renterUploadHandler handles the API call to upload a file.
func (api *API) renterUploadHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Get the source path.
	source := req.FormValue("source")
	// Source must be absolute path.
	if !filepath.IsAbs(source) {
		WriteError(w, Error{"source must be an absolute path"}, http.StatusBadRequest)
		return
	}
	// Check whether existing file should be overwritten
	var err error
	force := false
	if f := req.FormValue("force"); f != "" {
		force, err = strconv.ParseBool(f)
		if err != nil {
			WriteError(w, Error{"unable to parse 'force' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	// Parse the erasure coder.
	ec, err := parseErasureCodingParameters(req.FormValue("datapieces"), req.FormValue("paritypieces"))
	if err != nil {
		WriteError(w, Error{"unable to parse erasure code settings: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Call the renter to upload the file.
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	siaPath, err = rebaseInputSiaPath(siaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	err = api.renter.Upload(modules.FileUploadParams{
		Source:              source,
		SiaPath:             siaPath,
		ErasureCode:         ec,
		Force:               force,
		DisablePartialChunk: true, // TODO: remove this

		// NOTE: can make this an optional param.
		CipherType: crypto.TypeDefaultRenter,
	})
	if err != nil {
		WriteError(w, Error{"upload failed: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteSuccess(w)
}

// renterUploadReadyHandler handles the API call to check whether or not the
// renter is ready to upload files
func (api *API) renterUploadReadyHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Gather params
	dataPiecesStr := req.FormValue("datapieces")
	parityPiecesStr := req.FormValue("paritypieces")

	// Check params
	dataPieces, parityPieces, err := ParseDataAndParityPieces(dataPiecesStr, parityPiecesStr)
	if err != nil {
		WriteError(w, Error{"failed to parse query params: " + err.Error()}, http.StatusBadRequest)
		return
	}
	// Check if we need to set to defaults
	if dataPieces == 0 && parityPieces == 0 {
		dataPieces = modules.RenterDefaultDataPieces
		parityPieces = modules.RenterDefaultParityPieces
	}
	contractsNeeded := dataPieces + parityPieces

	// Get contracts - compare against data and parity pieces
	contracts := api.parseRenterContracts(false, false, false)
	WriteJSON(w, RenterUploadReadyGet{
		Ready:              len(contracts.ActiveContracts) >= contractsNeeded,
		ContractsNeeded:    contractsNeeded,
		NumActiveContracts: len(contracts.ActiveContracts),
		DataPieces:         dataPieces,
		ParityPieces:       parityPieces,
	})
}

// renterUploadsPauseHandler handles the api call to pause the renter's uploads,
// this includes repairs
func (api *API) renterUploadsPauseHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	durationStr := req.FormValue("duration")
	duration := renter.DefaultPauseDuration
	var err error
	if durationStr != "" {
		durationInt, err := strconv.ParseUint(durationStr, 10, 64)
		if err != nil {
			WriteError(w, Error{"failed to parse duration: " + err.Error()}, http.StatusBadRequest)
			return
		}
		duration = time.Second * time.Duration(durationInt)
	}

	err = api.renter.PauseRepairsAndUploads(duration)
	if err != nil {
		WriteError(w, Error{"failed to pause uploads: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterUploadsResumeHandler handles the api call to resume the renter's
// uploads, this includes repairs
func (api *API) renterUploadsResumeHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	err := api.renter.ResumeRepairsAndUploads()
	if err != nil {
		WriteError(w, Error{"failed to resume uploads: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterUploadStreamHandler handles the API call to upload a file using a
// stream.
func (api *API) renterUploadStreamHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse the query params.
	queryForm, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		WriteError(w, Error{"failed to parse query params"}, http.StatusBadRequest)
		return
	}
	// Check whether existing file should be overwritten
	force := false
	if f := queryForm.Get("force"); f != "" {
		force, err = strconv.ParseBool(f)
		if err != nil {
			WriteError(w, Error{"unable to parse 'force' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	// Check whether existing file should be repaired
	repair := false
	if r := queryForm.Get("repair"); r != "" {
		repair, err = strconv.ParseBool(r)
		if err != nil {
			WriteError(w, Error{"unable to parse 'repair' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	// Parse the erasure coder.
	ec, err := parseErasureCodingParameters(queryForm.Get("datapieces"), queryForm.Get("paritypieces"))
	if err != nil && !repair {
		WriteError(w, Error{"unable to parse erasure code settings: " + err.Error()}, http.StatusBadRequest)
		return
	}
	if repair && ec != nil {
		WriteError(w, Error{"can't provide erasure code settings when doing a repair"}, http.StatusBadRequest)
		return
	}

	// Call the renter to upload the file.
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	siaPath, err = rebaseInputSiaPath(siaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	up := modules.FileUploadParams{
		SiaPath:     siaPath,
		ErasureCode: ec,
		Force:       force,
		Repair:      repair,

		// NOTE: can make this an optional param.
		CipherType: crypto.TypeDefaultRenter,
	}
	err = api.renter.UploadStreamFromReader(up, req.Body)
	if err != nil {
		WriteError(w, Error{"upload failed: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteSuccess(w)
}

// renterValidateSiaPathHandler handles the API call that validates a siapath
func (api *API) renterValidateSiaPathHandler(w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	// Try and create a new siapath, this will validate the potential siapath
	_, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// renterDirHandlerGET handles the API call to query a directory
func (api *API) renterDirHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	var siaPath modules.SiaPath
	var err error

	// Check whether the user is requesting the directory from the root path.
	root, err := isCalledWithRootFlag(req)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	str := ps.ByName("siapath")
	if str == "" || str == "/" {
		siaPath = modules.RootSiaPath()
	} else {
		siaPath, err = modules.NewSiaPath(str)
	}
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	directories, err := api.renter.DirList(siaPath)
	if err != nil {
		WriteError(w, Error{"failed to get directory contents: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	if !root {
		directories, err = trimSiaDirFolder(directories...)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	var files []modules.FileInfo
	var mu sync.Mutex
	err = api.renter.FileList(siaPath, false, true, func(fi modules.FileInfo) {
		mu.Lock()
		files = append(files, fi)
		mu.Unlock()
	})
	if err != nil {
		WriteError(w, Error{"failed to get file infos: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	if !root {
		files, err = trimSiaDirFolderOnFiles(files...)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	WriteJSON(w, RenterDirectory{
		Directories: directories,
		Files:       files,
	})
	return
}

// renterDirHandlerPOST handles POST requests to /renter/dir/:siapath?action=<>
// in order to create, delete, and rename a directory
func (api *API) renterDirHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse action
	action := req.FormValue("action")
	if action == "" {
		WriteError(w, Error{"you must set the action you wish to execute"}, http.StatusInternalServerError)
		return
	}
	// Parse mode
	mode := os.FileMode(modules.DefaultDirPerm)
	if m := req.FormValue("mode"); m != "" {
		mode64, err := strconv.ParseUint(m, 10, 32)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to parse provided mode '%v'", m)}, http.StatusBadRequest)
			return
		}
		mode = os.FileMode(mode64)
	}
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Determine whether the user is requesting a user siapath, or a root siapath.
	root, err := isCalledWithRootFlag(req)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	// Rebase the user's input to the user folder if the user is requesting a user siapath.
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	if action == "create" {
		// Call the renter to create directory
		err := api.renter.CreateDir(siaPath, mode)
		if err != nil {
			WriteError(w, Error{"failed to create directory: " + err.Error()}, http.StatusInternalServerError)
			return
		}
		WriteSuccess(w)
		return
	}
	if action == "delete" {
		err := api.renter.DeleteDir(siaPath)
		if err != nil {
			WriteError(w, Error{"failed to delete directory: " + err.Error()}, http.StatusInternalServerError)
			return
		}
		WriteSuccess(w)
		return
	}
	if action == "rename" {
		newSiaPath, err := modules.NewSiaPath(req.FormValue("newsiapath"))
		if err != nil {
			WriteError(w, Error{"failed to parse newsiapath: " + err.Error()}, http.StatusBadRequest)
			return
		}
		newSiaPath, err = rebaseInputSiaPath(newSiaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
		err = api.renter.RenameDir(siaPath, newSiaPath)
		if err != nil {
			WriteError(w, Error{"failed to rename directory: " + err.Error()}, http.StatusInternalServerError)
			return
		}
		WriteSuccess(w)
		return
	}

	// Report that no calls were made
	WriteError(w, Error{"no calls were made, please check your submission and try again"}, http.StatusInternalServerError)
	return
}

// renterContractStatusHandler  handles the API call to check the status of a
// contract monitored by the renter.
func (api *API) renterContractStatusHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var fcID types.FileContractID
	if err := fcID.LoadString(req.FormValue("id")); err != nil {
		WriteError(w, Error{"unable to parse id: " + err.Error()}, http.StatusBadRequest)
		return
	}

	contractStatus, monitoringContract := api.renter.ContractStatus(fcID)
	if !monitoringContract {
		WriteError(w, Error{"renter unaware of contract"}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, contractStatus)
}

// renterWorkersHandler handles the API call to check the status of the renter's
// workers
func (api *API) renterWorkersHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	workerPoolStatus, err := api.renter.WorkerPoolStatus()
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, workerPoolStatus)
}

func (api *API) renterFileHostsHandler(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Determine the siapath that the user wants to get the file from.
	siaPath, err := modules.NewSiaPath(ps.ByName("siapath"))
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Determine whether the user is requesting a user siapath, or a root siapath.
	root, err := isCalledWithRootFlag(req)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}
	// Rebase the user's input to the user folder if the user is requesting a user siapath.
	if !root {
		siaPath, err = rebaseInputSiaPath(siaPath)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}
	}

	hosts, err := api.renter.FileHosts(siaPath)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, hosts)
}
