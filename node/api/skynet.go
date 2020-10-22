package api

import (
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
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
	"gitlab.com/NebulousLabs/Sia/types"
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

	// SkynetBlocklistGET contains the information queried for the
	// /skynet/blocklist GET endpoint
	//
	// NOTE: With v1.5.0 the return value for the Blocklist changed. Pre v1.5.0
	// the []crypto.Hash was a slice of MerkleRoots. Post v1.5.0 the []crypto.Hash
	// is a slice of the Hashes of the MerkleRoots
	SkynetBlocklistGET struct {
		Blacklist []crypto.Hash `json:"blacklist"` // Deprecated, kept for backwards compatibility
		Blocklist []crypto.Hash `json:"blocklist"`
	}

	// SkynetBlocklistPOST contains the information needed for the
	// /skynet/blocklist POST endpoint to be called
	SkynetBlocklistPOST struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`

		// IsHash indicates if the supplied Add and Remove strings are already
		// hashes of Skylinks
		IsHash bool `json:"ishash"`
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

	// RegistryHandlerGET is the response returned by the registryHandlerGET
	// handler.
	RegistryHandlerGET struct {
		Tweak     string `json:"tweak"`
		Data      string `json:"data"`
		Revision  uint64 `json:"revision"`
		Signature string `json:"signature"`
	}

	// RegistryHandlerRequestPOST is the expected format of the json request for
	// /skynet/registry [POST].
	RegistryHandlerRequestPOST struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		FileID    modules.FileID     `json:"fileid"`
		Revision  uint64             `json:"revision"`
		Signature crypto.Signature   `json:"signature"`
		Data      []byte             `json:"data"`
	}

	// archiveFunc is a function that serves subfiles from src to dst and
	// archives them using a certain algorithm.
	archiveFunc func(dst io.Writer, src io.Reader, files []modules.SkyfileSubfileMetadata) error
)

// skynetBaseSectorHandlerGET accepts a skylink as input and will return the
// encoded basesector.
func (api *API) skynetBaseSectorHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Start the timer for the performance measurement.
	startTime := time.Now()
	isErr := true
	defer func() {
		if isErr {
			skynetPerformanceStats.TimeToFirstByte.AddRequest(0)
		}
	}()

	// Parse the skylink from the raw URL of the request. Any special characters
	// in the raw URL are encoded, allowing us to differentiate e.g. the '?'
	// that begins query parameters from the encoded version '%3F'.
	skylink, _, _, err := parseSkylinkURL(req.URL.String(), "/skynet/basesector/")
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

	// Fetch the skyfile's  streamer to serve the basesector of the file
	streamer, err := api.renter.DownloadSkylinkBaseSector(skylink, timeout)
	if errors.Contains(err, renter.ErrRootNotFound) {
		WriteError(w, Error{fmt.Sprintf("failed to fetch skylink: %v", err)}, http.StatusNotFound)
		return
	}
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to fetch skylink: %v", err)}, http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = streamer.Close()
	}()

	// Stop the time here for TTFB.
	skynetPerformanceStatsMu.Lock()
	skynetPerformanceStats.TimeToFirstByte.AddRequest(time.Since(startTime))
	skynetPerformanceStatsMu.Unlock()
	// Defer a function to record the total performance time.
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

	// Serve the basesector
	http.ServeContent(w, req, "", time.Time{}, streamer)
	return
}

// skynetBlocklistHandlerGET handles the API call to get the list of blocked
// skylinks.
func (api *API) skynetBlocklistHandlerGET(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	// Get the Blocklist
	blocklist, err := api.renter.Blocklist()
	if err != nil {
		WriteError(w, Error{"unable to get the blocklist: " + err.Error()}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, SkynetBlocklistGET{
		Blocklist: blocklist,
	})
}

// skynetBlocklistHandlerPOST handles the API call to block certain skylinks.
func (api *API) skynetBlocklistHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse parameters
	var params SkynetBlocklistPOST
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

	// Convert to Skylinks or Hash
	addHashes := make([]crypto.Hash, len(params.Add))
	for i, addStr := range params.Add {
		var hash crypto.Hash
		// Convert Hash
		if params.IsHash {
			err := hash.LoadString(addStr)
			if err != nil {
				WriteError(w, Error{fmt.Sprintf("error parsing hash: %v", err)}, http.StatusBadRequest)
				return
			}
		} else {
			// Convert Skylink
			var skylink modules.Skylink
			err := skylink.LoadString(addStr)
			if err != nil {
				WriteError(w, Error{fmt.Sprintf("error parsing skylink: %v", err)}, http.StatusBadRequest)
				return
			}
			hash = crypto.HashObject(skylink.MerkleRoot())
		}
		addHashes[i] = hash
	}
	removeHashes := make([]crypto.Hash, len(params.Remove))
	for i, removeStr := range params.Remove {
		var hash crypto.Hash
		// Convert Hash
		if params.IsHash {
			err := hash.LoadString(removeStr)
			if err != nil {
				WriteError(w, Error{fmt.Sprintf("error parsing hash: %v", err)}, http.StatusBadRequest)
				return
			}
		} else {
			// Convert Skylink
			var skylink modules.Skylink
			err := skylink.LoadString(removeStr)
			if err != nil {
				WriteError(w, Error{fmt.Sprintf("error parsing skylink: %v", err)}, http.StatusBadRequest)
				return
			}
			hash = crypto.HashObject(skylink.MerkleRoot())
		}
		removeHashes[i] = hash
	}

	// Update the Skynet Blocklist
	err = api.renter.UpdateSkynetBlocklist(addHashes, removeHashes)
	if err != nil {
		WriteError(w, Error{"unable to update the skynet blocklist: " + err.Error()}, http.StatusInternalServerError)
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

	// Parse the skylink from the raw URL of the request. Any special characters
	// in the raw URL are encoded, allowing us to differentiate e.g. the '?'
	// that begins query parameters from the encoded version '%3F'.
	skylink, skylinkStringNoQuery, path, err := parseSkylinkURL(req.URL.String(), "/skynet/skylink/")
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

	// Parse the 'attachment' query string parameter.
	var attachment bool
	attachmentStr := queryForm.Get("attachment")
	if attachmentStr != "" {
		attachment, err = strconv.ParseBool(attachmentStr)
		if err != nil {
			WriteError(w, Error{"unable to parse 'attachment' parameter: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}

	// Parse the 'format' query string parameter.
	format := modules.SkyfileFormat(strings.ToLower(queryForm.Get("format")))
	switch format {
	case modules.SkyfileFormatNotSpecified:
	case modules.SkyfileFormatConcat:
	case modules.SkyfileFormatTar:
	case modules.SkyfileFormatTarGz:
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
	defer func() {
		_ = streamer.Close()
	}()

	// Validate Metadata
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
			location := skylinkStringNoQuery + "/"
			if req.URL.RawQuery != "" {
				location += "?" + req.URL.RawQuery
			}
			w.Header().Set("Location", location)
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

	// Set the Skylink response header
	w.Header().Set("Skynet-Skylink", skylink.String())

	// Set the ETag response header
	eTag := buildETag(skylink, req.Method, path, format)
	w.Header().Set("ETag", fmt.Sprintf("\"%v\"", eTag))

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
	// start the timer for the performance measurement.
	startTime := time.Now()

	// parse the request headers and parameters
	headers, params, err := parseUploadHeadersAndRequestParameters(req, ps)
	if err != nil {
		WriteError(w, Error{err.Error()}, http.StatusBadRequest)
		return
	}

	// build the upload parameters
	lup := modules.SkyfileUploadParameters{
		SiaPath:             params.siaPath,
		DryRun:              params.dryRun,
		Force:               params.force,
		BaseChunkRedundancy: params.baseChunkRedundancy,
	}

	// Build the Skyfile metadata from the request
	if isMultipartRequest(headers.mediaType) {
		subfiles, reader, err := parseMultiPartRequest(req)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed parsing multipart request: %v", err)}, http.StatusBadRequest)
			return
		}
		// Make sure temporary files created while parsing the multipart form
		// are properly removed on error. The fact that they tend to linger
		// unless explicitly removed is a known (open) issue:
		// https://github.com/golang/go/issues/20253
		defer func() {
			if err := req.MultipartForm.RemoveAll(); err != nil {
				log.Printf("failed to clean up multipart tmp file: %v", err)
			}
		}()

		// Use the filename of the first subfile if it's not passed as query
		// string parameter and there's only one subfile.
		var filename = params.filename
		if filename == "" && len(subfiles) == 1 {
			for _, sf := range subfiles {
				filename = sf.Filename
				break
			}
		}

		// Get the default path.
		var defaultPath string
		if !params.disableDefaultPath {
			defaultPath, err = validDefaultPath(params.defaultPath, subfiles)
			if err != nil {
				WriteError(w, Error{err.Error()}, http.StatusBadRequest)
				return
			}
		}

		lup.Reader = reader
		lup.FileMetadata = modules.SkyfileMetadata{
			Filename:           filename,
			Subfiles:           subfiles,
			DefaultPath:        defaultPath,
			DisableDefaultPath: params.disableDefaultPath,
		}
	} else {
		lup.Reader = req.Body
		lup.FileMetadata = modules.SkyfileMetadata{
			Mode:     params.mode,
			Filename: params.filename,
		}
	}

	// Set encryption key details
	lup.SkykeyName = params.skyKeyName
	lup.SkykeyID = params.skyKeyID

	// Check whether this is a streaming upload or a siafile conversion. If no
	// convert path is provided, assume that the req.Body will be used as a
	// streaming upload.
	if params.convertPath == "" {
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

		// Set the Skylink response header
		w.Header().Set("Skynet-Skylink", skylink.String())

		WriteJSON(w, SkynetSkyfileHandlerPOST{
			Skylink:    skylink.String(),
			MerkleRoot: skylink.MerkleRoot(),
			Bitfield:   skylink.Bitfield(),
		})
		return
	}

	// There is a convert path.
	convertPath, err := modules.NewSiaPath(params.convertPath)
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

	// Set the Skylink response header
	w.Header().Set("Skynet-Skylink", skylink.String())

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
		WriteError(w, Error{"you must specify the name of the skykey"}, http.StatusInternalServerError)
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

// registryHandlerPOST handles the POST calls to /skynet/registry.
func (api *API) registryHandlerPOST(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Decode request.
	dec := json.NewDecoder(req.Body)
	var rhp RegistryHandlerRequestPOST
	err := dec.Decode(&rhp)
	if err != nil {
		WriteError(w, Error{"Failed to decode request: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Check data length here to be able to offer a better and faster error
	// message than when the hosts return it.
	if len(rhp.Data) > modules.RegistryDataSize {
		WriteError(w, Error{fmt.Sprintf("Registry data is too big: %v > %v", len(rhp.Data), modules.RegistryDataSize)}, http.StatusBadRequest)
		return
	}

	// Check the version of the FileID object.
	if rhp.FileID.Version != modules.FileIDVersion {
		WriteError(w, Error{fmt.Sprintf("Unexpected FileID version '%v' != '%v'", rhp.FileID.Version, modules.FileIDVersion)}, http.StatusBadRequest)
		return
	}

	// Compute the tweak.
	tweak, err := rhp.FileID.Tweak()
	if err != nil {
		WriteError(w, Error{"Failed to compute tweak: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	// Update the registry.
	srv := modules.NewSignedRegistryValue(tweak, rhp.Data, rhp.Revision, rhp.Signature)
	err = api.renter.UpdateRegistry(rhp.PublicKey, srv, renter.DefaultRegistryUpdateTimeout)
	if err != nil {
		WriteError(w, Error{"Unable to update the registry: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteSuccess(w)
}

// registryHandlerGET handles the GET calls to /skynet/registry.
func (api *API) registryHandlerGET(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	// Parse public key
	var spk types.SiaPublicKey
	err := spk.LoadString(req.FormValue("publickey"))
	if err != nil {
		WriteError(w, Error{"Unable to parse publickey param: " + err.Error()}, http.StatusBadRequest)
		return
	}

	// Parse tweak.
	fileIDBytes, err := hex.DecodeString(req.FormValue("fileid"))
	if err != nil {
		WriteError(w, Error{"Unable to decode fileid param: " + err.Error()}, http.StatusBadRequest)
		return
	}
	// Decode fileid.
	var fileID modules.FileID
	err = json.Unmarshal(fileIDBytes, &fileID)
	if err != nil {
		WriteError(w, Error{"Unable to json decode fileid param: " + err.Error()}, http.StatusBadRequest)
		return
	}
	// Compute tweak.
	tweak, err := fileID.Tweak()
	if err != nil {
		WriteError(w, Error{"Unable to compute tweak: " + err.Error()}, http.StatusBadRequest)
		return
	}

	srv, err := api.renter.ReadRegistry(spk, tweak, renter.DefaultRegistryReadTimeout)
	if errors.Contains(err, renter.ErrRegistryEntryNotFound) {
		WriteError(w, Error{"Unable to read from the registry: " + err.Error()}, http.StatusNotFound)
		return
	}
	if errors.Contains(err, renter.ErrRegistryLookupTimeout) {
		WriteError(w, Error{"Unable to read from the registry: " + err.Error()}, http.StatusRequestTimeout)
		return
	}
	if err != nil {
		WriteError(w, Error{"Unable to read from the registry: " + err.Error()}, http.StatusInternalServerError)
		return
	}

	// Send response.
	WriteJSON(w, RegistryHandlerGET{
		Tweak:     hex.EncodeToString(srv.Tweak[:]),
		Data:      hex.EncodeToString(srv.Data),
		Revision:  srv.Revision,
		Signature: hex.EncodeToString(srv.Signature[:]),
	})
}
