package api

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/modules/renter/filesystem"
	"go.sia.tech/siad/modules/renter/filesystem/siafile"
	"go.sia.tech/siad/types"
)

const (
	testFunds       = "10000000000000000000000000000" // 10k SC
	testPeriod      = "5"
	testRenewWindow = "2"
)

// createRandFile creates a file on disk and fills it with random bytes.
func createRandFile(path string, size int) error {
	return ioutil.WriteFile(path, fastrand.Bytes(size), 0600)
}

// setupTestDownload creates a server tester with an uploaded file of size
// `size` and name `name`.
func setupTestDownload(t *testing.T, size int, name string, waitOnRedundancy bool) (*serverTester, string) {
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Announce the host and start accepting contracts.
	err = st.setHostStorage()
	if err != nil {
		t.Fatal(err)
	}
	err = st.announceHost()
	if err != nil {
		t.Fatal(err)
	}
	err = st.acceptContracts()
	if err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	testFunds := testFunds
	testPeriod := "10"
	renewWindow := "5"
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", renewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	err = st.stdPostAPI("/renter", allowanceValues)
	if err != nil {
		t.Fatal(err)
	}

	// Create a file.
	path := filepath.Join(build.SiaTestingDir, "api", t.Name(), name)
	err = createRandFile(path, size)
	if err != nil {
		t.Fatal(err)
	}

	// Upload to host.
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	uploadValues.Set("renew", "true")
	uploadValues.Set("datapieces", "1")
	uploadValues.Set("paritypieces", "1")
	err = st.stdPostAPI("/renter/upload/"+name, uploadValues)
	if err != nil {
		t.Fatal(err)
	}

	if waitOnRedundancy {
		// wait for the file to have a redundancy > 1
		err = build.Retry(200, time.Second, func() error {
			var rf RenterFile
			err = st.getAPI("/renter/file/"+name, &rf)
			if err != nil {
				t.Fatal(err)
			}
			if rf.File.Redundancy < 1 {
				return fmt.Errorf("the uploading is not succeeding for some reason: %v", rf.File)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	return st, path
}

// runDownloadTest uploads a file and downloads it using the specified
// parameters, verifying that the parameters are applied correctly and the file
// is downloaded successfully.
func runDownloadTest(t *testing.T, filesize, offset, length int64, useHttpResp bool, testName string) error {
	ulSiaPath, err := modules.NewSiaPath(testName + ".dat")
	if err != nil {
		t.Fatal(err)
	}
	st, path := setupTestDownload(t, int(filesize), ulSiaPath.String(), true)
	defer func() {
		st.server.panicClose()
		err = os.Remove(path)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Read the section to be downloaded from the original file.
	uf, err := os.Open(path) // Uploaded file.
	if err != nil {
		return errors.AddContext(err, "unable to open the uploaded file locally")
	}
	var originalBytes bytes.Buffer
	_, err = uf.Seek(offset, 0)
	if err != nil {
		return errors.AddContext(err, "error when seeking through the local uploaded file")
	}
	_, err = io.CopyN(&originalBytes, uf, length)
	if err != nil {
		return errors.AddContext(err, "error when copying from the local uploaded file")
	}

	// Download the original file from the passed offsets.
	fname := testName + "-download.dat"
	downpath := filepath.Join(st.dir, fname)
	if !useHttpResp {
		defer func() {
			err = os.Remove(downpath)
			if err != nil {
				t.Fatal(err)
			}
		}()
	}

	dlURL := fmt.Sprintf("/renter/download/%s?disablelocalfetch=true&offset=%d&length=%d", ulSiaPath, offset, length)

	var downbytes bytes.Buffer

	if useHttpResp {
		dlURL += "&httpresp=true"
		// Make request.
		resp, err := HttpGET("http://" + st.server.listener.Addr().String() + dlURL)
		if err != nil {
			return errors.AddContext(err, "unable to make an http request")
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
		}()
		if non2xx(resp.StatusCode) {
			return decodeError(resp)
		}
		_, err = io.Copy(&downbytes, resp.Body)
		if err != nil {
			return errors.AddContext(err, "unable to make a copy after the http request")
		}
	} else {
		dlURL += "&destination=" + downpath
		err := st.getAPI(dlURL, nil)
		if err != nil {
			return errors.AddContext(err, "download request failed")
		}
		// wait for the download to complete
		err = build.Retry(30, time.Second, func() error {
			var rdq RenterDownloadQueue
			err = st.getAPI("/renter/downloads", &rdq)
			if err != nil {
				return errors.AddContext(err, "unable to view the download queue")
			}
			for _, download := range rdq.Downloads {
				if download.Received == download.Filesize && download.SiaPath.Equals(ulSiaPath) {
					return nil
				}
			}
			return errors.New("file not downloaded")
		})
		if err != nil {
			t.Fatal(errors.AddContext(err, "download does not appear to have completed"))
		}

		// open the downloaded file
		df, err := os.Open(downpath)
		if err != nil {
			return err
		}
		defer func() {
			if err := df.Close(); err != nil {
				t.Fatal(err)
			}
		}()

		_, err = io.Copy(&downbytes, df)
		if err != nil {
			return err
		}
	}

	// should have correct length
	if int64(downbytes.Len()) != length {
		return fmt.Errorf("downloaded file has incorrect size: %d, %d expected", downbytes.Len(), length)
	}

	// should be byte-for-byte equal to the original uploaded file
	if !bytes.Equal(originalBytes.Bytes(), downbytes.Bytes()) {
		return fmt.Errorf("downloaded content differs from original content")
	}

	return nil
}

// TestRenterDownloadError tests that the /renter/download route sets the
// download's error field if it fails.
func TestRenterDownloadError(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	st, _ := setupTestDownload(t, 1e4, "test.dat", false)
	defer func() {
		if err := st.server.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// don't wait for the upload to complete, try to download immediately to
	// intentionally cause a download error
	downpath := filepath.Join(st.dir, "down.dat")
	expectedErr := st.getAPI("/renter/download/test.dat?disablelocalfetch=true&destination="+downpath, nil)
	if expectedErr == nil {
		t.Fatal("download unexpectedly succeeded")
	}

	// verify the file has the expected error
	var rdq RenterDownloadQueue
	err := st.getAPI("/renter/downloads", &rdq)
	if err != nil {
		t.Fatal(err)
	}
	for _, download := range rdq.Downloads {
		if download.SiaPath.String() == "test.dat" && download.Received == download.Filesize && download.Error == expectedErr.Error() {
			t.Fatal("download had unexpected error: ", download.Error)
		}
	}
}

// TestValidDownloads tests valid and boundary parameter combinations.
func TestValidDownloads(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sectorSize := int64(modules.SectorSize)

	testParams := []struct {
		filesize,
		offset,
		length int64
		useHttpResp bool
		testName    string
	}{
		// file-backed tests.
		{sectorSize, 40, sectorSize - 40, false, "OffsetSingleChunk"},
		{sectorSize * 2, 20, sectorSize*2 - 20, false, "OffsetTwoChunk"},
		{int64(float64(sectorSize) * 2.4), 20, int64(float64(sectorSize)*2.4) - 20, false, "OffsetThreeChunk"},
		{sectorSize, 0, sectorSize / 2, false, "ShortLengthSingleChunk"},
		{sectorSize, sectorSize / 4, sectorSize / 2, false, "ShortLengthAndOffsetSingleChunk"},
		{sectorSize * 2, 0, int64(float64(sectorSize) * 2 * 0.75), false, "ShortLengthTwoChunk"},
		{int64(float64(sectorSize) * 2.7), 0, int64(2.2 * float64(sectorSize)), false, "ShortLengthThreeChunkInThirdChunk"},
		{int64(float64(sectorSize) * 2.7), 0, int64(1.6 * float64(sectorSize)), false, "ShortLengthThreeChunkInSecondChunk"},
		{sectorSize * 5, 0, int64(float64(sectorSize*5) * 0.75), false, "ShortLengthMultiChunk"},
		{sectorSize * 2, 50, int64(float64(sectorSize*2) * 0.75), false, "ShortLengthAndOffsetTwoChunk"},
		{sectorSize * 3, 50, int64(float64(sectorSize*3) * 0.5), false, "ShortLengthAndOffsetThreeChunkInSecondChunk"},
		{sectorSize * 3, 50, int64(float64(sectorSize*3) * 0.75), false, "ShortLengthAndOffsetThreeChunkInThirdChunk"},

		// http response tests.
		{sectorSize, 40, sectorSize - 40, true, "HttpRespOffsetSingleChunk"},
		{sectorSize * 2, 40, sectorSize*2 - 40, true, "HttpRespOffsetTwoChunk"},
		{sectorSize * 5, 40, sectorSize*5 - 40, true, "HttpRespOffsetManyChunks"},
		{sectorSize, 40, 4 * sectorSize / 5, true, "RespOffsetAndLengthSingleChunk"},
		{sectorSize * 2, 80, 3 * (sectorSize * 2) / 4, true, "RespOffsetAndLengthTwoChunk"},
		{sectorSize * 5, 150, 3 * (sectorSize * 5) / 4, true, "HttpRespOffsetAndLengthManyChunks"},
		{sectorSize * 5, 150, sectorSize * 5 / 4, true, "HttpRespOffsetAndLengthManyChunksSubsetOfChunks"},
	}
	for _, params := range testParams {
		params := params
		t.Run(params.testName, func(st *testing.T) {
			st.Parallel()
			err := runDownloadTest(st, params.filesize, params.offset, params.length, params.useHttpResp, params.testName)
			if err != nil {
				st.Fatal(err)
			}
		})
	}
}

func runDownloadParamTest(t *testing.T, length, offset, filesize int) error {
	ulSiaPath := "test.dat"

	st, _ := setupTestDownload(t, filesize, ulSiaPath, true)
	defer func() {
		if err := st.server.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Download the original file from offset 40 and length 10.
	fname := "offsetsinglechunk.dat"
	downpath := filepath.Join(st.dir, fname)
	dlURL := fmt.Sprintf("/renter/download/%s?disablelocalfetch=true&destination=%s", ulSiaPath, downpath)
	dlURL += fmt.Sprintf("&length=%d", length)
	dlURL += fmt.Sprintf("&offset=%d", offset)
	return st.getAPI(dlURL, nil)
}

func TestInvalidDownloadParameters(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	testParams := []struct {
		length   int
		offset   int
		filesize int
		errorMsg string
	}{
		{0, -10, 1e4, "/download not prompting error when passing negative offset."},
		{0, 1e4, 1e4, "/download not prompting error when passing offset equal to filesize."},
		{1e4 + 1, 0, 1e4, "/download not prompting error when passing length exceeding filesize."},
		{1e4 + 11, 10, 1e4, "/download not prompting error when passing length exceeding filesize with non-zero offset."},
		{-1, 0, 1e4, "/download not prompting error when passing negative length."},
	}

	for _, params := range testParams {
		err := runDownloadParamTest(t, params.length, params.offset, params.filesize)
		if err == nil {
			t.Fatal(params.errorMsg)
		}
	}
}

func TestRenterDownloadAsyncAndHttpRespError(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	filesize := 1e4
	ulSiaPath := "test.dat"

	st, _ := setupTestDownload(t, int(filesize), ulSiaPath, true)
	defer func() {
		if err := st.server.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Download the original file from offset 40 and length 10.
	fname := "offsetsinglechunk.dat"
	dlURL := fmt.Sprintf("/renter/download/%s?disablelocalfetch=true&destination=%s&async=true&httpresp=true", ulSiaPath, fname)
	err := st.getAPI(dlURL, nil)
	if err == nil {
		t.Fatalf("/download not prompting error when only passing both async and httpresp fields.")
	}
}

func TestRenterDownloadAsyncNonexistentFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.server.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	downpath := filepath.Join(st.dir, "testfile")
	err = st.getAPI(fmt.Sprintf("/renter/downloadasync/doesntexist?destination=%v", downpath), nil)
	if err == nil {
		t.Error("should not be able to download a file that does not exist")
	}
}

func TestRenterDownloadAsyncAndNotDestinationError(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	filesize := 1e4
	ulSiaPath := "test.dat"

	st, _ := setupTestDownload(t, int(filesize), ulSiaPath, true)
	defer func() {
		if err := st.server.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Download the original file from offset 40 and length 10.
	dlURL := fmt.Sprintf("/renter/download/%s?disablelocalfetch=true&async=true", ulSiaPath)
	err := st.getAPI(dlURL, nil)
	if err == nil {
		t.Fatal("/download not prompting error when async is specified but destination is empty.")
	}
}

func TestRenterDownloadHttpRespAndDestinationError(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	filesize := 1e4
	ulSiaPath := "test.dat"

	st, _ := setupTestDownload(t, int(filesize), ulSiaPath, true)
	defer func() {
		if err := st.server.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Download the original file from offset 40 and length 10.
	fname := "test.dat"
	dlURL := fmt.Sprintf("/renter/download/%s?disablelocalfetch=true&destination=%shttpresp=true", ulSiaPath, fname)
	err := st.getAPI(dlURL, nil)
	if err == nil {
		t.Fatal("/download not prompting error when httpresp is specified and destination is non-empty.")
	}
}

// TestRenterAsyncDownloadError tests that the /renter/asyncdownload route sets
// the download's error field if it fails.
func TestRenterAsyncDownloadError(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	st, _ := setupTestDownload(t, 1e4, "test.dat", false)
	defer st.server.panicClose()

	// don't wait for the upload to complete, try to download immediately to
	// intentionally cause a download error
	downpath := filepath.Join(st.dir, "asyncdown.dat")
	err := st.getAPI("/renter/downloadasync/test.dat?disablelocalfetch=true&destination="+downpath, nil)
	if err != nil {
		t.Fatal(err)
	}

	// verify the file has an error
	var rdq RenterDownloadQueue
	err = st.getAPI("/renter/downloads", &rdq)
	if err != nil {
		t.Fatal(err)
	}
	for _, download := range rdq.Downloads {
		if download.SiaPath.String() == "test.dat" && download.Received == download.Filesize && download.Error == "" {
			t.Fatal("download had nil error")
		}
	}
}

// TestRenterAsyncDownload tests that the /renter/downloadasync route works
// correctly.
func TestRenterAsyncDownload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	st, _ := setupTestDownload(t, 1e4, "test.dat", true)
	defer st.server.panicClose()

	// Download the file asynchronously.
	downpath := filepath.Join(st.dir, "asyncdown.dat")
	err := st.getAPI("/renter/downloadasync/test.dat?disablelocalfetch=true&destination="+downpath, nil)
	if err != nil {
		t.Fatal(err)
	}

	// download should eventually complete
	var rdq RenterDownloadQueue
	success := false
	for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(time.Millisecond * 10) {
		err = st.getAPI("/renter/downloads", &rdq)
		if err != nil {
			t.Fatal(err)
		}
		for _, download := range rdq.Downloads {
			if download.Received == download.Filesize && download.SiaPath.String() == "test.dat" {
				success = true
			}
		}
		if success {
			break
		}
	}
	if !success {
		t.Fatal("/renter/downloadasync did not download our test file")
	}
}

// TestRenterPaths tests that the /renter routes handle path parameters
// properly.
func TestRenterPaths(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host.
	err = st.announceHost()
	if err != nil {
		t.Fatal(err)
	}

	// Create a file.
	path := filepath.Join(build.SiaTestingDir, "api", t.Name(), "test.dat")
	err = createRandFile(path, 1024)
	if err != nil {
		t.Fatal(err)
	}

	// Upload to host.
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	uploadValues.Set("renew", "true")
	err = st.stdPostAPI("/renter/upload/foo/bar/test", uploadValues)
	if err != nil {
		t.Fatal(err)
	}

	// File should be listed by the renter.
	var rf RenterFiles
	err = st.getAPI("/renter/files", &rf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.Files) != 1 || rf.Files[0].SiaPath.String() != "foo/bar/test" {
		t.Fatal("/renter/files did not return correct file:", rf)
	}
}

// TestRenterConflicts tests that the renter handles naming conflicts properly.
func TestRenterConflicts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host.
	err = st.announceHost()
	if err != nil {
		t.Fatal(err)
	}

	// Create a file.
	path := filepath.Join(build.SiaTestingDir, "api", t.Name(), "test.dat")
	err = createRandFile(path, 1024)
	if err != nil {
		t.Fatal(err)
	}

	// Upload to host, using a path designed to cause conflicts. The renter
	// should automatically create a folder called foo/bar.sia. Later, we'll
	// exploit this by uploading a file called foo/bar.
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	uploadValues.Set("renew", "true")
	err = st.stdPostAPI("/renter/upload/foo/bar.sia/test", uploadValues)
	if err != nil {
		t.Fatal(err)
	}

	// File should be listed by the renter.
	var rf RenterFiles
	err = st.getAPI("/renter/files", &rf)
	if err != nil {
		t.Fatal(err)
	}
	if len(rf.Files) != 1 || rf.Files[0].SiaPath.String() != "foo/bar.sia/test" {
		t.Fatal("/renter/files did not return correct file:", rf)
	}

	// Upload using the same nickname.
	err = st.stdPostAPI("/renter/upload/foo/bar.sia/test", uploadValues)
	if err == nil {
		t.Fatalf("expected %v, got %v", Error{"upload failed: " + filesystem.ErrExists.Error()}, err)
	}

	// Upload using nickname that conflicts with folder.
	err = st.stdPostAPI("/renter/upload/foo/bar", uploadValues)
	if err == nil {
		t.Fatal("expecting conflict error, got nil")
	}
}

// TestRenterHandlerContracts checks that contract formation between a host and
// renter behaves as expected, and that contract spending is the right amount.
func TestRenterHandlerContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// The renter should not have any contracts yet.
	var contracts RenterContracts
	if err = st.getAPI("/renter/contracts", &contracts); err != nil {
		t.Fatal(err)
	}
	if len(contracts.Contracts) != 0 {
		t.Fatalf("expected renter to have 0 contracts; got %v", len(contracts.Contracts))
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	allowanceValues.Set("expectedstorage", fmt.Sprint(modules.DefaultAllowance.ExpectedStorage))
	allowanceValues.Set("expectedupload", fmt.Sprint(modules.DefaultAllowance.ExpectedStorage))
	allowanceValues.Set("expecteddownload", fmt.Sprint(modules.DefaultAllowance.ExpectedStorage))
	allowanceValues.Set("expectedredundancy", fmt.Sprint(modules.DefaultAllowance.ExpectedRedundancy))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	// Block until the allowance has finished forming contracts.
	err = build.Retry(50, time.Millisecond*250, func() error {
		var rc RenterContracts
		err = st.getAPI("/renter/contracts", &rc)
		if err != nil {
			return errors.New("couldn't get renter stats")
		}
		if len(rc.Contracts) == 0 {
			return errors.New("no contracts")
		}
		if len(rc.Contracts) > 1 {
			return errors.New("more than one contract")
		}
		return nil
	})
	if err != nil {
		t.Fatal("allowance setting failed:", err)
	}

	// The renter should now have 1 contract.
	if err = st.getAPI("/renter/contracts", &contracts); err != nil {
		t.Fatal(err)
	}
	if len(contracts.Contracts) != 1 {
		t.Fatalf("expected renter to have 1 contract; got %v", len(contracts.Contracts))
	}
	if !contracts.Contracts[0].GoodForUpload || !contracts.Contracts[0].GoodForRenew {
		t.Errorf("expected contract to be good for upload and renew")
	}

	// Check the renter's contract spending.
	var get RenterGET
	if err = st.getAPI("/renter", &get); err != nil {
		t.Fatal(err)
	}
	expectedContractSpending := types.ZeroCurrency
	for _, contract := range contracts.Contracts {
		expectedContractSpending = expectedContractSpending.Add(contract.TotalCost)
	}
	if got := get.FinancialMetrics.TotalAllocated; got.Cmp(expectedContractSpending) != 0 {
		t.Fatalf("expected contract spending to be %v; got %v", expectedContractSpending, got)
	}
}

// TestRenterHandlerGetAndPost checks that valid /renter calls successfully set
// allowance values, while /renter calls with invalid allowance values are
// correctly handled.
func TestRenterHandlerGetAndPost(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	// Check that a call to /renter returns the expected values.
	var get RenterGET
	if err = st.getAPI("/renter", &get); err != nil {
		t.Fatal(err)
	}
	// Check the renter's funds.
	expectedFunds, ok := scanAmount(testFunds)
	if !ok {
		t.Fatal("scanAmount failed")
	}
	if got := get.Settings.Allowance.Funds; got.Cmp(expectedFunds) != 0 {
		t.Fatalf("expected funds to be %v; got %v", expectedFunds, got)
	}
	// Check the renter's period.
	intPeriod, err := strconv.Atoi(testPeriod)
	if err != nil {
		t.Fatal(err)
	}
	expectedPeriod := types.BlockHeight(intPeriod)
	if got := get.Settings.Allowance.Period; got != expectedPeriod {
		t.Fatalf("expected period to be %v; got %v", expectedPeriod, got)
	}
	// Check the renter's renew window.
	expectedRenewWindow := expectedPeriod / 2
	if got := get.Settings.Allowance.RenewWindow; got != expectedRenewWindow {
		t.Fatalf("expected renew window to be %v; got %v", expectedRenewWindow, got)
	}
	// Try an invalid period string.
	allowanceValues.Set("period", "-1")
	err = st.stdPostAPI("/renter", allowanceValues)
	if err == nil || !strings.Contains(err.Error(), "unable to parse period") {
		t.Errorf("expected error to begin with 'unable to parse period'; got %v", err)
	}
	// Try to set a zero renew window
	allowanceValues.Set("period", "2")
	allowanceValues.Set("renewwindow", "0")
	err = st.stdPostAPI("/renter", allowanceValues)
	if err == nil || err.Error() != contractor.ErrAllowanceZeroWindow.Error() {
		t.Errorf("expected error to be %v, got %v", contractor.ErrAllowanceZeroWindow, err)
	}
	// Try to set a negative bandwidth limit
	allowanceValues.Set("maxdownloadspeed", "-1")
	allowanceValues.Set("renewwindow", "1")
	err = st.stdPostAPI("/renter", allowanceValues)
	if err == nil {
		t.Errorf("expected error to be 'download/upload rate limit...'; got %v", err)
	}
	allowanceValues.Set("maxuploadspeed", "-1")
	err = st.stdPostAPI("/renter", allowanceValues)
	if err == nil {
		t.Errorf("expected error to be 'download/upload rate limit...'; got %v", err)
	}
}

// TestRenterLoadNonexistent checks that attempting to upload or download a
// nonexistent file triggers the appropriate error.
func TestRenterLoadNonexistent(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	// Try uploading a nonexistent file.
	fakepath := filepath.Join(st.dir, "dne.dat")
	uploadValues := url.Values{}
	uploadValues.Set("source", fakepath)
	err = st.stdPostAPI("/renter/upload/dne", uploadValues)
	if err == nil {
		t.Errorf("expected error when uploading nonexistent file")
	}

	// Try downloading a nonexistent file.
	downpath := filepath.Join(st.dir, "dnedown.dat")
	err = st.stdGetAPI("/renter/download/dne?disablelocalfetch=true&destination=" + downpath)
	if err == nil {
		t.Error("should not be able to download non-existent file")
	}

	// The renter's downloads queue should be empty.
	var queue RenterDownloadQueue
	if err = st.getAPI("/renter/downloads", &queue); err != nil {
		t.Fatal(err)
	}
	if len(queue.Downloads) != 0 {
		t.Fatalf("expected renter to have 0 downloads in the queue; got %v", len(queue.Downloads))
	}
}

// TestRenterHandlerRename checks that valid /renter/rename calls are
// successful, and that invalid calls fail with the appropriate error.
func TestRenterHandlerRename(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// Try renaming a nonexistent file.
	renameValues := url.Values{}
	renameValues.Set("newsiapath", "newdne")
	err = st.stdPostAPI("/renter/rename/dne", renameValues)
	if err == nil || err.Error() != filesystem.ErrNotExist.Error() {
		t.Errorf("Expected '%v' got '%v'", filesystem.ErrNotExist, err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	// Block until the allowance has finished forming contracts.
	err = build.Retry(50, time.Millisecond*250, func() error {
		var rc RenterContracts
		err = st.getAPI("/renter/contracts", &rc)
		if err != nil {
			return errors.New("couldn't get renter stats")
		}
		if len(rc.Contracts) != 1 {
			return errors.New("no contracts")
		}
		return nil
	})
	if err != nil {
		t.Fatal("allowance setting failed")
	}

	// Create a file.
	path1 := filepath.Join(st.dir, "test1.dat")
	if err = createRandFile(path1, 512); err != nil {
		t.Fatal(err)
	}

	// Upload to host.
	uploadValues := url.Values{}
	uploadValues.Set("source", path1)
	if err = st.stdPostAPI("/renter/upload/test1", uploadValues); err != nil {
		t.Fatal(err)
	}

	// Try renaming to an empty string.
	renameValues.Set("newsiapath", "")
	err = st.stdPostAPI("/renter/rename/test1", renameValues)
	if err == nil || !strings.Contains(err.Error(), modules.ErrEmptyPath.Error()) {
		t.Fatalf("expected error to contain %v; got %v", modules.ErrEmptyPath, err)
	}

	// Rename the file.
	renameValues.Set("newsiapath", "newtest1")
	if err = st.stdPostAPI("/renter/rename/test1", renameValues); err != nil {
		t.Fatal(err)
	}

	// Should be able to continue uploading and downloading using the new name.
	var rf RenterFiles
	for i := 0; i < 200 && (len(rf.Files) != 1 || rf.Files[0].UploadProgress < 10); i++ {
		err = st.getAPI("/renter/files", &rf)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(rf.Files) != 1 || rf.Files[0].UploadProgress < 10 {
		t.Fatal("upload is not succeeding:", rf.Files[0])
	}
	err = st.stdGetAPI("/renter/download/newtest1?disablelocalfetch=true&destination=" + filepath.Join(st.dir, "testdown2.dat"))
	if err != nil {
		t.Fatal(err)
	}

	// Create and upload another file.
	path2 := filepath.Join(st.dir, "test2.dat")
	if err = createRandFile(path2, 512); err != nil {
		t.Fatal(err)
	}
	uploadValues.Set("source", path2)
	if err = st.stdPostAPI("/renter/upload/test2", uploadValues); err != nil {
		t.Fatal(err)
	}
	// Try renaming to a name that's already taken.
	renameValues.Set("newsiapath", "newtest1")
	err = st.stdPostAPI("/renter/rename/test2", renameValues)
	if err == nil || err.Error() != filesystem.ErrExists.Error() {
		t.Errorf("expected error to be %v; got %v", siafile.ErrPathOverload, err)
	}
}

// TestRenterHandlerDelete checks that deleting a valid file from the renter
// goes as planned and that attempting to delete a nonexistent file fails with
// the appropriate error.
func TestRenterHandlerDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	// Create a file.
	path := filepath.Join(st.dir, "test.dat")
	if err = createRandFile(path, 1024); err != nil {
		t.Fatal(err)
	}

	// Upload to host.
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	if err = st.stdPostAPI("/renter/upload/test", uploadValues); err != nil {
		t.Fatal(err)
	}

	// Delete the file.
	if err = st.stdPostAPI("/renter/delete/test", url.Values{}); err != nil {
		t.Fatal(err)
	}

	// The renter's list of files should now be empty.
	var files RenterFiles
	if err = st.getAPI("/renter/files", &files); err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 0 {
		t.Fatalf("renter's list of files should be empty; got %v instead", files)
	}
	// Try deleting a nonexistent file.
	err = st.stdPostAPI("/renter/delete/dne", url.Values{})
	// NOTE: using strings.Contains because errors.Contains does not recognize
	// errors when errors.Extend is used
	if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Errorf("Expected error to contain %v but got '%v'", filesystem.ErrNotExist, err)
	}
}

// Tests that the /renter/upload call checks for relative paths.
func TestRenterRelativePathErrorUpload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	renterUploadAbsoluteError := "source must be an absolute path"

	// Create a file.
	path := filepath.Join(st.dir, "test.dat")
	if err = createRandFile(path, 1024); err != nil {
		t.Fatal(err)
	}

	// This should fail.
	uploadValues := url.Values{}
	uploadValues.Set("source", "test.dat")
	if err = st.stdPostAPI("/renter/upload/test", uploadValues); err.Error() != renterUploadAbsoluteError {
		t.Fatal(err)
	}

	// As should this.
	uploadValues = url.Values{}
	uploadValues.Set("source", "../test.dat")
	if err = st.stdPostAPI("/renter/upload/test", uploadValues); err.Error() != renterUploadAbsoluteError {
		t.Fatal(err)
	}

	// This should succeed.
	uploadValues = url.Values{}
	uploadValues.Set("source", path)
	if err = st.stdPostAPI("/renter/upload/test", uploadValues); err != nil {
		t.Fatal(err)
	}
}

// Tests that the /renter/download call checks for relative paths.
func TestRenterRelativePathErrorDownload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and start accepting contracts.
	if err = st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err := st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.acceptContracts(); err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	renterDownloadAbsoluteError := "download creation failed: destination must be an absolute path"

	// Create a file, and upload it.
	path := filepath.Join(st.dir, "test.dat")
	if err = createRandFile(path, 1024); err != nil {
		t.Fatal(err)
	}
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	if err = st.stdPostAPI("/renter/upload/test", uploadValues); err != nil {
		t.Fatal(err)
	}
	var rf RenterFiles
	for i := 0; i < 200 && (len(rf.Files) != 1 || rf.Files[0].UploadProgress < 10); i++ {
		err = st.getAPI("/renter/files", &rf)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if len(rf.Files) != 1 || rf.Files[0].UploadProgress < 10 {
		t.Fatal("the uploading is not succeeding for some reason:", rf.Files[0])
	}

	// Use a relative destination, which should fail.
	downloadPath := "test1.dat"
	if err = st.stdGetAPI("/renter/download/test?disablelocalfetch=true&destination=" + downloadPath); err.Error() != renterDownloadAbsoluteError {
		t.Fatal(err)
	}

	// Relative destination stepping backwards should also fail.
	downloadPath = "../test1.dat"
	if err = st.stdGetAPI("/renter/download/test?disablelocalfetch=true&destination=" + downloadPath); err.Error() != renterDownloadAbsoluteError {
		t.Fatal(err)
	}

	// Long relative destination should also fail (just missing leading slash).
	downloadPath = filepath.Join(st.dir[1:], "test1.dat")
	err = st.stdGetAPI("/renter/download/test?disablelocalfetch=true&destination=" + downloadPath)
	if err == nil {
		t.Fatal("expecting an error")
	}

	// Full destination should succeed.
	downloadPath = filepath.Join(st.dir, "test1.dat")
	err = st.stdGetAPI("/renter/download/test?disablelocalfetch=true&destination=" + downloadPath)
	if err != nil {
		t.Fatal("expecting an error")
	}
}

// TestRenterPricesHandler checks that the prices command returns reasonable
// values given the settings of the hosts.
func TestRenterPricesHandler(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and then get the calculated prices for when there is a
	// single host.
	var rpeSingle modules.RenterPriceEstimation
	if err := st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err = st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.getAPI("/renter/prices", &rpeSingle); err != nil {
		t.Fatal(err)
	}

	// Create several more hosts all using the default settings.
	stHost1, err := blankServerTester(t.Name() + " - Host 1")
	if err != nil {
		t.Fatal(err)
	}
	defer stHost1.panicClose()
	if err := stHost1.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	stHost2, err := blankServerTester(t.Name() + " - Host 2")
	if err != nil {
		t.Fatal(err)
	}
	defer stHost2.panicClose()
	if err := stHost2.setHostStorage(); err != nil {
		t.Fatal(err)
	}

	// Connect all the nodes and announce all of the hosts.
	sts := []*serverTester{st, stHost1, stHost2}
	err = fullyConnectNodes(sts)
	if err != nil {
		t.Fatal(err)
	}
	err = fundAllNodes(sts)
	if err != nil {
		t.Fatal(err)
	}
	err = announceAllHosts(sts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Grab the price estimates for when there are a bunch of hosts with the
	// same stats.
	var rpeMulti modules.RenterPriceEstimation
	if err = st.getAPI("/renter/prices", &rpeMulti); err != nil {
		t.Fatal(err)
	}

	// Verify that the aggregate is the same.
	if !rpeMulti.DownloadTerabyte.Equals(rpeSingle.DownloadTerabyte) {
		t.Log(rpeMulti.DownloadTerabyte)
		t.Log(rpeSingle.DownloadTerabyte)
		t.Error("price changed from single to multi")
	}
	if rpeMulti.FormContracts.Equals(rpeSingle.FormContracts) {
		t.Error("price of forming contracts should have increased from single to multi")
	}
	if !rpeMulti.StorageTerabyteMonth.Equals(rpeSingle.StorageTerabyteMonth) {
		t.Error("price changed from single to multi")
	}
	if !rpeMulti.UploadTerabyte.Equals(rpeSingle.UploadTerabyte) {
		t.Error("price changed from single to multi")
	}
}

// TestRenterPricesHandlerPricey checks that the prices command returns
// reasonable values given the settings of the hosts.
func TestRenterPricesHandlerPricey(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// Announce the host and then get the calculated prices for when there is a
	// single host.
	var rpeSingle modules.RenterPriceEstimation
	if err := st.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	if err = st.announceHost(); err != nil {
		t.Fatal(err)
	}
	if err = st.getAPI("/renter/prices", &rpeSingle); err != nil {
		t.Fatal(err)
	}

	// Create several more hosts all using the default settings.
	stHost1, err := blankServerTester(t.Name() + " - Host 1")
	if err != nil {
		t.Fatal(err)
	}
	if err := stHost1.setHostStorage(); err != nil {
		t.Fatal(err)
	}
	stHost2, err := blankServerTester(t.Name() + " - Host 2")
	if err != nil {
		t.Fatal(err)
	}
	if err := stHost2.setHostStorage(); err != nil {
		t.Fatal(err)
	}

	var hg HostGET
	err = st.getAPI("/host", &hg)
	if err != nil {
		t.Fatal(err)
	}
	err = stHost1.getAPI("/host", &hg)
	if err != nil {
		t.Fatal(err)
	}
	err = stHost2.getAPI("/host", &hg)
	if err != nil {
		t.Fatal(err)
	}

	// Set host 2 to be more expensive than the rest by a substantial amount. This
	// should result in an increase for the price estimation.
	vals := url.Values{}
	vals.Set("mindownloadbandwidthprice", "100000000000000000000")
	vals.Set("mincontractprice", "1000000000000000000000000")
	vals.Set("minstorageprice", "1000000000000000000000")
	vals.Set("minuploadbandwidthprice", "100000000000000000000")
	err = stHost2.stdPostAPI("/host", vals)
	if err != nil {
		t.Fatal(err)
	}

	// Connect all the nodes and announce all of the hosts.
	sts := []*serverTester{st, stHost1, stHost2}
	err = fullyConnectNodes(sts)
	if err != nil {
		t.Fatal(err)
	}
	err = fundAllNodes(sts)
	if err != nil {
		t.Fatal(err)
	}
	err = announceAllHosts(sts)
	if err != nil {
		t.Fatal(err)
	}

	// Grab the price estimates for when there are a bunch of hosts with the
	// same stats.
	var rpeMulti modules.RenterPriceEstimation
	if err = st.getAPI("/renter/prices", &rpeMulti); err != nil {
		t.Fatal(err)
	}

	// Verify that the estimate for downloading, uploading, and storing
	// increased but the estimate for formaing contracts decreased. Forming
	// contracts decreases because with a more expensive host you are not able
	// to store as much data therefore reducing the amount of host collateral
	// you have to pay the siafund fee on
	if !(rpeMulti.DownloadTerabyte.Cmp(rpeSingle.DownloadTerabyte) > 0) {
		t.Log("Multi DownloadTerabyte cost:", rpeMulti.DownloadTerabyte.HumanString())
		t.Log("Single DownloadTerabyte cost:", rpeSingle.DownloadTerabyte.HumanString())
		t.Error("price did not increase from single to multi")
	}
	if rpeMulti.FormContracts.Cmp(rpeSingle.FormContracts) > 0 {
		t.Log("Multi FormContracts cost:", rpeMulti.FormContracts.HumanString())
		t.Log("Single FormContracts cost:", rpeSingle.FormContracts.HumanString())
		t.Error("price did not drop from single to multi")
	}
	if !(rpeMulti.StorageTerabyteMonth.Cmp(rpeSingle.StorageTerabyteMonth) > 0) {
		t.Log("Multi StorageTerabyteMonth cost:", rpeMulti.StorageTerabyteMonth.HumanString())
		t.Log("Single StorageTerabyteMonth cost:", rpeSingle.StorageTerabyteMonth.HumanString())
		t.Error("price did not increase from single to multi")
	}
	if !(rpeMulti.UploadTerabyte.Cmp(rpeSingle.UploadTerabyte) > 0) {
		t.Log("Multi UploadTerabyte cost:", rpeMulti.UploadTerabyte.HumanString())
		t.Log("Single UploadTerabyte cost:", rpeSingle.UploadTerabyte.HumanString())
		t.Error("price did not increase from single to multi")
	}
}

// TestAdversarialPriceRenewal verifies that host cannot maliciously raise
// their storage price in order to trigger a premature file contract renewal.
func TestAdversarialPriceRenewal(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()
	st, err := createServerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer st.server.panicClose()

	// announce our host
	err = st.acceptContracts()
	if err != nil {
		t.Fatal(err)
	}
	err = st.setHostStorage()
	if err != nil {
		t.Fatal(err)
	}
	err = st.announceHost()
	if err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	testPeriod := "10000"
	allowanceValues.Set("funds", types.SiacoinPrecision.Mul64(10000).String())
	allowanceValues.Set("period", testPeriod)
	err = st.stdPostAPI("/renter", allowanceValues)
	if err != nil {
		t.Fatal(err)
	}

	// wait until we have a contract
	err = build.Retry(500, time.Millisecond*50, func() error {
		if len(st.renter.Contracts()) >= 1 {
			return nil
		}
		return errors.New("no renter contracts")
	})
	if err != nil {
		t.Fatal(err)
	}

	// upload a file
	path := filepath.Join(st.dir, "randUploadFile")
	size := int(modules.SectorSize * 50)
	err = createRandFile(path, size)
	if err != nil {
		t.Fatal(err)
	}
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	uploadValues.Set("renew", "true")
	uploadValues.Set("datapieces", "1")
	uploadValues.Set("paritypieces", "1")
	err = st.stdPostAPI("/renter/upload/"+filepath.Base(path), uploadValues)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, time.Millisecond*500, func() error {
		// mine blocks each iteration to trigger contract maintenance
		_, err = st.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
		available := true
		err := st.renter.FileList(modules.RootSiaPath(), true, false, func(fi modules.FileInfo) {
			if !fi.Available {
				available = false
			}
		})
		if err != nil {
			return err
		}
		if !available {
			return errors.New("file did not complete uploading")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	initialID := st.renter.Contracts()[0].ID

	// jack up the host's storage price to try to trigger a renew
	settings := st.host.InternalSettings()
	settings.MinStoragePrice = settings.MinStoragePrice.Mul64(10800000000)
	err = st.host.SetInternalSettings(settings)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		_, err = st.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
		if st.renter.Contracts()[0].ID != initialID {
			t.Fatal("changing host price caused renew")
		}
		time.Sleep(time.Millisecond * 100)
	}
}

// TestHealthLoop probes the code involved with threadedUpdateRenterHealth to
// verify the health information stored in the siadir metadata is getting
// updated as the health of the files changes
func TestHealthLoop(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter and hosts
	st1, err := createServerTester(t.Name() + "1")
	if err != nil {
		t.Fatal(err)
	}
	defer st1.server.panicClose()
	if err = st1.announceHost(); err != nil {
		t.Fatal(err)
	}
	st2, err := createServerTester(t.Name() + "2")
	if err != nil {
		t.Fatal(err)
	}

	// Connect the testers to eachother so that they are all on the same
	// blockchain.
	testGroup := []*serverTester{st1, st2}
	err = fullyConnectNodes(testGroup)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure that every wallet has money in it.
	err = fundAllNodes(testGroup)
	if err != nil {
		t.Fatal(err)
	}
	// Add storage to every host.
	err = addStorageToAllHosts(testGroup)
	if err != nil {
		t.Fatal(err)
	}
	// Announce the hosts.
	err = announceAllHosts(testGroup)
	if err != nil {
		t.Fatal(err)
	}

	// Set an allowance for the renter, allowing a contract to be formed.
	allowanceValues := url.Values{}
	allowanceValues.Set("funds", testFunds)
	allowanceValues.Set("period", testPeriod)
	allowanceValues.Set("renewwindow", testRenewWindow)
	allowanceValues.Set("hosts", fmt.Sprint(modules.DefaultAllowance.Hosts))
	if err = st1.stdPostAPI("/renter", allowanceValues); err != nil {
		t.Fatal(err)
	}

	// Block until the allowance has finished forming contracts.
	err = build.Retry(600, 100*time.Millisecond, func() error {
		var rc RenterContracts
		err = st1.getAPI("/renter/contracts", &rc)
		if err != nil {
			return errors.New("couldn't get renter stats")
		}
		if len(rc.Contracts) != 2 {
			return errors.New("no contracts")
		}
		return nil
	})
	if err != nil {
		t.Fatal("allowance setting failed")
	}

	// Create and upload files
	path := filepath.Join(st1.dir, "test.dat")
	if err = createRandFile(path, 1024); err != nil {
		t.Fatal(err)
	}

	// Upload to host.
	uploadValues := url.Values{}
	uploadValues.Set("source", path)
	uploadValues.Set("datapieces", "1")
	uploadValues.Set("paritypieces", "1")
	if err = st1.stdPostAPI("/renter/upload/test", uploadValues); err != nil {
		t.Fatal(err)
	}

	// redundancy should reach 2
	err = build.Retry(600, 100*time.Millisecond, func() error {
		var rf RenterFiles
		st1.getAPI("/renter/files", &rf)
		if len(rf.Files) >= 1 && rf.Files[0].Redundancy == 2 {
			return nil
		}
		return fmt.Errorf("file not uploaded, %v files found and redundancy of first file is %v", len(rf.Files), rf.Files[0].Redundancy)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify folder metadata is updated, directory health should be 0
	err = build.Retry(600, 100*time.Millisecond, func() error {
		var rd RenterDirectory
		err := st1.getAPI("/renter/dir/", &rd)
		if err != nil {
			return err
		}
		if rd.Directories[0].Health != 0 {
			return fmt.Errorf("directory health should be 0 but was %v", rd.Directories[0].Health)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Take Down a host
	st2.server.panicClose()

	// redundancy should drop
	err = build.Retry(600, 100*time.Millisecond, func() error {
		var rf RenterFiles
		st1.getAPI("/renter/files", &rf)
		if len(rf.Files) >= 1 && rf.Files[0].Redundancy == 2 {
			return fmt.Errorf("expected 1 file with a redundancy of less than 2, got %v files and redundancy of %v", len(rf.Files), rf.Files[0].Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check that the metadata has been updated
	err = build.Retry(600, 100*time.Millisecond, func() error {
		var rd RenterDirectory
		st1.getAPI("/renter/dir/", &rd)
		if rd.Directories[0].MaxHealth == 0 {
			return fmt.Errorf("directory max health should have dropped below 0 but was %v", rd.Directories[0].MaxHealth)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
