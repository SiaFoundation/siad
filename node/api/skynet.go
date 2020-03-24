package api

import (
	"archive/tar"
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
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

const (
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
	SkynetBlacklistGET struct {
		Blacklist []crypto.Hash `json:"blacklist"`
	}

	// SkynetBlacklistPOST contains the information needed for the
	// /skynet/blacklist POST endpoint to be called
	SkynetBlacklistPOST struct {
		Add    []string `json:"add"`
		Remove []string `json:"remove"`
	}

	// SkynetStatsGET contains the information queried for the /skynet/stats
	// GET endpoint
	SkynetStatsGET struct {
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
	}

	// SkykeyIDGET contains a base64 encoded SkykeyID.
	SkykeyIDGET struct {
		ID string `json:"id"` // base64 encoded SkykeyID
	}
)

// skynetBlacklistHandlerGET handles the API call to get the list of
// blacklisted skylinks
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

// skynetBlacklistHandlerPOST handles the API call to blacklist certain skylinks
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
		WriteError(w, Error{"unable to update the skynet blacklist: " + err.Error()}, http.StatusBadRequest)
		return
	}

	WriteSuccess(w)
}

// skynetSkylinkHandlerGET accepts a skylink as input and will stream the data
// from the skylink out of the response body as output.
func (api *API) skynetSkylinkHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	strLink := ps.ByName("skylink")
	strLink = strings.TrimPrefix(strLink, "/")

	// Parse out optional path to a subfile
	path := "/" // default to root
	splits := strings.SplitN(strLink, "?", 2)
	splits = strings.SplitN(splits[0], "/", 2)
	if len(splits) > 1 {
		path = fmt.Sprintf("/%s", splits[1])
	}

	// Parse skylink
	var skylink modules.Skylink
	err := skylink.LoadString(strLink)
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
	if format != modules.SkyfileFormatNotSpecified &&
		format != modules.SkyfileFormatTar &&
		format != modules.SkyfileFormatConcat &&
		format != modules.SkyfileFormatTarGz {
		WriteError(w, Error{"unable to parse 'format' parameter, allowed values are: 'concat', 'tar' and 'targz'"}, http.StatusBadRequest)
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
	} else if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to fetch skylink: %v", err)}, http.StatusInternalServerError)
		return
	}
	defer streamer.Close()

	// If path is different from the root, limit the streamer and return the
	// appropriate subset of the metadata. This is done by wrapping the streamer
	// so it only returns the files defined in the subset of the metadata.
	if path != "/" {
		var dir bool
		var offset, size uint64
		metadata, dir, offset, size = metadata.ForPath(path)
		if len(metadata.Subfiles) == 0 {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for path: %v", path)}, http.StatusNotFound)
			return
		}
		if dir && format == modules.SkyfileFormatNotSpecified {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for path: %v, format must be specified", path)}, http.StatusBadRequest)
			return
		}
		streamer, err = NewLimitStreamer(streamer, offset, size)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to download contents for path: %v, could not create limit streamer", path)}, http.StatusInternalServerError)
			return
		}
	} else {
		if len(metadata.Subfiles) > 1 && format == "" {
			WriteError(w, Error{fmt.Sprintf("failed to download directory for path: %v, format must be specified", path)}, http.StatusBadRequest)
			return
		}
	}

	// If requested, serve the content as a tar archive or compressed tar
	// archive.
	if format == modules.SkyfileFormatTar {
		w.Header().Set("content-type", "application/x-tar")
		err = serveTar(w, metadata, streamer)
		return
	} else if format == modules.SkyfileFormatTarGz {
		w.Header().Set("content-type", "application/x-gtar ")
		gzw := gzip.NewWriter(w)
		err = serveTar(gzw, metadata, streamer)
		err = errors.Compose(err, gzw.Close())
		return
	}
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to serve skyfile as archive: %v", err)}, http.StatusInternalServerError)
		return
	}

	// Encode the metadata
	encMetadata, err := json.Marshal(metadata)
	if err != nil {
		WriteError(w, Error{fmt.Sprintf("failed to write skylink metadata: %v", err)}, http.StatusInternalServerError)
		return
	}

	// Only set the Content-Type header when the metadata defines one, if we
	// were to set the header to an empty string, it would prevent the http
	// library from sniffing the file's content type.
	if metadata.ContentType() != "" {
		w.Header().Set("Content-Type", metadata.ContentType())
	}

	// Set Content-Disposition header, if 'attachment' is true, set the
	// disposition-type to attachment, otherwise we inline it.
	var cdh string
	if attachment {
		cdh = fmt.Sprintf("attachment; filename=%s", strconv.Quote(filepath.Base(metadata.Filename)))
	} else {
		cdh = fmt.Sprintf("inline; filename=%s", strconv.Quote(filepath.Base(metadata.Filename)))
	}
	w.Header().Set("Content-Disposition", cdh)
	w.Header().Set("Skynet-File-Metadata", string(encMetadata))
	w.Header().Set("Access-Control-Allow-Origin", "*")

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
		WriteError(w, Error{"'force' has been disabled on this node" + err.Error()}, http.StatusBadRequest)
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
	if strings.HasPrefix(mediaType, "multipart/form-data") {
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

		lup.Reader = reader
		lup.FileMetadata = modules.SkyfileMetadata{
			Filename: filename,
			Subfiles: subfiles,
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

	// Ensure we have a filename
	if lup.FileMetadata.Filename == "" {
		WriteError(w, Error{"no filename provided"}, http.StatusBadRequest)
		return
	}

	// Enable CORS
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Check whether this is a streaming upload or a siafile conversion. If no
	// convert path is provided, assume that the req.Body will be used as a
	// streaming upload.
	convertPathStr := queryForm.Get("convertpath")
	if convertPathStr == "" {
		skylink, err := api.renter.UploadSkyfile(lup)
		if err != nil {
			WriteError(w, Error{fmt.Sprintf("failed to upload file to Skynet: %v", err)}, http.StatusBadRequest)
			return
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

	WriteJSON(w, SkynetStatsGET{UploadStats: stats, VersionInfo: SkynetVersion{Version: version, GitRevision: build.GitRevision}})
}

// serveTar serves skyfiles as a tar by reading them from r and writing the
// archive to dst.
func serveTar(dst io.Writer, md modules.SkyfileMetadata, streamer modules.Streamer) error {
	tw := tar.NewWriter(dst)
	// Get the files to tar.
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
		// Fetch the length of the file by seeking to the end and then back to
		// the start.
		length, err := streamer.Seek(0, io.SeekEnd)
		if err != nil {
			return errors.AddContext(err, "serveTar: failed to seek to end of skyfile")
		}
		_, err = streamer.Seek(0, io.SeekStart)
		if err != nil {
			return errors.AddContext(err, "serveTar: failed to seek to start of skyfile")
		}
		// Construct the SkyfileSubfileMetadata.
		files = append(files, modules.SkyfileSubfileMetadata{
			FileMode: md.Mode,
			Filename: md.Filename,
			Offset:   0,
			Len:      uint64(length),
		})
	}
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
		if _, err := io.CopyN(tw, streamer, header.Size); err != nil {
			return err
		}
	}
	return tw.Close()
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

// skykeyHandlerGET handles the API call to get a Skykey using its
// name or ID.
func (api *API) skykeyHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse Skykey id and name.
	name := req.FormValue("name")
	idString := req.FormValue("id")

	if idString == "" && name == "" {
		WriteError(w, Error{"you must specify the name or ID of the Skykey"}, http.StatusInternalServerError)
		return
	}

	var sk skykey.Skykey
	var err error

	if name != "" {
		sk, err = api.renter.SkykeyByName(name)
		if err != nil {
			WriteError(w, Error{"failed to retrieve skykey: " + err.Error()}, http.StatusInternalServerError)
			return
		}
	} else if idString != "" {
		var id skykey.SkykeyID
		err = id.FromString(idString)
		if err != nil {
			WriteError(w, Error{"failed to decode ID string: "}, http.StatusInternalServerError)
			return
		}

		sk, err = api.renter.SkykeyByID(id)
		if err != nil {
			WriteError(w, Error{"failed to retrieve skykey: " + err.Error()}, http.StatusInternalServerError)
			return
		}
	}

	skString, err := sk.ToString()
	if err != nil {
		WriteError(w, Error{"failed to decode skykey: " + err.Error()}, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, SkykeyGET{
		Skykey: skString,
	})
}

// skykeyIDHandlerGET handles the API call to get a SkykeyID using its
// name.
func (api *API) skykeyIDHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse Skykey name.
	name := req.FormValue("name")

	if name == "" {
		WriteError(w, Error{"you must specify the name the Skykey"}, http.StatusInternalServerError)
		return
	}

	id, err := api.renter.SkykeyIDByName(name)
	if err != nil {
		WriteError(w, Error{"failed to retrieve skykey" + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, SkykeyIDGET{
		ID: id.ToString(),
	})
}

// skykeyCreateKeyHandlerPost handles the API call to create a skykey using the renter's
// skykey manager.
func (api *API) skykeyCreateKeyHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse Skykey name and ciphertype
	name := req.FormValue("name")
	ctString := req.FormValue("ciphertype")

	if name == "" {
		WriteError(w, Error{"you must specify the name the Skykey"}, http.StatusInternalServerError)
		return
	}

	var ct crypto.CipherType
	err := ct.FromString(ctString)
	if err != nil {
		WriteError(w, Error{"failed to decode ciphertype" + err.Error()}, http.StatusInternalServerError)
		return
	}

	sk, err := api.renter.CreateSkykey(name, ct)
	if err != nil {
		WriteError(w, Error{"failed to create skykey" + err.Error()}, http.StatusInternalServerError)
		return
	}

	keyString, err := sk.ToString()
	if err != nil {
		WriteError(w, Error{"failed to decode skykey" + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteJSON(w, SkykeyGET{
		Skykey: keyString,
	})
}

// skykeyAddKeyHandlerPost handles the API call to add a skykey to the renter's
// skykey manager.
func (api *API) skykeyAddKeyHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	// Parse Skykey name and ciphertype
	skString := req.FormValue("skykey")

	if skString == "" {
		WriteError(w, Error{"you must specify the name the Skykey"}, http.StatusInternalServerError)
		return
	}

	var sk skykey.Skykey
	err := sk.FromString(skString)
	if err != nil {
		WriteError(w, Error{"failed to decode skykey" + err.Error()}, http.StatusInternalServerError)
		return
	}

	err = api.renter.AddSkykey(sk)
	if err != nil {
		WriteError(w, Error{"failed to add skykey" + err.Error()}, http.StatusInternalServerError)
		return
	}

	WriteSuccess(w)
}
