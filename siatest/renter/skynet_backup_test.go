package renter

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkynetBackupAndRestore verifies the back up and restoration functionality
// of skynet.
func TestSkynetBackupAndRestore(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup with 2 portals.
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Miners:  1,
		Portals: 2,
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
	// Grab the portals
	portals := tg.Portals()
	portal1 := portals[0]
	portal2 := portals[1]

	// Add a SkyKey to both portals
	sk, err := portal1.SkykeyCreateKeyPost("singlefile", skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	err = portal2.SkykeyAddKeyPost(sk)
	if err != nil {
		t.Fatal(err)
	}

	// Define test function
	singleFileTest := func(filename, skykeyName string, data []byte) {
		// Portal 1 uploads the skyfile
		skylink, sup, _, err := portal1.UploadNewEncryptedSkyfileBlocking(filename, data, skykeyName, false)
		if err != nil {
			t.Fatalf("Test %v failed to upload: %v", filename, err)
		}

		// Verify the backup and restoration of the skylink
		err = verifyBackupAndRestore(tg, portal1, portal2, skylink, sup.SiaPath.String())
		if err != nil {
			t.Errorf("Test %v failed to backup and restore: %v", filename, err)
		}
	}

	// Define common params
	smallSize := 100
	smallData := fastrand.Bytes(smallSize)
	largeSize := 2*int(modules.SectorSize) + siatest.Fuzz()
	largeData := fastrand.Bytes(largeSize)

	// Small Skyfile
	singleFileTest("singleSmallFile", "", smallData)
	// Small Encrypted Skyfile
	singleFileTest("singleSmallFile_encrypted", sk.Name, smallData)
	// Large Skyfile
	singleFileTest("singleLargeFile", "", largeData)
	// Large Encrypted Skyfile
	singleFileTest("singleLargeFile_encrypted", sk.Name, largeData)
}

// testSingleFileMultiPart verifies that a single skyfile uploaded using the
// multiplart upload can be backed up by its skylink and then restored.
func testSingleFileMultiPart(t *testing.T, tg *siatest.TestGroup) {
	// Grab the portals
	portals := tg.Portals()
	portal1 := portals[0]
	portal2 := portals[1]

	// Add a SkyKey to both portals
	sk, err := portal1.SkykeyCreateKeyPost("multipartfile", skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	err = portal2.SkykeyAddKeyPost(sk)
	if err != nil {
		t.Fatal(err)
	}

	// Define test function
	multiFileTest := func(filename, skykeyName string, files []siatest.TestFile) {
		// Portal 1 uploads the multipart skyfile
		skylink, sup, _, err := portal1.UploadNewMultipartSkyfileEncryptedBlocking(filename, files, "", false, false, skykeyName, skykey.SkykeyID{})
		if err != nil {
			t.Fatalf("Test %v failed to upload: %v", filename, err)
		}

		// Verify the backup and restoration of the skylink
		err = verifyBackupAndRestore(tg, portal1, portal2, skylink, sup.SiaPath.String())
		if err != nil {
			t.Errorf("Test %v failed to backup and restore: %v", filename, err)
		}
	}

	// Small multipart
	data := []byte("contents_file1.png")
	files := []siatest.TestFile{{Name: "file1.png", Data: data}}
	multiFileTest("singleFileMulti", "", files)
	// Small encrypted multipart
	multiFileTest("singleFileMulti_encrypted", sk.Name, files)

	// Small multipart with html default path
	data = []byte("contents_file1.html")
	files = []siatest.TestFile{{Name: "file1.html", Data: data}}
	multiFileTest("singleFileMultiHTML", "", files)
	// Small multipart with html default path
	multiFileTest("singleFileMultiHTML_encryption", sk.Name, files)

	// Large multipart
	size := 2*int(modules.SectorSize) + siatest.Fuzz()
	data = fastrand.Bytes(size)
	files = []siatest.TestFile{{Name: "large.png", Data: data}}
	multiFileTest("singleLargeFileMulti", "", files)
	// Large encrypted multipart
	multiFileTest("singleLargeFileMulti_encrypted", sk.Name, files)
}

// testDirectoryBasic verifies that a directory skyfile can be backed up by its
// skylink and then restored.
func testDirectoryBasic(t *testing.T, tg *siatest.TestGroup) {
	// Grab the portals
	portals := tg.Portals()
	portal1 := portals[0]
	portal2 := portals[1]

	// Add a SkyKey to both portals
	sk, err := portal1.SkykeyCreateKeyPost("directoryBasic", skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	err = portal2.SkykeyAddKeyPost(sk)
	if err != nil {
		t.Fatal(err)
	}

	// Define test function
	directoryTest := func(filename, skykeyName, defaultPath string, files []siatest.TestFile, disableDefaultPath, force bool) {
		// Portal 1 uploads the directory
		skylink, sup, _, err := portal1.UploadNewMultipartSkyfileEncryptedBlocking(filename, files, defaultPath, disableDefaultPath, force, skykeyName, skykey.SkykeyID{})
		if err != nil {
			t.Fatalf("Test %v failed to upload: %v", filename, err)
		}

		// Verify the backup and restoration of the skylink
		err = verifyBackupAndRestore(tg, portal1, portal2, skylink, sup.SiaPath.String())
		if err != nil {
			t.Errorf("Test %v failed to backup and restore: %v", filename, err)
		}
	}

	// Basic Directory with Large Subfile
	size := 2*int(modules.SectorSize) + siatest.Fuzz()
	largeData := fastrand.Bytes(size)
	files := []siatest.TestFile{
		{Name: "index.html", Data: largeData},
		{Name: "about.html", Data: []byte("about.html_contents")},
	}
	directoryTest("DirectoryBasic_LargeFile", "", "", files, false, false)
	// Basic Encrypted Directory with Large Subfile
	directoryTest("DirectoryBasic_LargeFile_Encryption", sk.Name, "", files, false, false)

	// Basic directory
	files = []siatest.TestFile{
		{Name: "index.html", Data: []byte("index.html_contents")},
		{Name: "about.html", Data: []byte("about.html_contents")},
	}
	directoryTest("DirectoryBasic", "", "", files, false, false)
	// Basic encrypted directory
	directoryTest("DirectoryBasic_Encryption", sk.Name, "", files, false, false)

	// Same basic directory with different default path
	directoryTest("DirectoryBasic", "", "about.html", files, false, true)
	// Same basic encrypted directory with different default path
	directoryTest("DirectoryBasic_Encryption", sk.Name, "about.html", files, false, true)

	// Same basic directory with no default path
	directoryTest("DirectoryBasic", "", "", files, true, true)
	// Same basic encrypted directory with no default path
	directoryTest("DirectoryBasic_Encryption", sk.Name, "", files, true, true)
}

// testDirectoryNested verifies that a nested directory skyfile can be backed up
// by its skylink and then restored.
func testDirectoryNested(t *testing.T, tg *siatest.TestGroup) {
	// Grab the portals
	portals := tg.Portals()
	portal1 := portals[0]
	portal2 := portals[1]

	// Add a SkyKey to both portals
	sk, err := portal1.SkykeyCreateKeyPost("directoryNested", skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	err = portal2.SkykeyAddKeyPost(sk)
	if err != nil {
		t.Fatal(err)
	}

	// Define test function
	directoryTest := func(filename, skykeyName string, files []siatest.TestFile) {
		// Portal 1 uploads the directory
		skylink, sup, _, err := portal1.UploadNewMultipartSkyfileEncryptedBlocking(filename, files, "", false, false, skykeyName, skykey.SkykeyID{})
		if err != nil {
			t.Fatalf("Test %v failed to upload: %v", filename, err)
		}

		// Verify the backup and restoration of the skylink
		err = verifyBackupAndRestore(tg, portal1, portal2, skylink, sup.SiaPath.String())
		if err != nil {
			t.Errorf("Test %v failed to backup and restore: %v", filename, err)
		}
	}

	// Nested Directory
	files := []siatest.TestFile{
		{Name: "assets/images/file1.png", Data: []byte("file1.png_contents")},
		{Name: "assets/images/file2.png", Data: []byte("file2.png_contents")},
		{Name: "assets/index.html", Data: []byte("assets_index.html_contents")},
		{Name: "index.html", Data: []byte("index.html_contents")},
	}
	directoryTest("NestedDirectory", "", files)

	// Encrypted Nested Directory
	directoryTest("NestedDirectory_Encrypted", "", files)
}

// testConvertedSiaFile verifies that a skyfile that was converted from
// a siafile can be backed up by its skylink and then restored.
func testConvertedSiaFile(t *testing.T, tg *siatest.TestGroup) {
	// Grab the portals
	portals := tg.Portals()
	portal1 := portals[0]
	portal2 := portals[1]

	// Add a SkyKey to both portals
	sk, err := portal1.SkykeyCreateKeyPost("convertedsiafile", skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}
	err = portal2.SkykeyAddKeyPost(sk)
	if err != nil {
		t.Fatal(err)
	}

	// Define test function
	convertTest := func(filename, skykeyName string, size int) {
		// Portal 1 uploads a siafile
		_, rf, err := portal1.UploadNewFileBlocking(size, 1, 2, false)
		if err != nil {
			t.Fatalf("Test %v failed to upload siafile: %v", filename, err)
		}

		// Portal 1 converts the siafile to a skyfile
		sup := modules.SkyfileUploadParameters{
			SiaPath:    rf.SiaPath(),
			SkykeyName: skykeyName,
		}
		sshp, err := portal1.SkynetConvertSiafileToSkyfilePost(sup, rf.SiaPath())
		if skykeyName != "" {
			if err == nil {
				// Future proofing the test to fail when siafile conversion with
				// encryption is supported
				t.Fatal("Siafile Conversions with Encryption now supported, update test")
			}
			return
		}
		if err != nil {
			t.Fatalf("Test %v failed to convert siafile: %v", filename, err)
		}

		// Verify the backup and restoration of the skylink
		err = verifyBackupAndRestore(tg, portal1, portal2, sshp.Skylink, sup.SiaPath.String())
		if err != nil {
			t.Errorf("Test %v failed to backup and restore: %v", filename, err)
		}
	}

	// Define common params
	smallSize := 100
	largeSize := 2*int(modules.SectorSize) + siatest.Fuzz()

	// Small siafile
	convertTest("smallSiafile", "", smallSize)
	// Small siafile with encrypted conversion
	convertTest("smallSiafile_Encryption", sk.Name, smallSize)
	// Large siafile
	convertTest("largeSiafile", "", largeSize)
	// Large siafile with encrypted conversion
	convertTest("largeSiafile_Encryption", sk.Name, largeSize)
}

// verifyBackupAndRestore verifies the backup and restore functionality of
// skynet for the provided skylink
func verifyBackupAndRestore(tg *siatest.TestGroup, portal1, portal2 *siatest.TestNode, skylink, siaPath string) error {
	// Verify both portals can download the file
	err := verifyDownloadByAll(portal1, portal2, skylink)
	if err != nil {
		return errors.AddContext(err, "initial download failed")
	}

	// Have Portal 1 delete the file
	skySiaPath, err := modules.SkynetFolder.Join(siaPath)
	if err != nil {
		return err
	}
	err = portal1.RenterFileDeleteRootPost(skySiaPath)
	if err != nil {
		return err
	}
	skySiaPathExtended, err := skySiaPath.Join(modules.ExtendedSuffix)
	if err != nil {
		return err
	}
	err = portal1.RenterFileDeleteRootPost(skySiaPathExtended)
	if err != nil && !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		return err
	}

	// Verify both portals can still download the file
	err = verifyDownloadByAll(portal1, portal2, skylink)
	if err != nil {
		return errors.AddContext(err, "download after delete failed")
	}

	// Portal 2 Backups the skyfile
	var backupDst bytes.Buffer
	err = portal2.SkynetSkylinkBackup(skylink, &backupDst)
	if err != nil {
		return errors.AddContext(err, "backup call failed")
	}

	// Portal 2 Restores the Skyfile
	backupSrc := bytes.NewReader(backupDst.Bytes())
	backupSkylink, err := portal2.SkynetSkylinkRestorePost(backupSrc)
	if err != nil {
		return errors.AddContext(err, "restore call failed")
	}
	if backupSkylink != skylink {
		return fmt.Errorf("Skylinks not equal\nOriginal: %v\nBackup %v\n", skylink, backupSkylink)
	}

	// Verify both portals can download the restored file
	err = verifyDownloadByAll(portal1, portal2, backupSkylink)
	if err != nil {
		return errors.AddContext(err, "download after restore failed")
	}

	// Stop here unless vlong tests
	//
	// Saves ~3min on the test suite.
	if !build.VLONG {
		return nil
	}

	// Mine to a new period to ensure the original contract data from renter 1 is
	// dropped
	if err = siatest.RenewContractsByRenewWindow(portal1, tg); err != nil {
		return err
	}
	err1 := siatest.RenterContractsStable(portal1, tg)
	err2 := siatest.RenterContractsStable(portal2, tg)
	if err := errors.Compose(err1, err2); err != nil {
		return err
	}

	// Portal 1 and Portal 2 can still download the file
	err = verifyDownloadByAll(portal1, portal2, backupSkylink)
	if err != nil {
		return errors.AddContext(err, "download after renewal failed")
	}

	return nil
}

// verifyDownloadByAll verifies that both the renter's can download the skylink.
func verifyDownloadByAll(portal1, portal2 *siatest.TestNode, skylink string) error {
	data1, sm1, err1 := portal1.SkynetSkylinkGet(skylink)
	err1 = errors.AddContext(err1, "portal 1 download error")
	data2, sm2, err2 := portal2.SkynetSkylinkGet(skylink)
	err2 = errors.AddContext(err2, "portal 2 download error")
	if err := errors.Compose(err1, err2); err != nil {
		return err
	}
	if !bytes.Equal(data1, data2) {
		return fmt.Errorf("Bytes not equal\nPortal 1 Download: %v\nPortal 2 Download: %v\n", data1, data2)
	}
	if !reflect.DeepEqual(sm1, sm2) {
		return fmt.Errorf("Metadata not equal\nPortal 1 Download: %v\nPortal 2 Download: %v\n", sm1, sm2)
	}
	return nil
}
