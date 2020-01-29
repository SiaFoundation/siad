package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/big"
	"net/http"
	"path"
	"path/filepath"
	"runtime"

	"github.com/inconshreveable/go-update"
	"github.com/julienschmidt/httprouter"
	"github.com/kardianos/osext"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// The developer key is used to sign updates and other important Sia-
	// related information.
	developerKey = `-----BEGIN PGP PUBLIC KEY BLOCK-----

mQINBF4U62ABEADQip/SzQrFvmL761iKRk6L3N8yHX7y0LUaSxS+jaTlsROtBkEl
dvfoyt/o3HsAplEwVfOHPCZCmsqOR3HoibTVohHVpMCOWwsr9blSkTzGl1YUgQ73
qP+A31gt/4Gyiyn+5q/qGBa5e40Oy6bDXFeH/ByH6manf/bffe5kP0H4Qphrg7R1
aeShajtvIr5ioYbc/NUOWkPfsU1Jtn1dwkMZUXMIAKADhy8A0428kv6E3oTvHVbd
HzrEGo2T247Qe9bQE6sRkAy7CLYOmx/MexN65zlNoYFJl5LkGyM0pWCsEsqX5I7a
jYHK8FCAHihFhQ14QH57lvT5yQwgXeu69egrZixOw2EE82NekTqTKf9etU9iUpk8
DjjT2vChq7qGY6v1JILxU1UsvNgVDnrZDPkLQVlaW6rW27f0cOxcxjcxZNlBpVxe
RuuL4bImhXR4wO/EZYEaeyd4bOyRTenZlkx/vmAPCyjDchAQZvHlYGalU5VEmZGd
BTr22AB4+4J6y/MqprZv4U5NkRlEnM3sWgJRLMCwoIKHjJpSFzF+jyveeIlnL4i6
ezpfCdEEGxrmOPQj2H2m7MmQuOMkdhfFEWNNfhvOb7/7OadpBZ7d+wg9gfDHM62f
LqYypyWsX+3QGxtnOAvSsF6j5LAm2L7lyQ4p05BUoCvPW/7L+LoAJRdETwARAQAB
tCBTaWEgU2lnbmluZyBLZXkgPGhlbGxvQHNpYS50ZWNoPokCVAQTAQoAPhYhBB9J
RQueMeJKhy3DR6qPStVYMJQRBQJeFOtgAhsPBQkDwmcABQsJCAcCBhUKCQgLAgQW
AgMBAh4BAheAAAoJEKqPStVYMJQRRmYQALL6+MYvzx8cyIm3LwRH2rcK1XC8iLmP
6kZbFa4UJvjTD8dgJ5oxAKA7sSrMwROAx28ijFgMu4MM0XHvZgZS/pKjFQrYedWd
nI9rwwXgu8KLxEH4CaeYhaKMaJ5JSpCUdGdnG/bxldAnLGXuPK327DW4dXQOY8Om
xIkvU/gMnyjWmY5EB9Ytis3+ir6QOgpUsVaqJY/5u7BVLLbNOB/+RfzYkvIdHJj/
SEx9Qg36O9+igh1MfUUuHS/1logNsY9FoCnYrw/du26sKNh9kcjr2agSjtDJpeF4
kVSgIW5Toak64NgcLECUi/NE9gU5MydpiaLu7WmOuVIdWhWCU8CISUXRDo+KMyOa
fqCxaz/GXKn/GCEJq1qY3FNu28awSq2msQ7eE88y/qGVtmgEd9rWWhh7Ze27qx0d
vFZVTYyT2lARmlL6p7faFqpwxHFEx/ylLqAaEaBgKsZvmjPI11f4PLWkOAnQ8uqd
1SHWzm1v6frOAOpHBbTKPVTEnWhXDphprW43nTmS4mQP7L+lsJcEtATGHIVXv2Bg
O5msa2VAPRDmTtiXiQZXEnzxQSBI2/aA5wdYKen0woN9YS0MQsXXy2lcNSxWOXnq
dU1u9C6NxDnhx0CDVlcKmRzfmW5RtcgLiYXJoR4AgFbhSH2D+13dwrJrzR85ZeZK
y6/Gelaei3D0
=XTvn
-----END PGP PUBLIC KEY BLOCK-----`
)

type (
	// DaemonAlertsGet contains information about currently registered alerts
	// across all loaded modules.
	DaemonAlertsGet struct {
		Alerts []modules.Alert `json:"alerts"`
	}

	// DaemonVersionGet contains information about the running daemon's version.
	DaemonVersionGet struct {
		Version     string
		GitRevision string
		BuildTime   string
	}

	// DaemonUpdateGet contains information about a potential available update for
	// the daemon.
	DaemonUpdateGet struct {
		Available bool   `json:"available"`
		Version   string `json:"version"`
	}

	// UpdateInfo indicates whether an update is available, and to what
	// version.
	UpdateInfo struct {
		Available bool   `json:"available"`
		Version   string `json:"version"`
	}
	// gitlabRelease represents some of the JSON returned by the GitLab
	// release API endpoint. Only the fields relevant to updating are
	// included.
	gitlabRelease struct {
		Name string `json:"name"`
	}

	// SiaConstants is a struct listing all of the constants in use.
	SiaConstants struct {
		BlockFrequency         types.BlockHeight `json:"blockfrequency"`
		BlockSizeLimit         uint64            `json:"blocksizelimit"`
		ExtremeFutureThreshold types.Timestamp   `json:"extremefuturethreshold"`
		FutureThreshold        types.Timestamp   `json:"futurethreshold"`
		GenesisTimestamp       types.Timestamp   `json:"genesistimestamp"`
		MaturityDelay          types.BlockHeight `json:"maturitydelay"`
		MedianTimestampWindow  uint64            `json:"mediantimestampwindow"`
		SiafundCount           types.Currency    `json:"siafundcount"`
		SiafundPortion         *big.Rat          `json:"siafundportion"`
		TargetWindow           types.BlockHeight `json:"targetwindow"`

		InitialCoinbase uint64 `json:"initialcoinbase"`
		MinimumCoinbase uint64 `json:"minimumcoinbase"`

		RootTarget types.Target `json:"roottarget"`
		RootDepth  types.Target `json:"rootdepth"`

		DefaultAllowance modules.Allowance `json:"defaultallowance"`

		// DEPRECATED: same values as MaxTargetAdjustmentUp and
		// MaxTargetAdjustmentDown.
		MaxAdjustmentUp   *big.Rat `json:"maxadjustmentup"`
		MaxAdjustmentDown *big.Rat `json:"maxadjustmentdown"`

		MaxTargetAdjustmentUp   *big.Rat `json:"maxtargetadjustmentup"`
		MaxTargetAdjustmentDown *big.Rat `json:"maxtargetadjustmentdown"`

		SiacoinPrecision types.Currency `json:"siacoinprecision"`
	}

	// DaemonVersion holds the version information for siad
	DaemonVersion struct {
		Version     string `json:"version"`
		GitRevision string `json:"gitrevision"`
		BuildTime   string `json:"buildtime"`
	}
)

// fetchLatestRelease returns metadata about the most recent GitLab release.
func fetchLatestRelease() (gitlabRelease, error) {
	resp, err := http.Get("https://gitlab.com/api/v4/projects/7508674/repository/tags?order_by=name")
	if err != nil {
		return gitlabRelease{}, err
	}
	defer resp.Body.Close()
	var releases []gitlabRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return gitlabRelease{}, err
	} else if len(releases) == 0 {
		return gitlabRelease{}, errors.New("no releases found")
	}
	return releases[0], nil
}

// updateToRelease updates siad and siac to the release specified. siac is
// assumed to be in the same folder as siad.
func updateToRelease(version string) error {
	updateOpts := update.Options{
		Verifier: update.NewRSAVerifier(),
	}
	err := updateOpts.SetPublicKeyPEM([]byte(developerKey))
	if err != nil {
		// should never happen
		return err
	}

	binaryFolder, err := osext.ExecutableFolder()
	if err != nil {
		return err
	}

	// download release archive
	resp, err := http.Get(fmt.Sprintf("https://sia.tech/releases/Sia-%s-%s-%s.zip", version, runtime.GOOS, runtime.GOARCH))
	if err != nil {
		return err
	}
	// release should be small enough to store in memory (<10 MiB); use
	// LimitReader to ensure we don't download more than 32 MiB
	content, err := ioutil.ReadAll(io.LimitReader(resp.Body, 1<<25))
	resp.Body.Close()
	if err != nil {
		return err
	}
	r := bytes.NewReader(content)
	z, err := zip.NewReader(r, r.Size())
	if err != nil {
		return err
	}

	// process zip, finding siad/siac binaries and signatures
	for _, binary := range []string{"siad", "siac"} {
		var binData io.ReadCloser
		var signature []byte
		var binaryName string // needed for TargetPath below
		for _, zf := range z.File {
			switch base := path.Base(zf.Name); base {
			case binary, binary + ".exe":
				binaryName = base
				binData, err = zf.Open()
				if err != nil {
					return err
				}
				defer binData.Close()
			case binary + ".sig", binary + ".exe.sig":
				sigFile, err := zf.Open()
				if err != nil {
					return err
				}
				defer sigFile.Close()
				signature, err = ioutil.ReadAll(sigFile)
				if err != nil {
					return err
				}
			}
		}
		if binData == nil {
			return errors.New("could not find " + binary + " binary")
		} else if signature == nil {
			return errors.New("could not find " + binary + " signature")
		}

		// apply update
		updateOpts.Signature = signature
		updateOpts.TargetMode = 0775 // executable
		updateOpts.TargetPath = filepath.Join(binaryFolder, binaryName)
		err = update.Apply(binData, updateOpts)
		if err != nil {
			return err
		}
	}

	return nil
}

// daemonAlertsHandlerGET handles the API call that returns the alerts of all
// loaded modules.
func (api *API) daemonAlertsHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	alerts := make([]modules.Alert, 0, 6) // initialize slice to avoid "null" in response.
	if api.gateway != nil {
		alerts = append(alerts, api.gateway.Alerts()...)
	}
	if api.cs != nil {
		alerts = append(alerts, api.cs.Alerts()...)
	}
	if api.tpool != nil {
		alerts = append(alerts, api.tpool.Alerts()...)
	}
	if api.wallet != nil {
		alerts = append(alerts, api.wallet.Alerts()...)
	}
	if api.renter != nil {
		alerts = append(alerts, api.renter.Alerts()...)
	}
	if api.host != nil {
		alerts = append(alerts, api.host.Alerts()...)
	}
	WriteJSON(w, DaemonAlertsGet{
		Alerts: alerts,
	})
}

// daemonUpdateHandlerGET handles the API call that checks for an update.
func (api *API) daemonUpdateHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	release, err := fetchLatestRelease()
	if err != nil {
		WriteError(w, Error{Message: "Failed to fetch latest release: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	latestVersion := release.Name[1:] // delete leading 'v'
	WriteJSON(w, UpdateInfo{
		Available: build.VersionCmp(latestVersion, build.Version) > 0,
		Version:   latestVersion,
	})
}

// daemonUpdateHandlerPOST handles the API call that updates siad and siac.
// There is no safeguard to prevent "updating" to the same release, so callers
// should always check the latest version via daemonUpdateHandlerGET first.
// TODO: add support for specifying version to update to.
func (api *API) daemonUpdateHandlerPOST(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	release, err := fetchLatestRelease()
	if err != nil {
		WriteError(w, Error{Message: "Failed to fetch latest release: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	err = updateToRelease(release.Name)
	if err != nil {
		if rerr := update.RollbackError(err); rerr != nil {
			WriteError(w, Error{Message: "Serious error: Failed to rollback from bad update: " + rerr.Error()}, http.StatusInternalServerError)
		} else {
			WriteError(w, Error{Message: "Failed to apply update: " + err.Error()}, http.StatusInternalServerError)
		}
		return
	}
	WriteSuccess(w)
}

// debugConstantsHandler prints a json file containing all of the constants.
func (api *API) daemonConstantsHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	sc := SiaConstants{
		BlockFrequency:         types.BlockFrequency,
		BlockSizeLimit:         types.BlockSizeLimit,
		ExtremeFutureThreshold: types.ExtremeFutureThreshold,
		FutureThreshold:        types.FutureThreshold,
		GenesisTimestamp:       types.GenesisTimestamp,
		MaturityDelay:          types.MaturityDelay,
		MedianTimestampWindow:  types.MedianTimestampWindow,
		SiafundCount:           types.SiafundCount,
		SiafundPortion:         types.SiafundPortion,
		TargetWindow:           types.TargetWindow,

		InitialCoinbase: types.InitialCoinbase,
		MinimumCoinbase: types.MinimumCoinbase,

		RootTarget: types.RootTarget,
		RootDepth:  types.RootDepth,

		DefaultAllowance: modules.DefaultAllowance,

		// DEPRECATED: same values as MaxTargetAdjustmentUp and
		// MaxTargetAdjustmentDown.
		MaxAdjustmentUp:   types.MaxTargetAdjustmentUp,
		MaxAdjustmentDown: types.MaxTargetAdjustmentDown,

		MaxTargetAdjustmentUp:   types.MaxTargetAdjustmentUp,
		MaxTargetAdjustmentDown: types.MaxTargetAdjustmentDown,

		SiacoinPrecision: types.SiacoinPrecision,
	}

	WriteJSON(w, sc)
}

// daemonVersionHandler handles the API call that requests the daemon's version.
func (api *API) daemonVersionHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	version := build.Version
	if build.ReleaseTag != "" {
		version += "-" + build.ReleaseTag
	}
	WriteJSON(w, DaemonVersion{Version: version, GitRevision: build.GitRevision, BuildTime: build.BuildTime})
}

// daemonStopHandler handles the API call to stop the daemon cleanly.
func (api *API) daemonStopHandler(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// can't write after we stop the server, so lie a bit.
	WriteSuccess(w)

	// need to flush the response before shutting down the server
	f, ok := w.(http.Flusher)
	if !ok {
		panic("Server does not support flushing")
	}
	f.Flush()

	if err := api.Shutdown(); err != nil {
		build.Critical(err)
	}
}

// DaemonSettingsGet contains information about global daemon settings.
type DaemonSettingsGet struct {
	MaxDownloadSpeed int64 `json:"maxdownloadspeed"`
	MaxUploadSpeed   int64 `json:"maxuploadspeed"`
}

// daemonSettingsHandlerGET handles the API call asking for the daemon's
// settings.
func (api *API) daemonSettingsHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	gmds, gmus, _ := modules.GlobalRateLimits.Limits()
	WriteJSON(w, DaemonSettingsGet{gmds, gmus})
}

// daemonSettingsHandlerPOST handles the API call changing daemon specific
// settings.
func (api *API) daemonSettingsHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	maxDownloadSpeed, maxUploadSpeed, _ := modules.GlobalRateLimits.Limits()
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
	// Set the limit.
	if err := api.siadConfig.SetRatelimit(maxDownloadSpeed, maxUploadSpeed); err != nil {
		WriteError(w, Error{"unable to set limits: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}
