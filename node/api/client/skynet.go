package client

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// SkynetBaseSectorGet uses the /skynet/basesector endpoint to fetch a reader of
// the basesector data.
func (c *Client) SkynetBaseSectorGet(skylink string) (io.ReadCloser, error) {
	_, reader, err := c.getReaderResponse(fmt.Sprintf("/skynet/basesector/%s", skylink))
	return reader, err
}

// SkynetDownloadByRootGet uses the /skynet/root endpoint to fetch a reader of
// a sector.
func (c *Client) SkynetDownloadByRootGet(root crypto.Hash, offset, length uint64, timeout time.Duration) (io.ReadCloser, error) {
	values := url.Values{}
	values.Set("root", root.String())
	values.Set("offset", fmt.Sprint(offset))
	values.Set("length", fmt.Sprint(length))
	if timeout >= 0 {
		values.Set("timeout", fmt.Sprintf("%s", timeout))
	}
	getQuery := fmt.Sprintf("/skynet/root?%v", values.Encode())
	_, reader, err := c.getReaderResponse(getQuery)
	return reader, err
}

// SkynetSkylinkGetWithETag uses the /skynet/skylink endpoint to download a
// skylink file setting the given ETag as value in the If-None-Match request
// header.
func (uc *UnsafeClient) SkynetSkylinkGetWithETag(skylink string, eTag string) (*http.Response, error) {
	return uc.GetWithHeaders(skylinkQueryWithValues(skylink, url.Values{}), http.Header{"If-None-Match": []string{eTag}})
}

// SkynetSkyfilePostRawResponse uses the /skynet/skyfile endpoint to upload a
// skyfile.  This function is unsafe as it returns the raw response alongside
// the http headers.
func (uc *UnsafeClient) SkynetSkyfilePostRawResponse(params modules.SkyfileUploadParameters) (http.Header, []byte, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("filename", params.Filename)
	dryRunStr := fmt.Sprintf("%t", params.DryRun)
	values.Set("dryrun", dryRunStr)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", params.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)

	// Encode SkykeyName or SkykeyID.
	if params.SkykeyName != "" {
		values.Set("skykeyname", params.SkykeyName)
	}
	hasSkykeyID := params.SkykeyID != skykey.SkykeyID{}
	if hasSkykeyID {
		values.Set("skykeyid", params.SkykeyID.ToString())
	}

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", params.SiaPath.String(), values.Encode())
	return uc.postRawResponse(query, params.Reader)
}

// RenterSkyfileGet wraps RenterFileRootGet to query a skyfile.
func (c *Client) RenterSkyfileGet(siaPath modules.SiaPath, root bool) (rf api.RenterFile, err error) {
	if !root {
		siaPath, err = modules.SkynetFolder.Join(siaPath.String())
		if err != nil {
			return
		}
	}
	return c.RenterFileRootGet(siaPath)
}

// SkynetSkylinkGet uses the /skynet/skylink endpoint to download a skylink
// file.
func (c *Client) SkynetSkylinkGet(skylink string) ([]byte, modules.SkyfileMetadata, error) {
	return c.SkynetSkylinkGetWithTimeout(skylink, -1)
}

// SkynetSkylinkGetWithTimeout uses the /skynet/skylink endpoint to download a
// skylink file, specifying the given timeout.
func (c *Client) SkynetSkylinkGetWithTimeout(skylink string, timeout int) ([]byte, modules.SkyfileMetadata, error) {
	params := make(map[string]string)
	// Only set the timeout if it's valid. Seeing as 0 is a valid timeout,
	// callers need to pass -1 to ignore it.
	if timeout >= 0 {
		params["timeout"] = fmt.Sprintf("%d", timeout)
	}
	return c.skynetSkylinkGetWithParameters(skylink, params)
}

// SkynetSkylinkGetWithNoMetadata uses the /skynet/skylink endpoint to download
// a skylink file, specifying the given value for the 'no-response-metadata'
// parameter.
func (c *Client) SkynetSkylinkGetWithNoMetadata(skylink string, nometadata bool) ([]byte, modules.SkyfileMetadata, error) {
	return c.skynetSkylinkGetWithParameters(skylink, map[string]string{
		"no-response-metadata": fmt.Sprintf("%t", nometadata),
	})
}

// skynetSkylinkGetWithParameters uses the /skynet/skylink endpoint to download
// a skylink file, specifying the given parameters.
// The caller of this function is responsible for validating the parameters!
func (c *Client) skynetSkylinkGetWithParameters(skylink string, params map[string]string) ([]byte, modules.SkyfileMetadata, error) {
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}

	getQuery := skylinkQueryWithValues(skylink, values)
	header, fileData, err := c.getRawResponse(getQuery)
	if err != nil {
		return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "skynetSkylnkGet with parameters failed getRawResponse")
	}

	var sm modules.SkyfileMetadata
	strMetadata := header.Get("Skynet-File-Metadata")
	if strMetadata != "" {
		err = json.Unmarshal([]byte(strMetadata), &sm)
		if err != nil {
			return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "unable to unmarshal skyfile metadata")
		}
	}
	return fileData, sm, errors.AddContext(err, "unable to fetch skylink data")
}

// SkynetSkylinkHead uses the /skynet/skylink endpoint to get the headers that
// are returned if the skyfile were to be requested using the SkynetSkylinkGet
// method.
func (c *Client) SkynetSkylinkHead(skylink string) (int, http.Header, error) {
	return c.SkynetSkylinkHeadWithParameters(skylink, url.Values{})
}

// SkynetSkylinkHeadWithTimeout uses the /skynet/skylink endpoint to get the
// headers that are returned if the skyfile were to be requested using the
// SkynetSkylinkGet method. It allows to pass a timeout parameter for the
// request.
func (c *Client) SkynetSkylinkHeadWithTimeout(skylink string, timeout int) (int, http.Header, error) {
	values := url.Values{}
	values.Set("timeout", fmt.Sprintf("%d", timeout))
	return c.SkynetSkylinkHeadWithParameters(skylink, values)
}

// SkynetSkylinkHeadWithAttachment uses the /skynet/skylink endpoint to get the
// headers that are returned if the skyfile were to be requested using the
// SkynetSkylinkGet method. It allows to pass the 'attachment' parameter.
func (c *Client) SkynetSkylinkHeadWithAttachment(skylink string, attachment bool) (int, http.Header, error) {
	values := url.Values{}
	values.Set("attachment", fmt.Sprintf("%t", attachment))
	return c.SkynetSkylinkHeadWithParameters(skylink, values)
}

// SkynetSkylinkHeadWithFormat uses the /skynet/skylink endpoint to get the
// headers that are returned if the skyfile were to be requested using the
// SkynetSkylinkGet method. It allows to pass the 'format' parameter.
func (c *Client) SkynetSkylinkHeadWithFormat(skylink string, format modules.SkyfileFormat) (int, http.Header, error) {
	values := url.Values{}
	values.Set("format", string(format))
	return c.SkynetSkylinkHeadWithParameters(skylink, values)
}

// SkynetSkylinkHeadWithParameters uses the /skynet/skylink endpoint to get the
// headers that are returned if the skyfile were to be requested using the
// SkynetSkylinkGet method. The values are encoded in the querystring.
func (c *Client) SkynetSkylinkHeadWithParameters(skylink string, values url.Values) (int, http.Header, error) {
	getQuery := skylinkQueryWithValues(skylink, values)
	return c.head(getQuery)
}

// SkynetSkylinkConcatGet uses the /skynet/skylink endpoint to download a
// skylink file with the 'concat' format specified.
func (c *Client) SkynetSkylinkConcatGet(skylink string) (_ []byte, _ modules.SkyfileMetadata, err error) {
	values := url.Values{}
	values.Set("format", string(modules.SkyfileFormatConcat))
	getQuery := skylinkQueryWithValues(skylink, values)
	var reader io.Reader
	header, body, err := c.getReaderResponse(getQuery)
	if err != nil {
		return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "error fetching api response for GET with format=concat")
	}
	defer func() {
		err = errors.Compose(err, body.Close())
	}()
	reader = body

	// Read the fileData.
	fileData, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "unable to reader data from reader")
	}

	var sm modules.SkyfileMetadata
	strMetadata := header.Get("Skynet-File-Metadata")
	if strMetadata != "" {
		err = json.Unmarshal([]byte(strMetadata), &sm)
		if err != nil {
			return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "unable to unmarshal skyfile metadata")
		}
	}
	return fileData, sm, errors.AddContext(err, "unable to fetch skylink data")
}

// SkynetSkylinkBackup uses the /skynet/skylink endpoint to fetch the Skyfile's
// basesector, and reader for large Skyfiles, and writes it to the backupDst
// writer.
func (c *Client) SkynetSkylinkBackup(skylink string, backupDst io.Writer) error {
	// Download the BaseSector first
	baseSectorReader, err := c.SkynetBaseSectorGet(skylink)
	if err != nil {
		return errors.AddContext(err, "unable to download baseSector")
	}
	baseSector, err := ioutil.ReadAll(baseSectorReader)
	if err != nil {
		return errors.AddContext(err, "unable to read baseSector reader")
	}

	// If the baseSector isn't encrypted, parse out the layout and see if we can
	// return early.
	if !modules.IsEncryptedBaseSector(baseSector) {
		// Parse the layout from the baseSector
		sl, _, _, _, err := modules.ParseSkyfileMetadata(baseSector)
		if err != nil {
			return errors.AddContext(err, "unable to parse baseSector")
		}
		// If there is no fanout then we just need to backup the baseSector
		if sl.FanoutSize == 0 {
			return modules.BackupSkylink(skylink, baseSector, nil, backupDst)
		}
	}

	// To avoid having the caller provide the Skykey, we treat encrypted skyfiles the
	// same as skyfiles that have a fanout. Which means calling /skynet/skylink
	// for the remaining information needed for the backup

	// Fetch the header and reader for the Skylink
	getQuery := fmt.Sprintf("/skynet/skylink/%s", skylink)
	header, reader, err := c.getReaderResponse(getQuery)
	if err != nil {
		return errors.AddContext(err, "unable to fetch skylink data")
	}
	defer drainAndClose(reader)

	// Read the SkyfileMetadata
	var sm modules.SkyfileMetadata
	strMetadata := header.Get("Skynet-File-Metadata")
	if strMetadata == "" {
		return errors.AddContext(err, "no skyfile metadata returned")
	}

	// Unmarshal the metadata so we can check for subFiles
	err = json.Unmarshal([]byte(strMetadata), &sm)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal skyfile metadata")
	}

	// If there are no subFiles then we have all the information we need to
	// back up the file.
	if len(sm.Subfiles) == 0 {
		return modules.BackupSkylink(skylink, baseSector, reader, backupDst)
	}

	// Grab the default path for the skyfile
	defaultPath := strings.TrimPrefix(sm.DefaultPath, "/")
	if defaultPath == "" {
		defaultPath = api.DefaultSkynetDefaultPath
	}

	// Sort the subFiles by offset
	subFiles := make([]modules.SkyfileSubfileMetadata, 0, len(sm.Subfiles))
	for _, sfm := range sm.Subfiles {
		subFiles = append(subFiles, sfm)
	}
	sort.Slice(subFiles, func(i, j int) bool {
		return subFiles[i].Offset < subFiles[j].Offset
	})

	// Build a backup reader by downloading all the subFile data
	var backupReader io.Reader
	for _, sf := range subFiles {
		// Download the data from the subFile
		subgetQuery := fmt.Sprintf("%s/%s", getQuery, sf.Filename)
		_, subreader, err := c.getReaderResponse(subgetQuery)
		if err != nil {
			return errors.AddContext(err, "unable to download subFile")
		}
		if backupReader == nil {
			backupReader = io.MultiReader(subreader)
			continue
		}
		backupReader = io.MultiReader(backupReader, subreader)
	}

	// Create the backup file on disk
	return modules.BackupSkylink(skylink, baseSector, backupReader, backupDst)
}

// SkynetSkylinkRestorePost uses the /skynet/restore endpoint to restore
// a skylink from the backup.
func (c *Client) SkynetSkylinkRestorePost(backup io.Reader) (string, error) {
	// Submit the request
	_, resp, err := c.postRawResponse("/skynet/restore", backup)
	if err != nil {
		return "", errors.AddContext(err, "post call to /skynet/restore failed")
	}
	var srp api.SkynetRestorePOST
	err = json.Unmarshal(resp, &srp)
	if err != nil {
		return "", errors.AddContext(err, "unable to unmarshal response")
	}
	return srp.Skylink, nil
}

// SkynetSkylinkReaderGet uses the /skynet/skylink endpoint to fetch a reader of
// the file data.
func (c *Client) SkynetSkylinkReaderGet(skylink string) (io.ReadCloser, error) {
	getQuery := fmt.Sprintf("/skynet/skylink/%s", skylink)
	_, reader, err := c.getReaderResponse(getQuery)
	return reader, errors.AddContext(err, "unable to fetch skylink data")
}

// SkynetSkylinkConcatReaderGet uses the /skynet/skylink endpoint to fetch a
// reader of the file data with the 'concat' format specified.
func (c *Client) SkynetSkylinkConcatReaderGet(skylink string) (io.ReadCloser, error) {
	_, reader, err := c.SkynetSkylinkFormatGet(skylink, modules.SkyfileFormatConcat)
	return reader, err
}

// SkynetSkylinkFormatGet uses the /skynet/skylink endpoint to fetch a reader of
// the file data with the format specified.
func (c *Client) SkynetSkylinkFormatGet(skylink string, format modules.SkyfileFormat) (http.Header, io.ReadCloser, error) {
	values := url.Values{}
	values.Set("format", string(format))
	getQuery := skylinkQueryWithValues(skylink, values)
	header, reader, err := c.getReaderResponse(getQuery)
	return header, reader, errors.AddContext(err, "unable to fetch skylink data")
}

// SkynetSkylinkTarReaderGet uses the /skynet/skylink endpoint to fetch a
// reader of the file data with the 'tar' format specified.
func (c *Client) SkynetSkylinkTarReaderGet(skylink string) (http.Header, io.ReadCloser, error) {
	return c.SkynetSkylinkFormatGet(skylink, modules.SkyfileFormatTar)
}

// SkynetSkylinkTarGzReaderGet uses the /skynet/skylink endpoint to fetch a
// reader of the file data with the 'targz' format specified.
func (c *Client) SkynetSkylinkTarGzReaderGet(skylink string) (http.Header, io.ReadCloser, error) {
	return c.SkynetSkylinkFormatGet(skylink, modules.SkyfileFormatTarGz)
}

// SkynetSkylinkZipReaderGet uses the /skynet/skylink endpoint to fetch a
// reader of the file data with the 'zip' format specified.
func (c *Client) SkynetSkylinkZipReaderGet(skylink string) (http.Header, io.ReadCloser, error) {
	return c.SkynetSkylinkFormatGet(skylink, modules.SkyfileFormatZip)
}

// SkynetSkylinkPinPost uses the /skynet/pin endpoint to pin the file at the
// given skylink.
func (c *Client) SkynetSkylinkPinPost(skylink string, params modules.SkyfilePinParameters) error {
	return c.SkynetSkylinkPinPostWithTimeout(skylink, params, -1)
}

// SkynetSkylinkPinPostWithTimeout uses the /skynet/pin endpoint to pin the file
// at the given skylink, specifying the given timeout.
func (c *Client) SkynetSkylinkPinPostWithTimeout(skylink string, params modules.SkyfilePinParameters, timeout int) error {
	// Set the url values.
	values := url.Values{}
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)
	values.Set("siapath", params.SiaPath.String())
	values.Set("timeout", fmt.Sprintf("%d", timeout))

	query := fmt.Sprintf("/skynet/pin/%s?%s", skylink, values.Encode())
	_, _, err := c.postRawResponse(query, nil)
	if err != nil {
		return errors.AddContext(err, "post call to "+query+" failed")
	}
	return nil
}

// SkynetSkyfilePost uses the /skynet/skyfile endpoint to upload a skyfile.  The
// resulting skylink is returned along with an error.
func (c *Client) SkynetSkyfilePost(params modules.SkyfileUploadParameters) (string, api.SkynetSkyfileHandlerPOST, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("filename", params.Filename)
	dryRunStr := fmt.Sprintf("%t", params.DryRun)
	values.Set("dryrun", dryRunStr)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", params.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)

	// Encode SkykeyName or SkykeyID.
	if params.SkykeyName != "" {
		values.Set("skykeyname", params.SkykeyName)
	}
	hasSkykeyID := params.SkykeyID != skykey.SkykeyID{}
	if hasSkykeyID {
		values.Set("skykeyid", params.SkykeyID.ToString())
	}

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", params.SiaPath.String(), values.Encode())
	_, resp, err := c.postRawResponse(query, params.Reader)
	if err != nil {
		return "", api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "post call to "+query+" failed")
	}

	// Parse the response to get the skylink.
	var rshp api.SkynetSkyfileHandlerPOST
	err = json.Unmarshal(resp, &rshp)
	if err != nil {
		return "", api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "unable to parse the skylink upload response")
	}
	return rshp.Skylink, rshp, err
}

// SkynetSkyfilePostDisableForce uses the /skynet/skyfile endpoint to upload a
// skyfile. This method allows to set the Disable-Force header. The resulting
// skylink is returned along with an error.
func (c *Client) SkynetSkyfilePostDisableForce(params modules.SkyfileUploadParameters, disableForce bool) (string, api.SkynetSkyfileHandlerPOST, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("filename", params.Filename)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", params.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)

	// Set the headers
	headers := http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}
	if disableForce {
		headers.Add("Skynet-Disable-Force", strconv.FormatBool(disableForce))
	}

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", params.SiaPath.String(), values.Encode())
	_, resp, err := c.postRawResponseWithHeaders(query, params.Reader, headers)
	if err != nil {
		return "", api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "post call to "+query+" failed")
	}

	// Parse the response to get the skylink.
	var rshp api.SkynetSkyfileHandlerPOST
	err = json.Unmarshal(resp, &rshp)
	if err != nil {
		return "", api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "unable to parse the skylink upload response")
	}
	return rshp.Skylink, rshp, err
}

// SkynetSkyfileMultiPartPost uses the /skynet/skyfile endpoint to upload a
// skyfile using multipart form data.  The resulting skylink is returned along
// with an error.
func (c *Client) SkynetSkyfileMultiPartPost(params modules.SkyfileMultipartUploadParameters) (string, api.SkynetSkyfileHandlerPOST, error) {
	return c.SkynetSkyfileMultiPartEncryptedPost(params, "", skykey.SkykeyID{})
}

// SkynetSkyfileMultiPartEncryptedPost uses the /skynet/skyfile endpoint to
// upload a skyfile using multipart form data with Skykey params.  The resulting
// skylink is returned along with an error.
func (c *Client) SkynetSkyfileMultiPartEncryptedPost(params modules.SkyfileMultipartUploadParameters, skykeyName string, skykeyID skykey.SkykeyID) (string, api.SkynetSkyfileHandlerPOST, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("filename", params.Filename)
	values.Set("disabledefaultpath", strconv.FormatBool(params.DisableDefaultPath))
	values.Set("defaultpath", params.DefaultPath)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)
	values.Set("skykeyname", skykeyName)
	if skykeyID != (skykey.SkykeyID{}) {
		values.Set("skykeyid", skykeyID.ToString())
	}

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", params.SiaPath.String(), values.Encode())

	headers := http.Header{"Content-Type": []string{params.ContentType}}
	_, resp, err := c.postRawResponseWithHeaders(query, params.Reader, headers)
	if err != nil {
		return "", api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "post call to "+query+" failed")
	}

	// Parse the response to get the skylink.
	var rshp api.SkynetSkyfileHandlerPOST
	err = json.Unmarshal(resp, &rshp)
	if err != nil {
		return "", api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "unable to parse the skylink upload response")
	}
	return rshp.Skylink, rshp, err
}

// SkynetConvertSiafileToSkyfilePost uses the /skynet/skyfile endpoint to
// convert an existing siafile to a skyfile. The input SiaPath 'convert' is the
// siapath of the siafile that should be converted. The siapath provided inside
// of the upload params is the name that will be used for the base sector of the
// skyfile.
func (c *Client) SkynetConvertSiafileToSkyfilePost(lup modules.SkyfileUploadParameters, convert modules.SiaPath) (api.SkynetSkyfileHandlerPOST, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("filename", lup.Filename)
	forceStr := fmt.Sprintf("%t", lup.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", lup.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", lup.BaseChunkRedundancy)
	values.Set("redundancy", redundancyStr)
	values.Set("convertpath", convert.String())
	values.Set("skykeyname", lup.SkykeyName)
	if lup.SkykeyID != (skykey.SkykeyID{}) {
		values.Set("skykeyid", lup.SkykeyID.ToString())
	}

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", lup.SiaPath.String(), values.Encode())
	_, resp, err := c.postRawResponse(query, lup.Reader)
	if err != nil {
		return api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "post call to "+query+" failed")
	}

	// Parse the response to get the skylink.
	var rshp api.SkynetSkyfileHandlerPOST
	err = json.Unmarshal(resp, &rshp)
	if err != nil {
		return api.SkynetSkyfileHandlerPOST{}, errors.AddContext(err, "unable to parse the skylink upload response")
	}
	return rshp, nil
}

// SkynetBlocklistGet requests the /skynet/blocklist Get endpoint
func (c *Client) SkynetBlocklistGet() (blocklist api.SkynetBlocklistGET, err error) {
	err = c.get("/skynet/blocklist", &blocklist)
	return
}

// SkynetBlocklistHashPost requests the /skynet/blocklist Post endpoint
func (c *Client) SkynetBlocklistHashPost(additions, removals []string, isHash bool) (err error) {
	sbp := api.SkynetBlocklistPOST{
		Add:    additions,
		Remove: removals,
		IsHash: isHash,
	}
	data, err := json.Marshal(sbp)
	if err != nil {
		return err
	}
	err = c.post("/skynet/blocklist", string(data), nil)
	return
}

// SkynetBlocklistPost requests the /skynet/blocklist Post endpoint
func (c *Client) SkynetBlocklistPost(additions, removals []string) (err error) {
	err = c.SkynetBlocklistHashPost(additions, removals, false)
	return
}

// SkynetPortalsGet requests the /skynet/portals Get endpoint.
func (c *Client) SkynetPortalsGet() (portals api.SkynetPortalsGET, err error) {
	err = c.get("/skynet/portals", &portals)
	return
}

// SkynetPortalsPost requests the /skynet/portals Post endpoint.
func (c *Client) SkynetPortalsPost(additions []modules.SkynetPortal, removals []modules.NetAddress) (err error) {
	spp := api.SkynetPortalsPOST{
		Add:    additions,
		Remove: removals,
	}
	data, err := json.Marshal(spp)
	if err != nil {
		return err
	}
	err = c.post("/skynet/portals", string(data), nil)
	return
}

// SkynetStatsGet requests the /skynet/stats Get endpoint
func (c *Client) SkynetStatsGet() (stats api.SkynetStatsGET, err error) {
	err = c.get("/skynet/stats", &stats)
	return
}

// SkykeyGetByName requests the /skynet/skykey Get endpoint using the key name.
func (c *Client) SkykeyGetByName(name string) (skykey.Skykey, error) {
	values := url.Values{}
	values.Set("name", name)
	getQuery := fmt.Sprintf("/skynet/skykey?%s", values.Encode())

	var skykeyGet api.SkykeyGET
	err := c.get(getQuery, &skykeyGet)
	if err != nil {
		return skykey.Skykey{}, err
	}

	var sk skykey.Skykey
	err = sk.FromString(skykeyGet.Skykey)
	if err != nil {
		return skykey.Skykey{}, err
	}

	return sk, nil
}

// SkykeyGetByID requests the /skynet/skykey Get endpoint using the key ID.
func (c *Client) SkykeyGetByID(id skykey.SkykeyID) (skykey.Skykey, error) {
	values := url.Values{}
	values.Set("id", id.ToString())
	getQuery := fmt.Sprintf("/skynet/skykey?%s", values.Encode())

	var skykeyGet api.SkykeyGET
	err := c.get(getQuery, &skykeyGet)
	if err != nil {
		return skykey.Skykey{}, err
	}

	var sk skykey.Skykey
	err = sk.FromString(skykeyGet.Skykey)
	if err != nil {
		return skykey.Skykey{}, err
	}

	return sk, nil
}

// SkykeyDeleteByIDPost requests the /skynet/deleteskykey POST endpoint using the key ID.
func (c *Client) SkykeyDeleteByIDPost(id skykey.SkykeyID) error {
	values := url.Values{}
	values.Set("id", id.ToString())
	return c.post("/skynet/deleteskykey", values.Encode(), nil)
}

// SkykeyDeleteByNamePost requests the /skynet/deleteskykey POST endpoint using
// the key name.
func (c *Client) SkykeyDeleteByNamePost(name string) error {
	values := url.Values{}
	values.Set("name", name)
	return c.post("/skynet/deleteskykey", values.Encode(), nil)
}

// SkykeyCreateKeyPost requests the /skynet/createskykey POST endpoint.
func (c *Client) SkykeyCreateKeyPost(name string, skType skykey.SkykeyType) (skykey.Skykey, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("name", name)
	values.Set("type", skType.ToString())

	var skykeyGet api.SkykeyGET
	err := c.post("/skynet/createskykey", values.Encode(), &skykeyGet)
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "createskykey POST request failed")
	}

	var sk skykey.Skykey
	err = sk.FromString(skykeyGet.Skykey)
	if err != nil {
		return skykey.Skykey{}, errors.AddContext(err, "failed to decode skykey string")
	}
	return sk, nil
}

// SkykeyAddKeyPost requests the /skynet/addskykey POST endpoint.
func (c *Client) SkykeyAddKeyPost(sk skykey.Skykey) error {
	values := url.Values{}
	skString, err := sk.ToString()
	if err != nil {
		return errors.AddContext(err, "failed to encode skykey as string")
	}
	values.Set("skykey", skString)

	err = c.post("/skynet/addskykey", values.Encode(), nil)
	if err != nil {
		return errors.AddContext(err, "addskykey POST request failed")
	}

	return nil
}

// SkykeySkykeysGet requests the /skynet/skykeys GET endpoint.
func (c *Client) SkykeySkykeysGet() ([]skykey.Skykey, error) {
	var skykeysGet api.SkykeysGET
	err := c.get("/skynet/skykeys", &skykeysGet)
	if err != nil {
		return nil, errors.AddContext(err, "allskykeys GET request failed")
	}

	res := make([]skykey.Skykey, len(skykeysGet.Skykeys))
	for i, skGET := range skykeysGet.Skykeys {
		err = res[i].FromString(skGET.Skykey)
		if err != nil {
			return nil, errors.AddContext(err, "failed to decode skykey string")
		}
	}
	return res, nil
}

// RegistryRead queries the /skynet/registry [GET] endpoint.
func (c *Client) RegistryRead(spk types.SiaPublicKey, dataKey crypto.Hash) (modules.SignedRegistryValue, error) {
	return c.RegistryReadWithTimeout(spk, dataKey, 0)
}

// RegistryReadWithTimeout queries the /skynet/registry [GET] endpoint with the
// specified timeout.
func (c *Client) RegistryReadWithTimeout(spk types.SiaPublicKey, dataKey crypto.Hash, timeout time.Duration) (modules.SignedRegistryValue, error) {
	// Set the values.
	values := url.Values{}
	values.Set("publickey", spk.String())
	values.Set("datakey", dataKey.String())
	if timeout > 0 {
		values.Set("timeout", fmt.Sprint(int(timeout.Seconds())))
	}

	// Send request.
	var rhg api.RegistryHandlerGET
	err := c.get(fmt.Sprintf("/skynet/registry?%v", values.Encode()), &rhg)
	if err != nil {
		return modules.SignedRegistryValue{}, err
	}

	// Decode data.
	data, err := hex.DecodeString(rhg.Data)
	if err != nil {
		return modules.SignedRegistryValue{}, errors.AddContext(err, "failed to decode signature")
	}
	// Decode signature.
	var sig crypto.Signature
	sigBytes, err := hex.DecodeString(rhg.Signature)
	if err != nil {
		return modules.SignedRegistryValue{}, errors.AddContext(err, "failed to decode signature")
	}
	if len(sigBytes) != len(sig) {
		return modules.SignedRegistryValue{}, fmt.Errorf("unexpected signature length %v != %v", len(sigBytes), len(sig))
	}
	copy(sig[:], sigBytes)
	return modules.NewSignedRegistryValue(dataKey, data, rhg.Revision, sig), nil
}

// RegistryUpdate queries the /skynet/registry [POST] endpoint.
func (c *Client) RegistryUpdate(spk types.SiaPublicKey, dataKey crypto.Hash, revision uint64, sig crypto.Signature, skylink modules.Skylink) error {
	req := api.RegistryHandlerRequestPOST{
		PublicKey: spk,
		DataKey:   dataKey,
		Revision:  revision,
		Signature: sig,
		Data:      skylink.Bytes(),
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.post("/skynet/registry", string(reqBytes), nil)
}

// skylinkQueryWithValues returns a skylink query based on the given skylink and
// values. If the values are empty it will not append a `?` to the query.
func skylinkQueryWithValues(skylink string, values url.Values) string {
	getQuery := fmt.Sprintf("/skynet/skylink/%s", skylink)
	if len(values) > 0 {
		getQuery = fmt.Sprintf("%s?%s", getQuery, values.Encode())
	}
	return getQuery
}
