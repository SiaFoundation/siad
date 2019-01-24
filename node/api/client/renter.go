package client

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// AllowanceRequestPost is a helper type to be able to build an allowance
	// request.
	AllowanceRequestPost struct {
		c      *Client
		sent   bool
		values url.Values
	}
)

// RenterPostPartialAllowance starts an allowance request which can be extended
// using its methods.
func (c *Client) RenterPostPartialAllowance() *AllowanceRequestPost {
	return &AllowanceRequestPost{c: c, values: make(url.Values)}
}

// WithFunds adds the funds field to the request.
func (a *AllowanceRequestPost) WithFunds(funds types.Currency) *AllowanceRequestPost {
	a.values.Set("funds", funds.String())
	return a
}

// WithHosts adds the hosts field to the request.
func (a *AllowanceRequestPost) WithHosts(hosts uint64) *AllowanceRequestPost {
	a.values.Set("hosts", fmt.Sprint(hosts))
	return a
}

// WithPeriod adds the period field to the request.
func (a *AllowanceRequestPost) WithPeriod(period types.BlockHeight) *AllowanceRequestPost {
	a.values.Set("period", fmt.Sprint(period))
	return a
}

// WithRenewWindow adds the renewwindow field to the request.
func (a *AllowanceRequestPost) WithRenewWindow(renewWindow types.BlockHeight) *AllowanceRequestPost {
	a.values.Set("renewwindow", fmt.Sprint(renewWindow))
	return a
}

// WithExpectedStorage adds the expected storage field to the request.
func (a *AllowanceRequestPost) WithExpectedStorage(expectedStorage uint64) *AllowanceRequestPost {
	a.values.Set("expectedstorage", fmt.Sprint(expectedStorage))
	return a
}

// WithExpectedUpload adds the expected upload field to the request.
func (a *AllowanceRequestPost) WithExpectedUpload(expectedUpload uint64) *AllowanceRequestPost {
	a.values.Set("expectedupload", fmt.Sprint(expectedUpload))
	return a
}

// WithExpectedDownload adds the expected download field to the request.
func (a *AllowanceRequestPost) WithExpectedDownload(expectedDownload uint64) *AllowanceRequestPost {
	a.values.Set("expecteddownload", fmt.Sprint(expectedDownload))
	return a
}

// WithExpectedRedundancy adds the expected redundancy field to the request.
func (a *AllowanceRequestPost) WithExpectedRedundancy(expectedRedundancy float64) *AllowanceRequestPost {
	a.values.Set("expectedredundancy", fmt.Sprint(expectedRedundancy))
	return a
}

// Send finalizes and sends the request.
func (a *AllowanceRequestPost) Send() (err error) {
	if a.sent {
		return errors.New("Error, request already sent")
	}
	a.sent = true
	err = a.c.post("/renter", a.values.Encode(), nil)
	return
}

// escapeSiaPath escapes the siapath to make it safe to use within a URL. This
// should only be used on SiaPaths which are used as part of the URL path.
// Paths within the query have to be escaped with net.QueryEscape.
func escapeSiaPath(siaPath string) string {
	pathSegments := strings.Split(siaPath, "/")

	escapedSegments := make([]string, 0, len(pathSegments))
	for _, segment := range pathSegments {
		escapedSegments = append(escapedSegments, url.PathEscape(segment))
	}
	return strings.Join(escapedSegments, "/")
}

// trimSiaPath trims potential leading slashes within the siaPath.
func trimSiaPath(siaPath string) string {
	return strings.TrimPrefix(siaPath, "/")
}

// RenterContractCancelPost uses the /renter/contract/cancel endpoint to cancel
// a contract
func (c *Client) RenterContractCancelPost(id types.FileContractID) error {
	values := url.Values{}
	values.Set("id", id.String())
	err := c.post("/renter/contract/cancel", values.Encode(), nil)
	return err
}

// RenterContractsGet requests the /renter/contracts resource and returns
// Contracts and ActiveContracts
func (c *Client) RenterContractsGet() (rc api.RenterContracts, err error) {
	err = c.get("/renter/contracts", &rc)
	return
}

// RenterInactiveContractsGet requests the /renter/contracts resource with the
// inactive flag set to true
func (c *Client) RenterInactiveContractsGet() (rc api.RenterContracts, err error) {
	values := url.Values{}
	values.Set("inactive", fmt.Sprint(true))
	err = c.get("/renter/contracts?"+values.Encode(), &rc)
	return
}

// RenterInitContractRecoveryScanPost initializes a contract recovery scan
// using the /renter/recoveryscan endpoint.
func (c *Client) RenterInitContractRecoveryScanPost() (err error) {
	err = c.post("/renter/recoveryscan", "", nil)
	return
}

// RenterContractRecoveryProgressPost returns information about potentially
// ongoing contract recovery scans.
func (c *Client) RenterContractRecoveryProgressPost() (rrs api.RenterRecoveryStatusGET, err error) {
	err = c.get("/renter/recoveryscan", &rrs)
	return
}

// RenterExpiredContractsGet requests the /renter/contracts resource with the
// expired flag set to true
func (c *Client) RenterExpiredContractsGet() (rc api.RenterContracts, err error) {
	values := url.Values{}
	values.Set("expired", fmt.Sprint(true))
	err = c.get("/renter/contracts?"+values.Encode(), &rc)
	return
}

// RenterRecoverableContractsGet requests the /renter/contracts resource with the
// recoverable flag set to true
func (c *Client) RenterRecoverableContractsGet() (rc api.RenterContracts, err error) {
	values := url.Values{}
	values.Set("recoverable", fmt.Sprint(true))
	err = c.get("/renter/contracts?"+values.Encode(), &rc)
	return
}

// RenterDeletePost uses the /renter/delete endpoint to delete a file.
func (c *Client) RenterDeletePost(siaPath string) (err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	err = c.post(fmt.Sprintf("/renter/delete/%s", siaPath), "", nil)
	return err
}

// RenterDownloadGet uses the /renter/download endpoint to download a file to a
// destination on disk.
func (c *Client) RenterDownloadGet(siaPath, destination string, offset, length uint64, async bool) (err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	values := url.Values{}
	values.Set("destination", url.QueryEscape(destination))
	values.Set("offset", fmt.Sprint(offset))
	values.Set("length", fmt.Sprint(length))
	values.Set("async", fmt.Sprint(async))
	err = c.get(fmt.Sprintf("/renter/download/%s?%s", siaPath, values.Encode()), nil)
	return
}

// RenterDownloadFullGet uses the /renter/download endpoint to download a full
// file.
func (c *Client) RenterDownloadFullGet(siaPath, destination string, async bool) (err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	values := url.Values{}
	values.Set("destination", url.QueryEscape(destination))
	values.Set("httpresp", fmt.Sprint(false))
	values.Set("async", fmt.Sprint(async))
	err = c.get(fmt.Sprintf("/renter/download/%s?%s", siaPath, values.Encode()), nil)
	return
}

// RenterClearAllDownloadsPost requests the /renter/downloads/clear resource
// with no parameters
func (c *Client) RenterClearAllDownloadsPost() (err error) {
	err = c.post("/renter/downloads/clear", "", nil)
	return
}

// RenterClearDownloadsAfterPost requests the /renter/downloads/clear resource
// with only the after timestamp provided
func (c *Client) RenterClearDownloadsAfterPost(after time.Time) (err error) {
	values := url.Values{}
	values.Set("after", strconv.FormatInt(after.UnixNano(), 10))
	err = c.post("/renter/downloads/clear", values.Encode(), nil)
	return
}

// RenterClearDownloadsBeforePost requests the /renter/downloads/clear resource
// with only the before timestamp provided
func (c *Client) RenterClearDownloadsBeforePost(before time.Time) (err error) {
	values := url.Values{}
	values.Set("before", strconv.FormatInt(before.UnixNano(), 10))
	err = c.post("/renter/downloads/clear", values.Encode(), nil)
	return
}

// RenterClearDownloadsRangePost requests the /renter/downloads/clear resource
// with both before and after timestamps provided
func (c *Client) RenterClearDownloadsRangePost(after, before time.Time) (err error) {
	values := url.Values{}
	values.Set("before", strconv.FormatInt(before.UnixNano(), 10))
	values.Set("after", strconv.FormatInt(after.UnixNano(), 10))
	err = c.post("/renter/downloads/clear", values.Encode(), nil)
	return
}

// RenterDownloadsGet requests the /renter/downloads resource
func (c *Client) RenterDownloadsGet() (rdq api.RenterDownloadQueue, err error) {
	err = c.get("/renter/downloads", &rdq)
	return
}

// RenterDownloadHTTPResponseGet uses the /renter/download endpoint to download
// a file and return its data.
func (c *Client) RenterDownloadHTTPResponseGet(siaPath string, offset, length uint64) (resp []byte, err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	values := url.Values{}
	values.Set("offset", fmt.Sprint(offset))
	values.Set("length", fmt.Sprint(length))
	values.Set("httpresp", fmt.Sprint(true))
	resp, err = c.getRawResponse(fmt.Sprintf("/renter/download/%s?%s", siaPath, values.Encode()))
	return
}

// RenterFileGet uses the /renter/file/:siapath endpoint to query a file.
func (c *Client) RenterFileGet(siaPath string) (rf api.RenterFile, err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	err = c.get("/renter/file/"+siaPath, &rf)
	return
}

// RenterFilesGet requests the /renter/files resource.
func (c *Client) RenterFilesGet() (rf api.RenterFiles, err error) {
	err = c.get("/renter/files", &rf)
	return
}

// RenterGet requests the /renter resource.
func (c *Client) RenterGet() (rg api.RenterGET, err error) {
	err = c.get("/renter", &rg)
	return
}

// RenterPostAllowance uses the /renter endpoint to change the renter's allowance
func (c *Client) RenterPostAllowance(allowance modules.Allowance) error {
	a := c.RenterPostPartialAllowance()
	a = a.WithFunds(allowance.Funds)
	a = a.WithHosts(allowance.Hosts)
	a = a.WithPeriod(allowance.Period)
	a = a.WithRenewWindow(allowance.RenewWindow)
	a = a.WithExpectedStorage(allowance.ExpectedStorage)
	a = a.WithExpectedUpload(allowance.ExpectedUpload)
	a = a.WithExpectedDownload(allowance.ExpectedDownload)
	a = a.WithExpectedRedundancy(allowance.ExpectedRedundancy)
	return a.Send()
}

// RenterCancelAllowance uses the /renter endpoint to cancel the allowance.
func (c *Client) RenterCancelAllowance() (err error) {
	err = c.RenterPostAllowance(modules.Allowance{})
	return
}

// RenterPricesGet requests the /renter/prices endpoint's resources.
func (c *Client) RenterPricesGet(allowance modules.Allowance) (rpg api.RenterPricesGET, err error) {
	query := fmt.Sprintf("?funds=%v&hosts=%v&period=%v&renewwindow=%v",
		allowance.Funds, allowance.Hosts, allowance.Period, allowance.RenewWindow)
	err = c.get("/renter/prices"+query, &rpg)
	return
}

// RenterPostRateLimit uses the /renter endpoint to change the renter's bandwidth rate
// limit.
func (c *Client) RenterPostRateLimit(readBPS, writeBPS int64) (err error) {
	values := url.Values{}
	values.Set("maxdownloadspeed", strconv.FormatInt(readBPS, 10))
	values.Set("maxuploadspeed", strconv.FormatInt(writeBPS, 10))
	err = c.post("/renter", values.Encode(), nil)
	return
}

// RenterRenamePost uses the /renter/rename/:siapath endpoint to rename a file.
func (c *Client) RenterRenamePost(siaPathOld, siaPathNew string) (err error) {
	siaPathOld = escapeSiaPath(trimSiaPath(siaPathOld))
	values := url.Values{}
	values.Set("newsiapath", fmt.Sprintf("/%s", trimSiaPath(siaPathNew)))
	err = c.post(fmt.Sprintf("/renter/rename/%s", siaPathOld), values.Encode(), nil)
	return
}

// RenterSetStreamCacheSizePost uses the /renter endpoint to change the renter's
// streamCacheSize for streaming
func (c *Client) RenterSetStreamCacheSizePost(cacheSize uint64) (err error) {
	values := url.Values{}
	values.Set("streamcachesize", fmt.Sprint(cacheSize))
	err = c.post("/renter", values.Encode(), nil)
	return
}

// RenterSetCheckIPViolationPost uses the /renter endpoint to enable/disable the IP
// violation check in the renter.
func (c *Client) RenterSetCheckIPViolationPost(enabled bool) (err error) {
	values := url.Values{}
	values.Set("checkforipviolation", fmt.Sprint(enabled))
	err = c.post("/renter", values.Encode(), nil)
	return
}

// RenterStreamGet uses the /renter/stream endpoint to download data as a
// stream.
func (c *Client) RenterStreamGet(siaPath string) (resp []byte, err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	resp, err = c.getRawResponse(fmt.Sprintf("/renter/stream/%s", siaPath))
	return
}

// RenterStreamPartialGet uses the /renter/stream endpoint to download a part
// of data as a stream.
func (c *Client) RenterStreamPartialGet(siaPath string, start, end uint64) (resp []byte, err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	resp, err = c.getRawPartialResponse(fmt.Sprintf("/renter/stream/%s", siaPath), start, end)
	return
}

// RenterSetRepairPathPost uses the /renter/tracking endpoint to set the repair
// path of a file to a new location. The file at newPath must exists.
func (c *Client) RenterSetRepairPathPost(siaPath, newPath string) (err error) {
	values := url.Values{}
	values.Set("trackingpath", url.QueryEscape(newPath))
	err = c.post("/renter/file/"+siaPath, values.Encode(), nil)
	return
}

// RenterUploadPost uses the /renter/upload endpoint to upload a file
func (c *Client) RenterUploadPost(path, siaPath string, dataPieces, parityPieces uint64) (err error) {
	return c.RenterUploadForcePost(path, siaPath, dataPieces, parityPieces, false)
}

// RenterUploadForcePost uses the /renter/upload endpoint to upload a file
// and to overwrite if the file already exists
func (c *Client) RenterUploadForcePost(path, siaPath string, dataPieces, parityPieces uint64, force bool) (err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	values := url.Values{}
	values.Set("source", path)
	values.Set("datapieces", strconv.FormatUint(dataPieces, 10))
	values.Set("paritypieces", strconv.FormatUint(parityPieces, 10))
	values.Set("force", strconv.FormatBool(force))
	err = c.post(fmt.Sprintf("/renter/upload/%s", siaPath), values.Encode(), nil)
	return
}

// RenterUploadDefaultPost uses the /renter/upload endpoint with default
// redundancy settings to upload a file.
func (c *Client) RenterUploadDefaultPost(path, siaPath string) (err error) {
	siaPath = escapeSiaPath(trimSiaPath(siaPath))
	values := url.Values{}
	values.Set("source", path)
	err = c.post(fmt.Sprintf("/renter/upload/%s", siaPath), values.Encode(), nil)
	return
}

// RenterDirCreatePost uses the /renter/dir/ endpoint to create a directory for the
// renter
func (c *Client) RenterDirCreatePost(siaPath string) (err error) {
	siaPath = strings.TrimPrefix(siaPath, "/")
	err = c.post(fmt.Sprintf("/renter/dir/%s", siaPath), "action=create", nil)
	return
}

// RenterDirDeletePost uses the /renter/dir/ endpoint to delete a directory for the
// renter
func (c *Client) RenterDirDeletePost(siaPath string) (err error) {
	siaPath = strings.TrimPrefix(siaPath, "/")
	err = c.post(fmt.Sprintf("/renter/dir/%s", siaPath), "action=delete", nil)
	return
}

// RenterDirRenamePost uses the /renter/dir/ endpoint to rename a directory for the
// renter
func (c *Client) RenterDirRenamePost(siaPath, newSiaPath string) (err error) {
	siaPath = strings.TrimPrefix(siaPath, "/")
	newSiaPath = strings.TrimPrefix(newSiaPath, "/")
	err = c.post(fmt.Sprintf("/renter/dir/%s?newsiapath=%s", siaPath, newSiaPath), "action=rename", nil)
	return
}

// RenterGetDir uses the /renter/dir/ endpoint to query a directory
func (c *Client) RenterGetDir(siaPath string) (rd api.RenterDirectory, err error) {
	siaPath = strings.TrimPrefix(siaPath, "/")
	err = c.get(fmt.Sprintf("/renter/dir/%s", siaPath), &rd)
	return
}
