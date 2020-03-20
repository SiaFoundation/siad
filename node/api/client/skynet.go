package client

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
)

// SkynetSkylinkGet uses the /skynet/skylink endpoint to download a skylink
// file.
func (c *Client) SkynetSkylinkGet(skylink string) ([]byte, modules.SkyfileMetadata, error) {
	return c.SkynetSkylinkGetWithTimeout(skylink, -1)
}

// SkynetSkylinkGetWithTimeout uses the /skynet/skylink endpoint to download a
// skylink file, specifying the given timeout.
func (c *Client) SkynetSkylinkGetWithTimeout(skylink string, timeout int) ([]byte, modules.SkyfileMetadata, error) {
	values := url.Values{}
	// Only set the timeout if it's valid. Seeing as 0 is a valid timeout,
	// callers need to pass -1 to ignore it.
	if timeout >= 0 {
		values.Set("timeout", fmt.Sprintf("%d", timeout))
	}

	getQuery := fmt.Sprintf("/skynet/skylink/%s?%s", skylink, values.Encode())
	header, fileData, err := c.getRawResponse(getQuery)
	if err != nil {
		return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "error fetching api response")
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
func (c *Client) SkynetSkylinkHead(skylink string, timeout int) (int, http.Header, error) {
	getQuery := fmt.Sprintf("/skynet/skylink/%s?timeout=%d", skylink, timeout)
	return c.head(getQuery)
}

// SkynetSkylinkConcatGet uses the /skynet/skylink endpoint to download a
// skylink file with the 'concat' format specified.
func (c *Client) SkynetSkylinkConcatGet(skylink string) ([]byte, modules.SkyfileMetadata, error) {
	values := url.Values{}
	values.Set("format", string(modules.SkyfileFormatConcat))
	getQuery := fmt.Sprintf("/skynet/skylink/%s?%s", skylink, values.Encode())
	var reader io.Reader
	header, body, err := c.getReaderResponse(getQuery)
	if err != nil {
		return nil, modules.SkyfileMetadata{}, errors.AddContext(err, "error fetching api response")
	}
	defer body.Close()
	reader = body

	// Read the fileData.
	fileData, err := ioutil.ReadAll(reader)
	if err != nil {
		return nil, modules.SkyfileMetadata{}, err
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
	values := url.Values{}
	values.Set("format", string(modules.SkyfileFormatConcat))
	getQuery := fmt.Sprintf("/skynet/skylink/%s?%s", skylink, values.Encode())
	_, reader, err := c.getReaderResponse(getQuery)
	return reader, errors.AddContext(err, "unable to fetch skylink data")
}

// SkynetSkylinkTarReaderGet uses the /skynet/skylink endpoint to fetch a
// reader of the file data with the 'tar' format specified.
func (c *Client) SkynetSkylinkTarReaderGet(skylink string) (io.ReadCloser, error) {
	values := url.Values{}
	values.Set("format", string(modules.SkyfileFormatTar))
	getQuery := fmt.Sprintf("/skynet/skylink/%s?%s", skylink, values.Encode())
	_, reader, err := c.getReaderResponse(getQuery)
	return reader, errors.AddContext(err, "unable to fetch skylink data")
}

// SkynetSkylinkTarGzReaderGet uses the /skynet/skylink endpoint to fetch a
// reader of the file data with the 'targz' format specified.
func (c *Client) SkynetSkylinkTarGzReaderGet(skylink string) (io.ReadCloser, error) {
	values := url.Values{}
	values.Set("format", string(modules.SkyfileFormatTarGz))
	getQuery := fmt.Sprintf("/skynet/skylink/%s?%s", skylink, values.Encode())
	_, reader, err := c.getReaderResponse(getQuery)
	return reader, errors.AddContext(err, "unable to fetch skylink data")
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
	values.Set("filename", params.FileMetadata.Filename)
	dryRunStr := fmt.Sprintf("%t", params.DryRun)
	values.Set("dryrun", dryRunStr)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", params.FileMetadata.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)

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
	values.Set("filename", params.FileMetadata.Filename)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", params.FileMetadata.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)

	// Set the headers
	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if disableForce {
		headers["Skynet-Disable-Force"] = strconv.FormatBool(disableForce)
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
	// Set the url values.
	values := url.Values{}
	values.Set("filename", params.Filename)
	forceStr := fmt.Sprintf("%t", params.Force)
	values.Set("force", forceStr)
	redundancyStr := fmt.Sprintf("%v", params.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", params.Root)
	values.Set("root", rootStr)

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", params.SiaPath.String(), values.Encode())

	headers := map[string]string{"Content-Type": params.ContentType}
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
func (c *Client) SkynetConvertSiafileToSkyfilePost(lup modules.SkyfileUploadParameters, convert modules.SiaPath) (string, error) {
	// Set the url values.
	values := url.Values{}
	values.Set("filename", lup.FileMetadata.Filename)
	forceStr := fmt.Sprintf("%t", lup.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", lup.FileMetadata.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", lup.BaseChunkRedundancy)
	values.Set("redundancy", redundancyStr)
	values.Set("convertpath", convert.String())

	// Make the call to upload the file.
	query := fmt.Sprintf("/skynet/skyfile/%s?%s", lup.SiaPath.String(), values.Encode())
	_, resp, err := c.postRawResponse(query, lup.Reader)
	if err != nil {
		return "", errors.AddContext(err, "post call to "+query+" failed")
	}

	// Parse the response to get the skylink.
	var rshp api.SkynetSkyfileHandlerPOST
	err = json.Unmarshal(resp, &rshp)
	if err != nil {
		return "", errors.AddContext(err, "unable to parse the skylink upload response")
	}
	return rshp.Skylink, err
}

// SkynetBlacklistGet requests the /skynet/blacklist Get endpoint
func (c *Client) SkynetBlacklistGet() (blacklist api.SkynetBlacklistGET, err error) {
	err = c.get("/skynet/blacklist", &blacklist)
	return
}

// SkynetBlacklistPost requests the /skynet/blacklist Post endpoint
func (c *Client) SkynetBlacklistPost(additions, removals []string) (err error) {
	sbp := api.SkynetBlacklistPOST{
		Add:    additions,
		Remove: removals,
	}
	data, err := json.Marshal(sbp)
	if err != nil {
		return err
	}
	err = c.post("/skynet/blacklist", string(data), nil)
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
	getQuery := fmt.Sprintf("/skynet/skykey/%s", values.Encode())

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
	getQuery := fmt.Sprintf("/skynet/skykey/%s", values.Encode())

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

// SkykeyIDGet requests the /skynet/skykeyid Get endpoint.
func (c *Client) SkykeyIDGet(name string) (skykey.SkykeyID, error) {
	values := url.Values{}
	values.Set("name", name)
	getQuery := fmt.Sprintf("/skynet/skykeyid/%s", values.Encode())

	var skykeyIDGet api.SkykeyIDGET
	err := c.get(getQuery, &skykeyIDGet)
	if err != nil {
		return skykey.SkykeyID{}, err
	}

	var ID skykey.SkykeyID
	err = ID.FromString(skykeyIDGet.ID)
	if err != nil {
		return skykey.SkykeyID{}, err
	}

	return ID, nil
}
