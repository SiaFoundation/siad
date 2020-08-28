package api

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/modules/renter/skynetportals"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// DefaultSkynetDefaultPath is the defaultPath value we use when the user
	// hasn't specified one and `index.html` exists in the skyfile.
	DefaultSkynetDefaultPath = "index.html"

	// DefaultSkynetRequestTimeout is the default request timeout for routes
	// that have a timeout query string parameter. If the request can not be
	// resolved within the given amount of time, it times out. This is used for
	// Skynet routes where a request times out if the DownloadByRoot project
	// does not finish in due time.
	DefaultSkynetRequestTimeout = 30 * time.Second

	// MaxSkynetRequestTimeout is the maximum a user is allowed to set as
	// request timeout. This to prevent an attack vector where the attacker
	// could cause a go-routine leak by creating a bunch of requests with very
	// high timeouts.
	MaxSkynetRequestTimeout = 15 * 60 // in seconds
)

var (
	// ErrInvalidDefaultPath is returned when the specified default path is not
	// valid, e.g. the file it points to does not exist.
	ErrInvalidDefaultPath = errors.New("invalid default path provided")
)

type (
	// SkynetSkyfileHandlerPOST is the response that the api returns after the
	// /skynet/ POST endpoint has been used.
	SkynetSkyfileHandlerPOST struct {
		Skylink    string      `json:"skylink"`
		MerkleRoot crypto.Hash `json:"merkleroot"`
		Bitfield   uint16      `json:"bitfield"`
	}

	// SkynetBlacklistGET contains the information queried for the
	// /skynet/blacklist GET endpoint
	//
	// NOTE: With v1.5.0 the return value for the Blacklist changed. Pre v1.5.0
	// the []crypto.Hash was a slice of MerkleRoots. Post v1.5.0 the []crypto.Hash
	// is a slice of the Hashes of the MerkleRoots
	SkynetBlacklistGET struct {
		Blacklist []crypto.Hash `json:"blacklist"`
	}

	// SkynetBlacklistPOST contains the information needed for the
	// /skynet/blacklist POST endpoint to be called
	SkynetBlacklistPOST struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}

	// SkynetPortalsGET contains the information queried for the /skynet/portals
	// GET endpoint.
	SkynetPortalsGET struct {
		Portals []modules.SkynetPortal `json:"portals"`
	}

	// SkynetPortalsPOST contains the information needed for the /skynet/portals
	// POST endpoint to be called.
	SkynetPortalsPOST struct {
		Add    []modules.SkynetPortal `json:"add"`
		Remove []modules.NetAddress   `json:"remove"`
	}

	// SkynetStatsGET contains the information queried for the /skynet/stats
	// GET endpoint
	SkynetStatsGET struct {
		PerformanceStats SkynetPerformanceStats `json:"performancestats"`

		Uptime      int64         `json:"uptime"`
		UploadStats SkynetStats   `json:"uploadstats"`
		VersionInfo SkynetVersion `json:"versioninfo"`
	}

	// SkynetStats contains statistical data about skynet
	SkynetStats struct {
		NumFiles  int    `json:"numfiles"`
		TotalSize uint64 `json:"totalsize"`
	}

	// SkynetVersion contains version information
	SkynetVersion struct {
		Version     string `json:"version"`
		GitRevision string `json:"gitrevision"`
	}

	// SkykeyGET contains a base64 encoded Skykey.
	SkykeyGET struct {
		Skykey string `json:"skykey"` // base64 encoded Skykey
		Name   string `json:"name"`
		ID     string `json:"id"`   // base64 encoded Skykey ID
		Type   string `json:"type"` // human-readable Skykey Type
	}
	// SkykeysGET contains a slice of Skykeys.
	SkykeysGET struct {
		Skykeys []SkykeyGET `json:"skykeys"`
	}

	// archiveFunc is a function that serves subfiles from src to dst and
	// archives them using a certain algorithm.
	archiveFunc func(dst io.Writer, src io.Reader, files []modules.SkyfileSubfileMetadata) error
)

// skynetBlacklistHandlerGET handles the API call to get the list of blacklisted
// skylinks.
func (api *API) skynetBlacklistHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get the Blacklist
	blacklist, err := api.renter.Blacklist()
	if err != nil {
		WriteError(w, Error{"unable to get the blacklist: " + err.Error()}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, SkynetBlacklistGET{
		Blacklist: blacklist,
	})
}

// skynetBlacklistHandlerPOST handles the API call to blacklist certain skylinks.
func (api *API) skynetBlacklistHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters
	var params SkynetBlacklistPOST
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Check for nil input
	if len(append(params.Add, params.Remove...)) == 0 {
		WriteError(w, Error{"no skylinks submitted"}, http.StatusBadRequest)
		return
	}

	// Convert to Skylinks
	addSkylinks := make([]modules.Skylink, len(params.Add))
	for i, addStr := range params.Add {
		var skylink modules.Skylink
		err := skylink.LoadString(addStr)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("error parsing skylink: %v", err)}, http.StatusBadRequest)
			return
		}
		addSkylinks[i] = skylink
	}
	removeSkylinks := make([]modules.Skylink, len(params.Remove))
	for i, removeStr := range params.Remove {
		var skylink modules.Skylink
		err := skylink.LoadString(removeStr)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("error parsing skylink: %v", err)}, http.StatusBadRequest)
			return
		}
		removeSkylinks[i] = skylink
	}

	// Update the Skynet Blacklist
	err = api.renter.UpdateSkynetBlacklist(addSkylinks, removeSkylinks)
	if err != nil {
		WriteError(w, Error{"unable to update the skynet blacklist: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteSuccess(w)
}

// skynetPortalsHandlerGET handles the API call to get the list of known skynet
// portals.
func (api *API) skynetPortalsHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get the list of portals.
	portals, err := api.renter.Portals()
	if err != nil {
		WriteError(w, Error{"unable to get the portals list: " + err.Error()}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, SkynetPortalsGET{
		Portals: portals,
	})
}

// skynetPortalsHandlerPOST handles the API call to add and remove portals from
// the list of known skynet portals.
func (api *API) skynetPortalsHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters.
	var params SkynetPortalsPOST
	err := json.NewDecoder(req.Body).Decode(&params)
	if err != nil {
		WriteError(w, Error{"invalid parameters: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Update the list of known skynet portals.
	err = api.renter.UpdateSkynetPortals(params.Add, params.Remove)
	if err != nil {
		// If validation fails, return a bad request status.
		errStatus := http.StatusInternalServerError
		if strings.Contains(err.Error(), skynetportals.ErrSkynetPortalsValidation.Error()) {
			errStatus = http.StatusBadRequest
		}
		WriteError(w, Error{"unable to update the list of known skynet portals: " + err.Error()}, errStatus)
		return
	}

	WriteSuccess(w)
}

// skynetSkylinkHandlerGET accepts a skylink as input and will stream the data
// from the skylink out of the response body as output.
func (api *API) skynetSkylinkHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Start the timer for the performance measurement.
	startTime := time.Now()
	isErr := true
	defer func() {
		if isErr {
			skynetPerformanceStats.TimeToFirstByte.AddRequest(0)
		}
	}()

	skylink, skylinkStringNoQuery, path, err := splitSkylinkString(ps.ByName("skylink"))
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("error parsing skylink: %v", err)}, http.StatusBadRequest)
		return
	}

	// Parse the query params.
	queryForm, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		WriteError(w, Error{"failed to parse query params"}, http.StatusBadRequest)
		return
	}

	// Parse the querystring.
	var attachment bool
	attachmentStr := queryForm.Get("attachment")
	if attachmentStr != "" {
		attachment, err = strconv.ParseBool(attachmentStr)
		if err != nil {
			WriteError(w, Error{"unable to parse 'attachment' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse the format.
	format := modules.SkyfileFormat(strings.ToLower(queryForm.Get("format")))
	switch format {
	case modules.SkyfileFormatNotSpecified:
	case modules.SkyfileFormatTar:
	case modules.SkyfileFormatTarGz:
	case modules.SkyfileFormatConcat:
	case modules.SkyfileFormatZip:
	default:
		WriteError(w, Error{"unable to parse 'format' parameter, allowed values are: 'concat', 'tar', 'targz' and 'zip'"}, http.StatusBadRequest)
		return
	}

	// Parse the timeout.
	timeout := DefaultSkynetRequestTimeout
	timeoutStr := queryForm.Get("timeout")
	if timeoutStr != "" {
		timeoutInt, err := strconv.Atoi(timeoutStr)
		if err != nil {
			WriteError(w, Error{"unable to parse 'timeout' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}

		if timeoutInt > MaxSkynetRequestTimeout {
			WriteError(w, Error{fmt.Sprintf("'timeout' parameter too high, maximum allowed timeout is %ds", MaxSkynetRequestTimeout)}, http.StatusBadRequest)
			return
		}
		timeout = time.Duration(timeoutInt) * time.Second
	}

	// Fetch the skyfile's metadata and a streamer to download the file
	metadata, streamer, err := api.renter.DownloadSkylink(skylink, timeout)
	if errors.Contains(err, renter.ErrRootNotFound) {
		WriteError(w, Error{fmt.Sprintf("failed to fetch skylink: %v", err)}, http.StatusNotFound)
		return
	}
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to fetch skylink: %v", err)}, http.StatusInternalServerError)
		return
	}
	defer streamer.Close()

	if metadata.DefaultPath != "" && len(metadata.Subfiles) == 0 {
		WriteError(w, Error{"defaultpath is not allowed on single files, please specify a format"}, http.StatusBadRequest)
		return
	}
	if metadata.DefaultPath != "" && metadata.DisableDefaultPath && format == modules.SkyfileFormatNotSpecified {
		WriteError(w, Error{"invalid defaultpath state - both defaultpath and disabledefaultpath are set, please specify a format"}, http.StatusBadRequest)
		return
	}
	defaultPath := metadata.DefaultPath
	if metadata.DefaultPath == "" && !metadata.DisableDefaultPath {
		if len(metadata.Subfiles) == 1 {
			// If `defaultpath` and `disabledefaultpath` are not set and the
			// skyfile has a single subfile we automatically default to it.
			for filename := range metadata.Subfiles {
				defaultPath = modules.EnsurePrefix(filename, "/")
				break
			}
		} else {
			prefixedDefaultSkynetPath := modules.EnsurePrefix(DefaultSkynetDefaultPath, "/")
			for filename := range metadata.Subfiles {
				if modules.EnsurePrefix(filename, "/") == prefixedDefaultSkynetPath {
					defaultPath = prefixedDefaultSkynetPath
					break
				}
			}
		}
	}

	var isSubfile bool
	responseContentType := metadata.ContentType()

	// Serve the contents of the file at the default path if one is set. Note
	// that we return the metadata for the entire Skylink when we serve the
	// contents of the file at the default path.
	// We only use the default path when the user requests the root path because
	// we want to enable people to access individual subfile without forcing
	// them to download the entire skyfile.
	if path == "/" && defaultPath != "" && format == modules.SkyfileFormatNotSpecified {
		if strings.Count(defaultPath, "/") > 1 && len(metadata.Subfiles) > 1 {
			WriteError(w, Error{fmt.Sprintf("skyfile has invalid default path (%s) which refers to a non-root file, please specify a format", defaultPath)}, http.StatusBadRequest)
			return
		}
		isSkapp := strings.HasSuffix(defaultPath, ".html") || strings.HasSuffix(defaultPath, ".htm")
		// If we don't have a subPath and the skylink doesn't end with a trailing
		// slash we need to redirect in order to add the trailing slash.
		// This is only true for skapps - they need it in order to properly work
		// with relative paths.
		// We also don't need to redirect if this is a HEAD request or if it's a
		// download as attachment.
		if isSkapp && !attachment && req.Method == http.MethodGet && !strings.HasSuffix(skylinkStringNoQuery, "/") {
			w.Header().Set("Location", skylinkStringNoQuery+"/"+req.URL.RawQuery)
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		// Only serve the default path if it points to an HTML file (this is a
		// skapp) or it's the only file in the skyfile.
		if !isSkapp && len(metadata.Subfiles) > 1 {
			WriteError(w, Error{fmt.Sprintf("skyfile has invalid default path (%s), please specify a format", defaultPath)}, http.StatusBadRequest)
			return
		}
		metaForPath, isFile, offset, size := metadata.ForPath(defaultPath)
		if len(metaForPath.Subfiles) == 0 {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for default path: %v", path)}, http.StatusNotFound)
			return
		}
		if !isFile {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for default path: %v, please specify a specific path or a format in order to download the content", defaultPath)}, http.StatusNotFound)
			return
		}
		streamer, err = NewLimitStreamer(streamer, offset, size)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for default path: %v, could not create limit streamer", path)}, http.StatusInternalServerError)
			return
		}
		isSubfile = isFile
		responseContentType = metaForPath.ContentType()
	}

	// Serve the contents of the skyfile at path if one is set
	if path != "/" {
		metadataForPath, file, offset, size := metadata.ForPath(path)
		if len(metadataForPath.Subfiles) == 0 {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for path: %v", path)}, http.StatusNotFound)
			return
		}
		streamer, err = NewLimitStreamer(streamer, offset, size)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for path: %v, could not create limit streamer", path)}, http.StatusInternalServerError)
			return
		}

		metadata = metadataForPath
		isSubfile = file
	}

	// If we are serving more than one file, and the format is not
	// specified, default to downloading it as a zip archive.
	if !isSubfile && metadata.IsDirectory() && format == modules.SkyfileFormatNotSpecified {
		format = modules.SkyfileFormatZip
	}

	// Encode the metadata
	encMetadata, err := json.Marshal(metadata)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to write skylink metadata: %v", err)}, http.StatusInternalServerError)
		return
	}

	// Metadata has been parsed successfully, stop the time here for TTFB.
	// Metadata was fetched from Skynet itself.
	skynetPerformanceStatsMu.Lock()
	skynetPerformanceStats.TimeToFirstByte.AddRequest(time.Since(startTime))
	skynetPerformanceStatsMu.Unlock()

	// No more errors, defer a function to record the total performance time.
	isErr = false
	defer func() {
		skynetPerformanceStatsMu.Lock()
		defer skynetPerformanceStatsMu.Unlock()

		_, fetchSize, err := skylink.OffsetAndFetchSize()
		if err != nil {
			return
		}
		if fetchSize <= 64e3 {
			skynetPerformanceStats.Download64KB.AddRequest(time.Since(startTime))
			return
		}
		if fetchSize <= 1e6 {
			skynetPerformanceStats.Download1MB.AddRequest(time.Since(startTime))
			return
		}
		if fetchSize <= 4e6 {
			skynetPerformanceStats.Download4MB.AddRequest(time.Since(startTime))
			return
		}
		skynetPerformanceStats.DownloadLarge.AddRequest(time.Since(startTime))
	}()

	// Set an appropriate Content-Disposition header
	var cdh string
	filename := filepath.Base(metadata.Filename)
	if format.IsArchive() {
		cdh = fmt.Sprintf("attachment; filename=%s", strconv.Quote(filename+format.Extension()))
	} else if attachment {
		cdh = fmt.Sprintf("attachment; filename=%s", strconv.Quote(filename))
	} else {
		cdh = fmt.Sprintf("inline; filename=%s", strconv.Quote(filename))
	}
	w.Header().Set("Content-Disposition", cdh)

	// If requested, serve the content as a tar archive, compressed tar
	// archive or zip archive.
	if format == modules.SkyfileFormatTar {
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Skynet-File-Metadata", string(encMetadata))
		err = serveArchive(w, streamer, metadata, serveTar)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to serve skyfile as tar archive: %v", err)}, http.StatusInternalServerError)
		}
		return
	}
	if format == modules.SkyfileFormatTarGz {
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Skynet-File-Metadata", string(encMetadata))
		gzw := gzip.NewWriter(w)
		err = serveArchive(gzw, streamer, metadata, serveTar)
		err = errors.Compose(err, gzw.Close())
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to serve skyfile as tar gz archive: %v", err)}, http.StatusInternalServerError)
		}
		return
	}
	if format == modules.SkyfileFormatZip {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Skynet-File-Metadata", string(encMetadata))
		err = serveArchive(w, streamer, metadata, serveZip)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to serve skyfile as zip archive: %v", err)}, http.StatusInternalServerError)
		}
		return
	}

	// Only set the Content-Type header when the metadata defines one, if we
	// were to set the header to an empty string, it would prevent the http
	// library from sniffing the file's content type.
	if responseContentType != "" {
		w.Header().Set("Content-Type", responseContentType)
	}
	w.Header().Set("Skynet-File-Metadata", string(encMetadata))

	http.ServeContent(w, req, metadata.Filename, time.Time{}, streamer)
}

// splitSkylinkString splits a skylink string into its component - a skylink,
// a string representation of the skylink with the query parameters stripped,
// and a path.
func splitSkylinkString(s string) (skylink modules.Skylink, skylinkStringNoQuery, path string, err error) {
	s = strings.TrimPrefix(s, "/")
	// Parse out optional path to a subfile
	path = "/" // default to root
	splits := strings.SplitN(s, "?", 2)
	skylinkStringNoQuery = splits[0]
	splits = strings.SplitN(skylinkStringNoQuery, "/", 2)
	// Check if a path is passed.
	if len(splits) > 1 && len(splits[1]) > 0 {
		path = modules.EnsurePrefix(splits[1], "/")
	}
	// Parse skylink
	err = skylink.LoadString(s)
	return
}

// skynetSkylinkPinHandlerPOST will pin a skylink to this Sia node, ensuring
// uptime even if the original uploader stops paying for the file.
func (api *API) skynetSkylinkPinHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse the query params.
	queryForm, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		WriteError(w, Error{"failed to parse query params"}, http.StatusBadRequest)
		return
	}

	strLink := ps.ByName("skylink")
	var skylink modules.Skylink
	err = skylink.LoadString(strLink)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("error parsing skylink: %v", err)}, http.StatusBadRequest)
		return
	}

	// Parse whether the siapath should be from root or from the skynet folder.
	var root bool
	rootStr := queryForm.Get("root")
	if rootStr != "" {
		root, err = strconv.ParseBool(rootStr)
		if err != nil {
			WriteError(w, Error{"unable to parse 'root' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse out the intended siapath.
	var siaPath modules.SiaPath
	siaPathStr := queryForm.Get("siapath")
	if root {
		siaPath, err = modules.NewSiaPath(siaPathStr)
	} else {
		siaPath, err = modules.SkynetFolder.Join(siaPathStr)
	}
	if err != nil {
		WriteError(w, Error{"invalid siapath provided: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Parse the timeout.
	timeout, err := parseTimeout(queryForm)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// Check whether force upload is allowed. Skynet portals might disallow
	// passing the force flag, if they want to they can set overrule the force
	// flag by passing in the 'Skynet-Disable-Force' header
	allowForce := true
	strDisableForce := req.Header.Get("Skynet-Disable-Force")
	if strDisableForce != "" {
		disableForce, err := strconv.ParseBool(strDisableForce)
		if err != nil {
			WriteError(w, Error{"unable to parse 'Skynet-Disable-Force' header: " + err.Error()}, http.StatusBadRequest)
			return
		}
		allowForce = !disableForce
	}

	// Check whether existing file should be overwritten
	force := false
	if strForce := queryForm.Get("force"); strForce != "" {
		force, err = strconv.ParseBool(strForce)
		if err != nil {
			WriteError(w, Error{"unable to parse 'force' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Notify the caller force has been disabled
	if !allowForce && force {
		WriteError(w, Error{"'force' has been disabled on this node: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Check whether the redundancy has been set.
	redundancy := uint8(0)
	if rStr := queryForm.Get("basechunkredundancy"); rStr != "" {
		if _, err := fmt.Sscan(rStr, &redundancy); err != nil {
			WriteError(w, Error{"unable to parse basechunkredundancy: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Create the upload parameters. Notably, the fanout redundancy, the file
	// metadata and the filename are not included. Changing those would change
	// the skylink, which is not the goal.
	lup := modules.SkyfileUploadParameters{
		SiaPath:             siaPath,
		Force:               force,
		BaseChunkRedundancy: redundancy,
	}

	err = api.renter.PinSkylink(skylink, lup, timeout)
	if errors.Contains(err, renter.ErrRootNotFound) {
		WriteError(w, Error{fmt.Sprintf("Failed to pin file to Skynet: %v", err)}, http.StatusNotFound)
		return
	} else if err != nil {
		WriteError(w, Error{fmt.Sprintf("Failed to pin file to Skynet: %v", err)}, http.StatusInternalServerError)
		return
	}

	WriteSuccess(w)
}

// skynetSkyfileHandlerPOST is a dual purpose endpoint. If the 'convertpath'
// field is set, this endpoint will create a skyfile using an existing siafile.
// The original siafile and the skyfile will both need to be kept in order for
// the file to remain available on Skynet. If the 'convertpath' field is not
// set, this is essentially an upload streaming endpoint for Skynet which
// returns a skylink.
func (api *API) skynetSkyfileHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Start the timer for the performance measurement.
	startTime := time.Now()

	// Parse the query params.
	queryForm, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		WriteError(w, Error{"failed to parse query params"}, http.StatusBadRequest)
		return
	}

	// Parse whether the upload should be performed as a dry-run.
	var dryRun bool
	dryRunStr := queryForm.Get("dryrun")
	if dryRunStr != "" {
		dryRun, err = strconv.ParseBool(dryRunStr)
		if err != nil {
			WriteError(w, Error{"unable to parse 'dryrun' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse whether the siapath should be from root or from the skynet folder.
	var root bool
	rootStr := queryForm.Get("root")
	if rootStr != "" {
		root, err = strconv.ParseBool(rootStr)
		if err != nil {
			WriteError(w, Error{"unable to parse 'root' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse out the intended siapath.
	var siaPath modules.SiaPath
	siaPathStr := ps.ByName("siapath")
	if root {
		siaPath, err = modules.NewSiaPath(siaPathStr)
	} else {
		siaPath, err = modules.SkynetFolder.Join(siaPathStr)
	}
	if err != nil {
		WriteError(w, Error{"invalid siapath provided: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Check whether force upload is allowed. Skynet portals might disallow
	// passing the force flag, if they want to they can set overrule the force
	// flag by passing in the 'Skynet-Disable-Force' header
	allowForce := true
	strDisableForce := req.Header.Get("Skynet-Disable-Force")
	if strDisableForce != "" {
		disableForce, err := strconv.ParseBool(strDisableForce)
		if err != nil {
			WriteError(w, Error{"unable to parse 'Skynet-Disable-Force' header: " + err.Error()}, http.StatusBadRequest)
			return
		}
		allowForce = !disableForce
	}

	// Check whether existing file should be overwritten
	force := false
	if strForce := queryForm.Get("force"); strForce != "" {
		force, err = strconv.ParseBool(strForce)
		if err != nil {
			WriteError(w, Error{"unable to parse 'force' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Notify the caller force has been disabled
	if !allowForce && force {
		WriteError(w, Error{"'force' has been disabled on this node"}, http.StatusBadRequest)
		return
	}

	// Verify the dry-run and force parameter are not combined
	if allowForce && force && dryRun {
		WriteError(w, Error{"'dryRun' and 'force' can not be combined"}, http.StatusBadRequest)
		return
	}

	// Check whether the redundancy has been set.
	redundancy := uint8(0)
	if rStr := queryForm.Get("basechunkredundancy"); rStr != "" {
		if _, err := fmt.Sscan(rStr, &redundancy); err != nil {
			WriteError(w, Error{"unable to parse basechunkredundancy: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse the filename from the query params.
	filename := queryForm.Get("filename")

	// Parse Content-Type from the request headers
	ct := req.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed parsing Content-Type header: %v", err)}, http.StatusBadRequest)
		return
	}

	// Build the upload parameters
	lup := modules.SkyfileUploadParameters{
		SiaPath:             siaPath,
		DryRun:              dryRun,
		Force:               force,
		BaseChunkRedundancy: redundancy,
	}

	// Build the Skyfile metadata from the request
	if isMultipartRequest(mediaType) {
		subfiles, reader, err := skyfileParseMultiPartRequest(req)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed parsing multipart request: %v", err)}, http.StatusBadRequest)
			return
		}

		// Use the filename of the first subfile if it's not passed as query
		// string parameter and there's only one subfile.
		if filename == "" && len(subfiles) == 1 {
			for _, sf := range subfiles {
				filename = sf.Filename
				break
			}
		}

		// Get the default path.
		defaultPath, disableDefPath, err := defaultPath(queryForm, subfiles)
		if err != nil {
			WriteError(w, Error{err.Error()}, http.StatusBadRequest)
			return
		}

		lup.Reader = reader
		lup.FileMetadata = modules.SkyfileMetadata{
			Filename:           filename,
			Subfiles:           subfiles,
			DefaultPath:        defaultPath,
			DisableDefaultPath: disableDefPath,
		}
	} else {
		// Parse out the filemode
		modeStr := queryForm.Get("mode")
		var mode os.FileMode
		if modeStr != "" {
			_, err := fmt.Sscanf(modeStr, "%o", &mode)
			if err != nil {
				WriteError(w, Error{"unable to parse mode: " + err.Error()}, http.StatusBadRequest)
				return
			}
		}

		lup.Reader = req.Body
		lup.FileMetadata = modules.SkyfileMetadata{
			Mode:     mode,
			Filename: filename,
		}
	}

	// Grab the skykey specified.
	skykeyName := queryForm.Get("skykeyname")
	skykeyID := queryForm.Get("skykeyid")
	if skykeyName != "" && skykeyID != "" {
		WriteError(w, Error{"Can only use either skykeyname or skykeyid flag, not both."}, http.StatusBadRequest)
		return
	}

	if skykeyName != "" {
		lup.SkykeyName = skykeyName
	}
	if skykeyID != "" {
		var ID skykey.SkykeyID
		err = ID.FromString(skykeyID)
		if err != nil {
			WriteError(w, Error{"Unable to parse skykey ID"}, http.StatusBadRequest)
			return
		}
		lup.SkykeyID = ID
	}

	// Check for a convertpath input
	convertPathStr := queryForm.Get("convertpath")
	if convertPathStr != "" && lup.FileMetadata.Filename != "" {
		WriteError(w, Error{"cannot set both a convertpath and a filename"}, http.StatusBadRequest)
		return
	}

	// Check whether this is a streaming upload or a siafile conversion. If no
	// convert path is provided, assume that the req.Body will be used as a
	// streaming upload.
	if convertPathStr == "" {
		// Ensure we have a valid filename.
		if err = modules.ValidatePathString(lup.FileMetadata.Filename, false); err != nil {
			WriteError(w, Error{fmt.Sprintf("invalid filename provided: %v", err)}, http.StatusBadRequest)
			return
		}

		// Check filenames of subfiles.
		if lup.FileMetadata.Subfiles != nil {
			for subfile, metadata := range lup.FileMetadata.Subfiles {
				if subfile != metadata.Filename {
					WriteError(w, Error{"subfile name did not match metadata filename"}, http.StatusBadRequest)
					return
				}
				if err = modules.ValidatePathString(subfile, false); err != nil {
					WriteError(w, Error{fmt.Sprintf("invalid filename provided: %v", err)}, http.StatusBadRequest)
					return
				}
			}
		}

		skylink, err := api.renter.UploadSkyfile(lup)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to upload file to Skynet: %v", err)}, http.StatusBadRequest)
			return
		}

		// Determine whether the file is large or not, and update the
		// appropriate bucket.
		file, err := api.renter.File(lup.SiaPath)
		if err == nil && file.Filesize <= 4e6 {
			skynetPerformanceStatsMu.Lock()
			skynetPerformanceStats.Upload4MB.AddRequest(time.Since(startTime))
			skynetPerformanceStatsMu.Unlock()
		} else if err == nil {
			skynetPerformanceStatsMu.Lock()
			skynetPerformanceStats.UploadLarge.AddRequest(time.Since(startTime))
			skynetPerformanceStatsMu.Unlock()
		}

		WriteJSON(w, SkynetSkyfileHandlerPOST{
			Skylink:    skylink.String(),
			MerkleRoot: skylink.MerkleRoot(),
			Bitfield:   skylink.Bitfield(),
		})
		return
	}

	// There is a convert path.
	convertPath, err := modules.NewSiaPath(convertPathStr)
	if err != nil {
		WriteError(w, Error{"invalid convertpath provided: " + err.Error()}, http.StatusBadRequest)
		return
	}
	convertPath, err = rebaseInputSiaPath(convertPath)
	if err != nil {
		WriteError(w, Error{"invalid convertpath provided - can't rebase: " + err.Error()}, http.StatusBadRequest)
		return
	}
	skylink, err := api.renter.CreateSkylinkFromSiafile(lup, convertPath)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to convert siafile to skyfile: %v", err)}, http.StatusBadRequest)
		return
	}

	// No more errors, add metrics for the upload time. A convert is a 4MB
	// upload.
	skynetPerformanceStatsMu.Lock()
	skynetPerformanceStats.Upload4MB.AddRequest(time.Since(startTime))
	skynetPerformanceStatsMu.Unlock()

	WriteJSON(w, SkynetSkyfileHandlerPOST{
		Skylink:    skylink.String(),
		MerkleRoot: skylink.MerkleRoot(),
		Bitfield:   skylink.Bitfield(),
	})
}

// skynetStatsHandlerGET responds with a JSON with statistical data about
// skynet, e.g. number of files uploaded, total size, etc.
func (api *API) skynetStatsHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	files, err := api.renter.FileList(modules.SkynetFolder, true, true)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to get the list of files: %v", err)}, http.StatusInternalServerError)
		return
	}

	// calculate upload statistics
	stats := SkynetStats{}
	for _, f := range files {
		// do not double-count large files by counting both the header file and
		// the extended file
		if !strings.HasSuffix(f.Name(), renter.ExtendedSuffix) {
			stats.NumFiles++
		}
		stats.TotalSize += f.Filesize
	}

	// get version
	version := build.Version
	if build.ReleaseTag != "" {
		version += "-" + build.ReleaseTag
	}

	// Grab a copy of the performance stats.
	skynetPerformanceStatsMu.Lock()
	skynetPerformanceStats.Update()
	perfStats := skynetPerformanceStats.Copy()
	skynetPerformanceStatsMu.Unlock()

	// Grab the siad uptime
	uptime := time.Since(api.StartTime()).Seconds()

	WriteJSON(w, SkynetStatsGET{
		PerformanceStats: perfStats,

		Uptime:      int64(uptime),
		UploadStats: stats,
		VersionInfo: SkynetVersion{
			Version:     version,
			GitRevision: build.GitRevision,
		},
	})
}

// serveArchive serves skyfiles as an archive by reading them from r and writing
// the archive to dst using the given archiveFunc.
func serveArchive(dst io.Writer, src io.ReadSeeker, md modules.SkyfileMetadata, archiveFunc archiveFunc) error {
	// Get the files to archive.
	var files []modules.SkyfileSubfileMetadata
	for _, file := range md.Subfiles {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Offset < files[j].Offset
	})
	// If there are no files, it's a single file download. Manually construct a
	// SkyfileSubfileMetadata from the SkyfileMetadata.
	if len(files) == 0 {
		length := md.Length
		if md.Length == 0 {
			// v150Compat a missing length is fine for legacy links but new
			// links should always have the length set.
			if build.Release == "testing" {
				build.Critical("SkyfileMetadata is missing length")
			}
			// Fetch the length of the file by seeking to the end and then back to
			// the start.
			seekLen, err := src.Seek(0, io.SeekEnd)
			if err != nil {
				return errors.AddContext(err, "serveArchive: failed to seek to end of skyfile")
			}
			_, err = src.Seek(0, io.SeekStart)
			if err != nil {
				return errors.AddContext(err, "serveArchive: failed to seek to start of skyfile")
			}
			length = uint64(seekLen)
		}
		// Construct the SkyfileSubfileMetadata.
		files = append(files, modules.SkyfileSubfileMetadata{
			FileMode: md.Mode,
			Filename: md.Filename,
			Offset:   0,
			Len:      length,
		})
	}
	return archiveFunc(dst, src, files)
}

// serveTar is an archiveFunc that implements serving the files from src to dst
// as a tar.
func serveTar(dst io.Writer, src io.Reader, files []modules.SkyfileSubfileMetadata) error {
	tw := tar.NewWriter(dst)
	for _, file := range files {
		// Create header.
		header, err := tar.FileInfoHeader(file, file.Name())
		if err != nil {
			return err
		}
		// Modify name to match path within skyfile.
		header.Name = file.Filename
		// Write header.
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		// Write file content.
		if _, err := io.CopyN(tw, src, header.Size); err != nil {
			return err
		}
	}
	return tw.Close()
}

// serveZip is an archiveFunc that implements serving the files from src to dst
// as a zip.
func serveZip(dst io.Writer, src io.Reader, files []modules.SkyfileSubfileMetadata) error {
	zw := zip.NewWriter(dst)
	for _, file := range files {
		f, err := zw.Create(file.Filename)
		if err != nil {
			return errors.AddContext(err, "serveZip: failed to add the file to the zip")
		}

		// Write file content.
		_, err = io.CopyN(f, src, int64(file.Len))
		if err != nil {
			return errors.AddContext(err, "serveZip: failed to write file contents to the zip")
		}
	}
	return zw.Close()
}

// parseTimeout tries to parse the timeout from the query string and validate
// it. If not present, it will default to DefaultSkynetRequestTimeout.
func parseTimeout(queryForm url.Values) (time.Duration, error) {
	timeoutStr := queryForm.Get("timeout")
	if timeoutStr == "" {
		return DefaultSkynetRequestTimeout, nil
	}

	timeoutInt, err := strconv.Atoi(timeoutStr)
	if err != nil {
		return 0, errors.AddContext(err, "unable to parse 'timeout'")
	}
	if timeoutInt > MaxSkynetRequestTimeout {
		return 0, errors.AddContext(err, fmt.Sprintf("'timeout' parameter too high, maximum allowed timeout is %ds", MaxSkynetRequestTimeout))
	}
	return time.Duration(timeoutInt) * time.Second, nil
}

// skyfileParseMultiPartRequest parses the given request and returns the
// subfiles found in the multipart request body, alongside with an io.Reader
// containing all of the files.
func skyfileParseMultiPartRequest(req *http.Request) (modules.SkyfileSubfiles, io.Reader, error) {
	subfiles := make(modules.SkyfileSubfiles)

	// Parse the multipart form
	err := req.ParseMultipartForm(32 << 20) // 32MB max memory
	if err != nil {
		return subfiles, nil, errors.AddContext(err, "failed parsing multipart form")
	}

	// Parse out all of the multipart file headers
	mpfHeaders := append(req.MultipartForm.File["file"], req.MultipartForm.File["files[]"]...)
	if len(mpfHeaders) == 0 {
		return subfiles, nil, errors.New("could not find multipart file")
	}

	// If there are multiple, treat the entire upload as one with all separate
	// files being subfiles. This is used for uploading a directory to Skynet.
	readers := make([]io.Reader, len(mpfHeaders))
	var offset uint64
	for i, fh := range mpfHeaders {
		f, err := fh.Open()
		if err != nil {
			return subfiles, nil, errors.AddContext(err, "could not open multipart file")
		}
		readers[i] = f

		// parse mode from multipart header
		modeStr := fh.Header.Get("Mode")
		var mode os.FileMode
		if modeStr != "" {
			_, err := fmt.Sscanf(modeStr, "%o", &mode)
			if err != nil {
				return subfiles, nil, errors.AddContext(err, "failed to parse file mode")
			}
		}

		// parse filename from multipart
		filename := fh.Filename
		if filename == "" {
			return subfiles, nil, errors.New("no filename provided")
		}

		// parse content type from multipart header
		contentType := fh.Header.Get("Content-Type")
		subfiles[fh.Filename] = modules.SkyfileSubfileMetadata{
			FileMode:    mode,
			Filename:    filename,
			ContentType: contentType,
			Offset:      offset,
			Len:         uint64(fh.Size),
		}
		offset += uint64(fh.Size)
	}

	return subfiles, io.MultiReader(readers...), nil
}

// skykeyHandlerGET handles the API call to get a Skykey and its ID using its
// name or ID.
func (api *API) skykeyHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse Skykey id and name.
	name := req.FormValue("name")
	idString := req.FormValue("id")

	if idString == "" && name == "" {
		WriteError(w, Error{"you must specify the name or ID of the skykey"}, http.StatusInternalServerError)
		return
	}
	if idString != "" && name != "" {
		WriteError(w, Error{"you must specify either the name or ID of the skykey, not both"}, http.StatusInternalServerError)
		return
	}

	var sk skykey.Skykey
	var err error
	if name != "" {
		sk, err = api.renter.SkykeyByName(name)
	} else if idString != "" {
		var id skykey.SkykeyID
		err = id.FromString(idString)
		if err != nil {
			WriteError(w, Error{"failed to decode ID string: "}, http.StatusInternalServerError)
			return
		}
		sk, err = api.renter.SkykeyByID(id)
	}
	if err != nil {
		WriteError(w, Error{"failed to retrieve skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	skString, err := sk.ToString()
	if err != nil {
		WriteError(w, Error{"failed to decode skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, SkykeyGET{
		Skykey: skString,
		Name:   sk.Name,
		ID:     sk.ID().ToString(),
		Type:   sk.Type.ToString(),
	})
}

// skykeyDeleteHandlerGET handles the API call to delete a Skykey using its name
// or ID.
func (api *API) skykeyDeleteHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse Skykey id and name.
	name := req.FormValue("name")
	idString := req.FormValue("id")

	if idString == "" && name == "" {
		WriteError(w, Error{"you must specify the name or ID of the skykey"}, http.StatusBadRequest)
		return
	}
	if idString != "" && name != "" {
		WriteError(w, Error{"you must specify either the name or ID of the skykey, not both"}, http.StatusBadRequest)
		return
	}

	var err error
	if name != "" {
		err = api.renter.DeleteSkykeyByName(name)
	} else if idString != "" {
		var id skykey.SkykeyID
		err = id.FromString(idString)
		if err != nil {
			WriteError(w, Error{"Invalid skykey ID: " + err.Error()}, http.StatusBadRequest)
			return
		}
		err = api.renter.DeleteSkykeyByID(id)
	}

	if err != nil {
		WriteError(w, Error{"failed to delete skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteSuccess(w)
}

// skykeyCreateKeyHandlerPost handles the API call to create a skykey using the renter's
// skykey manager.
func (api *API) skykeyCreateKeyHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse skykey name and type
	name := req.FormValue("name")
	skykeyTypeString := req.FormValue("type")

	if name == "" {
		WriteError(w, Error{"you must specify the name the skykey"}, http.StatusInternalServerError)
		return
	}

	if skykeyTypeString == "" {
		WriteError(w, Error{"you must specify the type of the skykey"}, http.StatusInternalServerError)
		return
	}

	var skykeyType skykey.SkykeyType
	err := skykeyType.FromString(skykeyTypeString)
	if err != nil {
		WriteError(w, Error{"failed to decode skykey type: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	sk, err := api.renter.CreateSkykey(name, skykeyType)
	if err != nil {
		WriteError(w, Error{"failed to create skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	keyString, err := sk.ToString()
	if err != nil {
		WriteError(w, Error{"failed to decode skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, SkykeyGET{
		Skykey: keyString,
		Name:   name,
		ID:     sk.ID().ToString(),
		Type:   skykeyTypeString,
	})
}

// skykeyAddKeyHandlerPost handles the API call to add a skykey to the renter's
// skykey manager.
func (api *API) skykeyAddKeyHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse skykey.
	skString := req.FormValue("skykey")
	if skString == "" {
		WriteError(w, Error{"you must specify the name of the skykey"}, http.StatusInternalServerError)
		return
	}

	var sk skykey.Skykey
	err := sk.FromString(skString)
	if err != nil {
		WriteError(w, Error{"failed to decode skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	err = api.renter.AddSkykey(sk)
	if err != nil {
		WriteError(w, Error{"failed to add skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteSuccess(w)
}

// skykeysHandlerGET handles the API call to get all of the renter's skykeys.
func (api *API) skykeysHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	skykeys, err := api.renter.Skykeys()
	if err != nil {
		WriteError(w, Error{"Unable to get skykeys: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	res := SkykeysGET{
		Skykeys: make([]SkykeyGET, len(skykeys)),
	}
	for i, sk := range skykeys {
		skStr, err := sk.ToString()
		if err != nil {
			WriteError(w, Error{"failed to write skykey string: " + err.Error()}, http.StatusInternalServerError)
			return
		}
		res.Skykeys[i] = SkykeyGET{
			Skykey: skStr,
			Name:   sk.Name,
			ID:     sk.ID().ToString(),
			Type:   sk.Type.ToString(),
		}
	}
	WriteJSON(w, res)
}

// defaultPath extracts the defaultPath from the request or returns a default.
// It will never return a directory because `subfiles` contains only files.
func defaultPath(queryForm url.Values, subfiles modules.SkyfileSubfiles) (defaultPath string, disableDefaultPath bool, err error) {
	defer func() {
		// Ensure the defaultPath always has a leading slash.
		if defaultPath != "" {
			defaultPath = modules.EnsurePrefix(defaultPath, "/")
		}
	}()
	// Parse the "disabledefaultpath" param.
	disableDefaultPathStr := queryForm.Get(modules.SkyfileDisableDefaultPathParamName)
	if disableDefaultPathStr != "" {
		disableDefaultPath, err = strconv.ParseBool(disableDefaultPathStr)
		if err != nil {
			return "", false, fmt.Errorf("unable to parse 'disabledefaultpath' parameter: " + err.Error())
		}
	}
	// Parse the "defaultPath" param.
	defaultPath = queryForm.Get(modules.SkyfileDefaultPathParamName)
	if len(subfiles) == 0 && (disableDefaultPath || defaultPath != "") {
		return "", false, errors.AddContext(ErrInvalidDefaultPath, "DefaultPath and DisableDefaultPath are not applicable to skyfiles without subfiles")
	}
	if disableDefaultPath && defaultPath != "" {
		return "", false, errors.AddContext(ErrInvalidDefaultPath, "DefaultPath and DisableDefaultPath are mutually exclusive and cannot be set together")
	}
	if disableDefaultPath {
		return "", true, nil
	}
	if defaultPath == "" {
		return "", false, nil
	}
	// Check if the defaultPath exists. Omit the leading slash because
	// the filenames in `subfiles` won't have it.
	if _, exists := subfiles[strings.TrimPrefix(defaultPath, "/")]; !exists {
		return "", false, errors.AddContext(ErrInvalidDefaultPath, fmt.Sprintf("no such path: %s", defaultPath))
	}
	// Ensure that the defaultPath is an HTML file.
	if !strings.HasSuffix(defaultPath, ".html") && !strings.HasSuffix(defaultPath, ".htm") {
		return "", false, errors.AddContext(ErrInvalidDefaultPath, "DefaultPath must point to an HTML file")
	}
	defaultPath = modules.EnsurePrefix(defaultPath, "/")
	// Do not allow default path to point to files which are not in the root
	// directory of the skyfile.
	if strings.Count(defaultPath, "/") > 1 {
		return "", false, errors.AddContext(ErrInvalidDefaultPath, "DefaultPath must point to a file in the root directory of the skyfile")
	}
	return defaultPath, false, nil
}

// isMultipartRequest is a helper method that checks if the given media type
// matches that of a multipart form.
func isMultipartRequest(mediaType string) bool {
	return strings.HasPrefix(mediaType, "multipart/form-data")
}
