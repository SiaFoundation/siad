package renter

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestSkynetBackupAndRestore verifies the back up and restoration functionality
// of skynet.
func TestSkynetBackupAndRestore(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup with 2 renters.
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Miners:  1,
		Renters: 2,
	}
	groupDir := renterTestDir(t.Name())

	// Specify subtests to run
	subTests := []siatest.SubTest{
		{Name: "SingleFileRegular", Test: testSingleFileRegular},
		{Name: "SingleFileMultiPart", Test: testSingleFileMultiPart},
		{Name: "DirectoryBasic", Test: testDirectoryBasic},
		{Name: "DirectoryNested", Test: testDirectoryNested},
		{Name: "ConvertedSiafile", Test: testConvertedSiaFile},
	}

	// Run tests
	if err := siatest.RunSubTests(t, groupParams, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

// testSingleFileRegular verifies that a single skyfile can be backed up by its
// skylink and then restored.
func testSingleFileRegular(t *testing.T, tg *siatest.TestGroup) {
	// Grab the renters
	renters := tg.Renters()
	renter1 := renters[0]
	renter2 := renters[1]
	backupDir := renterTestDir(t.Name())

	// TODO: This code is setting the renters to act as portals, we should
	// probably make this a setting within the testgroup and testnode and make
	// sure all the Skynet tests are using this setting.
	//
	// NOTE: Until then, subsequent subtests will fail unless this code is
	// unnecessarily added to them or this subtest is run first.
	rg, err := renter1.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	allowance := rg.Settings.Allowance
	allowance.PaymentContractInitialFunding = types.NewCurrency64(10000000)
	err = renter1.RenterPostAllowance(allowance)
	if err != nil {
		t.Fatal(err)
	}
	rg, err = renter2.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	allowance = rg.Settings.Allowance
	allowance.PaymentContractInitialFunding = types.NewCurrency64(10000000)
	err = renter2.RenterPostAllowance(allowance)
	if err != nil {
		t.Fatal(err)
	}

	// Renter 1 uploads a small skyfile
	filename := "singleSmallFile"
	skylink, sup, _, err := renter1.UploadNewSkyfileBlocking(filename, 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Small Single File Test Failed:", err)
	}

	// Renter 1 uploads a large skyfile
	filename = "singleLargeFile"
	size := 2 * int(modules.SectorSize)
	skylink, sup, _, err = renter1.UploadNewSkyfileBlocking(filename, uint64(size), false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Large Single File Test Failed:", err)
	}
}

// testSingleFileMultiPart verifies that a single skyfile uploaded using the
// multiplart upload can be backed up by its skylink and then restored.
func testSingleFileMultiPart(t *testing.T, tg *siatest.TestGroup) {
	// Grab the renters
	renters := tg.Renters()
	renter1 := renters[0]
	renter2 := renters[1]
	backupDir := renterTestDir(t.Name())

	// Renter 1 uploads a skyfile
	filename := "singleFileMulti"
	data := []byte("contents_file1.png")
	files := []siatest.TestFile{{Name: "file1.png", Data: data}}
	skylink, sup, _, err := renter1.UploadNewMultipartSkyfileBlocking(filename, files, "", false, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Initial Single File Multi Part Test Failed:", err)
	}

	// Test with html default path
	filename = "singleFileMultiHTML"
	data = []byte("contents_file1.html")
	files = []siatest.TestFile{{Name: "file1.html", Data: data}}
	skylink, sup, _, err = renter1.UploadNewMultipartSkyfileBlocking(filename, files, "", false, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Single File Multi Part html path Test Failed:", err)
	}
}

// testDirectoryBasic verifies that a directory skyfile can be backed up by its
// skylink and then restored.
func testDirectoryBasic(t *testing.T, tg *siatest.TestGroup) {
	// Grab the renters
	renters := tg.Renters()
	renter1 := renters[0]
	renter2 := renters[1]
	backupDir := renterTestDir(t.Name())

	// Renter 1 uploads a skyfile
	filename := "DirectoryBasic"
	files := []siatest.TestFile{
		{Name: "index.html", Data: []byte("index.html_contents")},
		{Name: "about.html", Data: []byte("about.html_contents")},
	}
	skylink, sup, _, err := renter1.UploadNewMultipartSkyfileBlocking(filename, files, "", false, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Initial Directory Test Failed:", err)
	}

	// Upload the same files but with a different default path
	skylink, sup, _, err = renter1.UploadNewMultipartSkyfileBlocking(filename, files, "about.html", false, true)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Directory Test different default path Failed:", err)
	}

	// Upload the same files but with no default path
	skylink, sup, _, err = renter1.UploadNewMultipartSkyfileBlocking(filename, files, "", true, true)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Directory Test no default path Failed:", err)
	}
}

// testDirectoryNested verifies that a nested directory skyfile can be backed up
// by its skylink and then restored.
func testDirectoryNested(t *testing.T, tg *siatest.TestGroup) {
	// Grab the renters
	renters := tg.Renters()
	renter1 := renters[0]
	renter2 := renters[1]
	backupDir := renterTestDir(t.Name())

	// Renter 1 uploads a skyfile
	filename := "DirectoryNested"
	files := []siatest.TestFile{
		{Name: "assets/images/file1.png", Data: []byte("file1.png_contents")},
		{Name: "assets/images/file2.png", Data: []byte("file2.png_contents")},
		{Name: "assets/index.html", Data: []byte("assets_index.html_contents")},
		{Name: "index.html", Data: []byte("index.html_contents")},
	}
	skylink, sup, _, err := renter1.UploadNewMultipartSkyfileBlocking(filename, files, "", false, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Initial Directory Test Failed:", err)
	}
}

// testConvertedSiaFile verifies that a skyfile that was converted from
// a siafile can be backed up by its skylink and then restored.
func testConvertedSiaFile(t *testing.T, tg *siatest.TestGroup) {
	// Grab the renters
	renters := tg.Renters()
	renter1 := renters[0]
	renter2 := renters[1]
	backupDir := renterTestDir(t.Name())

	// Renter 1 uploads a siafile
	_, rf, err := renter1.UploadNewFileBlocking(100, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}

	// Renter 1 converts the siafile to a skyfile
	sup := modules.SkyfileUploadParameters{
		SiaPath:             rf.SiaPath(),
		BaseChunkRedundancy: 2,
	}
	sshp, err := renter1.SkynetConvertSiafileToSkyfilePost(sup, rf.SiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// Verify the backup and restoration of the skylink
	err = verifyBackupAndRestore(tg, renter1, renter2, sshp.Skylink, sup.SiaPath.String(), backupDir)
	if err != nil {
		t.Error("Initial Directory Test Failed:", err)
	}
}

// verifyBackupAndRestore verifies the backup and restore functionality of
// skynet for the provided skylink
func verifyBackupAndRestore(tg *siatest.TestGroup, renter1, renter2 *siatest.TestNode, skylink, siaPath, backupDir string) error {
	// Verify both renters can download the file
	err := verifyDownloadByAll(renter1, renter2, skylink)
	if err != nil {
		return err
	}

	// Have Renter 1 delete the file
	skySiaPath, err := modules.SkynetFolder.Join(siaPath)
	if err != nil {
		return err
	}
	err = renter1.RenterFileDeleteRootPost(skySiaPath)
	if err != nil {
		return err
	}
	skySiaPathExtended, err := skySiaPath.Join(renter.ExtendedSuffix)
	if err != nil {
		return err
	}
	err = renter1.RenterFileDeleteRootPost(skySiaPathExtended)
	if err != nil && !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		return err
	}

	// Verify both renters can still download the file
	err = verifyDownloadByAll(renter1, renter2, skylink)
	if err != nil {
		return err
	}

	// Renter 2 Backups the skyfile
	backupPath, err := renter2.SkynetSkylinkBackup(skylink, backupDir)
	if err != nil {
		return err
	}

	// Renter 2 Restores the Skyfile
	backupSkylink, err := renter2.SkynetSkylinkRestorePost(backupPath)
	if err != nil {
		return err
	}
	if backupSkylink != skylink {
		return fmt.Errorf("Skylinks not equal\nOriginal: %v\nBackup %v\n", skylink, backupSkylink)
	}

	// Verify both renters can download the restored file
	err = verifyDownloadByAll(renter1, renter2, backupSkylink)
	if err != nil {
		return err
	}

	// Mine to a new period to ensure the original contract data from renter 1 is
	// dropped
	if err = siatest.RenewContractsByRenewWindow(renter1, tg); err != nil {
		return err
	}

	// Renter 1 and Renter 2 can still download the file
	err = verifyDownloadByAll(renter1, renter2, backupSkylink)
	if err != nil {
		return err
	}

	return nil
}

// verifyDownloadByAll verifies that both the renter's can download the skylink.
func verifyDownloadByAll(renter1, renter2 *siatest.TestNode, skylink string) error {
	data1, sm1, err1 := renter1.SkynetSkylinkGet(skylink)
	data2, sm2, err2 := renter2.SkynetSkylinkGet(skylink)
	if err := errors.Compose(err1, err2); err != nil {
		return err
	}
	if !bytes.Equal(data1, data2) {
		return fmt.Errorf("Bytes not equal\nRenter 1 Download: %v\nRenter 2 Download: %v\n", data1, data2)
	}
	if !reflect.DeepEqual(sm1, sm2) {
		return fmt.Errorf("Metadata not equal\nRenter 1 Download: %v\nRenter 2 Download: %v\n", sm1, sm2)
	}
	return nil
}
