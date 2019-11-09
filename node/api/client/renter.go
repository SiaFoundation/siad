package client

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
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
// Paths within the query have to be escaped with url.PathEscape.
func escapeSiaPath(siaPath modules.SiaPath) string {
	sp := siaPath.String()
	pathSegments := strings.Split(sp, "/")

	escapedSegments := make([]string, 0, len(pathSegments))
	for _, segment := range pathSegments {
		escapedSegments = append(escapedSegments, url.PathEscape(segment))
	}
	return strings.Join(escapedSegments, "/")
}

// RenterContractCancelPost uses the /renter/contract/cancel endpoint to cancel
// a contract
func (c *Client) RenterContractCancelPost(id types.FileContractID) (err error) {
	values := url.Values{}
	values.Set("id", id.String())
	err = c.post("/renter/contract/cancel", values.Encode(), nil)
	return
}

// RenterAllContractsGet requests the /renter/contracts resource with all
// options set to true
func (c *Client) RenterAllContractsGet() (rc api.RenterContracts, err error) {
	values := url.Values{}
	values.Set("disabled", fmt.Sprint(true))
	values.Set("expired", fmt.Sprint(true))
	values.Set("recoverable", fmt.Sprint(true))
	err = c.get("/renter/contracts?"+values.Encode(), &rc)
	return
}

// RenterContractsGet requests the /renter/contracts resource and returns
// Contracts and ActiveContracts
func (c *Client) RenterContractsGet() (rc api.RenterContracts, err error) {
	err = c.get("/renter/contracts", &rc)
	return
}

// RenterContractStatus requests the /watchdog/contractstatus resource and returns
// the status of a contract.
func (c *Client) RenterContractStatus(fcID types.FileContractID) (status modules.ContractWatchStatus, err error) {
	values := url.Values{}
	values.Set("id", fcID.String())
	err = c.get("/renter/contractstatus?"+values.Encode(), &status)
	return
}

// RenterDisabledContractsGet requests the /renter/contracts resource with the
// disabled flag set to true
func (c *Client) RenterDisabledContractsGet() (rc api.RenterContracts, err error) {
	values := url.Values{}
	values.Set("disabled", fmt.Sprint(true))
	err = c.get("/renter/contracts?"+values.Encode(), &rc)
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

// RenterContractRecoveryProgressGet returns information about potentially
// ongoing contract recovery scans.
func (c *Client) RenterContractRecoveryProgressGet() (rrs api.RenterRecoveryStatusGET, err error) {
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

// RenterCancelDownloadPost requests the /renter/download/cancel endpoint to
// cancel an ongoing doing.
func (c *Client) RenterCancelDownloadPost(id modules.DownloadID) (err error) {
	values := url.Values{}
	values.Set("id", string(id))
	err = c.post("/renter/download/cancel", values.Encode(), nil)
	return
}

// RenterDeletePost uses the /renter/delete endpoint to delete a file.
func (c *Client) RenterDeletePost(siaPath modules.SiaPath) (err error) {
	sp := escapeSiaPath(siaPath)
	err = c.post(fmt.Sprintf("/renter/delete/%s", sp), "", nil)
	return
}

// RenterDownloadGet uses the /renter/download endpoint to download a file to a
// destination on disk.
func (c *Client) RenterDownloadGet(siaPath modules.SiaPath, destination string, offset, length uint64, async bool) (modules.DownloadID, error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("destination", destination)
	values.Set("offset", fmt.Sprint(offset))
	values.Set("length", fmt.Sprint(length))
	values.Set("async", fmt.Sprint(async))
	h, _, err := c.getRawResponse(fmt.Sprintf("/renter/download/%s?%s", sp, values.Encode()))
	if err != nil {
		return "", err
	}
	return modules.DownloadID(h.Get("ID")), nil
}

// RenterDownloadInfoGet uses the /renter/downloadinfo endpoint to fetch
// information about a download from the history.
func (c *Client) RenterDownloadInfoGet(uid modules.DownloadID) (di api.DownloadInfo, err error) {
	err = c.get(fmt.Sprintf("/renter/downloadinfo/%s", uid), &di)
	return
}

// RenterBackups lists the backups the renter has uploaded to hosts.
func (c *Client) RenterBackups() (ubs api.RenterBackupsGET, err error) {
	err = c.get("/renter/backups", &ubs)
	return
}

// RenterBackupsOnHost lists the backups that the renter has uploaded to a
// specific host.
func (c *Client) RenterBackupsOnHost(host types.SiaPublicKey) (ubs api.RenterBackupsGET, err error) {
	values := url.Values{}
	values.Set("host", host.String())
	err = c.get("/renter/backups?"+values.Encode(), &ubs)
	return
}

// RenterCreateBackupPost creates a backup of the SiaFiles of the renter and
// uploads it to hosts.
func (c *Client) RenterCreateBackupPost(name string) (err error) {
	values := url.Values{}
	values.Set("name", name)
	err = c.post("/renter/backups/create", values.Encode(), nil)
	return
}

// RenterRecoverBackupPost downloads and restores the specified backup.
func (c *Client) RenterRecoverBackupPost(name string) (err error) {
	values := url.Values{}
	values.Set("name", name)
	err = c.post("/renter/backups/restore", values.Encode(), nil)
	return
}

// RenterCreateLocalBackupPost creates a local backup of the SiaFiles of the
// renter.
//
// Deprecated: Use RenterCreateBackupPost instead.
func (c *Client) RenterCreateLocalBackupPost(dst string) (err error) {
	values := url.Values{}
	values.Set("destination", dst)
	err = c.post("/renter/backup", values.Encode(), nil)
	return
}

// RenterRecoverLocalBackupPost restores the specified backup.
//
// Deprecated: Use RenterCreateBackupPost instead.
func (c *Client) RenterRecoverLocalBackupPost(src string) (err error) {
	values := url.Values{}
	values.Set("source", src)
	err = c.post("/renter/recoverbackup", values.Encode(), nil)
	return
}

// RenterDownloadFullGet uses the /renter/download endpoint to download a full
// file.
func (c *Client) RenterDownloadFullGet(siaPath modules.SiaPath, destination string, async bool) (modules.DownloadID, error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("destination", destination)
	values.Set("httpresp", fmt.Sprint(false))
	values.Set("async", fmt.Sprint(async))
	h, _, err := c.getRawResponse(fmt.Sprintf("/renter/download/%s?%s", sp, values.Encode()))
	if err != nil {
		return "", err
	}
	return modules.DownloadID(h.Get("ID")), nil
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
func (c *Client) RenterDownloadHTTPResponseGet(siaPath modules.SiaPath, offset, length uint64) (modules.DownloadID, []byte, error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("offset", fmt.Sprint(offset))
	values.Set("length", fmt.Sprint(length))
	values.Set("httpresp", fmt.Sprint(true))
	h, resp, err := c.getRawResponse(fmt.Sprintf("/renter/download/%s?%s", sp, values.Encode()))
	if err != nil {
		return "", nil, err
	}
	return modules.DownloadID(h.Get("ID")), resp, nil
}

// RenterFileGet uses the /renter/file/:siapath endpoint to query a file.
func (c *Client) RenterFileGet(siaPath modules.SiaPath) (rf api.RenterFile, err error) {
	sp := escapeSiaPath(siaPath)
	err = c.get("/renter/file/"+sp, &rf)
	return
}

// RenterFilesGet requests the /renter/files resource.
func (c *Client) RenterFilesGet(cached bool) (rf api.RenterFiles, err error) {
	err = c.get("/renter/files?cached="+fmt.Sprint(cached), &rf)
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

// RenterAllowanceCancelPost uses the /renter/allowance/cancel endpoint to cancel
// the allowance.
func (c *Client) RenterAllowanceCancelPost() (err error) {
	err = c.post("/renter/allowance/cancel", "", nil)
	return
}

// RenterPricesGet requests the /renter/prices endpoint's resources.
func (c *Client) RenterPricesGet(allowance modules.Allowance) (rpg api.RenterPricesGET, err error) {
	query := fmt.Sprintf("?funds=%v&hosts=%v&period=%v&renewwindow=%v",
		allowance.Funds, allowance.Hosts, allowance.Period, allowance.RenewWindow)
	err = c.get("/renter/prices"+query, &rpg)
	return
}

// RenterRateLimitPost uses the /renter endpoint to change the renter's bandwidth rate
// limit.
func (c *Client) RenterRateLimitPost(readBPS, writeBPS int64) (err error) {
	values := url.Values{}
	values.Set("maxdownloadspeed", strconv.FormatInt(readBPS, 10))
	values.Set("maxuploadspeed", strconv.FormatInt(writeBPS, 10))
	err = c.post("/renter", values.Encode(), nil)
	return
}

// RenterRenamePost uses the /renter/rename/:siapath endpoint to rename a file.
func (c *Client) RenterRenamePost(siaPathOld, siaPathNew modules.SiaPath) (err error) {
	spo := escapeSiaPath(siaPathOld)
	values := url.Values{}
	values.Set("newsiapath", fmt.Sprintf("/%s", siaPathNew.String()))
	err = c.post(fmt.Sprintf("/renter/rename/%s", spo), values.Encode(), nil)
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
func (c *Client) RenterStreamGet(siaPath modules.SiaPath) (resp []byte, err error) {
	sp := escapeSiaPath(siaPath)
	_, resp, err = c.getRawResponse(fmt.Sprintf("/renter/stream/%s", sp))
	return
}

// RenterStreamPartialGet uses the /renter/stream endpoint to download a part
// of data as a stream.
func (c *Client) RenterStreamPartialGet(siaPath modules.SiaPath, start, end uint64) (resp []byte, err error) {
	sp := escapeSiaPath(siaPath)
	resp, err = c.getRawPartialResponse(fmt.Sprintf("/renter/stream/%s", sp), start, end)
	return
}

// RenterSetRepairPathPost uses the /renter/tracking endpoint to set the repair
// path of a file to a new location. The file at newPath must exists.
func (c *Client) RenterSetRepairPathPost(siaPath modules.SiaPath, newPath string) (err error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("trackingpath", newPath)
	err = c.post(fmt.Sprintf("/renter/file/%v", sp), values.Encode(), nil)
	return
}

// RenterSetFileStuckPost sets the 'stuck' field of the siafile at siaPath to
// stuck.
func (c *Client) RenterSetFileStuckPost(siaPath modules.SiaPath, stuck bool) (err error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("stuck", fmt.Sprint(stuck))
	err = c.post(fmt.Sprintf("/renter/file/%v", sp), values.Encode(), nil)
	return
}

// RenterUploadPost uses the /renter/upload endpoint to upload a file
func (c *Client) RenterUploadPost(path string, siaPath modules.SiaPath, dataPieces, parityPieces uint64) (err error) {
	return c.RenterUploadForcePost(path, siaPath, dataPieces, parityPieces, false)
}

// RenterUploadForcePost uses the /renter/upload endpoint to upload a file
// and to overwrite if the file already exists
func (c *Client) RenterUploadForcePost(path string, siaPath modules.SiaPath, dataPieces, parityPieces uint64, force bool) (err error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("source", path)
	values.Set("datapieces", strconv.FormatUint(dataPieces, 10))
	values.Set("paritypieces", strconv.FormatUint(parityPieces, 10))
	values.Set("force", strconv.FormatBool(force))
	err = c.post(fmt.Sprintf("/renter/upload/%s", sp), values.Encode(), nil)
	return
}

// RenterUploadDefaultPost uses the /renter/upload endpoint with default
// redundancy settings to upload a file.
func (c *Client) RenterUploadDefaultPost(path string, siaPath modules.SiaPath) (err error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("source", path)
	err = c.post(fmt.Sprintf("/renter/upload/%s", sp), values.Encode(), nil)
	return
}

// RenterUploadStreamPost uploads data using a stream.
func (c *Client) RenterUploadStreamPost(r io.Reader, siaPath modules.SiaPath, dataPieces, parityPieces uint64, force bool) error {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("datapieces", strconv.FormatUint(dataPieces, 10))
	values.Set("paritypieces", strconv.FormatUint(parityPieces, 10))
	values.Set("force", strconv.FormatBool(force))
	values.Set("stream", strconv.FormatBool(true))
	_, err := c.postRawResponse(fmt.Sprintf("/renter/uploadstream/%s?%s", sp, values.Encode()), r)
	return err
}

// RenterUploadStreamRepairPost a siafile using a stream. If the data provided
// by r is not the same as the previously uploaded data, the data will be
// corrupted.
func (c *Client) RenterUploadStreamRepairPost(r io.Reader, siaPath modules.SiaPath) error {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("repair", strconv.FormatBool(true))
	values.Set("stream", strconv.FormatBool(true))
	_, err := c.postRawResponse(fmt.Sprintf("/renter/uploadstream/%s?%s", sp, values.Encode()), r)
	return err
}

// RenterDirCreatePost uses the /renter/dir/ endpoint to create a directory for the
// renter
func (c *Client) RenterDirCreatePost(siaPath modules.SiaPath) (err error) {
	sp := escapeSiaPath(siaPath)
	err = c.post(fmt.Sprintf("/renter/dir/%s", sp), "action=create", nil)
	return
}

// RenterDirDeletePost uses the /renter/dir/ endpoint to delete a directory for the
// renter
func (c *Client) RenterDirDeletePost(siaPath modules.SiaPath) (err error) {
	sp := escapeSiaPath(siaPath)
	err = c.post(fmt.Sprintf("/renter/dir/%s", sp), "action=delete", nil)
	return
}

// RenterDirRenamePost uses the /renter/dir/ endpoint to rename a directory for the
// renter
func (c *Client) RenterDirRenamePost(siaPath, newSiaPath modules.SiaPath) (err error) {
	sp := escapeSiaPath(siaPath)
	nsp := escapeSiaPath(newSiaPath)
	err = c.post(fmt.Sprintf("/renter/dir/%s?newsiapath=%s", sp, nsp), "action=rename", nil)
	return
}

// RenterGetDir uses the /renter/dir/ endpoint to query a directory
func (c *Client) RenterGetDir(siaPath modules.SiaPath) (rd api.RenterDirectory, err error) {
	sp := escapeSiaPath(siaPath)
	err = c.get(fmt.Sprintf("/renter/dir/%s", sp), &rd)
	return
}

// RenterValidateSiaPathPost uses the /renter/validatesiapath endpoint to
// validate a potential siapath
//
// NOTE: This function specifically takes a string as an argument not a type
// SiaPath
func (c *Client) RenterValidateSiaPathPost(siaPathStr string) (err error) {
	err = c.post(fmt.Sprintf("/renter/validatesiapath/%s", siaPathStr), "", nil)
	return
}

// RenterUploadReadyGet uses the /renter/uploadready endpoint to determine if
// the renter is ready for upload.
func (c *Client) RenterUploadReadyGet(dataPieces, parityPieces uint64) (rur api.RenterUploadReadyGet, err error) {
	strDataPieces := strconv.FormatUint(dataPieces, 10)
	strParityPieces := strconv.FormatUint(parityPieces, 10)
	query := fmt.Sprintf("?datapieces=%v&paritypieces=%v",
		strDataPieces, strParityPieces)
	err = c.get("/renter/uploadready"+query, &rur)
	return
}

// RenterUploadReadyDefaultGet uses the /renter/uploadready endpoint to
// determine if the renter is ready for upload.
func (c *Client) RenterUploadReadyDefaultGet() (rur api.RenterUploadReadyGet, err error) {
	err = c.get("/renter/uploadready", &rur)
	return
}

// RenterFuse uses the /renter/fuse endpoint to return information about the
// current fuse mount point.
func (c *Client) RenterFuse() (fi api.RenterFuseInfo, err error) {
	err = c.get("/renter/fuse", &fi)
	return
}

// RenterFuseMount uses the /renter/fuse/mount endpoint to mount a fuse
// filesystem serving the provided siapath.
func (c *Client) RenterFuseMount(siaPath modules.SiaPath, mount string, readOnly bool) (err error) {
	sp := escapeSiaPath(siaPath)
	values := url.Values{}
	values.Set("siapath", sp)
	values.Set("mount", mount)
	values.Set("readonly", strconv.FormatBool(readOnly))
	err = c.post("/renter/fuse/mount", values.Encode(), nil)
	return
}

// RenterFuseUnmount uses the /renter/fuse/unmount endpoint to unmount the
// currently-mounted fuse filesystem.
func (c *Client) RenterFuseUnmount(mount string) (err error) {
	values := url.Values{}
	values.Set("mount", mount)
	err = c.post("/renter/fuse/unmount", values.Encode(), nil)
	return
}

// RenterPost uses the /renter POST endpoint to set fields of the renter. Values
// are encoded as a query string in the body
func (c *Client) RenterPost(values url.Values) (err error) {
	err = c.post("/renter", values.Encode(), nil)
	return
}
