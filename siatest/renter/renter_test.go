package renter

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/modules/renter/contractor"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// test is a helper struct for running subtests when tests can use the same test
// group
type test struct {
	name string
	test func(*testing.T, *siatest.TestGroup)
}

// runRenterTests is a helper function to run the subtests when tests can use
// the same test group
func runRenterTests(t *testing.T, gp siatest.GroupParams, tests []test) error {
	tg, err := siatest.NewGroupFromTemplate(renterTestDir(t.Name()), gp)
	if err != nil {
		return errors.AddContext(err, "failed to create group")
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Run subtests
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.test(t, tg)
		})
	}
	return nil
}

// TestRenter executes a number of subtests using the same TestGroup to
// save time on initialization
func TestRenter(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for the subtests
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}

	// Specify subtests to run
	subTests := []test{
		{"TestDownloadMultipleLargeSectors", testDownloadMultipleLargeSectors},
		{"TestLocalRepair", testLocalRepair},
		{"TestClearDownloadHistory", testClearDownloadHistory},
		{"TestSetFileTrackingPath", testSetFileTrackingPath},
		{"TestDownloadAfterRenew", testDownloadAfterRenew},
		{"TestDirectories", testDirectories},
	}

	// Run tests
	if err := runRenterTests(t, groupParams, subTests); err != nil {
		t.Fatal(err)
	}
}

// TestRenterTwo executes a number of subtests using the same TestGroup to
// save time on initialization
func TestRenterTwo(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for the subtests
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}

	// Specify subtests to run
	subTests := []test{
		{"TestReceivedFieldEqualsFileSize", testReceivedFieldEqualsFileSize},
		{"TestRemoteRepair", testRemoteRepair},
		{"TestSingleFileGet", testSingleFileGet},
		{"TestSiaFileTimestamps", testSiafileTimestamps},
		{"TestZeroByteFile", testZeroByteFile},
		{"TestUploadWithAndWithoutForceParameter", testUploadWithAndWithoutForceParameter},
	}

	// Run tests
	if err := runRenterTests(t, groupParams, subTests); err != nil {
		t.Fatal(err)
	}
}

// testSiafileTimestamps tests if timestamps are set correctly when creating,
// uploading, downloading and modifying a file.
func testSiafileTimestamps(t *testing.T, tg *siatest.TestGroup) {
	// Grab the renter.
	r := tg.Renters()[0]

	// Get the current time.
	beforeUploadTime := time.Now()

	// Upload a new file.
	_, rf, err := r.UploadNewFileBlocking(100+siatest.Fuzz(), 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// Get the time again.
	afterUploadTime := time.Now()

	// Get the timestamps using the API.
	fi, err := r.File(rf)
	if err != nil {
		t.Fatal(err)
	}

	// The timestamps should all be between beforeUploadTime and
	// afterUploadTime.
	if fi.CreateTime.Before(beforeUploadTime) || fi.CreateTime.After(afterUploadTime) {
		t.Fatal("CreateTime was not within the correct interval")
	}
	if fi.AccessTime.Before(beforeUploadTime) || fi.AccessTime.After(afterUploadTime) {
		t.Fatal("AccessTime was not within the correct interval")
	}
	if fi.ChangeTime.Before(beforeUploadTime) || fi.ChangeTime.After(afterUploadTime) {
		t.Fatal("ChangeTime was not within the correct interval")
	}
	if fi.ModTime.Before(beforeUploadTime) || fi.ModTime.After(afterUploadTime) {
		t.Fatal("ModTime was not within the correct interval")
	}

	// After uploading a file the AccessTime, ChangeTime and ModTime should be
	// the same.
	if fi.AccessTime != fi.ChangeTime || fi.ChangeTime != fi.ModTime {
		t.Fatal("AccessTime, ChangeTime and ModTime are not the same")
	}

	// The CreateTime should precede the other timestamps.
	if fi.CreateTime.After(fi.AccessTime) {
		t.Fatal("CreateTime should before other timestamps")
	}

	// Get the time before starting the download.
	beforeDownloadTime := time.Now()

	// Download the file.
	_, err = r.DownloadByStream(rf)
	if err != nil {
		t.Fatal(err)
	}

	// Get the time after the download is done.
	afterDownloadTime := time.Now()

	// Get the timestamps using the API.
	fi2, err := r.File(rf)
	if err != nil {
		t.Fatal(err)
	}

	// Only the AccessTime should have changed.
	if fi2.AccessTime.Before(beforeDownloadTime) || fi2.AccessTime.After(afterDownloadTime) {
		t.Fatal("AccessTime was not within the correct interval")
	}
	if fi.CreateTime != fi2.CreateTime {
		t.Fatal("CreateTime changed after download")
	}
	if fi.ChangeTime != fi2.ChangeTime {
		t.Fatal("ChangeTime changed after download")
	}
	if fi.ModTime != fi2.ModTime {
		t.Fatal("ModTime changed after download")
	}

	// TODO Once we can change the localPath using the API, check that it only
	// changes the ChangeTime to do so.

	// Get the time before renaming.
	beforeRenameTime := time.Now()

	// Rename the file and check that only the ChangeTime changed.
	rf, err = r.Rename(rf, "newsiapath")
	if err != nil {
		t.Fatal(err)
	}

	// Get the time after renaming.
	afterRenameTime := time.Now()

	// Get the timestamps using the API.
	fi3, err := r.File(rf)
	if err != nil {
		t.Fatal(err)
	}

	// Only the ChangeTime should have changed.
	if fi3.ChangeTime.Before(beforeRenameTime) || fi3.ChangeTime.After(afterRenameTime) {
		t.Fatal("ChangeTime was not within the correct interval")
	}
	if fi2.CreateTime != fi3.CreateTime {
		t.Fatal("CreateTime changed after download")
	}
	if fi2.AccessTime != fi3.AccessTime {
		t.Fatal("AccessTime changed after download")
	}
	if fi2.ModTime != fi3.ModTime {
		t.Fatal("ModTime changed after download")
	}
}

// TestRenterThree executes a number of subtests using the same TestGroup to
// save time on initialization
func TestRenterThree(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for the subtests
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}

	// Specify subtests to run
	subTests := []test{
		{"TestAllowanceDefaultSet", testAllowanceDefaultSet},
		{"TestFileAvailableAndRecoverable", testFileAvailableAndRecoverable},
		{"TestUploadDownload", testUploadDownload}, // Needs to be last as it impacts hosts
	}

	// Run tests
	if err := runRenterTests(t, groupParams, subTests); err != nil {
		t.Fatal(err)
	}
}

// testAllowanceDefaultSet tests that a renter's allowance is correctly set to
// the defaults after creating it and therefore confirming that the API
// endpoint and siatest package both work.
func testAllowanceDefaultSet(t *testing.T, tg *siatest.TestGroup) {
	if len(tg.Renters()) == 0 {
		t.Fatal("Test requires at least 1 renter")
	}
	// Get allowance.
	r := tg.Renters()[0]
	rg, err := r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	// Make sure that the allowance was set correctly.
	if !reflect.DeepEqual(rg.Settings.Allowance, siatest.DefaultAllowance) {
		expected, _ := json.Marshal(siatest.DefaultAllowance)
		was, _ := json.Marshal(rg.Settings.Allowance)
		t.Log("Expected", string(expected))
		t.Log("Was", string(was))
		t.Fatal("Renter's allowance doesn't match siatest.DefaultAllowance")
	}
}

// testReceivedFieldEqualsFileSize tests that the bug that caused finished
// downloads to stall in the UI and siac is gone.
func testReceivedFieldEqualsFileSize(t *testing.T, tg *siatest.TestGroup) {
	// Make sure the test has enough hosts.
	if len(tg.Hosts()) < 4 {
		t.Fatal("testReceivedFieldEqualsFileSize requires at least 4 hosts")
	}
	// Grab the first of the group's renters
	r := tg.Renters()[0]

	// Clear the download history to make sure it's empty before we start the test.
	err := r.RenterClearAllDownloadsPost()
	if err != nil {
		t.Fatal(err)
	}

	// Upload a file.
	dataPieces := uint64(3)
	parityPieces := uint64(1)
	fileSize := int(modules.SectorSize)
	lf, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// This code sums up the 'received' variable in a similar way the renter
	// does it. We use it to find a fetchLen for which received != fetchLen due
	// to the implicit rounding of the unsigned integers.
	var fetchLen uint64
	for fetchLen = uint64(100); ; fetchLen++ {
		received := uint64(0)
		for piecesCompleted := uint64(1); piecesCompleted <= dataPieces; piecesCompleted++ {
			received += fetchLen / dataPieces
		}
		if received != fetchLen {
			break
		}
	}

	// Download fetchLen bytes of the file.
	_, err = r.DownloadToDiskPartial(rf, lf, false, 0, fetchLen)
	if err != nil {
		t.Fatal(err)
	}

	// Get the download.
	rdg, err := r.RenterDownloadsGet()
	if err != nil {
		t.Fatal(err)
	}
	d := rdg.Downloads[0]

	// Make sure that 'Received' matches the amount of data we fetched.
	if !d.Completed {
		t.Error("Download should be completed but wasn't")
	}
	if d.Received != fetchLen {
		t.Errorf("Received was %v but should be %v", d.Received, fetchLen)
	}
}

// testClearDownloadHistory makes sure that the download history is
// properly cleared when called through the API
func testClearDownloadHistory(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	r := tg.Renters()[0]

	rdg, err := r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	numDownloads := 10
	if len(rdg.Downloads) < numDownloads {
		remainingDownloads := numDownloads - len(rdg.Downloads)
		rf, err := r.RenterFilesGet()
		if err != nil {
			t.Fatal(err)
		}
		// Check if the renter has any files
		// Upload a file if none
		if len(rf.Files) == 0 {
			dataPieces := uint64(1)
			parityPieces := uint64(1)
			fileSize := 100 + siatest.Fuzz()
			_, _, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
			if err != nil {
				t.Fatal("Failed to upload a file for testing: ", err)
			}
			rf, err = r.RenterFilesGet()
			if err != nil {
				t.Fatal(err)
			}
		}
		// Download files to build download history
		dest := filepath.Join(siatest.SiaTestingDir, strconv.Itoa(fastrand.Intn(math.MaxInt32)))
		for i := 0; i < remainingDownloads; i++ {
			err = r.RenterDownloadGet(rf.Files[0].SiaPath, dest, 0, rf.Files[0].Filesize, false)
			if err != nil {
				t.Fatal("Could not Download file:", err)
			}
		}
		rdg, err = r.RenterDownloadsGet()
		if err != nil {
			t.Fatal("Could not get download history:", err)
		}
		// Confirm download history is not empty
		if len(rdg.Downloads) != numDownloads {
			t.Fatalf("Not all downloads added to download history: only %v downloads added, expected %v", len(rdg.Downloads), numDownloads)
		}
	}
	numDownloads = len(rdg.Downloads)

	// Check removing one download from history
	// Remove First Download
	timestamp := rdg.Downloads[0].StartTime
	err = r.RenterClearDownloadsRangePost(timestamp, timestamp)
	if err != nil {
		t.Fatal("Error in API endpoint to remove download from history:", err)
	}
	numDownloads--
	rdg, err = r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	if len(rdg.Downloads) != numDownloads {
		t.Fatalf("Download history not reduced: history has %v downloads, expected %v", len(rdg.Downloads), numDownloads)
	}
	i := sort.Search(len(rdg.Downloads), func(i int) bool { return rdg.Downloads[i].StartTime.Equal(timestamp) })
	if i < len(rdg.Downloads) {
		t.Fatal("Specified download not removed from history")
	}
	// Remove Last Download
	timestamp = rdg.Downloads[len(rdg.Downloads)-1].StartTime
	err = r.RenterClearDownloadsRangePost(timestamp, timestamp)
	if err != nil {
		t.Fatal("Error in API endpoint to remove download from history:", err)
	}
	numDownloads--
	rdg, err = r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	if len(rdg.Downloads) != numDownloads {
		t.Fatalf("Download history not reduced: history has %v downloads, expected %v", len(rdg.Downloads), numDownloads)
	}
	i = sort.Search(len(rdg.Downloads), func(i int) bool { return rdg.Downloads[i].StartTime.Equal(timestamp) })
	if i < len(rdg.Downloads) {
		t.Fatal("Specified download not removed from history")
	}

	// Check Clear Before
	timestamp = rdg.Downloads[len(rdg.Downloads)-2].StartTime
	err = r.RenterClearDownloadsBeforePost(timestamp)
	if err != nil {
		t.Fatal("Error in API endpoint to clear download history before timestamp:", err)
	}
	rdg, err = r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	i = sort.Search(len(rdg.Downloads), func(i int) bool { return rdg.Downloads[i].StartTime.Before(timestamp) })
	if i < len(rdg.Downloads) {
		t.Fatal("Download found that was before given time")
	}

	// Check Clear After
	timestamp = rdg.Downloads[1].StartTime
	err = r.RenterClearDownloadsAfterPost(timestamp)
	if err != nil {
		t.Fatal("Error in API endpoint to clear download history after timestamp:", err)
	}
	rdg, err = r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	i = sort.Search(len(rdg.Downloads), func(i int) bool { return rdg.Downloads[i].StartTime.After(timestamp) })
	if i < len(rdg.Downloads) {
		t.Fatal("Download found that was after given time")
	}

	// Check clear range
	before := rdg.Downloads[1].StartTime
	after := rdg.Downloads[len(rdg.Downloads)-1].StartTime
	err = r.RenterClearDownloadsRangePost(after, before)
	if err != nil {
		t.Fatal("Error in API endpoint to remove range of downloads from history:", err)
	}
	rdg, err = r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	i = sort.Search(len(rdg.Downloads), func(i int) bool {
		return rdg.Downloads[i].StartTime.Before(before) && rdg.Downloads[i].StartTime.After(after)
	})
	if i < len(rdg.Downloads) {
		t.Fatal("Not all downloads from range removed from history")
	}

	// Check clearing download history
	err = r.RenterClearAllDownloadsPost()
	if err != nil {
		t.Fatal("Error in API endpoint to clear download history:", err)
	}
	rdg, err = r.RenterDownloadsGet()
	if err != nil {
		t.Fatal("Could not get download history:", err)
	}
	if len(rdg.Downloads) != 0 {
		t.Fatalf("Download history not cleared: history has %v downloads, expected 0", len(rdg.Downloads))
	}
}

// testDirectories checks the functionality of directories in the Renter
func testDirectories(t *testing.T, tg *siatest.TestGroup) {
	// Grab Renter
	r := tg.Renters()[0]

	// Test Directory endpoint for creating empty directory
	rd, err := r.UploadNewDirectory()
	if err != nil {
		t.Fatal(err)
	}

	// Check directory
	rgd, err := r.RenterGetDir(rd.SiaPath())
	if err != nil {
		t.Fatal(err)
	}
	// Directory should return 0 FileInfos and 1 DirectoryInfo with would belong
	// to the directory itself
	if len(rgd.Directories) != 1 {
		t.Fatal("Expected 1 DirectoryInfo to be returned but got:", len(rgd.Directories))
	}
	if rgd.Directories[0].SiaPath != rd.SiaPath() {
		t.Fatalf("SiaPaths do not match %v and %v", rgd.Directories[0].SiaPath, rd.SiaPath())
	}
	if len(rgd.Files) != 0 {
		t.Fatal("Expected no files in directory but found:", len(rgd.Files))
	}

	// Check uploading file to new subdirectory
	// Create local file
	size := 100 + siatest.Fuzz()
	fd := r.FilesDir()
	ld, err := fd.CreateDir("subDir1/subDir2/subDir3-" + persist.RandomSuffix())
	if err != nil {
		t.Fatal(err)
	}
	lf, err := ld.NewFile(size)
	if err != nil {
		t.Fatal(err)
	}

	// Upload file
	dataPieces := uint64(1)
	parityPieces := uint64(1)
	rf, err := r.Upload(lf, r.SiaPath(lf.Path()), dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Check directory that file was uploaded to
	siaPath := filepath.Dir(rf.SiaPath())
	rgd, err = r.RenterGetDir(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Directory should have 1 file and 0 sub directories
	if len(rgd.Directories) != 1 {
		t.Fatal("Expected 1 DirectoryInfo to be returned but got:", len(rgd.Directories))
	}
	if len(rgd.Files) != 1 {
		t.Fatal("Expected 1 file in directory but found:", len(rgd.Files))
	}

	// Check parent directory
	siaPath = filepath.Dir(siaPath)
	rgd, err = r.RenterGetDir(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Directory should have 0 files and 1 sub directory
	if len(rgd.Directories) != 2 {
		t.Fatal("Expected 2 DirectoryInfos to be returned but got:", len(rgd.Directories))
	}
	if len(rgd.Files) != 0 {
		t.Fatal("Expected 0 files in directory but found:", len(rgd.Files))
	}

	// Test deleting directory
	if err = r.RenterDirDeletePost(rd.SiaPath()); err != nil {
		t.Fatal(err)
	}

	// Check that siadir was deleted from disk
	_, err = os.Stat(filepath.Join(r.RenterFilesDir(), rd.SiaPath()))
	if !os.IsNotExist(err) {
		t.Fatal("Expected IsNotExist err, but got err:", err)
	}
}

// testDownloadAfterRenew makes sure that we can still download a file
// after the contract period has ended.
func testDownloadAfterRenew(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]
	// Upload file, creating a piece for each host in the group
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := 100 + siatest.Fuzz()
	_, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	// Mine enough blocks for the next period to start. This means the
	// contracts should be renewed and the data should still be available for
	// download.
	miner := tg.Miners()[0]
	for i := types.BlockHeight(0); i < siatest.DefaultAllowance.Period; i++ {
		if err := miner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	// Download the file synchronously directly into memory.
	_, err = renter.DownloadByStream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
}

// testDownloadMultipleLargeSectors downloads multiple large files (>5 Sectors)
// in parallel and makes sure that the downloads are blocking each other.
func testDownloadMultipleLargeSectors(t *testing.T, tg *siatest.TestGroup) {
	// parallelDownloads is the number of downloads that are run in parallel.
	parallelDownloads := 10
	// fileSize is the size of the downloaded file.
	fileSize := int(10*modules.SectorSize) + siatest.Fuzz()
	// set download limits and reset them after test.
	// uniqueRemoteFiles is the number of files that will be uploaded to the
	// network. Downloads will choose the remote file to download randomly.
	uniqueRemoteFiles := 5
	// Grab the first of the group's renters
	renter := tg.Renters()[0]
	// set download limits and reset them after test.
	if err := renter.RenterPostRateLimit(int64(fileSize)*2, 0); err != nil {
		t.Fatal("failed to set renter bandwidth limit", err)
	}
	defer func() {
		if err := renter.RenterPostRateLimit(0, 0); err != nil {
			t.Error("failed to reset renter bandwidth limit", err)
		}
	}()

	// Upload files
	dataPieces := uint64(len(tg.Hosts())) - 1
	parityPieces := uint64(1)
	remoteFiles := make([]*siatest.RemoteFile, 0, uniqueRemoteFiles)
	for i := 0; i < uniqueRemoteFiles; i++ {
		_, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
		if err != nil {
			t.Fatal("Failed to upload a file for testing: ", err)
		}
		remoteFiles = append(remoteFiles, remoteFile)
	}

	// Randomly download using download to file and download to stream methods.
	wg := new(sync.WaitGroup)
	for i := 0; i < parallelDownloads; i++ {
		wg.Add(1)
		go func() {
			var err error
			var rf = remoteFiles[fastrand.Intn(len(remoteFiles))]
			if fastrand.Intn(2) == 0 {
				_, err = renter.DownloadByStream(rf)
			} else {
				_, err = renter.DownloadToDisk(rf, false)
			}
			if err != nil {
				t.Error("Download failed:", err)
			}
			wg.Done()
		}()
	}
	wg.Wait()
}

// testLocalRepair tests if a renter correctly repairs a file from disk
// after a host goes offline.
func testLocalRepair(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]

	// Check that we have enough hosts for this test.
	if len(tg.Hosts()) < 2 {
		t.Fatal("This test requires at least 2 hosts")
	}

	// Set fileSize and redundancy for upload
	fileSize := int(modules.SectorSize)
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces

	// Upload file
	_, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	// Get the file info of the fully uploaded file. Tha way we can compare the
	// redundancies later.
	fi, err := renter.File(remoteFile)
	if err != nil {
		t.Fatal("failed to get file info", err)
	}

	// Take down one of the hosts and check if redundancy decreases.
	if err := tg.RemoveNode(tg.Hosts()[0]); err != nil {
		t.Fatal("Failed to shutdown host", err)
	}
	expectedRedundancy := float64(dataPieces+parityPieces-1) / float64(dataPieces)
	if err := renter.WaitForDecreasingRedundancy(remoteFile, expectedRedundancy); err != nil {
		t.Fatal("Redundancy isn't decreasing", err)
	}
	// Mine a block to trigger the repair loop so the chunk is marked as stuck
	m := tg.Miners()[0]
	if err := m.MineBlock(); err != nil {
		t.Fatal(err)
	}
	// Check to see if a chunk got marked as stuck
	err = renter.WaitForStuckChunksToBubble()
	if err != nil {
		t.Fatal(err)
	}
	// We should still be able to download
	if _, err := renter.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download file", err)
	}
	// Bring up a new host and check if redundancy increments again.
	_, err = tg.AddNodes(node.HostTemplate)
	if err != nil {
		t.Fatal("Failed to create a new host", err)
	}
	if err := renter.WaitForUploadRedundancy(remoteFile, fi.Redundancy); err != nil {
		t.Fatal("File wasn't repaired", err)
	}
	// Check to see if a chunk got repaired and marked as unstuck
	err = renter.WaitForStuckChunksToRepair()
	if err != nil {
		t.Fatal(err)
	}
	// We should be able to download
	if _, err := renter.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download file", err)
	}
}

// testRemoteRepair tests if a renter correctly repairs a file by
// downloading it after a host goes offline.
func testRemoteRepair(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	r := tg.Renters()[0]

	// Check that we have enough hosts for this test.
	if len(tg.Hosts()) < 2 {
		t.Fatal("This test requires at least 2 hosts")
	}

	// Set fileSize and redundancy for upload
	fileSize := int(modules.SectorSize)
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces

	// Upload file
	localFile, remoteFile, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	// Get the file info of the fully uploaded file. Tha way we can compare the
	// redundancieslater.
	fi, err := r.File(remoteFile)
	if err != nil {
		t.Fatal("failed to get file info", err)
	}

	// Delete the file locally.
	if err := localFile.Delete(); err != nil {
		t.Fatal("failed to delete local file", err)
	}

	// Take down all of the parity hosts and check if redundancy decreases.
	for i := uint64(0); i < parityPieces; i++ {
		if err := tg.RemoveNode(tg.Hosts()[0]); err != nil {
			t.Fatal("Failed to shutdown host", err)
		}
	}
	expectedRedundancy := float64(dataPieces+parityPieces-1) / float64(dataPieces)
	if err := r.WaitForDecreasingRedundancy(remoteFile, expectedRedundancy); err != nil {
		t.Fatal("Redundancy isn't decreasing", err)
	}
	// Mine a block to trigger the repair loop so the chunk is marked as stuck
	m := tg.Miners()[0]
	if err := m.MineBlock(); err != nil {
		t.Fatal(err)
	}
	// Check to see if a chunk got marked as stuck
	err = r.WaitForStuckChunksToBubble()
	if err != nil {
		t.Fatal(err)
	}
	// We should still be able to download
	if _, err := r.DownloadByStream(remoteFile); err != nil {
		t.Error("Failed to download file", err)
	}
	// Bring up new parity hosts and check if redundancy increments again.
	_, err = tg.AddNodeN(node.HostTemplate, int(parityPieces))
	if err != nil {
		t.Fatal("Failed to create a new host", err)
	}
	// When doing remote repair the redundancy might not reach 100%.
	expectedRedundancy = (1.0 - siafile.RemoteRepairDownloadThreshold) * fi.Redundancy
	if err := r.WaitForUploadRedundancy(remoteFile, expectedRedundancy); err != nil {
		t.Fatal("File wasn't repaired", err)
	}
	// Check to see if a chunk got repaired and marked as unstuck
	err = r.WaitForStuckChunksToRepair()
	if err != nil {
		t.Fatal(err)
	}
	// We should be able to download
	if _, err := r.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download file", err)
	}
}

// testSingleFileGet is a subtest that uses an existing TestGroup to test if
// using the single file API endpoint works
func testSingleFileGet(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]
	// Upload file, creating a piece for each host in the group
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := 100 + siatest.Fuzz()
	_, _, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	files, err := renter.Files()
	if err != nil {
		t.Fatal("Failed to get renter files: ", err)
	}

	checks := 0
	for _, f := range files {
		// Only request files if file was fully uploaded for first API request
		if f.UploadProgress < 100 {
			continue
		}
		checks++
		rf, err := renter.RenterFileGet(f.SiaPath)
		if err != nil {
			t.Fatal("Failed to request single file", err)
		}

		// Can't use reflect.DeepEqual because certain fields are too dynamic,
		// however those fields are also not indicative of whether or not the
		// files are the same.  Not checking Redundancy, Available, Renewing
		// ,UploadProgress, UploadedBytes, or Renewing
		if f.Expiration != rf.File.Expiration {
			t.Log("File from Files() Expiration:", f.Expiration)
			t.Log("File from File() Expiration:", rf.File.Expiration)
			t.Fatal("Single file queries does not match file previously requested.")
		}
		if f.Filesize != rf.File.Filesize {
			t.Log("File from Files() Filesize:", f.Filesize)
			t.Log("File from File() Filesize:", rf.File.Filesize)
			t.Fatal("Single file queries does not match file previously requested.")
		}
		if f.LocalPath != rf.File.LocalPath {
			t.Log("File from Files() LocalPath:", f.LocalPath)
			t.Log("File from File() LocalPath:", rf.File.LocalPath)
			t.Fatal("Single file queries does not match file previously requested.")
		}
		if f.SiaPath != rf.File.SiaPath {
			t.Log("File from Files() SiaPath:", f.SiaPath)
			t.Log("File from File() SiaPath:", rf.File.SiaPath)
			t.Fatal("Single file queries does not match file previously requested.")
		}
	}
	if checks == 0 {
		t.Fatal("No files checks through single file endpoint.")
	}
}

// testUploadDownload is a subtest that uses an existing TestGroup to test if
// uploading and downloading a file works
func testUploadDownload(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]
	// Upload file, creating a piece for each host in the group
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := fastrand.Intn(2*int(modules.SectorSize)) + siatest.Fuzz() + 2 // between 1 and 2*SectorSize + 3 bytes
	localFile, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	// Download the file synchronously directly into memory
	_, err = renter.DownloadByStream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	// Download the file synchronously to a file on disk
	_, err = renter.DownloadToDisk(remoteFile, false)
	if err != nil {
		t.Fatal(err)
	}
	// Download the file asynchronously and wait for the download to finish.
	localFile, err = renter.DownloadToDisk(remoteFile, true)
	if err != nil {
		t.Error(err)
	}
	if err := renter.WaitForDownload(localFile, remoteFile); err != nil {
		t.Error(err)
	}
	// Stream the file.
	_, err = renter.Stream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	// Stream the file partially a few times. At least 1 byte is streamed.
	for i := 0; i < 5; i++ {
		from := fastrand.Intn(fileSize - 1)             // [0..fileSize-2]
		to := from + 1 + fastrand.Intn(fileSize-from-1) // [from+1..fileSize-1]
		_, err = renter.StreamPartial(remoteFile, localFile, uint64(from), uint64(to))
		if err != nil {
			t.Fatal(err)
		}
	}
}

// testUploadWithAndWithoutForceParameter is a subtest that uses an existing TestGroup to test if
// uploading an existing file is successful when setting 'force' to 'true' and 'force' set to 'false'
func testUploadWithAndWithoutForceParameter(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]

	// Upload file, creating a piece for each host in the group
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := 100 + siatest.Fuzz()
	localFile, _, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	_, err = renter.UploadBlocking(localFile, dataPieces, parityPieces, true)
	if err != nil {
		t.Fatal("Failed to force overwrite a file when specifying 'force=true': ", err)
	}

	// Upload file, creating a piece for each host in the group
	dataPieces = uint64(1)
	parityPieces = uint64(len(tg.Hosts())) - dataPieces
	fileSize = 100 + siatest.Fuzz()
	localFile, _, err = renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	_, err = renter.UploadBlocking(localFile, dataPieces, parityPieces, false)
	if err == nil {
		t.Fatal("File overwritten without specifying 'force=true'")
	}
}

// TestRenterInterrupt executes a number of subtests using the same TestGroup to
// save time on initialization
func TestRenterInterrupt(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for the subtests
	groupParams := siatest.GroupParams{
		Hosts:  5,
		Miners: 1,
	}

	// Specify sub tests
	subTests := []test{
		{"TestContractInterruptedSaveToDiskAfterDeletion", testContractInterruptedSaveToDiskAfterDeletion},
		{"TestDownloadInterruptedAfterSendingRevision", testDownloadInterruptedAfterSendingRevision},
		{"TestDownloadInterruptedBeforeSendingRevision", testDownloadInterruptedBeforeSendingRevision},
		{"TestUploadInterruptedAfterSendingRevision", testUploadInterruptedAfterSendingRevision},
		{"TestUploadInterruptedBeforeSendingRevision", testUploadInterruptedBeforeSendingRevision},
	}

	// Run tests
	if err := runRenterTests(t, groupParams, subTests); err != nil {
		t.Fatal(err)
	}
}

// testContractInterruptedSaveToDiskAfterDeletion runs testDownloadInterrupted with
// a dependency that interrupts the download after sending the signed revision
// to the host.
func testContractInterruptedSaveToDiskAfterDeletion(t *testing.T, tg *siatest.TestGroup) {
	testContractInterrupted(t, tg, newDependencyInterruptContractSaveToDiskAfterDeletion())
}

// testDownloadInterruptedAfterSendingRevision runs testDownloadInterrupted with
// a dependency that interrupts the download after sending the signed revision
// to the host.
func testDownloadInterruptedAfterSendingRevision(t *testing.T, tg *siatest.TestGroup) {
	testDownloadInterrupted(t, tg, newDependencyInterruptDownloadAfterSendingRevision())
}

// testDownloadInterruptedBeforeSendingRevision runs testDownloadInterrupted
// with a dependency that interrupts the download before sending the signed
// revision to the host.
func testDownloadInterruptedBeforeSendingRevision(t *testing.T, tg *siatest.TestGroup) {
	testDownloadInterrupted(t, tg, newDependencyInterruptDownloadBeforeSendingRevision())
}

// testUploadInterruptedAfterSendingRevision runs testUploadInterrupted with a
// dependency that interrupts the upload after sending the signed revision to
// the host.
func testUploadInterruptedAfterSendingRevision(t *testing.T, tg *siatest.TestGroup) {
	testUploadInterrupted(t, tg, newDependencyInterruptUploadAfterSendingRevision())
}

// testUploadInterruptedBeforeSendingRevision runs testUploadInterrupted with a
// dependency that interrupts the upload before sending the signed revision to
// the host.
func testUploadInterruptedBeforeSendingRevision(t *testing.T, tg *siatest.TestGroup) {
	testUploadInterrupted(t, tg, newDependencyInterruptUploadBeforeSendingRevision())
}

// testContractInterrupted interrupts a download using the provided dependencies.
func testContractInterrupted(t *testing.T, tg *siatest.TestGroup, deps *siatest.DependencyInterruptOnceOnKeyword) {
	// Add Renter
	testDir := renterTestDir(t.Name())
	renterTemplate := node.Renter(testDir + "/renter")
	renterTemplate.ContractorDeps = deps
	renterTemplate.Allowance = siatest.DefaultAllowance
	renterTemplate.Allowance.Period = 100
	renterTemplate.Allowance.RenewWindow = 75
	nodes, err := tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// Call fail on the dependency every 10 ms.
	cancel := make(chan struct{})
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		for {
			// Cause the contract renewal to fail
			deps.Fail()
			select {
			case <-cancel:
				wg.Done()
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()

	// Renew contracts.
	if err = renewContractsByRenewWindow(renter, tg); err != nil {
		t.Fatal(err)
	}

	// Disrupt statement should prevent contracts from being renewed properly.
	// This means that both old and new contracts will be staticContracts which
	// are exported through the API via RenterContracts.Contracts
	err = build.Retry(50, 100*time.Millisecond, func() error {
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.Contracts) != len(tg.Hosts())*2 {
			return fmt.Errorf("Incorrect number of staticContracts: have %v expected %v", len(rc.Contracts), len(tg.Hosts())*2)
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, false, true)
		t.Fatal(err)
	}

	// By mining blocks to trigger threadContractMaintenance,
	// managedCheckForDuplicates should move renewed contracts from
	// staticContracts to oldContracts even though disrupt statement is still
	// interrupting renew code.
	m := tg.Miners()[0]
	if err = m.MineBlock(); err != nil {
		t.Fatal(err)
	}
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}
	err = build.Retry(70, 100*time.Millisecond, func() error {
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(rc.InactiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("Incorrect number of inactive contracts: have %v expected %v", len(rc.InactiveContracts), len(tg.Hosts()))
		}
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("Incorrect number of active contracts: have %v expected %v", len(rc.ActiveContracts), len(tg.Hosts()))
		}
		if len(rc.Contracts) != len(tg.Hosts()) {
			return fmt.Errorf("Incorrect number of staticContracts: have %v expected %v", len(rc.Contracts), len(tg.Hosts()))
		}
		if err = m.MineBlock(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, false, true)
		t.Fatal(err)
	}

	// Stop calling fail on the dependency.
	close(cancel)
	wg.Wait()
	deps.Disable()
}

// testDownloadInterrupted interrupts a download using the provided dependencies.
func testDownloadInterrupted(t *testing.T, tg *siatest.TestGroup, deps *siatest.DependencyInterruptOnceOnKeyword) {
	// Add Renter
	testDir := renterTestDir(t.Name())
	renterTemplate := node.Renter(testDir + "/renter")
	renterTemplate.ContractSetDeps = deps
	nodes, err := tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// Set the bandwidth limit to 1 chunk per second.
	renter := nodes[0]
	ct := crypto.TypeDefaultRenter
	dataPieces := uint64(len(tg.Hosts())) - 1
	parityPieces := uint64(1)
	chunkSize := siatest.ChunkSize(dataPieces, ct)
	_, remoteFile, err := renter.UploadNewFileBlocking(int(chunkSize), dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := renter.RenterPostRateLimit(int64(chunkSize), int64(chunkSize)); err != nil {
		t.Fatal(err)
	}

	// Call fail on the dependency every 10 ms.
	cancel := make(chan struct{})
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		for {
			// Cause the next download to fail.
			deps.Fail()
			select {
			case <-cancel:
				wg.Done()
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	// Try downloading the file 5 times.
	for i := 0; i < 5; i++ {
		if _, err := renter.DownloadByStream(remoteFile); err == nil {
			t.Fatal("Download shouldn't succeed since it was interrupted")
		}
	}
	// Stop calling fail on the dependency.
	close(cancel)
	wg.Wait()
	deps.Disable()
	// Download the file once more successfully
	if _, err := renter.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download the file", err)
	}
}

// testUploadInterrupted let's the upload fail using the provided dependencies
// and makes sure that this doesn't corrupt the contract.
func testUploadInterrupted(t *testing.T, tg *siatest.TestGroup, deps *siatest.DependencyInterruptOnceOnKeyword) {
	// Add Renter
	testDir := renterTestDir(t.Name())
	renterTemplate := node.Renter(testDir + "/renter")
	renterTemplate.ContractSetDeps = deps
	nodes, err := tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// Set the bandwidth limit to 1 chunk per second.
	ct := crypto.TypeDefaultRenter
	renter := nodes[0]
	dataPieces := uint64(len(tg.Hosts())) - 1
	parityPieces := uint64(1)
	chunkSize := siatest.ChunkSize(dataPieces, ct)
	if err := renter.RenterPostRateLimit(int64(chunkSize), int64(chunkSize)); err != nil {
		t.Fatal(err)
	}

	// Call fail on the dependency every two seconds to allow some uploads to
	// finish.
	cancel := make(chan struct{})
	done := make(chan struct{})
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer close(done)
		// Loop until cancel was closed or we reach 5 iterations. Otherwise we
		// might end up blocking the upload for too long.
		for i := 0; i < 10; i++ {
			// Cause the next upload to fail.
			deps.Fail()
			select {
			case <-cancel:
				wg.Done()
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
		wg.Done()
	}()

	// Upload a file that's 1 chunk large.
	_, remoteFile, err := renter.UploadNewFileBlocking(int(chunkSize), dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure that the upload does not finish before the interrupting go
	// routine is finished
	select {
	case <-done:
	default:
		t.Fatal("Upload finished before interrupt signal is done")
	}
	// Stop calling fail on the dependency.
	close(cancel)
	wg.Wait()
	deps.Disable()
	// Download the file.
	if _, err := renter.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download the file", err)
	}
}

// TestRenterAddNodes runs a subset of tests that require adding their own renter
func TestRenterAddNodes(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}

	// Specify subtests to run
	subTests := []test{
		{"TestRedundancyReporting", testRedundancyReporting}, // Put first because it pulls the original tg renter
		{"TestRenterCancelAllowance", testRenterCancelAllowance},
		{"TestRenewFailing", testRenewFailing}, // Put last because it impacts a host
	}

	// Run tests
	if err := runRenterTests(t, groupParams, subTests); err != nil {
		t.Fatal(err)
	}
}

// testRedundancyReporting verifies that redundancy reporting is accurate if
// contracts become offline.
func testRedundancyReporting(t *testing.T, tg *siatest.TestGroup) {
	// Upload a file.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts()) - 1)

	renter := tg.Renters()[0]
	_, rf, err := renter.UploadNewFileBlocking(100, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Stop a host.
	host := tg.Hosts()[0]
	if err := tg.StopNode(host); err != nil {
		t.Fatal(err)
	}

	// Mine a block to trigger contract maintenance.
	miner := tg.Miners()[0]
	if err := miner.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Redundancy should decrease.
	expectedRedundancy := float64(dataPieces+parityPieces-1) / float64(dataPieces)
	if err := renter.WaitForDecreasingRedundancy(rf, expectedRedundancy); err != nil {
		t.Fatal("Redundancy isn't decreasing", err)
	}

	// Restart the host.
	if err := tg.StartNode(host); err != nil {
		t.Fatal(err)
	}

	// Wait until the host shows up as active again.
	pk, err := host.HostPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(60, time.Second, func() error {
		hdag, err := renter.HostDbActiveGet()
		if err != nil {
			return err
		}
		for _, h := range hdag.Hosts {
			if reflect.DeepEqual(h.PublicKey, pk) {
				return nil
			}
		}
		// If host is not active, announce it again and mine a block.
		if err := host.HostAnnouncePost(); err != nil {
			return (err)
		}
		miner := tg.Miners()[0]
		if err := miner.MineBlock(); err != nil {
			return (err)
		}
		if err := tg.Sync(); err != nil {
			return (err)
		}
		hg, err := host.HostGet()
		if err != nil {
			return err
		}
		return fmt.Errorf("host with address %v not active", hg.InternalSettings.NetAddress)
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := miner.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Redundancy should go back to normal.
	expectedRedundancy = float64(dataPieces+parityPieces) / float64(dataPieces)
	if err := renter.WaitForUploadRedundancy(rf, expectedRedundancy); err != nil {
		t.Fatal("Redundancy is not increasing")
	}
}

// testRenewFailing checks if a contract gets marked as !goodForRenew after
// failing multiple times in a row.
func testRenewFailing(t *testing.T, tg *siatest.TestGroup) {
	// Add a renter with a custom allowance to give it plenty of time to renew
	// the contract later.
	renterParams := node.Renter(filepath.Join(renterTestDir(t.Name()), "renter"))
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Hosts = uint64(len(tg.Hosts()) - 1)
	renterParams.Allowance.Period = 100
	renterParams.Allowance.RenewWindow = 50
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// All the contracts of the renter should be goodForRenew. So there should
	// be no inactive contracts, only active contracts
	rcg, err := renter.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(rcg.ActiveContracts)) != renterParams.Allowance.Hosts {
		for i, c := range rcg.ActiveContracts {
			fmt.Println(i, c.HostPublicKey)
		}
		t.Fatalf("renter had %v contracts but should have %v",
			len(rcg.ActiveContracts), renterParams.Allowance.Hosts)
	}
	if len(rcg.InactiveContracts) != 0 {
		t.Fatal("Renter should have 0 inactive contracts but has", len(rcg.InactiveContracts))
	}

	// Create a map of the hosts in the group.
	hostMap := make(map[string]*siatest.TestNode)
	for _, host := range tg.Hosts() {
		pk, err := host.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		hostMap[pk.String()] = host
	}
	// Lock the wallet of one of the used hosts to make the renew fail.
	var lockedHostPK types.SiaPublicKey
	for _, c := range rcg.ActiveContracts {
		if host, used := hostMap[c.HostPublicKey.String()]; used {
			lockedHostPK = c.HostPublicKey
			if err := host.WalletLockPost(); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	// Wait until the contract is supposed to be renewed.
	cg, err := renter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	rg, err := renter.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	miner := tg.Miners()[0]
	blockHeight := cg.Height
	for blockHeight+rg.Settings.Allowance.RenewWindow < rcg.ActiveContracts[0].EndHeight {
		if err := miner.MineBlock(); err != nil {
			t.Fatal(err)
		}
		blockHeight++
	}

	// there should be no inactive contracts, only active contracts.
	rcg, err = renter.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if uint64(len(rcg.ActiveContracts)) != renterParams.Allowance.Hosts {
		for i, c := range rcg.ActiveContracts {
			fmt.Println(i, c.HostPublicKey)
		}
		t.Fatalf("renter had %v contracts but should have %v",
			len(rcg.ActiveContracts), renterParams.Allowance.Hosts)
	}
	if len(rcg.InactiveContracts) != 0 {
		t.Fatal("Renter should have 0 inactive contracts but has", len(rcg.InactiveContracts))
	}

	// mine enough blocks to reach the second half of the renew window.
	for ; blockHeight+rg.Settings.Allowance.RenewWindow/2 < rcg.ActiveContracts[0].EndHeight; blockHeight++ {
		if err := miner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// We should be within the second half of the renew window now. We keep
	// mining blocks until the host with the locked wallet has been replaced.
	// This should happen before we reach the endHeight of the contracts. This
	// means we should have number of hosts - 1 active contracts and number of
	// hosts - 1 inactive contracts.  One of the inactive contracts will be
	// !goodForRenew due to the host
	err = build.Retry(int(rcg.ActiveContracts[0].EndHeight-blockHeight), 1*time.Second, func() error {
		if err := miner.MineBlock(); err != nil {
			return err
		}

		// contract should be !goodForRenew now.
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts())-1 {
			return fmt.Errorf("Expected %v active contracts, got %v", len(tg.Hosts())-1, len(rc.ActiveContracts))
		}
		if len(rc.InactiveContracts) != len(tg.Hosts())-1 {
			return fmt.Errorf("Expected %v inactive contracts, got %v", len(tg.Hosts())-1, len(rc.InactiveContracts))
		}

		// Check that the locked host is in inactive and not in active.
		for _, c := range rc.ActiveContracts {
			if c.HostPublicKey.String() == lockedHostPK.String() {
				return errors.New("locked host still appears in set of active contracts")
			}
		}
		// If the host does appear in the inactive, set, then the test has
		// passed.
		for _, c := range rc.ActiveContracts {
			if c.HostPublicKey.String() == lockedHostPK.String() {
				return nil
			}
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
}

// testRenterCancelAllowance tests that setting an empty allowance causes
// uploads, downloads, and renewals to cease as well as tests that resetting the
// allowance after the allowance was cancelled will trigger the correct contract
// formation.
func testRenterCancelAllowance(t *testing.T, tg *siatest.TestGroup) {
	renterParams := node.Renter(filepath.Join(renterTestDir(t.Name()), "renter"))
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// Test Resetting allowance
	// Cancel the allowance
	if err := renter.RenterCancelAllowance(); err != nil {
		t.Fatal(err)
	}

	// Give it some time to mark the contracts as !goodForUpload and
	// !goodForRenew.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Should now only have inactive contracts.
		if len(rc.ActiveContracts) != 0 {
			return fmt.Errorf("expected 0 active contracts, got %v", len(rc.ActiveContracts))
		}
		if len(rc.InactiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v inactive contracts, got %v", len(tg.Hosts()), len(rc.InactiveContracts))
		}
		for _, c := range rc.InactiveContracts {
			if c.GoodForUpload {
				return errors.New("contract shouldn't be goodForUpload")
			}
			if c.GoodForRenew {
				return errors.New("contract shouldn't be goodForRenew")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set the allowance again.
	if err := renter.RenterPostAllowance(siatest.DefaultAllowance); err != nil {
		t.Fatal(err)
	}

	// Mine a block to start the threadedContractMaintenance.
	if err := tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Give it some time to mark the contracts as goodForUpload and
	// goodForRenew again.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Should now only have active contracts.
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v active contracts, got %v", len(tg.Hosts()), len(rc.ActiveContracts))
		}
		if len(rc.InactiveContracts) != 0 {
			return fmt.Errorf("expected 0 inactive contracts, got %v", len(rc.InactiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			if !c.GoodForUpload {
				return errors.New("contract should be goodForUpload")
			}
			if !c.GoodForRenew {
				return errors.New("contract should be goodForRenew")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test Canceling allowance
	// Upload a file.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts()) - 1)
	_, rf, err := renter.UploadNewFileBlocking(100, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Cancel the allowance
	if err := renter.RenterCancelAllowance(); err != nil {
		t.Fatal(err)
	}

	// Give it some time to mark the contracts as !goodForUpload and
	// !goodForRenew.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Should now have 2 inactive contracts.
		if len(rc.ActiveContracts) != 0 {
			return fmt.Errorf("expected 0 active contracts, got %v", len(rc.ActiveContracts))
		}
		if len(rc.InactiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v inactive contracts, got %v", len(tg.Hosts()), len(rc.InactiveContracts))
		}
		for _, c := range rc.InactiveContracts {
			if c.GoodForUpload {
				return errors.New("contract shouldn't be goodForUpload")
			}
			if c.GoodForRenew {
				return errors.New("contract shouldn't be goodForRenew")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try downloading the file; should succeed.
	if _, err := renter.DownloadByStream(rf); err != nil {
		t.Fatal("downloading file failed", err)
	}

	// Wait for a few seconds to make sure that the upload heap is rebuilt.
	// The rebuilt interval is 3 seconds. Sleep for 5 to be safe.
	time.Sleep(5 * time.Second)

	// Try to upload a file after the allowance was cancelled. Should succeed.
	_, rf2, err := renter.UploadNewFile(100, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Give it some time to upload.
	time.Sleep(time.Second)

	// Redundancy should still be 0.
	renterFiles, err := renter.RenterFilesGet()
	if err != nil {
		t.Fatal("Failed to get files")
	}
	if len(renterFiles.Files) != 2 {
		t.Fatal("There should be exactly 2 tracked files")
	}
	fileInfo, err := renter.File(rf2)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.UploadProgress > 0 || fileInfo.UploadedBytes > 0 || fileInfo.Redundancy > 0 {
		t.Fatal("Uploading a file after canceling the allowance should fail")
	}

	// Mine enough blocks for the period to pass and the contracts to expire.
	miner := tg.Miners()[0]
	for i := types.BlockHeight(0); i < siatest.DefaultAllowance.Period; i++ {
		if err := miner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// All contracts should be archived.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := renter.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		// Should now have num of hosts expired contracts.
		if len(rc.ActiveContracts) != 0 {
			return fmt.Errorf("expected 0 active contracts, got %v", len(rc.ActiveContracts))
		}
		if len(rc.InactiveContracts) != 0 {
			return fmt.Errorf("expected 0 inactive contracts, got %v", len(rc.InactiveContracts))
		}
		if len(rcExpired.ExpiredContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v expired contracts, got %v", len(tg.Hosts()), len(rc.InactiveContracts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try downloading the file; should fail.
	if _, err := renter.DownloadByStream(rf2); err == nil {
		t.Fatal("downloading file succeeded even though it shouldnt", err)
	}

	// The uploaded files should have 0x redundancy now.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rf, err := renter.RenterFilesGet()
		if err != nil {
			return errors.New("Failed to get files")
		}
		if len(rf.Files) != 2 || rf.Files[0].Redundancy != 0 || rf.Files[1].Redundancy != 0 {
			return errors.New("file redundancy should be 0 now")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterContracts tests the formation of the contracts, the contracts
// endpoint, and canceling a contract
func TestRenterContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Renters: 1,
		Miners:  1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get Renter
	r := tg.Renters()[0]
	rg, err := r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}

	// Record the start period at the beginning of test
	currentPeriodStart := rg.CurrentPeriod
	period := rg.Settings.Allowance.Period
	renewWindow := rg.Settings.Allowance.RenewWindow
	numRenewals := 0

	// Check if the current period was set in the past
	cg, err := r.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	if currentPeriodStart > cg.Height-renewWindow {
		t.Fatalf(`Current period not set in the past as expected.
		CP: %v
		BH: %v
		RW: %v
		`, currentPeriodStart, cg.Height, renewWindow)
	}

	// Confirm Contracts were created as expected.  There should only be active
	// contracts and no inactive or expired contracts
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("Expected %v active contracts, got %v", len(tg.Hosts()), len(rc.ActiveContracts))
		}
		if len(rc.InactiveContracts) != 0 {
			return fmt.Errorf("Expected 0 inactive contracts, got %v", len(rc.InactiveContracts))
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if len(rcExpired.ExpiredContracts) != 0 {
			return fmt.Errorf("Expected 0 expired contracts, got %v", len(rcExpired.ExpiredContracts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm contract end heights were set properly
	for _, c := range rc.ActiveContracts {
		if c.EndHeight != currentPeriodStart+period+renewWindow {
			t.Log("Endheight:", c.EndHeight)
			t.Log("Allowance Period:", period)
			t.Log("Renew Window:", renewWindow)
			t.Log("Current Period:", currentPeriodStart)
			t.Fatal("Contract endheight not set to Current period + Allowance Period + Renew Window")
		}
	}

	// Record original Contracts and create Maps for comparison
	originalContracts := rc.ActiveContracts
	originalContractIDMap := make(map[types.FileContractID]struct{})
	for _, c := range originalContracts {
		originalContractIDMap[c.ID] = struct{}{}
	}

	// Mine blocks to force contract renewal
	if err = renewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}
	numRenewals++

	// Confirm Contracts were renewed as expected, all original contracts should
	// have been renewed if GoodForRenew = true.  There should be the same
	// number of active and inactive contracts, and 0 expired contracts since we
	// are still within the endheight of the original contracts, and the
	// inactive contracts should be the same contracts as the original active
	// contracts.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(originalContracts) != len(rc.InactiveContracts) {
			return fmt.Errorf("Didn't get expected number of inactive contracts, expected %v got %v", len(originalContracts), len(rc.InactiveContracts))
		}
		for _, c := range rc.InactiveContracts {
			if _, ok := originalContractIDMap[c.ID]; !ok {
				return errors.New("ID from rc not found in originalContracts")
			}
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if len(rcExpired.ExpiredContracts) != 0 {
			return fmt.Errorf("Expected 0 expired contracts, got %v", len(rcExpired.ExpiredContracts))
		}
		// checkContracts will confirm correct number of inactive and active contracts
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		if err = checkRenewedContracts(rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm contract end heights were set properly End height should be the
	// end of the next period as the contracts are renewed due to reaching the
	// renew window
	rc, err = r.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		if c.EndHeight != currentPeriodStart+(2*period)+renewWindow && c.GoodForRenew {
			t.Log("Endheight:", c.EndHeight)
			t.Log("Allowance Period:", period)
			t.Log("Renew Window:", renewWindow)
			t.Log("Current Period:", currentPeriodStart)
			t.Fatal("Contract endheight not set to Current period + 2 * Allowance Period + Renew Window")
		}
	}

	// Record inactive contracts
	inactiveContracts := rc.InactiveContracts
	inactiveContractIDMap := make(map[types.FileContractID]struct{})
	for _, c := range inactiveContracts {
		inactiveContractIDMap[c.ID] = struct{}{}
	}

	// Mine to force inactive contracts to be expired contracts
	m := tg.Miners()[0]
	cg, err = r.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < int(inactiveContracts[0].EndHeight-cg.Height+types.MaturityDelay); i++ {
		if err = m.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}

	// Confirm contracts, the expired contracts should now be the same contracts
	// as the previous inactive contracts.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err = r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return errors.New("Waiting for active contracts to form")
		}
		if len(rc.ExpiredContracts) != len(inactiveContracts) {
			return fmt.Errorf("Expected the same number of expired and inactive contracts; got %v expired and %v inactive", len(rc.ExpiredContracts), len(inactiveContracts))
		}
		for _, c := range inactiveContracts {
			if _, ok := inactiveContractIDMap[c.ID]; !ok {
				return errors.New("ID from rc not found in inactiveContracts")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Renewing contracts by spending is very time consuming, the rest of the
	// test is only run during vlong so the rest of the test package doesn't
	// time out
	if !build.VLONG {
		return
	}

	// Record current active and expired contracts
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err = r.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("waiting for active contracts to form")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rc, err = r.RenterExpiredContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	activeContracts := rc.ActiveContracts
	expiredContracts := rc.ExpiredContracts
	if err != nil {
		t.Fatal(err)
	}
	expiredContractIDMap := make(map[types.FileContractID]struct{})
	for _, c := range expiredContracts {
		expiredContractIDMap[c.ID] = struct{}{}
	}

	// Capturing end height to compare against renewed contracts
	endHeight := rc.ActiveContracts[0].EndHeight

	// Renew contracts by running out of funds
	startingUploadSpend, err := drainContractsByUploading(r, tg, contractor.MinContractFundRenewalThreshold)
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
	numRenewals++

	// Confirm contracts were renewed as expected.  Active contracts prior to
	// renewal should now be in the inactive contracts
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err = r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return errors.New("Waiting for active contracts to form")
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}

		// Confirm active and inactive contracts
		inactiveContractIDMap := make(map[types.FileContractID]struct{})
		for _, c := range rc.InactiveContracts {
			inactiveContractIDMap[c.ID] = struct{}{}
		}
		for _, c := range activeContracts {
			if _, ok := inactiveContractIDMap[c.ID]; !ok && c.UploadSpending.Cmp(startingUploadSpend) <= 0 {
				return errors.New("ID from activeContacts not found in rc")
			}
		}

		// Check that there are inactive contracts, and that the inactive
		// contracts correctly mark the GoodForUpload and GoodForRenew fields as
		// false.
		if len(rc.InactiveContracts) == 0 {
			return errors.New("no reported inactive contracts")
		}
		for _, c := range rc.InactiveContracts {
			if c.GoodForUpload || c.GoodForRenew {
				return errors.New("an inactive contract is being reported as either good for upload or good for renew")
			}
		}

		// Confirm expired contracts
		if len(expiredContracts) != len(rcExpired.ExpiredContracts) {
			return fmt.Errorf("Didn't get expected number of expired contracts, expected %v got %v", len(expiredContracts), len(rcExpired.ExpiredContracts))
		}
		for _, c := range rcExpired.ExpiredContracts {
			if _, ok := expiredContractIDMap[c.ID]; !ok {
				return errors.New("ID from rcExpired not found in expiredContracts")
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm contract end heights were set properly
	// End height should not have changed since the renewal
	// was due to running out of funds
	rc, err = r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		if c.EndHeight != endHeight && c.GoodForRenew && c.UploadSpending.Cmp(startingUploadSpend) <= 0 {
			t.Log("Allowance Period:", period)
			t.Log("Current Period:", currentPeriodStart)
			t.Fatalf("Contract endheight Changed, EH was %v, expected %v\n", c.EndHeight, endHeight)
		}
	}

	// Mine blocks to force contract renewal to start with fresh set of contracts
	if err = renewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}
	numRenewals++

	// Confirm Contracts were renewed as expected
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		// checkContracts will confirm correct number of inactive and active contracts
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test canceling contract
	// Grab contract to cancel
	rc, err = r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	contract := rc.ActiveContracts[0]
	// Cancel Contract
	if err := r.RenterContractCancelPost(contract.ID); err != nil {
		t.Fatal(err)
	}

	// Add a new host so new contract can be formed
	hostParams := node.Host(testDir + "/host")
	_, err = tg.AddNodes(hostParams)
	if err != nil {
		t.Fatal(err)
	}

	err = build.Retry(200, 100*time.Millisecond, func() error {
		// Check that Contract is now in inactive contracts and no longer in Active contracts
		rc, err = r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Confirm Renter has the expected number of contracts, meaning canceled contract should have been replaced.
		if len(rc.ActiveContracts) < len(tg.Hosts())-1 {
			return fmt.Errorf("Canceled contract was not replaced, only %v active contracts, expected at least %v", len(rc.ActiveContracts), len(tg.Hosts())-1)
		}
		for _, c := range rc.ActiveContracts {
			if c.ID == contract.ID {
				return errors.New("Contract not cancelled, contract found in Active Contracts")
			}
		}
		i := 1
		for _, c := range rc.InactiveContracts {
			if c.ID == contract.ID {
				break
			}
			if i == len(rc.InactiveContracts) {
				return errors.New("Contract not found in Inactive Contracts")
			}
			i++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterLosingHosts tests that hosts will be replaced if they go offline
// and downloads will succeed with hosts going offline until the redundancy
// drops below 1
func TestRenterLosingHosts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup without a renter so renter can be added with custom
	// allowance
	groupParams := siatest.GroupParams{
		Hosts:  4,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer tg.Close()

	// Add renter to the group
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Hosts = 3
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal("Failed to add renter:", err)
	}
	r := nodes[0]

	// Remember hosts with whom there are contracts
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	contractHosts := make(map[string]struct{})
	for _, c := range rc.ActiveContracts {
		if _, ok := contractHosts[c.HostPublicKey.String()]; ok {
			continue
		}
		contractHosts[c.HostPublicKey.String()] = struct{}{}
	}

	// Upload a file
	_, rf, err := r.UploadNewFileBlocking(100, 2, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// File should be at redundancy of 1.5
	files, err := r.RenterFilesGet()
	if err != nil {
		t.Fatal(err)
	}
	if files.Files[0].Redundancy != 1.5 {
		t.Fatal("Expected filed redundancy to be 1.5 but was", files.Files[0].Redundancy)
	}

	// Verify we can download the file
	_, err = r.DownloadToDisk(rf, false)
	if err != nil {
		t.Fatal(err)
	}

	// Stop one of the hosts that the renter has a contract with
	var pk types.SiaPublicKey
	for _, h := range tg.Hosts() {
		pk, err = h.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := contractHosts[pk.String()]; !ok {
			continue
		}
		if err = tg.StopNode(h); err != nil {
			t.Fatal(err)
		}
		break
	}

	// Wait for contract to be replaced
	loop := 0
	m := tg.Miners()[0]
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if loop%10 == 0 {
			if err := m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err = r.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != int(renterParams.Allowance.Hosts) {
			return fmt.Errorf("Expected %v contracts but got %v", int(renterParams.Allowance.Hosts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			if _, ok := contractHosts[c.HostPublicKey.String()]; !ok {
				contractHosts[c.HostPublicKey.String()] = struct{}{}
				return nil
			}
		}
		return errors.New("Contract not formed with new host")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Remove stopped host for map
	delete(contractHosts, pk.String())

	// Since there is another host, another contract should form and the
	// redundancy should stay at 1.5
	err = build.Retry(100, 100*time.Millisecond, func() error {
		files, err := r.RenterFilesGet()
		if err != nil {
			return err
		}
		if len(files.Files) == 0 {
			return errors.New("renter has no files")
		}
		if files.Files[0].Redundancy != 1.5 {
			return fmt.Errorf("Expected redundancy to be 1.5 but was %v", files.Files[0].Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify that renter can still download file
	_, err = r.DownloadToDisk(rf, false)
	if err != nil {
		t.Fatal(err)
	}

	// Stop another one of the hosts that the renter has a contract with
	for _, h := range tg.Hosts() {
		pk, err = h.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := contractHosts[pk.String()]; !ok {
			continue
		}
		if err = tg.StopNode(h); err != nil {
			t.Fatal(err)
		}
		break
	}
	// Remove stopped host for map
	delete(contractHosts, pk.String())

	// Now that the renter has fewer hosts online than needed the redundancy
	// should drop to 1
	err = build.Retry(100, 100*time.Millisecond, func() error {
		files, err := r.RenterFilesGet()
		if err != nil {
			return err
		}
		if files.Files[0].Redundancy != 1 {
			return fmt.Errorf("Expected redundancy to be 1 but was %v", files.Files[0].Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify that renter can still download file
	if _, err = r.DownloadToDisk(rf, false); err != nil {
		r.PrintDebugInfo(t, true, false, true)
		t.Fatal(err)
	}

	// Stop another one of the hosts that the renter has a contract with
	for _, h := range tg.Hosts() {
		pk, err = h.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := contractHosts[pk.String()]; !ok {
			continue
		}
		if err = tg.StopNode(h); err != nil {
			t.Fatal(err)
		}
		break
	}
	// Remove stopped host for map
	delete(contractHosts, pk.String())

	// Now that the renter only has one host online the redundancy should be 0.5
	err = build.Retry(100, 100*time.Millisecond, func() error {
		files, err := r.RenterFilesGet()
		if err != nil {
			return err
		}
		if files.Files[0].Redundancy != 0.5 {
			return fmt.Errorf("Expected redundancy to be 0.5 but was %v", files.Files[0].Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the download will now fail because the file is less than a
	// redundancy of 1
	_, err = r.DownloadToDisk(rf, false)
	if err == nil {
		t.Fatal("Expected download to fail")
	}
}

// TestRenterFailingStandbyDownload checks a very specific edge case regarding
// standby workers. It uploads a file with a 2/3 redundancy to 4 hosts, causes
// a single piece to be stored on 2 hosts. Then it will take 3 hosts offline,
// Since 4 hosts are in the worker pool but only 2 are needed, Sia will put 2
// of them on standby and try to download from the other 2. Since only 1 worker
// can succeed, Sia should wake up one worker after another until it finally
// realizes that it doesn't have enough workers and the download fails.
func TestRenterFailingStandbyDownload(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup without a renter so renter can be added with custom
	// allowance
	groupParams := siatest.GroupParams{
		Hosts:  4,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer tg.Close()

	// Add renter to the group
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Hosts = 3
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal("Failed to add renter:", err)
	}
	r := nodes[0]

	// Remember hosts with whom there are contracts
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	contractHosts := make(map[string]struct{})
	for _, c := range rc.ActiveContracts {
		if _, ok := contractHosts[c.HostPublicKey.String()]; ok {
			continue
		}
		contractHosts[c.HostPublicKey.String()] = struct{}{}
	}

	// Upload a file
	_, rf, err := r.UploadNewFileBlocking(100, 2, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// File should be at redundancy of 1.5
	files, err := r.RenterFilesGet()
	if err != nil {
		t.Fatal(err)
	}
	if files.Files[0].Redundancy != 1.5 {
		t.Fatal("Expected filed redundancy to be 1.5 but was", files.Files[0].Redundancy)
	}

	// Stop one of the hosts that the renter has a contract with
	var pk types.SiaPublicKey
	var stoppedHost *siatest.TestNode
	for _, h := range tg.Hosts() {
		pk, err = h.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := contractHosts[pk.String()]; !ok {
			continue
		}
		if err = tg.StopNode(h); err != nil {
			t.Fatal(err)
		}
		stoppedHost = h
		break
	}

	// Wait for contract to be replaced
	loop := 0
	m := tg.Miners()[0]
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if loop%10 == 0 {
			if err := m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err = r.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != int(renterParams.Allowance.Hosts) {
			return fmt.Errorf("Expected %v contracts but got %v", int(renterParams.Allowance.Hosts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			if _, ok := contractHosts[c.HostPublicKey.String()]; !ok {
				return nil
			}
		}
		return errors.New("Contract not formed with new host")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Since there is another host, another contract should form and the
	// redundancy should stay at 1.5
	err = build.Retry(100, 100*time.Millisecond, func() error {
		files, err := r.RenterFilesGet()
		if err != nil {
			return err
		}
		if files.Files[0].Redundancy != 1.5 {
			return fmt.Errorf("Expected redundancy to be 1.5 but was %v", files.Files[0].Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Bring the stopped host back up.
	pk, _ = stoppedHost.HostPublicKey()
	if err := tg.StartNode(stoppedHost); err != nil {
		t.Fatal(err)
	}

	// Announce it again to speed discovery up.
	if err := stoppedHost.HostAnnouncePost(); err != nil {
		t.Fatal(err)
	}

	// Wait until the contract is considered good again.
	loop = 0
	err = build.Retry(600, 500*time.Millisecond, func() error {
		if loop%10 == 0 {
			if err := m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err = r.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != int(renterParams.Allowance.Hosts)+1 {
			return fmt.Errorf("Expected %v contracts but got %v", renterParams.Allowance.Hosts+1, len(rc.ActiveContracts))
		}
		return nil
	})
	if err != nil {
		r.PrintDebugInfo(t, true, false, true)
		t.Fatal(err)
	}

	// Stop 3 out of 4 hosts. We didn't add the replacement host to
	// contractHosts so it should contain the original 3 hosts.
	stoppedHosts := 0
	for _, h := range tg.Hosts() {
		pk, err = h.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := contractHosts[pk.String()]; !ok {
			continue
		}
		if err = tg.StopNode(h); err != nil {
			t.Fatal(err)
		}
		stoppedHosts++
	}

	// Check that we stopped the right amount of hosts.
	if stoppedHosts != len(tg.Hosts())-1 {
		t.Fatalf("Expected to stop %v hosts but was %v", stoppedHosts, len(tg.Hosts())-1)
	}

	// Verify that the download will now fail because the file is less than a
	// redundancy of 1
	_, err = r.DownloadToDisk(rf, false)
	if err == nil {
		t.Fatal("Expected download to fail")
	}
}

// TestRenterPersistData checks if the RenterSettings are persisted
func TestRenterPersistData(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Get test directory
	testDir := renterTestDir(t.Name())

	// Copying legacy file to test directory
	source := "../../compatibility/renter_v04.json"
	destination := filepath.Join(testDir, "renter", "renter.json")
	if err := copyFile(source, destination); err != nil {
		t.Fatal(err)
	}

	// Create new node from legacy renter.json persistence file
	r, err := siatest.NewNode(node.AllModules(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err = r.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Set renter allowance to finish renter set up
	// Currently /renter POST endpoint errors if the allowance
	// is not previously set or passed in as an argument
	err = r.RenterPostAllowance(siatest.DefaultAllowance)
	if err != nil {
		t.Fatal(err)
	}

	// Check Settings, should be defaults
	rg, err := r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	if rg.Settings.StreamCacheSize != renter.DefaultStreamCacheSize {
		t.Fatalf("StreamCacheSize not set to default of %v, set to %v",
			renter.DefaultStreamCacheSize, rg.Settings.StreamCacheSize)
	}
	if rg.Settings.MaxDownloadSpeed != renter.DefaultMaxDownloadSpeed {
		t.Fatalf("MaxDownloadSpeed not set to default of %v, set to %v",
			renter.DefaultMaxDownloadSpeed, rg.Settings.MaxDownloadSpeed)
	}
	if rg.Settings.MaxUploadSpeed != renter.DefaultMaxUploadSpeed {
		t.Fatalf("MaxUploadSpeed not set to default of %v, set to %v",
			renter.DefaultMaxUploadSpeed, rg.Settings.MaxUploadSpeed)
	}

	// Set StreamCacheSize, MaxDownloadSpeed, and MaxUploadSpeed to new values
	cacheSize := uint64(4)
	ds := int64(20)
	us := int64(10)
	if err := r.RenterSetStreamCacheSizePost(cacheSize); err != nil {
		t.Fatalf("%v: Could not set StreamCacheSize to %v", err, cacheSize)
	}
	if err := r.RenterPostRateLimit(ds, us); err != nil {
		t.Fatalf("%v: Could not set RateLimits to %v and %v", err, ds, us)
	}

	// Confirm Settings were updated
	rg, err = r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	if rg.Settings.StreamCacheSize != cacheSize {
		t.Fatalf("StreamCacheSize not set to %v, set to %v", cacheSize, rg.Settings.StreamCacheSize)
	}
	if rg.Settings.MaxDownloadSpeed != ds {
		t.Fatalf("MaxDownloadSpeed not set to %v, set to %v", ds, rg.Settings.MaxDownloadSpeed)
	}
	if rg.Settings.MaxUploadSpeed != us {
		t.Fatalf("MaxUploadSpeed not set to %v, set to %v", us, rg.Settings.MaxUploadSpeed)
	}

	// Restart node
	err = r.RestartNode()
	if err != nil {
		t.Fatal("Failed to restart node:", err)
	}

	// check Settings, settings should be values set through API endpoints
	rg, err = r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	if rg.Settings.StreamCacheSize != cacheSize {
		t.Fatalf("StreamCacheSize not persisted as %v, set to %v", cacheSize, rg.Settings.StreamCacheSize)
	}
	if rg.Settings.MaxDownloadSpeed != ds {
		t.Fatalf("MaxDownloadSpeed not persisted as %v, set to %v", ds, rg.Settings.MaxDownloadSpeed)
	}
	if rg.Settings.MaxUploadSpeed != us {
		t.Fatalf("MaxUploadSpeed not persisted as %v, set to %v", us, rg.Settings.MaxUploadSpeed)
	}
}

// TestRenterSpendingReporting checks the accuracy for the reported
// spending
func TestRenterSpendingReporting(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup, creating without renter so the renter's
	// initial balance can be obtained
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a Renter node
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.SkipSetAllowance = true
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := nodes[0]

	// Get largest WindowSize from Hosts
	var windowSize types.BlockHeight
	for _, h := range tg.Hosts() {
		hg, err := h.HostGet()
		if err != nil {
			t.Fatal(err)
		}
		if hg.ExternalSettings.WindowSize >= windowSize {
			windowSize = hg.ExternalSettings.WindowSize
		}
	}

	// Get renter's initial siacoin balance
	wg, err := r.WalletGet()
	if err != nil {
		t.Fatal("Failed to get wallet:", err)
	}
	initialBalance := wg.ConfirmedSiacoinBalance

	// Set allowance
	if err = tg.SetRenterAllowance(r, siatest.DefaultAllowance); err != nil {
		t.Fatal("Failed to set renter allowance:", err)
	}
	numRenewals := 0

	// Confirm Contracts were created as expected, check that the funds
	// allocated when setting the allowance are reflected correctly in the
	// wallet balance
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		err = checkBalanceVsSpending(r, initialBalance)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Upload and download files to show spending
	var remoteFiles []*siatest.RemoteFile
	for i := 0; i < 10; i++ {
		dataPieces := uint64(1)
		parityPieces := uint64(1)
		fileSize := 100 + siatest.Fuzz()
		_, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
		if err != nil {
			t.Fatal("Failed to upload a file for testing: ", err)
		}
		remoteFiles = append(remoteFiles, rf)
	}
	for _, rf := range remoteFiles {
		_, err = r.DownloadToDisk(rf, false)
		if err != nil {
			t.Fatal("Could not DownloadToDisk:", err)
		}
	}

	// Check to confirm upload and download spending was captured correctly
	// and reflected in the wallet balance
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = checkBalanceVsSpending(r, initialBalance)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine blocks to force contract renewal
	if err = renewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}
	numRenewals++

	// Confirm Contracts were renewed as expected
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		if err = checkRenewedContracts(rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine Block to confirm contracts and spending into blockchain
	m := tg.Miners()[0]
	if err = m.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}

	// Check contract spending against reported spending
	rc, err := r.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	rcExpired, err := r.RenterExpiredContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkContractVsReportedSpending(r, windowSize, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
		t.Fatal(err)
	}

	// Check to confirm reported spending is still accurate with the renewed contracts
	// and reflected in the wallet balance
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = checkBalanceVsSpending(r, initialBalance)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Record current Wallet Balance
	wg, err = r.WalletGet()
	if err != nil {
		t.Fatal("Failed to get wallet:", err)
	}
	initialPeriodEndBalance := wg.ConfirmedSiacoinBalance

	// Mine blocks to force contract renewal and new period
	cg, err := r.ConsensusGet()
	if err != nil {
		t.Fatal("Failed to get consensus:", err)
	}
	blockHeight := cg.Height
	endHeight := rc.ActiveContracts[0].EndHeight
	rg, err := r.RenterGet()
	if err != nil {
		t.Fatal("Failed to get renter:", err)
	}
	rw := rg.Settings.Allowance.RenewWindow
	for i := 0; i < int(endHeight-rw-blockHeight+types.MaturityDelay); i++ {
		if err = m.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	numRenewals++

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}

	// Check if Unspent unallocated funds were released after allowance period
	// was exceeded
	wg, err = r.WalletGet()
	if err != nil {
		t.Fatal("Failed to get wallet:", err)
	}
	if initialPeriodEndBalance.Cmp(wg.ConfirmedSiacoinBalance) > 0 {
		t.Fatal("Unspent Unallocated funds not released after contract renewal and maturity delay")
	}

	// Confirm Contracts were renewed as expected
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		if err = checkRenewedContracts(rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine Block to confirm contracts and spending on blockchain
	if err = m.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}

	// Check contract spending against reported spending
	rc, err = r.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	rcExpired, err = r.RenterExpiredContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkContractVsReportedSpending(r, windowSize, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
		t.Fatal(err)
	}

	// Check to confirm reported spending is still accurate with the renewed contracts
	// and a new period and reflected in the wallet balance
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = checkBalanceVsSpending(r, initialBalance)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Renew contracts by running out of funds
	_, err = drainContractsByUploading(r, tg, contractor.MinContractFundRenewalThreshold)
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
	numRenewals++

	// Confirm Contracts were renewed as expected
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		if err = checkRenewedContracts(rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine Block to confirm contracts and spending on blockchain
	if err = m.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}

	// Check contract spending against reported spending
	rc, err = r.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	rcExpired, err = r.RenterExpiredContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkContractVsReportedSpending(r, windowSize, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
		t.Fatal(err)
	}

	// Check to confirm reported spending is still accurate with the renewed contracts
	// and a new period and reflected in the wallet balance
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = checkBalanceVsSpending(r, initialBalance)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine blocks to force contract renewal
	if err = renewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}
	numRenewals++

	// Confirm Contracts were renewed as expected
	err = build.Retry(200, 100*time.Millisecond, func() error {
		rc, err := r.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		rcExpired, err := r.RenterExpiredContractsGet()
		if err != nil {
			return err
		}
		if err = checkContracts(len(tg.Hosts()), numRenewals, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
			return err
		}
		if err = checkRenewedContracts(rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine Block to confirm contracts and spending into blockchain
	if err = m.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		t.Fatal(err)
	}

	// Check contract spending against reported spending
	rc, err = r.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	rcExpired, err = r.RenterExpiredContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkContractVsReportedSpending(r, windowSize, append(rc.InactiveContracts, rcExpired.ExpiredContracts...), rc.ActiveContracts); err != nil {
		t.Fatal(err)
	}

	// Check to confirm reported spending is still accurate with the renewed contracts
	// and reflected in the wallet balance
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = checkBalanceVsSpending(r, initialBalance)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// testZeroByteFile tests uploading and downloading a 0 and 1 byte file
func testZeroByteFile(t *testing.T, tg *siatest.TestGroup) {
	// Grab renter
	r := tg.Renters()[0]

	// Create 0 and 1 byte file
	zeroByteFile := 0
	oneByteFile := 1

	// Test uploading 0 byte file
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	redundancy := float64((dataPieces + parityPieces) / dataPieces)
	_, zeroRF, err := r.UploadNewFile(zeroByteFile, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	// Get zerobyte file
	rf, err := r.File(zeroRF)
	if err != nil {
		t.Fatal(err)
	}
	// Check redundancy and upload progress
	if rf.Redundancy != redundancy {
		t.Fatalf("Expected redundancy to be %v, got %v", redundancy, rf.Redundancy)
	}
	if rf.UploadProgress != 100 {
		t.Fatalf("Expected upload progress to be 100, got %v", rf.UploadProgress)
	}
	// Check health information
	if rf.Health != 0 {
		t.Fatalf("Expected health to be 0, got %v", rf.Health)
	}
	if rf.MaxHealth != 0 {
		t.Fatalf("Expected max health to be 0, got %v", rf.MaxHealth)
	}
	if rf.MaxHealthPercent != 100 {
		t.Fatalf("Expected max health percentage to be 100, got %v", rf.MaxHealthPercent)
	}
	if rf.NumStuckChunks != 0 {
		t.Fatalf("Expected number of stuck chunks to be 0, got %v", rf.NumStuckChunks)
	}
	if rf.Stuck {
		t.Fatalf("Expected file not to be stuck")
	}
	if rf.StuckHealth != 0 {
		t.Fatalf("Expected stuck health to be 0, got %v", rf.StuckHealth)
	}

	// Test uploading 1 byte file
	_, oneRF, err := r.UploadNewFileBlocking(oneByteFile, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Test downloading 0 byte file
	_, err = r.DownloadToDisk(zeroRF, false)
	if err != nil {
		t.Fatal(err)
	}

	// Test downloading 1 byte file
	_, err = r.DownloadToDisk(oneRF, false)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterFileChangeDuringDownload confirms that a download will continue and
// succeed if the file is renamed or deleted after the download has started
func TestRenterFileChangeDuringDownload(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup,
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Renters: 1,
		Miners:  1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Grab Renter and upload file
	r := tg.Renters()[0]
	dataPieces := uint64(1)
	parityPieces := uint64(1)
	chunkSize := int64(siatest.ChunkSize(dataPieces, crypto.TypeDefaultRenter))
	fileSize := 3 * int(chunkSize)
	_, rf1, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	_, rf2, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	_, rf3, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	_, rf4, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	_, rf5, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Set the bandwidth limit to 1 chunk per second.
	if err := r.RenterPostRateLimit(chunkSize, chunkSize); err != nil {
		t.Fatal(err)
	}

	// Create Wait group
	wg := new(sync.WaitGroup)

	// Test Renaming while Downloading and Streaming on 5 files.
	wg.Add(1)
	go renameDuringDownloadAndStream(r, rf1, t, wg, time.Second)
	wg.Add(1)
	go renameDuringDownloadAndStream(r, rf2, t, wg, time.Second)
	wg.Add(1)
	go renameDuringDownloadAndStream(r, rf3, t, wg, time.Second)
	wg.Add(1)
	go renameDuringDownloadAndStream(r, rf4, t, wg, time.Second)
	wg.Add(1)
	go renameDuringDownloadAndStream(r, rf5, t, wg, time.Second)
	wg.Wait()

	// Test Deleting while Downloading and Streaming
	//
	// Download the file
	wg.Add(1)
	go deleteDuringDownloadAndStream(r, rf1, t, wg, time.Second)
	wg.Add(1)
	go deleteDuringDownloadAndStream(r, rf2, t, wg, time.Second)
	wg.Add(1)
	go deleteDuringDownloadAndStream(r, rf3, t, wg, time.Second)
	wg.Add(1)
	go deleteDuringDownloadAndStream(r, rf4, t, wg, time.Second)
	wg.Add(1)
	go deleteDuringDownloadAndStream(r, rf5, t, wg, time.Second)

	wg.Wait()
}

// testSetFileTrackingPath tests if changing the repairPath of a file works.
func testSetFileTrackingPath(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]
	// Check that we have enough hosts for this test.
	if len(tg.Hosts()) < 2 {
		t.Fatal("This test requires at least 2 hosts")
	}
	// Set fileSize and redundancy for upload
	fileSize := int(modules.SectorSize)
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces

	// Upload file
	localFile, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}
	// Move the file to a new location.
	if err := localFile.Move(); err != nil {
		t.Fatal(err)
	}
	// Take down all the hosts.
	numHosts := len(tg.Hosts())
	for _, host := range tg.Hosts() {
		if err := tg.RemoveNode(host); err != nil {
			t.Fatal("Failed to shutdown host", err)
		}
	}
	// File should have 0 redundancy now.
	if err := renter.WaitForDecreasingRedundancy(remoteFile, 0); err != nil {
		t.Fatal("Redundancy isn't decreasing", err)
	}
	// Rename the repairPath to match the new location.
	if err := renter.SetFileRepairPath(remoteFile, localFile); err != nil {
		t.Fatal("Failed to change the repair path", err)
	}
	// Create new hosts.
	_, err = tg.AddNodeN(node.HostTemplate, numHosts)
	if err != nil {
		t.Fatal("Failed to create a new host", err)
	}
	// We should reach full redundancy again.
	expectedRedundancy := float64((dataPieces + parityPieces)) / float64(dataPieces)
	if err := renter.WaitForUploadRedundancy(remoteFile, expectedRedundancy); err != nil {
		t.Logf("numHosts: %v", len(tg.Hosts()))
		t.Fatal("File wasn't repaired", err)
	}
	// We should be able to download
	if _, err := renter.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download file", err)
	}
	// Create a new file that is smaller than the first one.
	smallFile, err := renter.FilesDir().NewFile(fileSize - 1)
	if err != nil {
		t.Fatal(err)
	}
	// Try to change the repairPath of the remote file again. This shouldn't
	// work.
	if err := renter.SetFileRepairPath(remoteFile, smallFile); err == nil {
		t.Fatal("Changing repair path to file of different size shouldn't work")
	}
	// Delete the small file and try again. This also shouldn't work.
	if err := smallFile.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := renter.SetFileRepairPath(remoteFile, smallFile); err == nil {
		t.Fatal("Changing repair path to a nonexistent file shouldn't work")
	}
}

// TestRenterFileContractIdentifier checks that the file contract's identifier
// is set correctly when forming a contract and after renewing it.
func TestRenterFileContractIdentifier(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup, creating without renter so the renter's
	// contract transactions can easily be obtained.
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a Renter node
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := nodes[0]

	rcg, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Get the endheight of the contracts.
	eh := rcg.ActiveContracts[0].EndHeight

	// Get the blockheight.
	cg, err := tg.Hosts()[0].ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	bh := cg.Height

	// Mine blocks until we reach the endheight
	m := tg.Miners()[0]
	for i := 0; i < int(eh-bh); i++ {
		if err := m.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Confirm that the contracts got renewed.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Mine a block.
		if err := m.MineBlock(); err != nil {
			t.Fatal(err)
		}
		// Get the contracts from the renter.
		rcg, err := r.RenterExpiredContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		// We should have one contract for each host.
		if len(rcg.ActiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v active contracts but got %v",
				len(tg.Hosts()), rcg.ActiveContracts)
		}
		// We should have one expired contract for each host.
		if len(rcg.ExpiredContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v expired contracts but got %v",
				len(tg.Hosts()), len(rcg.ExpiredContracts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get the transaction which are related to the renter since we started the
	// renter.
	txns, err := r.WalletTransactionsGet(0, ^types.BlockHeight(0))
	if err != nil {
		t.Fatal(err)
	}
	// Filter out transactions without file contracts.
	var fcTxns []modules.ProcessedTransaction
	for _, txn := range txns.ConfirmedTransactions {
		if len(txn.Transaction.FileContracts) > 0 {
			fcTxns = append(fcTxns, txn)
		}
	}
	// There should be twice as many transactions with contracts as there are hosts.
	if len(fcTxns) != 2*len(tg.Hosts()) {
		t.Fatalf("Expected %v txns but got %v", 2*len(tg.Hosts()), len(fcTxns))
	}

	// Get the wallet seed of the renter.
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed, err := modules.StringToSeed(wsg.PrimarySeed, "english")
	if err != nil {
		t.Fatal(err)
	}
	renterSeed := proto.DeriveRenterSeed(seed)
	defer fastrand.Read(renterSeed[:])

	// Check the arbitrary data of each transaction and contract.
	for _, fcTxn := range fcTxns {
		txn := fcTxn.Transaction
		for _, fc := range txn.FileContracts {
			// Check that the arbitrary data has correct length.
			if len(txn.ArbitraryData) != 1 {
				t.Fatal("arbitrary data has wrong length")
			}
			csi := proto.ContractSignedIdentifier{}
			n := copy(csi[:], txn.ArbitraryData[0])
			encryptedHostKey := txn.ArbitraryData[0][n:]
			// Calculate the renter seed given the WindowStart of the contract.
			rs := renterSeed.EphemeralRenterSeed(fc.WindowStart)
			// Check if the identifier is valid.
			spk, valid := csi.IsValid(rs, txn, encryptedHostKey)
			if !valid {
				t.Fatal("identifier is invalid")
			}
			// Check that the host's key is a valid key from the hostb.
			_, err := r.HostDbHostsGet(spk)
			if err != nil {
				t.Fatal("hostKey is invalid", err)
			}
		}
	}
}

// TestUploadAfterDelete tests that rapidly uploading a file to the same
// siapath as a previously deleted file works.
func TestUploadAfterDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a Renter node
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.RenterDeps = &dependencyDisableCloseUploadEntry{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// Upload file, creating a piece for each host in the group
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := int(modules.SectorSize)
	localFile, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	// Repeatedly upload and delete a file with the same SiaPath without
	// closing the entry. That shouldn't cause issues.
	for i := 0; i < 5; i++ {
		// Delete the file.
		if err := renter.RenterDeletePost(remoteFile.SiaPath()); err != nil {
			t.Fatal(err)
		}
		// Upload the file again right after deleting it.
		if _, err := renter.UploadBlocking(localFile, dataPieces, parityPieces, false); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRenterContractRecovery tests that recovering a node from a seed that has
// contracts associated with it will recover those contracts.
func TestRenterContractRecovery(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup, creating without renter so the renter's
	// contract transactions can easily be obtained.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get the renter node and its seed.
	r := tg.Renters()[0]
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed := wsg.PrimarySeed

	// Upload a file to the renter.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := int(10 * modules.SectorSize)
	lf, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Remember the contracts the renter formed with the hosts.
	oldContracts := make(map[types.FileContractID]api.RenterContract)
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		oldContracts[c.ID] = c
	}

	// Stop the renter.
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}

	// Copy the siafile to the new location.
	oldPath := filepath.Join(r.Dir, modules.RenterDir, modules.SiapathRoot, lf.FileName()+modules.SiaFileExtension)
	siaFile, err := ioutil.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	newRenterDir := filepath.Join(testDir, "renter")
	newPath := filepath.Join(newRenterDir, modules.RenterDir, modules.SiapathRoot, lf.FileName()+modules.SiaFileExtension)
	if err := os.MkdirAll(filepath.Dir(newPath), 0777); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(newPath, siaFile, 0777); err != nil {
		t.Fatal(err)
	}

	// Start a new renter with the same seed.
	renterParams := node.Renter(newRenterDir)
	renterParams.PrimarySeed = seed
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	newRenter := nodes[0]

	// Make sure that the new renter actually uses the same primary seed.
	wsg, err = newRenter.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	newRenterSeed := wsg.PrimarySeed
	if seed != newRenterSeed {
		t.Log("old seed", seed)
		t.Log("new seed", newRenterSeed)
		t.Fatal("Seeds of new and old renters don't match")
	}

	// The new renter should have the same active contracts as the old one.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = newRenter.RenterContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.ActiveContracts) != len(oldContracts) {
			return fmt.Errorf("Didn't recover the right number of contracts, expected %v but was %v",
				len(oldContracts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			contract, exists := oldContracts[c.ID]
			if !exists {
				return errors.New(fmt.Sprint("Recovered unknown contract", c.ID))
			}
			if contract.HostPublicKey.String() != c.HostPublicKey.String() {
				return errors.New("public keys don't match")
			}
			if contract.StartHeight != c.StartHeight {
				return errors.New("startheights don't match")
			}
			if contract.EndHeight != c.EndHeight {
				return errors.New("endheights don't match")
			}
			if c.Fees.Cmp(types.ZeroCurrency) <= 0 {
				return errors.New("Fees wasn't set")
			}
			if contract.GoodForRenew != c.GoodForRenew {
				return errors.New("GoodForRenew doesn't match")
			}
			if contract.GoodForUpload != c.GoodForUpload {
				return errors.New("GoodForRenew doesn't match")
			}
		}
		return nil
	})
	if err != nil {
		rc, _ = newRenter.RenterContractsGet()
		t.Log("Contracts in total:", len(rc.Contracts))
		t.Fatal(err)
	}
	// Download the whole file again to see if all roots were recovered.
	_, err = newRenter.DownloadByStream(rf)
	if err != nil {
		t.Fatal(err)
	}
}

// TestSiafileCompatCode checks that legacy renters can upgrade to the latest
// siafile format.
func TestSiafileCompatCode(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Get test directory
	testDir := renterTestDir(t.Name())

	// The siapath stored in the legacy file.
	expectedSiaPath := "sub1/sub2/testfile"

	// Copying legacy file to test directory
	renterDir := filepath.Join(testDir, "renter")
	source := filepath.Join("..", "..", "compatibility", "siafile_v1.3.7.sia")
	destination := filepath.Join(renterDir, "sub1", "sub2", "testfile.sia")
	if err := copyFile(source, destination); err != nil {
		t.Fatal(err)
	}
	// Copy the legacy settings file to the test directory.
	source2 := "../../compatibility/renter_v137.json"
	destination2 := filepath.Join(renterDir, "renter.json")
	if err := copyFile(source2, destination2); err != nil {
		t.Fatal(err)
	}
	// Copy the legacy contracts into the test directory.
	contractsSource := "../../compatibility/contracts_v137"
	contracts, err := ioutil.ReadDir(contractsSource)
	if err != nil {
		t.Fatal(err)
	}
	for _, fi := range contracts {
		contractDst := filepath.Join(contractsSource, fi.Name())
		err := copyFile(contractDst, filepath.Join(renterDir, "contracts", fi.Name()))
		if err != nil {
			t.Fatal(err)
		}
	}

	// Create new node with legacy sia file.
	r, err := siatest.NewNode(node.AllModules(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err = r.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Check that exactly 1 siafile exists and that it's the correct one.
	fis, err := r.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(fis) != 1 {
		t.Fatal("Expected 1 file but got", len(fis))
	}
	if fis[0].SiaPath != expectedSiaPath {
		t.Fatalf("Siapath should be '%v' but was '%v'",
			expectedSiaPath, fis[0].SiaPath)
	}
	// Make sure the folder containing the legacy file was deleted.
	if _, err := os.Stat(filepath.Join(renterDir, "sub1")); !os.IsNotExist(err) {
		t.Fatal("Error should be ErrNotExist but was", err)
	}
	// Make sure the siafile is exactly where we would expect it.
	expectedLocation := filepath.Join(renterDir, "siafiles", "sub1", "sub2", "testfile.sia")
	if _, err := os.Stat(expectedLocation); err != nil {
		t.Fatal(err)
	}
	// Check the other fields of the file.
	sf := fis[0]
	if sf.AccessTime.IsZero() {
		t.Fatal("AccessTime wasn't set correctly")
	}
	if sf.ChangeTime.IsZero() {
		t.Fatal("ChangeTime wasn't set correctly")
	}
	if sf.CreateTime.IsZero() {
		t.Fatal("CreateTime wasn't set correctly")
	}
	if sf.ModTime.IsZero() {
		t.Fatal("ModTime wasn't set correctly")
	}
	if sf.Available {
		t.Fatal("File shouldn't be available since we don't know the hosts")
	}
	if sf.CipherType != crypto.TypeTwofish.String() {
		t.Fatal("CipherType should be twofish but was", sf.CipherType)
	}
	if sf.Filesize != 4096 {
		t.Fatal("Filesize should be 4096 but was", sf.Filesize)
	}
	if sf.Expiration != 91 {
		t.Fatal("Expiration should be 91 but was", sf.Expiration)
	}
	if sf.LocalPath != "/tmp/SiaTesting/siatest/TestRenterTwo/gctwr-EKYAZSVOZ6U2T4HZYIAQ/files/4096bytes 16951a61" {
		t.Fatal("LocalPath doesn't match")
	}
	if sf.Redundancy != 0 {
		t.Fatal("Redundancy should be 0 since we don't know the hosts")
	}
	if sf.UploadProgress != 100 {
		t.Fatal("File was uploaded before so the progress should be 100")
	}
	if sf.UploadedBytes != 40960 {
		t.Fatal("Redundancy should be 10/20 so 10x the Filesize = 40960 bytes should be uploaded")
	}
	if sf.OnDisk {
		t.Fatal("OnDisk should be false but was true")
	}
	if sf.Recoverable {
		t.Fatal("Recoverable should be false but was true")
	}
	if !sf.Renewing {
		t.Fatal("Renewing should be true but wasn't")
	}
}

// TestRenterContractInitRecoveryScan tests that a renter which has already
// scanned the whole blockchain and has lost its contracts, can recover them by
// triggering a rescan through the API.
func TestRenterContractInitRecoveryScan(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a Renter node
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.ContractorDeps = &dependencyDisableRecoveryStatusReset{}
	_, err = tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := tg.Renters()[0]

	// Upload a file to the renter.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := int(10 * modules.SectorSize)
	_, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Remember the contracts the renter formed with the hosts.
	oldContracts := make(map[types.FileContractID]api.RenterContract)
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		oldContracts[c.ID] = c
	}

	// Cancel the allowance to avoid new contracts replacing the recoverable
	// ones.
	if err := r.RenterCancelAllowance(); err != nil {
		t.Fatal(err)
	}

	// Stop the renter.
	if err := tg.StopNode(r); err != nil {
		t.Fatal(err)
	}

	// Delete the contracts.
	if err := os.RemoveAll(filepath.Join(r.Dir, modules.RenterDir, "contracts")); err != nil {
		t.Fatal(err)
	}

	// Start the renter again.
	if err := tg.StartNode(r); err != nil {
		t.Fatal(err)
	}

	// The renter shouldn't have any contracts.
	rcg, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rcg.ActiveContracts)+len(rcg.InactiveContracts)+len(rcg.ExpiredContracts) > 0 {
		t.Fatal("There shouldn't be any contracts after deleting them")
	}

	// Trigger a rescan of the blockchain.
	if err := r.RenterInitContractRecoveryScanPost(); err != nil {
		t.Fatal(err)
	}

	// The new renter should have the same active contracts as the old one.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = r.RenterContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.ActiveContracts) != len(oldContracts) {
			return fmt.Errorf("Didn't recover the right number of contracts, expected %v but was %v",
				len(oldContracts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			contract, exists := oldContracts[c.ID]
			if !exists {
				return errors.New(fmt.Sprint("Recovered unknown contract", c.ID))
			}
			if contract.HostPublicKey.String() != c.HostPublicKey.String() {
				return errors.New("public keys don't match")
			}
			if contract.EndHeight != c.EndHeight {
				return errors.New("endheights don't match")
			}
			if contract.GoodForRenew != c.GoodForRenew {
				return errors.New("GoodForRenew doesn't match")
			}
			if contract.GoodForUpload != c.GoodForUpload {
				return errors.New("GoodForRenew doesn't match")
			}
		}
		return nil
	})
	if err != nil {
		rc, _ = r.RenterContractsGet()
		t.Log("Contracts in total:", len(rc.Contracts))
		t.Fatal(err)
	}
	// Download the whole file again to see if all roots were recovered.
	_, err = r.DownloadByStream(rf)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the RecoveryScanStatus was set.
	rrs, err := r.RenterContractRecoveryProgressGet()
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Check the recovery progress endpoint.
		if !rrs.ScanInProgress || rrs.ScannedHeight == 0 {
			return fmt.Errorf("ScanInProgress and/or ScannedHeight weren't set correctly: %v", rrs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestCreateLoadBackup tests that creating a backup with the /renter/backup
// endpoint works as expected and that it can be loaded with the
// /renter/recoverbackup endpoint.
func TestCreateLoadBackup(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Create a subdir in the renter's files folder.
	r := tg.Renters()[0]
	subDir, err := r.FilesDir().CreateDir("subDir")
	if err != nil {
		t.Fatal(err)
	}
	// Add a file to that dir.
	lf, err := subDir.NewFile(100)
	if err != nil {
		t.Fatal(err)
	}
	// Upload the file.
	dataPieces := uint64(len(tg.Hosts()) - 1)
	parityPieces := uint64(1)
	rf, err := r.UploadBlocking(lf, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}
	// Create a backup.
	backupPath := filepath.Join(r.FilesDir().Path(), "test.backup")
	err = r.RenterCreateBackupPost(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	// Get the renter's seed.
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Shut down the renter.
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}
	// Start a new renter from the same seed.
	rt := node.RenterTemplate
	rt.PrimarySeed = wsg.PrimarySeed
	nodes, err := tg.AddNodes(rt)
	if err != nil {
		t.Fatal(err)
	}
	r = nodes[0]
	// Recover the backup.
	if err := r.RenterRecoverBackupPost(backupPath); err != nil {
		t.Fatal(err)
	}
	// The file should be available and ready for download again.
	if _, err := r.DownloadByStream(rf); err != nil {
		t.Fatal(err)
	}
	// Recover the backup again. Now there should be another file with a suffix
	// at the end.
	if err := r.RenterRecoverBackupPost(backupPath); err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileGet(rf.SiaPath() + "_1")
	if err != nil {
		t.Fatal(err)
	}
}

// TestRemoveRecoverableContracts makes sure that recoverable contracts which
// have been reverted by a reorg are removed from the map.
func TestRemoveRecoverableContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup, creating without renter so the renter's
	// contract transactions can easily be obtained.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get the renter node and its seed.
	r := tg.Renters()[0]
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed := wsg.PrimarySeed

	// The renter should have one contract with each host.
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.ActiveContracts) != len(tg.Hosts()) {
		t.Fatal("Insufficient active contracts")
	}

	// Stop the renter.
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}
	// Bring up new hosts for the new renter to form contracts with, otherwise no
	// contracts will form because it will not form contracts with hosts it see to
	// have recoverable contracts with
	_, err = tg.AddNodeN(node.HostTemplate, 2)
	if err != nil {
		t.Fatal("Failed to create a new host", err)
	}

	// Start a new renter with the same seed but disable contract recovery.
	newRenterDir := filepath.Join(testDir, "renter")
	renterParams := node.Renter(newRenterDir)
	renterParams.Allowance = modules.DefaultAllowance
	renterParams.Allowance.Hosts = 2
	renterParams.PrimarySeed = seed
	renterParams.ContractorDeps = &dependencyDisableContractRecovery{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	newRenter := nodes[0]

	// The new renter should have the right number of recoverable contracts.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = newRenter.RenterRecoverableContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.RecoverableContracts) != len(tg.Hosts()) {
			return fmt.Errorf("Don't have enough recoverable contracts, expected %v but was %v",
				len(tg.Hosts()), len(rc.RecoverableContracts))
		}
		return nil
	})

	// Get the current blockheight of the group.
	cg, err := newRenter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	bh := cg.Height

	// Start a new miner which has a longer chain than the group.
	newMiner, err := siatest.NewNode(siatest.Miner(filepath.Join(testDir, "miner")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := newMiner.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Mine a longer chain.
	for i := types.BlockHeight(0); i < bh+10; i++ {
		if err := newMiner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	// Connect the miner to the renter.
	gg, err := newRenter.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMiner.GatewayConnectPost(gg.NetAddress); err != nil {
		t.Fatal(err)
	}
	// The recoverable contracts should be gone now after the reorg.
	err = build.Retry(60, time.Second, func() error {
		rc, err = newRenter.RenterRecoverableContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.RecoverableContracts) != 0 {
			return fmt.Errorf("Expected no recoverable contracts, but was %v",
				len(rc.RecoverableContracts))
		}
		return nil
	})
}

// testFileAvailableAndRecoverable checks to make sure that the API properly
// reports if a file is available and/or recoverable
func testFileAvailableAndRecoverable(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	r := tg.Renters()[0]

	// Check that we have 5 hosts for this test so that the redundancy
	// assumptions work for the test
	if len(tg.Hosts()) != 5 {
		t.Fatal("This test requires 5 hosts")
	}

	// Set fileSize and redundancy for upload
	fileSize := int(modules.SectorSize)
	dataPieces := uint64(4)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces

	// Upload file
	localFile, remoteFile, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Get the file info and check if it is available and recoverable. File
	// should be available, recoverable, redundancy >1, and the file should be
	// on disk
	fi, err := r.File(remoteFile)
	if err != nil {
		t.Fatal("failed to get file info", err)
	}
	if fi.Redundancy < 1 {
		t.Fatal("redundancy of file is less than 1:", fi.Redundancy)
	}
	if !fi.OnDisk {
		t.Fatal("file is not on disk")
	}
	if !fi.Available {
		t.Fatal("file is not available")
	}
	if !fi.Recoverable {
		t.Fatal("file is not recoverable")
	}

	// Take down two hosts so that the redundancy drops below 1
	for i := 0; i < 2; i++ {
		if err := tg.RemoveNode(tg.Hosts()[0]); err != nil {
			t.Fatal("Failed to shutdown host", err)
		}
	}
	expectedRedundancy := float64(dataPieces+parityPieces-2) / float64(dataPieces)
	if err := r.WaitForDecreasingRedundancy(remoteFile, expectedRedundancy); err != nil {
		t.Fatal("Redundancy isn't decreasing", err)
	}

	// Get file into, file should not be available because the redundancy is  <1
	// but it should be recoverable because the file is on disk
	fi, err = r.File(remoteFile)
	if err != nil {
		t.Fatal("failed to get file info", err)
	}
	if fi.Redundancy >= 1 {
		t.Fatal("redundancy of file should be less than 1:", fi.Redundancy)
	}
	if !fi.OnDisk {
		t.Fatal("file is not on disk")
	}
	if fi.Available {
		t.Fatal("file should not be available")
	}
	if !fi.Recoverable {
		t.Fatal("file should be recoverable")
	}

	// Delete the file locally.
	if err := localFile.Delete(); err != nil {
		t.Fatal("failed to delete local file", err)
	}

	// Get file into, file should now not be available or recoverable
	fi, err = r.File(remoteFile)
	if err != nil {
		t.Fatal("failed to get file info", err)
	}
	if fi.Redundancy >= 1 {
		t.Fatal("redundancy of file should be less than 1:", fi.Redundancy)
	}
	if fi.OnDisk {
		t.Fatal("file is still on disk")
	}
	if fi.Available {
		t.Fatal("file should not be available")
	}
	if fi.Recoverable {
		t.Fatal("file should not be recoverable")
	}
}

// TestRenterDownloadWithDrainedContract tests if draining a contract below
// MinContractFundUploadThreshold correctly sets a contract to !GoodForUpload
// while still being able to download the file.
func TestRenterDownloadWithDrainedContract(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Add a renter with a dependency that prevents contract renewals due to
	// low funds.
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.RenterDeps = &dependencyDisableRenewal{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]
	miner := tg.Miners()[0]
	// Drain the contracts until they are supposed to no longer be good for
	// uploading.
	_, err = drainContractsByUploading(renter, tg, contractor.MinContractFundUploadThreshold)
	if err != nil {
		t.Fatal(err)
	}
	numRetries := 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// The 2 contracts should no longer be good for upload.
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		if len(rc.Contracts) != len(tg.Hosts()) {
			return fmt.Errorf("There should be %v contracts but was %v", len(tg.Hosts()), len(rc.Contracts))
		}
		for _, c := range rc.Contracts {
			if c.GoodForUpload || !c.GoodForRenew {
				return fmt.Errorf("Contract shouldn't be good for uploads but it should be good for renew: %v %v",
					c.GoodForUpload, c.GoodForRenew)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Choose a random file and download it.
	files, err := renter.Files()
	if err != nil {
		t.Fatal(err)
	}
	_, err = renter.RenterStreamGet(files[fastrand.Intn(len(files))].SiaPath)
	if err != nil {
		t.Fatal(err)
	}
}
