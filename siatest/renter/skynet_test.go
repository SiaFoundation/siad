package renter

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/skykey"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkynet verifies the functionality of Skynet, a decentralized CDN and
// sharing platform.
func TestSkynet(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   3,
		Miners:  1,
		Renters: 1,
	}
	groupDir := renterTestDir(t.Name())

	// Specify subtests to run
	subTests := []siatest.SubTest{
		{Name: "TestSkynetBasic", Test: testSkynetBasic},
		{Name: "TestConvertSiaFile", Test: testConvertSiaFile},
		{Name: "TestSkynetSkykey", Test: testSkynetSkykey},
		{Name: "TestSkynetLargeMetadata", Test: testSkynetLargeMetadata},
		{Name: "TestSkynetMultipartUpload", Test: testSkynetMultipartUpload},
		{Name: "TestSkynetNoFilename", Test: testSkynetNoFilename},
		{Name: "TestSkynetSubDirDownload", Test: testSkynetSubDirDownload},
		{Name: "TestSkynetDisableForce", Test: testSkynetDisableForce},
		{Name: "TestSkynetBlacklist", Test: testSkynetBlacklist},
		{Name: "TestSkynetPortals", Test: testSkynetPortals},
		{Name: "TestSkynetHeadRequest", Test: testSkynetHeadRequest},
		{Name: "TestSkynetStats", Test: testSkynetStats},
		{Name: "TestSkynetRequestTimeout", Test: testSkynetRequestTimeout},
		{Name: "TestSkynetDryRunUpload", Test: testSkynetDryRunUpload},
		{Name: "TestRegressionTimeoutPanic", Test: testRegressionTimeoutPanic},
		{Name: "TestRenameSiaPath", Test: testRenameSiaPath},
		{Name: "TestSkynetNoWorkers", Test: testSkynetNoWorkers},
		{Name: "TestSkynetEncryptionPublicID", Test: testSkynetEncryptionWithType(skykey.TypePublicID)},
		{Name: "TestSkynetEncryptionPrivateID", Test: testSkynetEncryptionWithType(skykey.TypePrivateID)},
		{Name: "TestSkynetEncryptionLargeFilePublicID", Test: testSkynetEncryptionLargeFileWithType(skykey.TypePublicID)},
		{Name: "TestSkynetEncryptionLargeFilePrivateID", Test: testSkynetEncryptionLargeFileWithType(skykey.TypePrivateID)},
		{Name: "TestSkynetDefaultPath", Test: testSkynetDefaultPath},
		{Name: "TestSkynetSingleFileNoSubfiles", Test: testSkynetSingleFileNoSubfiles},
		{Name: "TestSkynetDownloadFormats", Test: testSkynetDownloadFormats},
	}

	// Run tests
	if err := siatest.RunSubTests(t, groupParams, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

// testSkynetBasic provides basic end-to-end testing for uploading skyfiles and
// downloading the resulting skylinks.
func testSkynetBasic(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Create some data to upload as a skyfile.
	data := fastrand.Bytes(100 + siatest.Fuzz())
	// Need it to be a reader.
	reader := bytes.NewReader(data)
	// Call the upload skyfile client call.
	filename := "testSmall"
	uploadSiaPath, err := modules.NewSiaPath("testSmallPath")
	if err != nil {
		t.Fatal(err)
	}
	// Quick fuzz on the force value so that sometimes it is set, sometimes it
	// is not.
	var force bool
	if fastrand.Intn(2) == 0 {
		force = true
	}
	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               force,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     0640, // Intentionally does not match any defaults.
		},
		Reader: reader,
	}
	skylink, rshp, err := r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}
	var realSkylink modules.Skylink
	err = realSkylink.LoadString(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if rshp.MerkleRoot != realSkylink.MerkleRoot() {
		t.Fatal("mismatch")
	}
	if rshp.Bitfield != realSkylink.Bitfield() {
		t.Fatal("mismatch")
	}

	// Check the redundancy on the file.
	skynetUploadPath, err := modules.SkynetFolder.Join(uploadSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(25, 250*time.Millisecond, func() error {
		uploadedFile, err := r.RenterFileRootGet(skynetUploadPath)
		if err != nil {
			return err
		}
		if uploadedFile.File.Redundancy != 2 {
			return fmt.Errorf("bad redundancy: %v", uploadedFile.File.Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the file behind the skylink.
	fetchedData, metadata, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}
	if metadata.Mode != 0640 {
		t.Error("bad mode")
	}
	if metadata.Filename != filename {
		t.Error("bad filename")
	}

	// Try to download the file explicitly using the ReaderGet method with the
	// no formatter.
	skylinkReader, err := r.SkynetSkylinkReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	readerData, err := ioutil.ReadAll(skylinkReader)
	if err != nil {
		err = errors.Compose(err, skylinkReader.Close())
		t.Fatal(err)
	}
	err = skylinkReader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readerData, data) {
		t.Fatal("reader data doesn't match data")
	}

	// Try to download the file using the ReaderGet method with the concat
	// formatter.
	skylinkReader, err = r.SkynetSkylinkConcatReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	readerData, err = ioutil.ReadAll(skylinkReader)
	if err != nil {
		err = errors.Compose(err, skylinkReader.Close())
		t.Fatal(err)
	}
	if !bytes.Equal(readerData, data) {
		t.Fatal("reader data doesn't match data")
	}
	err = skylinkReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the file using the ReaderGet method with the zip
	// formatter.
	skylinkReader, err = r.SkynetSkylinkZipReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	files, err := readZipArchive(skylinkReader)
	if err != nil {
		t.Fatal(err)
	}
	err = skylinkReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// verify the contents
	if len(files) != 1 {
		t.Fatal("Unexpected amount of files")
	}
	dataFile1Received, exists := files[filename]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filename)
	}
	if !bytes.Equal(dataFile1Received, data) {
		t.Fatal("file data doesn't match expected content")
	}

	// Try to download the file using the ReaderGet method with the tar
	// formatter.
	skylinkReader, err = r.SkynetSkylinkTarReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(skylinkReader)
	header, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != filename {
		t.Fatalf("expected filename in archive to be %v but was %v", filename, header.Name)
	}
	readerData, err = ioutil.ReadAll(tr)
	if err != nil {
		err = errors.Compose(err, skylinkReader.Close())
		t.Fatal(err)
	}
	if !bytes.Equal(readerData, data) {
		t.Fatal("reader data doesn't match data")
	}
	_, err = tr.Next()
	if err != io.EOF {
		t.Fatal("expected error to be EOF but was", err)
	}
	err = skylinkReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the file using the ReaderGet method with the targz
	// formatter.
	skylinkReader, err = r.SkynetSkylinkTarGzReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	gzr, err := gzip.NewReader(skylinkReader)
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()
	tr = tar.NewReader(gzr)
	header, err = tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != filename {
		t.Fatalf("expected filename in archive to be %v but was %v", filename, header.Name)
	}
	readerData, err = ioutil.ReadAll(tr)
	if err != nil {
		err = errors.Compose(err, skylinkReader.Close())
		t.Fatal(err)
	}
	if !bytes.Equal(readerData, data) {
		t.Fatal("reader data doesn't match data")
	}
	_, err = tr.Next()
	if err != io.EOF {
		t.Fatal("expected error to be EOF but was", err)
	}
	err = skylinkReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Get the list of files in the skynet directory and see if the file is
	// present.
	rdg, err := r.RenterDirRootGet(modules.SkynetFolder)
	if err != nil {
		t.Fatal(err)
	}
	if len(rdg.Files) != 1 {
		t.Fatal("expecting a file to be in the SkynetFolder after uploading")
	}

	// Create some data to upload as a skyfile.
	rootData := fastrand.Bytes(100 + siatest.Fuzz())
	// Need it to be a reader.
	rootReader := bytes.NewReader(rootData)
	// Call the upload skyfile client call.
	rootFilename := "rootTestSmall"
	rootUploadSiaPath, err := modules.NewSiaPath("rootTestSmallPath")
	if err != nil {
		t.Fatal(err)
	}
	// Quick fuzz on the force value so that sometimes it is set, sometimes it
	// is not.
	var rootForce bool
	if fastrand.Intn(2) == 0 {
		rootForce = true
	}
	rootLup := modules.SkyfileUploadParameters{
		SiaPath:             rootUploadSiaPath,
		Force:               rootForce,
		Root:                true,
		BaseChunkRedundancy: 3,
		FileMetadata: modules.SkyfileMetadata{
			Filename: rootFilename,
			Mode:     0600, // Intentionally does not match any defaults.
		},

		Reader: rootReader,
	}
	_, _, err = r.SkynetSkyfilePost(rootLup)
	if err != nil {
		t.Fatal(err)
	}

	// Get the list of files in the skynet directory and see if the file is
	// present.
	rootRdg, err := r.RenterDirRootGet(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(rootRdg.Files) != 1 {
		t.Fatal("expecting a file to be in the root folder after uploading")
	}
	err = build.Retry(250, 250*time.Millisecond, func() error {
		uploadedFile, err := r.RenterFileRootGet(rootUploadSiaPath)
		if err != nil {
			return err
		}
		if uploadedFile.File.Redundancy != 3 {
			return fmt.Errorf("bad redundancy: %v", uploadedFile.File.Redundancy)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Upload another skyfile, this time ensure that the skyfile is more than
	// one sector.
	largeData := fastrand.Bytes(int(modules.SectorSize*2) + siatest.Fuzz())
	largeReader := bytes.NewReader(largeData)
	largeFilename := "testLarge"
	largeSiaPath, err := modules.NewSiaPath("testLargePath")
	if err != nil {
		t.Fatal(err)
	}
	var force2 bool
	if fastrand.Intn(2) == 0 {
		force2 = true
	}
	largeLup := modules.SkyfileUploadParameters{
		SiaPath:             largeSiaPath,
		Force:               force2,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: largeFilename,
			// Remaining fields intentionally left blank so the renter sets
			// defaults.
		},

		Reader: largeReader,
	}
	largeSkylink, _, err := r.SkynetSkyfilePost(largeLup)
	if err != nil {
		t.Fatal(err)
	}
	largeFetchedData, _, err := r.SkynetSkylinkGet(largeSkylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(largeFetchedData, largeData) {
		t.Error("upload and download data does not match for large siafiles", len(largeFetchedData), len(largeData))
	}

	// Check the metadata of the siafile, see that the metadata of the siafile
	// has the skylink referenced.
	largeUploadPath, err := modules.NewSiaPath("testLargePath")
	if err != nil {
		t.Fatal(err)
	}
	largeSkyfilePath, err := modules.SkynetFolder.Join(largeUploadPath.String())
	if err != nil {
		t.Fatal(err)
	}
	largeRenterFile, err := r.RenterFileRootGet(largeSkyfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(largeRenterFile.File.Skylinks) != 1 {
		t.Fatal("expecting one skylink:", len(largeRenterFile.File.Skylinks))
	}
	if largeRenterFile.File.Skylinks[0] != largeSkylink {
		t.Error("skylinks should match")
		t.Log(largeRenterFile.File.Skylinks[0])
		t.Log(largeSkylink)
	}

	// TODO: Need to verify the mode, name, and create-time. At this time, I'm
	// not sure how we can feed those out of the API. They aren't going to be
	// the same as the siafile values, because the siafile was created
	// separately.
	//
	// Maybe this can be accomplished by tagging a flag to the API which has the
	// layout and metadata streamed as the first bytes? Maybe there is some
	// easier way.

	// Pinning test.
	//
	// Try to download the file behind the skylink.
	pinSiaPath, err := modules.NewSiaPath("testSmallPinPath")
	if err != nil {
		t.Fatal(err)
	}
	pinLUP := modules.SkyfilePinParameters{
		SiaPath:             pinSiaPath,
		Force:               force,
		Root:                false,
		BaseChunkRedundancy: 2,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinLUP)
	if err != nil {
		t.Fatal(err)
	}
	// Get the list of files in the skynet directory and see if the file is
	// present.
	fullPinSiaPath, err := modules.SkynetFolder.Join(pinSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	// See if the file is present.
	pinnedFile, err := r.RenterFileRootGet(fullPinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedFile.File.Skylinks) != 1 {
		t.Fatal("expecting 1 skylink")
	}
	if pinnedFile.File.Skylinks[0] != skylink {
		t.Fatal("skylink mismatch")
	}

	// Unpinning test.
	//
	// Try deleting the file (equivalent to unpin).
	err = r.RenterFileDeleteRootPost(fullPinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure the file is no longer present.
	_, err = r.RenterFileRootGet(fullPinSiaPath)
	if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Fatal("skyfile still present after deletion")
	}

	// Try another pin test, this time with the large skylink.
	largePinSiaPath, err := modules.NewSiaPath("testLargePinPath")
	if err != nil {
		t.Fatal(err)
	}
	largePinLUP := modules.SkyfilePinParameters{
		SiaPath:             largePinSiaPath,
		Force:               force,
		Root:                false,
		BaseChunkRedundancy: 2,
	}
	err = r.SkynetSkylinkPinPost(largeSkylink, largePinLUP)
	if err != nil {
		t.Fatal(err)
	}
	// Pin the file again but without specifying the BaseChunkRedundancy.
	// Use a different Siapath to avoid path conflict.
	largePinSiaPath, err = modules.NewSiaPath("testLargePinPath2")
	if err != nil {
		t.Fatal(err)
	}
	largePinLUP = modules.SkyfilePinParameters{
		SiaPath: largePinSiaPath,
		Force:   force,
		Root:    false,
	}
	err = r.SkynetSkylinkPinPost(largeSkylink, largePinLUP)
	if err != nil {
		t.Fatal(err)
	}
	// See if the file is present.
	fullLargePinSiaPath, err := modules.SkynetFolder.Join(largePinSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	pinnedFile, err = r.RenterFileRootGet(fullLargePinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedFile.File.Skylinks) != 1 {
		t.Fatal("expecting 1 skylink")
	}
	if pinnedFile.File.Skylinks[0] != largeSkylink {
		t.Fatal("skylink mismatch")
	}
	// Try deleting the file.
	err = r.RenterFileDeleteRootPost(fullLargePinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Make sure the file is no longer present.
	_, err = r.RenterFileRootGet(fullLargePinSiaPath)
	if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Fatal("skyfile still present after deletion")
	}

	// TODO: We don't actually check at all whether the presence of the new
	// skylinks is going to keep the file online. We could do that by deleting
	// the old files and then churning the hosts over, and checking that the
	// renter does a repair operation to keep everyone alive.

	// TODO: Fetch both the skyfile and the siafile that was uploaded, make sure
	// that they both have the new skylink added to their metadata.

	// TODO: Need to verify the mode, name, and create-time. At this time, I'm
	// not sure how we can feed those out of the API. They aren't going to be
	// the same as the siafile values, because the siafile was created
	// separately.
	//
	// Maybe this can be accomplished by tagging a flag to the API which has the
	// layout and metadata streamed as the first bytes? Maybe there is some
	// easier way.
}

// testConvertSiaFile tests converting a siafile to a skyfile. This test checks
// for 1-of-N redundancies and N-of-M redundancies.
func testConvertSiaFile(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Upload a siafile that will then be converted to a skyfile.
	//
	// Set 2 as the datapieces to check for N-of-M redundancy conversions
	filesize := int(modules.SectorSize) + siatest.Fuzz()
	localFile, remoteFile, err := r.UploadNewFileBlocking(filesize, 2, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// Create Skyfile Upload Parameters
	sup := modules.SkyfileUploadParameters{
		SiaPath: modules.RandomSiaPath(),
	}

	// Try and convert to a Skyfile, this should fail due to the original
	// siafile being a N-of-M redundancy
	skylink, err := r.SkynetConvertSiafileToSkyfilePost(sup, remoteFile.SiaPath())
	if !strings.Contains(err.Error(), renter.ErrRedundancyNotSupported.Error()) {
		t.Fatalf("Expected Error to contain %v but got %v", renter.ErrRedundancyNotSupported, err)
	}

	// Upload a new file with a 1-N redundancy by setting the datapieces to 1
	localFile, remoteFile, err = r.UploadNewFileBlocking(filesize, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}

	// Get the local and remote data for comparison
	localData, err := localFile.Data()
	if err != nil {
		t.Fatal(err)
	}
	_, remoteData, err := r.DownloadByStream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}

	// Convert to a Skyfile
	skylink, err = r.SkynetConvertSiafileToSkyfilePost(sup, remoteFile.SiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the skylink.
	fetchedData, _, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}

	// Compare the data fetched from the Skylink to the local data and the
	// previously uploaded data
	if !bytes.Equal(fetchedData, localData) {
		t.Error("converted skylink data doesn't match local data")
	}
	if !bytes.Equal(fetchedData, remoteData) {
		t.Error("converted skylink data doesn't match remote data")
	}
}

// testSkynetMultipartUpload tests you can perform a multipart upload. It will
// verify the upload without any subfiles, with small subfiles and with large
// subfiles. Small files are files which are smaller than one sector, and thus
// don't need a fanout. Large files are files what span multiple sectors
func testSkynetMultipartUpload(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// create a multipart upload that without any files
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(body.Bytes())

	uploadSiaPath, err := modules.NewSiaPath("TestNoFileUpload")
	if err != nil {
		t.Fatal(err)
	}

	sup := modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            "TestNoFileUpload",
	}

	if _, _, err = r.SkynetSkyfileMultiPartPost(sup); err == nil || !strings.Contains(err.Error(), "could not find multipart file") {
		t.Fatal("Expected upload to fail because no files are given, err:", err)
	}

	// TEST SMALL SUBFILE
	var offset uint64

	// create a multipart upload that uploads several files.
	body = new(bytes.Buffer)
	writer = multipart.NewWriter(body)
	subfiles := make(modules.SkyfileSubfiles)

	// add a file at root level
	data := []byte("File1Contents")
	subfile := siatest.AddMultipartFile(writer, data, "files[]", "file1", 0600, &offset)
	subfiles[subfile.Filename] = subfile

	// add a nested file
	data = []byte("File2Contents")
	subfile = siatest.AddMultipartFile(writer, data, "files[]", "nested/file2", 0640, &offset)
	subfiles[subfile.Filename] = subfile

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader = bytes.NewReader(body.Bytes())

	// Call the upload skyfile client call.
	uploadSiaPath, err = modules.NewSiaPath("TestFolderUpload")
	if err != nil {
		t.Fatal(err)
	}

	sup = modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            "TestFolderUpload",
	}

	skylink, _, err := r.SkynetSkyfileMultiPartPost(sup)
	if err != nil {
		t.Fatal(err)
	}
	var realSkylink modules.Skylink
	err = realSkylink.LoadString(skylink)
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the file behind the skylink.
	_, fileMetadata, err := r.SkynetSkylinkConcatGet(skylink)
	if err != nil {
		t.Fatal(err)
	}

	expected := modules.SkyfileMetadata{Filename: uploadSiaPath.String(), Subfiles: subfiles}
	if !reflect.DeepEqual(expected, fileMetadata) {
		t.Log("Expected:", expected)
		t.Log("Actual:", fileMetadata)
		t.Fatal("Metadata mismatch")
	}

	// Download the second file
	nestedfile, _, err := r.SkynetSkylinkGet(fmt.Sprintf("%s/%s", skylink, "nested/file2"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(nestedfile, data) {
		t.Fatal("Expected only second file to be downloaded")
	}

	// LARGE SUBFILES

	// create a multipart upload that uploads several files.
	body = new(bytes.Buffer)
	writer = multipart.NewWriter(body)
	subfiles = make(modules.SkyfileSubfiles)

	// add a small file at root level
	smallData := []byte("File1Contents")
	subfile = siatest.AddMultipartFile(writer, smallData, "files[]", "smallfile1.txt", 0600, &offset)
	subfiles[subfile.Filename] = subfile

	// add a large nested file
	largeData := fastrand.Bytes(2 * int(modules.SectorSize))
	subfile = siatest.AddMultipartFile(writer, largeData, "files[]", "nested/largefile2.txt", 0644, &offset)
	subfiles[subfile.Filename] = subfile

	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	allData := body.Bytes()
	reader = bytes.NewReader(allData)

	// Call the upload skyfile client call.
	uploadSiaPath, err = modules.NewSiaPath("TestFolderUploadLarge")
	if err != nil {
		t.Fatal(err)
	}

	sup = modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		Filename:            "TestFolderUploadLarge",
		ContentType:         writer.FormDataContentType(),
	}

	largeSkylink, _, err := r.SkynetSkyfileMultiPartPost(sup)
	if err != nil {
		t.Fatal(err)
	}

	largeFetchedData, _, err := r.SkynetSkylinkConcatGet(largeSkylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(largeFetchedData, append(smallData, largeData...)) {
		t.Fatal("upload and download data does not match for large siafiles", len(largeFetchedData), len(allData))
	}

	// Check the metadata of the siafile, see that the metadata of the siafile
	// has the skylink referenced.
	if err != nil {
		t.Fatal(err)
	}
	largeSkyfilePath, err := modules.SkynetFolder.Join(uploadSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	largeRenterFile, err := r.RenterFileRootGet(largeSkyfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(largeRenterFile.File.Skylinks) != 1 {
		t.Fatal("expecting one skylink:", len(largeRenterFile.File.Skylinks))
	}
	if largeRenterFile.File.Skylinks[0] != largeSkylink {
		t.Fatal("skylinks should match")
		t.Log(largeRenterFile.File.Skylinks[0])
		t.Log(largeSkylink)
	}

	// Test the small download
	smallFetchedData, _, err := r.SkynetSkylinkGet(fmt.Sprintf("%s/%s", largeSkylink, "smallfile1.txt"))

	if !bytes.Equal(smallFetchedData, smallData) {
		t.Fatal("upload and download data does not match for large siafiles with subfiles", len(smallFetchedData), len(smallData))
	}

	largeFetchedData, _, err = r.SkynetSkylinkGet(fmt.Sprintf("%s/%s", largeSkylink, "nested/largefile2.txt"))

	if !bytes.Equal(largeFetchedData, largeData) {
		t.Fatal("upload and download data does not match for large siafiles with subfiles", len(largeFetchedData), len(largeData))
	}
}

// testSkynetStats tests the validity of the response of /skynet/stats endpoint
// by uploading some test files and verifying that the reported statistics
// change proportionally
func testSkynetStats(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// get the stats
	stats, err := r.SkynetStatsGet()
	if err != nil {
		t.Fatal(err)
	}

	// verify it contains the node's version information
	expected := build.Version
	if build.ReleaseTag != "" {
		expected += "-" + build.ReleaseTag
	}
	if stats.VersionInfo.Version != expected {
		t.Fatalf("Unexpected version return, expected '%v', actual '%v'", expected, stats.VersionInfo.Version)
	}
	if stats.VersionInfo.GitRevision != build.GitRevision {
		t.Fatalf("Unexpected git revision return, expected '%v', actual '%v'", build.GitRevision, stats.VersionInfo.GitRevision)
	}

	// Uptime should be non zero
	if stats.Uptime == 0 {
		t.Error("Uptime is zero")
	}

	// create two test files with sizes below and above the sector size
	files := make(map[string]uint64)
	files["statfile1"] = 2033
	files["statfile2"] = modules.SectorSize + 123

	// upload the files and keep track of their expected impact on the stats
	var uploadedFilesSize, uploadedFilesCount uint64
	for name, size := range files {
		if _, _, _, err := r.UploadNewSkyfileBlocking(name, size, false); err != nil {
			t.Fatal(err)
		}

		if size < modules.SectorSize {
			// small files get padded up to a full sector
			uploadedFilesSize += modules.SectorSize
		} else {
			// large files have an extra sector with header data
			uploadedFilesSize += size + modules.SectorSize
		}
		uploadedFilesCount++
	}

	// get the stats after the upload of the test files
	statsAfter, err := r.SkynetStatsGet()
	if err != nil {
		t.Fatal(err)
	}

	// make sure the stats changed by exactly the expected amounts
	statsBefore := stats
	if uint64(statsBefore.UploadStats.NumFiles)+uploadedFilesCount != uint64(statsAfter.UploadStats.NumFiles) {
		t.Fatal(fmt.Sprintf("stats did not report the correct number of files. expected %d, found %d", uint64(statsBefore.UploadStats.NumFiles)+uploadedFilesCount, statsAfter.UploadStats.NumFiles))
	}
	if statsBefore.UploadStats.TotalSize+uploadedFilesSize != statsAfter.UploadStats.TotalSize {
		t.Fatal(fmt.Sprintf("stats did not report the correct size. expected %d, found %d", statsBefore.UploadStats.TotalSize+uploadedFilesSize, statsAfter.UploadStats.TotalSize))
	}
	lt := statsAfter.PerformanceStats.Upload4MB.Lifetime
	if lt.N60ms+lt.N120ms+lt.N240ms+lt.N500ms+lt.N1000ms+lt.N2000ms+lt.N5000ms+lt.N10s+lt.NLong == 0 {
		t.Error("lifetime upload stats are not reporting any uploads")
	}
}

// TestSkynetNoFilename verifies that posting a Skyfile without providing a
// filename fails.
func testSkynetNoFilename(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Create some data to upload as a skyfile.
	data := fastrand.Bytes(100 + siatest.Fuzz())
	reader := bytes.NewReader(data)

	// Call the upload skyfile client call.
	uploadSiaPath, err := modules.NewSiaPath("testNoFilename")
	if err != nil {
		t.Fatal(err)
	}

	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: "",   // Intentionally leave empty to trigger failure.
			Mode:     0640, // Intentionally does not match any defaults.
		},

		Reader: reader,
	}

	// Try posting the skyfile without providing a filename
	_, _, err = r.SkynetSkyfilePost(sup)
	if err == nil || !strings.Contains(err.Error(), "no filename provided") {
		t.Log("Error:", err)
		t.Fatal("Expected SkynetSkyfilePost to fail due to lack of a filename")
	}

	sup.FileMetadata.Filename = "testNoFilename"
	_, _, err = r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Log("Error:", err)
		t.Fatal("Expected SkynetSkyfilePost to succeed if filename is provided")
	}

	// Do the same for a multipart upload
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	data = []byte("File1Contents")
	nofilename := ""
	subfile := siatest.AddMultipartFile(writer, data, "files[]", nofilename, 0600, nil)
	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader = bytes.NewReader(body.Bytes())

	// Call the upload skyfile client call.
	uploadSiaPath, err = modules.NewSiaPath("testNoFilenameMultipart")
	if err != nil {
		t.Fatal(err)
	}

	subfiles := make(modules.SkyfileSubfiles)
	subfiles[subfile.Filename] = subfile
	mup := modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            "testNoFilenameMultipart",
	}

	// Note: we have to check for a different error message here. This is due to
	// the fact that the http library uses the filename when parsing the
	// multipart form request. Not providing a filename, makes it interpret the
	// file as a form value, which leads to the file not being find, opposed to
	// erroring on the filename not being set.
	_, _, err = r.SkynetSkyfileMultiPartPost(mup)
	if err == nil || !strings.Contains(err.Error(), "could not find multipart file") {
		t.Log("Error:", err)
		t.Fatal("Expected SkynetSkyfilePost to fail due to lack of a filename")
	}

	// recreate the reader
	body = new(bytes.Buffer)
	writer = multipart.NewWriter(body)

	subfile = siatest.AddMultipartFile(writer, []byte("File1Contents"), "files[]", "testNoFilenameMultipart", 0600, nil)
	err = writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader = bytes.NewReader(body.Bytes())

	subfiles = make(modules.SkyfileSubfiles)
	subfiles[subfile.Filename] = subfile
	mup = modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            "testNoFilenameMultipart",
	}

	_, _, err = r.SkynetSkyfileMultiPartPost(mup)
	if err != nil {
		t.Log("Error:", err)
		t.Fatal("Expected SkynetSkyfileMultiPartPost to succeed if filename is provided")
	}
}

// testSkynetDownloadFormats verifies downloading data in different formats
func testSkynetDownloadFormats(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	dataFile1 := []byte("file1.txt")
	dataFile2 := []byte("file2.txt")
	dataFile3 := []byte("file3.txt")
	filePath1 := "/a/5.f4f8b583.chunk.js"
	filePath2 := "/a/5.f4f.chunk.js.map"
	filePath3 := "/b/file3.txt"
	siatest.AddMultipartFile(writer, dataFile1, "files[]", filePath1, 0600, nil)
	siatest.AddMultipartFile(writer, dataFile2, "files[]", filePath2, 0600, nil)
	siatest.AddMultipartFile(writer, dataFile3, "files[]", filePath3, 0640, nil)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	uploadSiaPath, err := modules.NewSiaPath("testSkynetDownloadFormats")
	if err != nil {
		t.Fatal(err)
	}

	reader := bytes.NewReader(body.Bytes())
	mup := modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            "testSkynetSubfileDownload",
	}

	skylink, _, err := r.SkynetSkyfileMultiPartPost(mup)
	if err != nil {
		t.Fatal(err)
	}

	// download the data specifying the 'concat' format
	allData, _, err := r.SkynetSkylinkConcatGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	expected := append(dataFile1, dataFile2...)
	expected = append(expected, dataFile3...)
	if !bytes.Equal(expected, allData) {
		t.Log("expected:", expected)
		t.Log("actual:", allData)
		t.Fatal("Unexpected data for dir A")
	}

	// now specify the zip format
	skyfileReader, err := r.SkynetSkylinkZipReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}

	// read the zip archive
	files, err := readZipArchive(skyfileReader)
	if err != nil {
		t.Fatal(err)
	}
	err = skyfileReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// verify the contents
	dataFile1Received, exists := files[filePath1]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath1)
	}
	if !bytes.Equal(dataFile1Received, dataFile1) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile2Received, exists := files[filePath2]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath2)
	}
	if !bytes.Equal(dataFile2Received, dataFile2) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile3Received, exists := files[filePath3]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath3)
	}
	if !bytes.Equal(dataFile3Received, dataFile3) {
		t.Log(dataFile3Received)
		t.Log(dataFile3)
		t.Fatal("file data doesn't match expected content")
	}

	// now specify the tar format
	skyfileReader, err = r.SkynetSkylinkTarReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}

	// read the tar archive
	files, err = readTarArchive(skyfileReader)
	if err != nil {
		t.Fatal(err)
	}
	err = skyfileReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// verify the contents
	dataFile1Received, exists = files[filePath1]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath1)
	}
	if !bytes.Equal(dataFile1Received, dataFile1) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile2Received, exists = files[filePath2]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath2)
	}
	if !bytes.Equal(dataFile2Received, dataFile2) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile3Received, exists = files[filePath3]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath3)
	}
	if !bytes.Equal(dataFile3Received, dataFile3) {
		t.Log(dataFile3Received)
		t.Log(dataFile3)
		t.Fatal("file data doesn't match expected content")
	}

	// now specify the targz format
	skyfileReader, err = r.SkynetSkylinkTarGzReaderGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	gzr, err := gzip.NewReader(skyfileReader)
	if err != nil {
		t.Fatal(err)
	}
	files, err = readTarArchive(gzr)
	if err != nil {
		t.Fatal(err)
	}
	err = errors.Compose(skyfileReader.Close(), gzr.Close())
	if err != nil {
		t.Fatal(err)
	}

	// verify the contents
	dataFile1Received, exists = files[filePath1]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath1)
	}
	if !bytes.Equal(dataFile1Received, dataFile1) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile2Received, exists = files[filePath2]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath2)
	}
	if !bytes.Equal(dataFile2Received, dataFile2) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile3Received, exists = files[filePath3]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath3)
	}
	if !bytes.Equal(dataFile3Received, dataFile3) {
		t.Log(dataFile3Received)
		t.Log(dataFile3)
		t.Fatal("file data doesn't match expected content")
	}

	// get all data for path "a" using the concat format
	dataDirA, _, err := r.SkynetSkylinkConcatGet(fmt.Sprintf("%s/a", skylink))
	if err != nil {
		t.Fatal(err)
	}
	expected = append(dataFile1, dataFile2...)
	if !bytes.Equal(expected, dataDirA) {
		t.Log("expected:", expected)
		t.Log("actual:", dataDirA)
		t.Fatal("Unexpected data for dir A")
	}

	// now specify the tar format
	skyfileReader, err = r.SkynetSkylinkTarReaderGet(fmt.Sprintf("%s/a", skylink))
	if err != nil {
		t.Fatal(err)
	}

	// read the tar archive
	files, err = readTarArchive(skyfileReader)
	if err != nil {
		t.Fatal(err)
	}
	err = skyfileReader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// verify the contents
	dataFile1Received, exists = files[filePath1]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath1)
	}
	if !bytes.Equal(dataFile1Received, dataFile1) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile2Received, exists = files[filePath2]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath2)
	}
	if !bytes.Equal(dataFile2Received, dataFile2) {
		t.Fatal("file data doesn't match expected content")
	}
	if len(files) != 2 {
		t.Fatal("unexpected amount of files")
	}

	// now specify the targz format
	skyfileReader, err = r.SkynetSkylinkTarGzReaderGet(fmt.Sprintf("%s/a", skylink))
	if err != nil {
		t.Fatal(err)
	}
	gzr, err = gzip.NewReader(skyfileReader)
	if err != nil {
		t.Fatal(err)
	}
	files, err = readTarArchive(gzr)
	if err != nil {
		t.Fatal(err)
	}
	err = errors.Compose(skyfileReader.Close(), gzr.Close())
	if err != nil {
		t.Fatal(err)
	}

	// verify the contents
	dataFile1Received, exists = files[filePath1]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath1)
	}
	if !bytes.Equal(dataFile1Received, dataFile1) {
		t.Fatal("file data doesn't match expected content")
	}
	dataFile2Received, exists = files[filePath2]
	if !exists {
		t.Fatalf("file at path '%v' not present in zip", filePath2)
	}
	if !bytes.Equal(dataFile2Received, dataFile2) {
		t.Fatal("file data doesn't match expected content")
	}
	if len(files) != 2 {
		t.Fatal("unexpected amount of files")
	}

	// now specify the zip format
	skyfileReader, err = r.SkynetSkylinkZipReaderGet(fmt.Sprintf("%s/a", skylink))
	if err != nil {
		t.Fatal(err)
	}

	// verify we get a 400 if we supply an unsupported format parameter
	_, _, err = r.SkynetSkylinkGet(fmt.Sprintf("%s/b?format=raw", skylink))
	if err == nil || !strings.Contains(err.Error(), "unable to parse 'format'") {
		t.Fatal("Expected download to fail because we are downloading a directory and an invalid format was provided, err:", err)
	}

	// verify we default to the `zip` format if it is a directory and we have
	// not specified it (use a HEAD call as that returns the response headers)
	_, header, err := r.SkynetSkylinkHead(skylink, 0)
	if err != nil {
		t.Fatal("unexpected error")
	}
	ct := header.Get("Content-Type")
	if ct != "application/zip" {
		t.Fatal("unexpected content type: ", ct)
	}
}

// testSkynetSubDirDownload verifies downloading data from a skyfile using a
// path to download single subfiles or subdirectories
func testSkynetSubDirDownload(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	dataFile1 := []byte("file1.txt")
	dataFile2 := []byte("file2.txt")
	dataFile3 := []byte("file3.txt")
	filePath1 := "/a/5.f4f8b583.chunk.js"
	filePath2 := "/a/5.f4f.chunk.js.map"
	filePath3 := "/b/file3.txt"
	siatest.AddMultipartFile(writer, dataFile1, "files[]", filePath1, 0600, nil)
	siatest.AddMultipartFile(writer, dataFile2, "files[]", filePath2, 0600, nil)
	siatest.AddMultipartFile(writer, dataFile3, "files[]", filePath3, 0640, nil)

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(body.Bytes())

	name := "testSkynetSubfileDownload"
	uploadSiaPath, err := modules.NewSiaPath(name)
	if err != nil {
		t.Fatal(err)
	}

	mup := modules.SkyfileMultipartUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		Reader:              reader,
		ContentType:         writer.FormDataContentType(),
		Filename:            name,
	}

	skylink, _, err := r.SkynetSkyfileMultiPartPost(mup)
	if err != nil {
		t.Fatal(err)
	}

	// get all the data
	data, metadata, err := r.SkynetSkylinkConcatGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Filename != name {
		t.Fatal("Unexpected filename")
	}
	expected := append(dataFile1, dataFile2...)
	expected = append(expected, dataFile3...)
	if !bytes.Equal(data, expected) {
		t.Fatal("Unexpected data")
	}

	// get all data for path "a"
	data, metadata, err = r.SkynetSkylinkConcatGet(fmt.Sprintf("%s/a", skylink))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Filename != "/a" {
		t.Fatal("Unexpected filename", metadata.Filename)
	}
	expected = append(dataFile1, dataFile2...)
	if !bytes.Equal(data, expected) {
		t.Fatal("Unexpected data")
	}

	// get all data for path "b"
	data, metadata, err = r.SkynetSkylinkConcatGet(fmt.Sprintf("%s/b", skylink))
	if err != nil {
		t.Fatal(err)
	}
	expected = dataFile3
	if !bytes.Equal(expected, data) {
		t.Fatal("Unexpected data")
	}
	if metadata.Filename != "/b" {
		t.Fatal("Unexpected filename", metadata.Filename)
	}
	mdF3, ok := metadata.Subfiles["/b/file3.txt"]
	if !ok {
		t.Fatal("Expected subfile metadata of file3 to be present")
	}

	mdF3Expected := modules.SkyfileSubfileMetadata{
		FileMode:    os.FileMode(0640),
		Filename:    "/b/file3.txt",
		ContentType: "application/octet-stream",
		Offset:      0,
		Len:         uint64(len(dataFile3)),
	}
	if mdF3 != mdF3Expected {
		t.Log("expected: ", mdF3Expected)
		t.Log("actual: ", mdF3)
		t.Fatal("Unexpected subfile metadata for file 3")
	}

	// get a single sub file
	downloadFile2, _, err := r.SkynetSkylinkGet(fmt.Sprintf("%s/a/5.f4f.chunk.js.map", skylink))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dataFile2, downloadFile2) {
		t.Log("expected:", dataFile2)
		t.Log("actual:", downloadFile2)
		t.Fatal("Unexpected data for file 2")
	}
}

// testSkynetDisableForce verifies the behavior of force and the header that
// allows disabling forcefully uploading a Skyfile
func testSkynetDisableForce(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Upload Skyfile
	_, _, _, err := r.UploadNewSkyfileBlocking(t.Name(), 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// Upload at same path without force, assert this fails
	_, _, _, err = r.UploadNewSkyfileBlocking(t.Name(), 100, false)
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}

	// Upload once more, but now use force. It should allow us to
	// overwrite the file at the existing path
	_, sup, _, err := r.UploadNewSkyfileBlocking(t.Name(), 100, true)
	if err != nil {
		t.Fatal(err)
	}

	// Upload using the force flag again, however now we set the
	// Skynet-Disable-Force to true, which should prevent us from uploading.
	// Because we have to pass in a custom header, we have to setup the request
	// ourselves and can not use the client.
	_, _, err = r.SkynetSkyfilePostDisableForce(sup, true)
	if err == nil {
		t.Fatal("Unexpected response")
	}
	if !strings.Contains(err.Error(), "'force' has been disabled") {
		t.Log(err)
		t.Fatalf("Unexpected response, expected error to contain a mention of the force flag but instaed received: %v", err.Error())
	}
}

// testSkynetBlacklist tests the skynet blacklist module
func testSkynetBlacklist(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Create skyfile upload params, data should be larger than a sector size to
	// test large file uploads and the deletion of their extended data.
	size := modules.SectorSize + uint64(100+siatest.Fuzz())
	skylink, sup, sshp, err := r.UploadNewSkyfileBlocking(t.Name(), size, false)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the skyfile and its extended info are registered with the
	// renter
	sp, err := modules.SkynetFolder.Join(sup.SiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileRootGet(sp)
	if err != nil {
		t.Fatal(err)
	}
	spExtended, err := modules.NewSiaPath(sp.String() + renter.ExtendedSuffix)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileRootGet(spExtended)
	if err != nil {
		t.Fatal(err)
	}

	// Download the data
	data, _, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}

	// Blacklist the skylink
	add := []string{skylink}
	remove := []string{}
	err = r.SkynetBlacklistPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the Skylink is blacklisted by verifying the merkleroot is in
	// the blacklist
	sbg, err := r.SkynetBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(sbg.Blacklist) != 1 {
		t.Fatalf("Incorrect number of blacklisted merkleroots, expected %v got %v", 1, len(sbg.Blacklist))
	}
	if sbg.Blacklist[0] != sshp.MerkleRoot {
		t.Fatalf("Merkleroots don't match, expected %v got %v", sshp.MerkleRoot, sbg.Blacklist[0])
	}

	// Try to download the file behind the skylink, this should fail because of
	// the blacklist.
	_, _, err = r.SkynetSkylinkGet(skylink)
	if err == nil {
		t.Fatal("Download should have failed")
	}
	if !strings.Contains(err.Error(), renter.ErrSkylinkBlacklisted.Error()) {
		t.Fatalf("Expected error %v but got %v", renter.ErrSkylinkBlacklisted, err)
	}

	// Try and upload again with force as true to avoid error of path already
	// existing. Additionally need to recreate the reader again from the file
	// data. This should also fail due to the blacklist
	sup.Force = true
	sup.Reader = bytes.NewReader(data)
	_, _, err = r.SkynetSkyfilePost(sup)
	if err == nil {
		t.Fatal("Expected upload to fail")
	}
	if !strings.Contains(err.Error(), renter.ErrSkylinkBlacklisted.Error()) {
		t.Fatalf("Expected error %v but got %v", renter.ErrSkylinkBlacklisted, err)
	}

	// Verify that the SiaPath and Extended SiaPath were removed from the renter
	// due to the upload seeing the blacklist
	_, err = r.RenterFileGet(sp)
	if err == nil {
		t.Fatal("expected error for file not found")
	}
	if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Fatalf("Expected error %v but got %v", filesystem.ErrNotExist, err)
	}
	_, err = r.RenterFileGet(spExtended)
	if err == nil {
		t.Fatal("expected error for file not found")
	}
	if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Fatalf("Expected error %v but got %v", filesystem.ErrNotExist, err)
	}

	// Try Pinning the file, this should fail due to the blacklist
	pinlup := modules.SkyfilePinParameters{
		SiaPath:             sup.SiaPath,
		BaseChunkRedundancy: 2,
		Force:               true,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinlup)
	if err == nil {
		t.Fatal("Expected pin to fail")
	}
	if !strings.Contains(err.Error(), renter.ErrSkylinkBlacklisted.Error()) {
		t.Fatalf("Expected error %v but got %v", renter.ErrSkylinkBlacklisted, err)
	}

	// Remove skylink from blacklist
	add = []string{}
	remove = []string{skylink}
	err = r.SkynetBlacklistPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the skylink is removed from the Blacklist
	sbg, err = r.SkynetBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(sbg.Blacklist) != 0 {
		t.Fatalf("Incorrect number of blacklisted merkleroots, expected %v got %v", 0, len(sbg.Blacklist))
	}

	// Try to download the file behind the skylink. Even though the file was
	// removed from the renter node that uploaded it, it should still be
	// downloadable.
	fetchedData, _, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}

	// Pinning the skylink should also work now
	err = r.SkynetSkylinkPinPost(skylink, pinlup)
	if err != nil {
		t.Fatal(err)
	}

	// Upload a normal siafile with 1-of-N redundancy
	convertData := fastrand.Bytes(int(size))
	reader := bytes.NewReader(convertData)
	siafileSiaPath, err := modules.NewSiaPath("siafileSiaPath")
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterUploadStreamPost(reader, siafileSiaPath, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}

	// Convert to a skyfile
	convertUP := modules.SkyfileUploadParameters{
		SiaPath: siafileSiaPath,
	}
	convertSkylink, err := r.SkynetConvertSiafileToSkyfilePost(convertUP, siafileSiaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm there is a siafile and a skyfile
	_, err = r.RenterFileGet(siafileSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	skyfilePath, err := modules.SkynetFolder.Join(siafileSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileRootGet(skyfilePath)
	if err != nil {
		t.Fatal(err)
	}

	// Blacklist the skylink
	add = []string{convertSkylink}
	remove = []string{}
	err = r.SkynetBlacklistPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}
	sbg, err = r.SkynetBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(sbg.Blacklist) != 1 {
		t.Fatalf("Incorrect number of blacklisted merkleroots, expected %v got %v", 1, len(sbg.Blacklist))
	}

	// Confirm skyfile download returns blacklisted error
	//
	// NOTE: Calling DownloadSkylink doesn't attempt to delete any underlying file
	_, _, err = r.SkynetSkylinkGet(convertSkylink)
	if err == nil || !strings.Contains(err.Error(), renter.ErrSkylinkBlacklisted.Error()) {
		t.Fatalf("Expected error %v but got %v", renter.ErrSkylinkBlacklisted, err)
	}

	// Try and convert to skylink again, should fail. Set the Force Flag to true
	// to avoid error for file already existing
	convertUP.Force = true
	_, err = r.SkynetConvertSiafileToSkyfilePost(convertUP, siafileSiaPath)
	if err == nil || !strings.Contains(err.Error(), renter.ErrSkylinkBlacklisted.Error()) {
		t.Fatalf("Expected error %v but got %v", renter.ErrSkylinkBlacklisted, err)
	}

	// This should delete the skyfile but not the siafile
	_, err = r.RenterFileGet(siafileSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileRootGet(skyfilePath)
	if err == nil || !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Fatalf("Expected error %v but got %v", filesystem.ErrNotExist, err)
	}

	// remove from blacklist
	add = []string{}
	remove = []string{convertSkylink}
	err = r.SkynetBlacklistPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}
	sbg, err = r.SkynetBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(sbg.Blacklist) != 0 {
		t.Fatalf("Incorrect number of blacklisted merkleroots, expected %v got %v", 0, len(sbg.Blacklist))
	}

	// Convert should succeed
	_, err = r.SkynetConvertSiafileToSkyfilePost(convertUP, siafileSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.RenterFileRootGet(skyfilePath)
	if err != nil {
		t.Fatal(err)
	}
}

// testSkynetPortals tests the skynet portals module.
func testSkynetPortals(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	portal1 := modules.SkynetPortal{
		Address: modules.NetAddress("siasky.net:9980"),
		Public:  true,
	}
	// loopback address
	portal2 := modules.SkynetPortal{
		Address: "localhost:9980",
		Public:  true,
	}
	// address without a port
	portal3 := modules.SkynetPortal{
		Address: modules.NetAddress("siasky.net"),
		Public:  true,
	}

	// Add portal.
	add := []modules.SkynetPortal{portal1}
	remove := []modules.NetAddress{}
	err := r.SkynetPortalsPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the portal has been added.
	spg, err := r.SkynetPortalsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(spg.Portals) != 1 {
		t.Fatalf("Incorrect number of portals, expected %v got %v", 1, len(spg.Portals))
	}
	if !reflect.DeepEqual(spg.Portals[0], portal1) {
		t.Fatalf("Portals don't match, expected %v got %v", portal1, spg.Portals[0])
	}

	// Remove the portal.
	add = []modules.SkynetPortal{}
	remove = []modules.NetAddress{portal1.Address}
	err = r.SkynetPortalsPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the portal has been removed.
	spg, err = r.SkynetPortalsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(spg.Portals) != 0 {
		t.Fatalf("Incorrect number of portals, expected %v got %v", 0, len(spg.Portals))
	}

	// Try removing a portal that's not there.
	add = []modules.SkynetPortal{}
	remove = []modules.NetAddress{portal1.Address}
	err = r.SkynetPortalsPost(add, remove)
	if !strings.Contains(err.Error(), "address "+string(portal1.Address)+" not already present in list of portals or being added") {
		t.Fatal("portal should fail to be removed")
	}

	// Try to add and remove a portal at the same time.
	add = []modules.SkynetPortal{portal2}
	remove = []modules.NetAddress{portal2.Address}
	err = r.SkynetPortalsPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the portal was not added.
	spg, err = r.SkynetPortalsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(spg.Portals) != 0 {
		t.Fatalf("Incorrect number of portals, expected %v got %v", 0, len(spg.Portals))
	}

	// Test updating a portal's public status.
	portal1.Public = false
	add = []modules.SkynetPortal{portal1}
	remove = []modules.NetAddress{}
	err = r.SkynetPortalsPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	spg, err = r.SkynetPortalsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(spg.Portals) != 1 {
		t.Fatalf("Incorrect number of portals, expected %v got %v", 1, len(spg.Portals))
	}
	if !reflect.DeepEqual(spg.Portals[0], portal1) {
		t.Fatalf("Portals don't match, expected %v got %v", portal1, spg.Portals[0])
	}

	portal1.Public = true
	add = []modules.SkynetPortal{portal1}
	remove = []modules.NetAddress{}
	err = r.SkynetPortalsPost(add, remove)
	if err != nil {
		t.Fatal(err)
	}

	spg, err = r.SkynetPortalsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(spg.Portals) != 1 {
		t.Fatalf("Incorrect number of portals, expected %v got %v", 1, len(spg.Portals))
	}
	if !reflect.DeepEqual(spg.Portals[0], portal1) {
		t.Fatalf("Portals don't match, expected %v got %v", portal1, spg.Portals[0])
	}

	// Test an invalid network address.
	add = []modules.SkynetPortal{portal3}
	remove = []modules.NetAddress{}
	err = r.SkynetPortalsPost(add, remove)
	if !strings.Contains(err.Error(), "missing port in address") {
		t.Fatal("expected 'missing port' error")
	}

	// Test adding an existing portal with an uppercase address.
	portalUpper := portal1
	portalUpper.Address = modules.NetAddress(strings.ToUpper(string(portalUpper.Address)))
	add = []modules.SkynetPortal{portalUpper}
	remove = []modules.NetAddress{}
	err = r.SkynetPortalsPost(add, remove)
	// This does not currently return an error.
	if err != nil {
		t.Fatal(err)
	}

	spg, err = r.SkynetPortalsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(spg.Portals) != 2 {
		t.Fatalf("Incorrect number of portals, expected %v got %v", 2, len(spg.Portals))
	}
}

// testSkynetHeadRequest verifies the functionality of sending a HEAD request to
// the skylink GET route.
func testSkynetHeadRequest(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Upload a skyfile
	skylink, _, _, err := r.UploadNewSkyfileBlocking(t.Name(), 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// Perform a GET and HEAD request and compare the response headers and
	// content length.
	data, metadata, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	status, header, err := r.SkynetSkylinkHead(skylink, 0)
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK {
		t.Fatalf("Unexpected status for HEAD request, expected %v but received %v", http.StatusOK, status)
	}

	// Verify Skynet-File-Metadata
	strMetadata := header.Get("Skynet-File-Metadata")
	if strMetadata == "" {
		t.Fatal("Expected 'Skynet-File-Metadata' response header to be present")
	}
	var sm modules.SkyfileMetadata
	err = json.Unmarshal([]byte(strMetadata), &sm)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata, sm) {
		t.Log(metadata)
		t.Log(sm)
		t.Fatal("Expected metadatas to be identical")
	}

	// Verify Content-Length
	strContentLength := header.Get("Content-Length")
	if strContentLength == "" {
		t.Fatal("Expected 'Content-Length' response header to be present")
	}
	cl, err := strconv.Atoi(strContentLength)
	if err != nil {
		t.Fatal(err)
	}
	if cl != len(data) {
		t.Fatalf("Content-Length header did not match actual content length of response body, %v vs %v", cl, len(data))
	}

	// Verify Content-Type
	strContentType := header.Get("Content-Type")
	if strContentType == "" {
		t.Fatal("Expected 'Content-Type' response header to be present")
	}

	// Verify Content-Disposition
	strContentDisposition := header.Get("Content-Disposition")
	if strContentDisposition == "" {
		t.Fatal("Expected 'Content-Disposition' response header to be present")
	}
	if !strings.Contains(strContentDisposition, "inline; filename=") {
		t.Fatal("Unexpected 'Content-Disposition' header")
	}

	// Perform a HEAD request with a timeout that exceeds the max timeout
	status, _, _ = r.SkynetSkylinkHead(skylink, api.MaxSkynetRequestTimeout+1)
	if status != http.StatusBadRequest {
		t.Fatalf("Expected StatusBadRequest for a request with a timeout that exceeds the MaxSkynetRequestTimeout, instead received %v", status)
	}

	// Perform a HEAD request for a skylink that does not exist
	status, header, err = r.SkynetSkylinkHead(skylink[:len(skylink)-3]+"abc", 0)
	if status != http.StatusNotFound {
		t.Fatalf("Expected http.StatusNotFound for random skylink but received %v", status)
	}
}

// testSkynetNoWorkers verifies that SkynetSkylinkGet returns an error and does
// not deadlock if there are no workers.
func testSkynetNoWorkers(t *testing.T, tg *siatest.TestGroup) {
	// Create renter, skip setting the allowance so that we can ensure there are
	// no contracts created and therefore no workers in the worker pool
	testDir := renterTestDir(t.Name())
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.SkipSetAllowance = true
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := nodes[0]
	defer func() {
		err = tg.RemoveNode(r)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Since the renter doesn't have an allowance, we know the renter doesn't
	// have any contracts and therefore the worker pool will be empty. Confirm
	// that attempting to download a skylink will return an error and not dead
	// lock.
	_, _, err = r.SkynetSkylinkGet(modules.Skylink{}.String())
	if err == nil {
		t.Fatal("Error is nil, expected error due to no worker")
	} else if !strings.Contains(err.Error(), "no workers") {
		t.Errorf("Expected error containing 'no workers' but got %v", err)
	}
}

// testSkynetDryRunUpload verifies the --dry-run flag when uploading a Skyfile.
func testSkynetDryRunUpload(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]
	siaPath, err := modules.NewSiaPath(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// verify we can perform a skyfile upload (note that we need this to trigger
	// contracts being created, this issue only surfaces when commenting out all
	// other skynet tets)
	_, _, err = r.SkynetSkyfilePost(modules.SkyfileUploadParameters{
		SiaPath:             siaPath,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: "testSkynetDryRun",
			Mode:     0640,
		},
	})
	if err != nil {
		t.Fatal("Expected skynet upload to be successful, instead received err:", err)
	}

	// verify you can't perform a dry-run using the force parameter
	_, _, err = r.SkynetSkyfilePost(modules.SkyfileUploadParameters{
		SiaPath:             siaPath,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: "testSkynetDryRun",
			Mode:     0640,
		},
		Force:  true,
		DryRun: true,
	})
	if err == nil {
		t.Fatal("Expected failure when both 'force' and 'dryrun' parameter are given")
	}

	verifyDryRun := func(sup modules.SkyfileUploadParameters, dataSize int) {
		data := fastrand.Bytes(dataSize)

		sup.DryRun = true
		sup.Reader = bytes.NewReader(data)
		skylinkDry, _, err := r.SkynetSkyfilePost(sup)
		if err != nil {
			t.Fatal(err)
		}

		// verify the skylink can't be found after a dry run
		status, _, _ := r.SkynetSkylinkHead(skylinkDry, 0)
		if status != http.StatusNotFound {
			t.Fatal(fmt.Errorf("Expected 404 not found when trying to fetch a skylink retrieved from a dry run, instead received status %d", status))
		}

		// verify the skfyile got deleted properly
		skyfilePath, err := modules.SkynetFolder.Join(sup.SiaPath.String())
		if err != nil {
			t.Fatal(err)
		}
		_, err = r.RenterFileRootGet(skyfilePath)
		if err == nil || !strings.Contains(err.Error(), "path does not exist") {
			t.Fatal(errors.New("skyfile not deleted after dry run"))
		}

		sup.DryRun = false
		sup.Reader = bytes.NewReader(data)
		skylink, _, err := r.SkynetSkyfilePost(sup)
		if err != nil {
			t.Fatal(err)
		}

		if skylinkDry != skylink {
			t.Log("Expected:", skylink)
			t.Log("Actual:  ", skylinkDry)
			t.Fatalf("VerifyDryRun failed for data size %db, skylink received during the dry-run is not identical to the skylink received when performing the actual upload.", dataSize)
		}
	}

	// verify dry-run of small file
	uploadSiaPath, err := modules.NewSiaPath(fmt.Sprintf("%s%s", t.Name(), "S"))
	if err != nil {
		t.Fatal(err)
	}
	verifyDryRun(modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: "testSkynetDryRunUploadSmall",
			Mode:     0640,
		},
	}, 100)

	// verify dry-run of large file
	uploadSiaPath, err = modules.NewSiaPath(fmt.Sprintf("%s%s", t.Name(), "L"))
	if err != nil {
		t.Fatal(err)
	}
	verifyDryRun(modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: "testSkynetDryRunUploadLarge",
			Mode:     0640,
		},
	}, int(modules.SectorSize*2)+siatest.Fuzz())
}

// testSkynetRequestTimeout verifies that the Skylink routes timeout when a
// timeout query string parameter has been passed.
func testSkynetRequestTimeout(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Upload a skyfile
	skylink, _, _, err := r.UploadNewSkyfileBlocking(t.Name(), 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify we can pin it
	pinSiaPath, err := modules.NewSiaPath(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	pinLUP := modules.SkyfilePinParameters{
		SiaPath:             pinSiaPath,
		Force:               true,
		Root:                false,
		BaseChunkRedundancy: 2,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinLUP)
	if err != nil {
		t.Fatal(err)
	}

	// Create a renter with a timeout dependency injected
	testDir := renterTestDir(t.Name())
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.RenterDeps = &dependencies.DependencyTimeoutProjectDownloadByRoot{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r = nodes[0]
	defer tg.RemoveNode(r)

	// Verify timeout on head request
	status, _, err := r.SkynetSkylinkHead(skylink, 1)
	if status != http.StatusNotFound {
		t.Fatalf("Expected http.StatusNotFound for random skylink but received %v", status)
	}

	// Verify timeout on download request
	_, _, err = r.SkynetSkylinkGetWithTimeout(skylink, 1)
	if errors.Contains(err, renter.ErrProjectTimedOut) {
		t.Fatal("Expected download request to time out")
	}
	if !strings.Contains(err.Error(), "timed out after 1s") {
		t.Log(err)
		t.Fatal("Expected error to specify the timeout")
	}

	// Verify timeout on pin request
	err = r.SkynetSkylinkPinPostWithTimeout(skylink, pinLUP, 2)
	if errors.Contains(err, renter.ErrProjectTimedOut) {
		t.Fatal("Expected pin request to time out")
	}
	if err == nil || !strings.Contains(err.Error(), "timed out after 2s") {
		t.Log(err)
		t.Fatal("Expected error to specify the timeout")
	}
}

// testRegressionTimeoutPanic is a regression test for a double channel close
// which happened when a timeout was hit right before a download project was
// resumed.
func testRegressionTimeoutPanic(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Upload a skyfile
	skylink, _, _, err := r.UploadNewSkyfileBlocking(t.Name(), 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// Create a renter with a BlockResumeJobDownloadUntilTimeout dependency.
	testDir := renterTestDir(t.Name())
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.RenterDeps = dependencies.NewDependencyBlockResumeJobDownloadUntilTimeout()
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r = nodes[0]
	defer tg.RemoveNode(r)

	// Verify timeout on download request doesn't panic.
	_, _, err = r.SkynetSkylinkGetWithTimeout(skylink, 1)
	if errors.Contains(err, renter.ErrProjectTimedOut) {
		t.Fatal("Expected download request to time out")
	}
}

// testSkynetLargeMetadata makes sure that
func testSkynetLargeMetadata(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// Prepare a filename that's greater than a sector. That's the easiest way
	// to force the metadata to be larger than a sector.
	filename := hex.EncodeToString(fastrand.Bytes(int(modules.SectorSize + 1)))

	// Quick fuzz on the force value so that sometimes it is set, sometimes it
	// is not.
	var force bool
	if fastrand.Intn(2) == 0 {
		force = true
	}

	_, _, _, err := r.UploadNewSkyfileBlocking(filename, uint64(100+siatest.Fuzz()), force)
	if err == nil || !strings.Contains(err.Error(), renter.ErrMetadataTooBig.Error()) {
		t.Fatal("Should fail due to ErrMetadataTooBig", err)
	}
}

// testSkynetSkykey tests basic Skykey manager functionality.
func testSkynetSkykey(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	// The renter should be initialized with 0 skykeys.
	skykeys, err := r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != 0 {
		t.Log(skykeys)
		t.Fatal("Expected 0 skykeys")
	}

	sk, err := r.SkykeyCreateKeyPost("testkey1", skykey.TypePrivateID)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the newly created skykey shows up.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != 1 {
		t.Log(skykeys)
		t.Fatal("Expected 1 skykey")
	}
	if skykeys[0].ID() != sk.ID() || skykeys[0].Name != sk.Name {
		t.Log(skykeys[0])
		t.Log(sk)
		t.Fatal("Expected same skykey")
	}

	// Adding the same key should return an error.
	err = r.SkykeyAddKeyPost(sk)
	if err == nil {
		t.Fatal("Expected error", err)
	}

	// Create a testkey from a hard-coded skykey string.
	testSkykeyString := "skykey:AbAc7Uz4NxBrVIzR2lY-LsVs3VWsuCA0D01jxYjaHdRwrfVUuo8DutiGD7OF1B1b3P1olWPXZO1X?name=hardcodedtestkey"
	var testSkykey skykey.Skykey
	err = testSkykey.FromString(testSkykeyString)
	if err != nil {
		t.Fatal(err)
	}

	// Adding an unknown key should succeed.
	err = r.SkykeyAddKeyPost(testSkykey)
	if err != nil {
		t.Fatal(err)
	}

	sk2, err := r.SkykeyGetByName("testkey1")
	if err != nil {
		t.Fatal(err)
	}

	skStr, err := sk.ToString()
	if err != nil {
		t.Fatal(err)
	}
	sk2Str, err := sk2.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skStr != sk2Str {
		t.Fatal("Expected same Skykey string")
	}

	// Check byte equality and string equality.
	skID := sk.ID()
	sk2ID := sk2.ID()
	if !bytes.Equal(skID[:], sk2ID[:]) {
		t.Fatal("Expected byte level equality in IDs")
	}
	if sk2.ID().ToString() != sk.ID().ToString() {
		t.Fatal("Expected to get same key")
	}

	// Check the GetByID endpoint
	sk3, err := r.SkykeyGetByID(sk.ID())
	if err != nil {
		t.Fatal(err)
	}
	sk3Str, err := sk3.ToString()
	if err != nil {
		t.Fatal(err)
	}
	if skStr != sk3Str {
		t.Fatal("Expected same Skykey string")
	}

	// Create a set with the strings of every skykey in the test.
	skykeySet := make(map[string]struct{})
	skykeySet[testSkykeyString] = struct{}{}
	skykeySet[sk2Str] = struct{}{}

	// Create a bunch of skykeys and check that they all get returned.
	nKeys := 10
	for i := 0; i < nKeys; i++ {
		nextSk, err := r.SkykeyCreateKeyPost(fmt.Sprintf("anotherkey-%d", i), skykey.TypePrivateID)
		if err != nil {
			t.Fatal(err)
		}
		nextSkStr, err := nextSk.ToString()
		if err != nil {
			t.Fatal(err)
		}
		skykeySet[nextSkStr] = struct{}{}
	}

	// Check that the expected number of keys was created.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != nKeys+2 {
		t.Log(len(skykeys), nKeys+2)
		t.Fatal("Wrong number of keys")
	}

	// Check that getting all the keys returns all the keys we just created.
	for _, skFromList := range skykeys {
		skStrFromList, err := skFromList.ToString()
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := skykeySet[skStrFromList]; !ok {
			t.Log(skStrFromList, skykeys)
			t.Fatal("Didn't find key")
		}
	}

	// Test misuse of the /skynet/skykey endpoint using an UnsafeClient.
	uc := client.NewUnsafeClient(r.Client)

	// Passing in 0 params shouild return an error.
	baseQuery := "/skynet/skykey"
	var skykeyGet api.SkykeyGET
	err = uc.Get(baseQuery, &skykeyGet)
	if err == nil {
		t.Fatal("Expected an error")
	}

	// Passing in 2 params shouild return an error.
	values := url.Values{}
	values.Set("name", "testkey1")
	values.Set("id", skID.ToString())
	err = uc.Get(fmt.Sprintf("%s?%s", baseQuery, values.Encode()), &skykeyGet)
	if err == nil {
		t.Fatal("Expected an error")
	}

	// Sanity check: uc.Get should return the same value as the safe client
	// method.
	values = url.Values{}
	values.Set("name", "testkey1")
	err = uc.Get(fmt.Sprintf("%s?%s", baseQuery, values.Encode()), &skykeyGet)
	if err != nil {
		t.Fatal(err)
	}
	if skykeyGet.Skykey != sk2Str {
		t.Fatal("Expected same result from  unsafe client")
	}

	// Use the unsafe client to check the Name and ID parameters are set in the
	// GET response.
	values = url.Values{}
	values.Set("name", testSkykey.Name)
	getQuery := fmt.Sprintf("/skynet/skykey?%s", values.Encode())

	skykeyGet = api.SkykeyGET{}
	err = uc.Get(getQuery, &skykeyGet)
	if err != nil {
		t.Fatal(err)
	}
	if skykeyGet.Name != testSkykey.Name {
		t.Log(skykeyGet)
		t.Fatal("Wrong skykey name")
	}
	if skykeyGet.ID != testSkykey.ID().ToString() {
		t.Log(skykeyGet)
		t.Fatal("Wrong skykey ID")
	}
	if skykeyGet.Skykey != testSkykeyString {
		t.Log(skykeyGet)
		t.Fatal("Wrong skykey string")
	}

	// Check the Name and ID params from the /skynet/skykeys GET response.
	var skykeysGet api.SkykeysGET
	err = uc.Get("/skynet/skykeys", &skykeysGet)
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeysGet.Skykeys) != len(skykeySet) {
		t.Fatalf("Got %d skykeys, expected %d", len(skykeysGet.Skykeys), len(skykeySet))
	}
	for _, skGet := range skykeysGet.Skykeys {
		if _, ok := skykeySet[skGet.Skykey]; !ok {
			t.Fatal("skykey not in test set")
		}

		var nextSk skykey.Skykey
		err = nextSk.FromString(skGet.Skykey)
		if err != nil {
			t.Fatal(err)
		}
		if nextSk.Name != skGet.Name {
			t.Fatal("Wrong skykey name")
		}
		if nextSk.ID().ToString() != skGet.ID {
			t.Fatal("Wrong skykey id")
		}
		if nextSk.Type.ToString() != skGet.Type {
			t.Fatal("Wrong skykey type")
		}
	}

	// Test deletion endpoints by deleting half of the keys.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}

	deletedKeys := make(map[skykey.SkykeyID]struct{})
	nKeys = len(skykeys)
	nToDelete := nKeys / 2
	for i, sk := range skykeys {
		if i >= nToDelete {
			break
		}

		if i%2 == 0 {
			err = r.SkykeyDeleteByNamePost(sk.Name)
		} else {
			err = r.SkykeyDeleteByIDPost(sk.ID())
		}
		if err != nil {
			t.Fatal(err)
		}
		deletedKeys[sk.ID()] = struct{}{}
	}

	// Check that the skykeys were deleted.
	skykeys, err = r.SkykeySkykeysGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(skykeys) != nKeys-nToDelete {
		t.Fatalf("Expected %d keys, got %d", nKeys-nToDelete, len(skykeys))
	}

	// Sanity check: Make sure deleted keys are not still around.
	for _, sk := range skykeys {
		if _, ok := deletedKeys[sk.ID()]; ok {
			t.Fatal("Found a key that should have been deleted")
		}
	}
}

// testRenameSiaPath verifies that the siapath to the skyfile can be renamed.
func testRenameSiaPath(t *testing.T, tg *siatest.TestGroup) {
	// Grab Renter
	r := tg.Renters()[0]

	// Create a skyfile
	skylink, sup, _, err := r.UploadNewSkyfileBlocking("testRenameFile", 100, false)
	siaPath := sup.SiaPath
	if err != nil {
		t.Fatal(err)
	}

	// Rename Skyfile with root set to false should fail
	err = r.RenterRenamePost(siaPath, modules.RandomSiaPath(), false)
	if err == nil {
		t.Error("Rename should have failed if the root flag is false")
	}
	if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		t.Errorf("Expected error to contain %v but got %v", filesystem.ErrNotExist, err)
	}

	// Rename Skyfile with root set to true should be successful
	siaPath, err = modules.SkynetFolder.Join(siaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	newSiaPath, err := modules.SkynetFolder.Join(persist.RandomSuffix())
	if err != nil {
		t.Fatal(err)
	}
	err = r.RenterRenamePost(siaPath, newSiaPath, true)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the skyfile can still be downloaded
	_, _, err = r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
}

// testSkynetEncryptionWithType returns the encryption test with the given
// skykeyType set.
func testSkynetEncryptionWithType(skykeyType skykey.SkykeyType) func(t *testing.T, tg *siatest.TestGroup) {
	return func(t *testing.T, tg *siatest.TestGroup) {
		testSkynetEncryption(t, tg, skykeyType)
	}
}

// testSkynetEncryption tests the uploading and pinning of small skyfiles using
// encryption with the given skykeyType.
func testSkynetEncryption(t *testing.T, tg *siatest.TestGroup, skykeyType skykey.SkykeyType) {
	r := tg.Renters()[0]
	encKeyName := "encryption-test-key-" + skykeyType.ToString()

	// Create some data to upload as a skyfile.
	data := fastrand.Bytes(100 + siatest.Fuzz())
	// Need it to be a reader.
	reader := bytes.NewReader(data)
	// Call the upload skyfile client call.
	filename := "testEncryptSmall-" + skykeyType.ToString()
	uploadSiaPath, err := modules.NewSiaPath(filename)
	if err != nil {
		t.Fatal(err)
	}
	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     0640, // Intentionally does not match any defaults.
		},
		Reader:     reader,
		SkykeyName: encKeyName,
	}

	_, _, err = r.SkynetSkyfilePost(sup)
	if err == nil {
		t.Fatal("Expected error for using unknown key")
	}

	// Try again after adding a key.
	// Note we must create a new reader in the params!
	sup.Reader = bytes.NewReader(data)

	_, err = r.SkykeyCreateKeyPost(encKeyName, skykeyType)
	if err != nil {
		t.Fatal(err)
	}

	skylink, sfMeta, err := r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the file behind the skylink.
	fetchedData, metadata, err := r.SkynetSkylinkGet(sfMeta.Skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}
	if metadata.Mode != 0640 {
		t.Error("bad mode")
	}
	if metadata.Filename != filename {
		t.Error("bad filename")
	}

	if sfMeta.Skylink != skylink {
		t.Log(sfMeta.Skylink)
		t.Log(skylink)
		t.Fatal("Expected metadata skylink to match returned skylink")
	}

	// Pin the encrypted Skyfile.
	pinSiaPath, err := modules.NewSiaPath("testSmallEncryptedPinPath" + skykeyType.ToString())
	if err != nil {
		t.Fatal(err)
	}
	pinLUP := modules.SkyfilePinParameters{
		SiaPath:             pinSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 3,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinLUP)
	if err != nil {
		t.Fatal(err)
	}

	// See if the file is present.
	fullPinSiaPath, err := modules.SkynetFolder.Join(pinSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	pinnedFile, err := r.RenterFileRootGet(fullPinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedFile.File.Skylinks) != 1 {
		t.Fatal("expecting 1 skylink")
	}
	if pinnedFile.File.Skylinks[0] != skylink {
		t.Fatal("skylink mismatch")
	}
}

// testSkynetEncryptionLargeFileWithType returns the large-file encryption test with the given
// skykeyType.
func testSkynetEncryptionLargeFileWithType(skykeyType skykey.SkykeyType) func(t *testing.T, tg *siatest.TestGroup) {
	return func(t *testing.T, tg *siatest.TestGroup) {
		testSkynetEncryptionLargeFile(t, tg, skykeyType)
	}
}

// testSkynetEncryption tests the uploading and pinning of large skyfiles using
// encryption.
func testSkynetEncryptionLargeFile(t *testing.T, tg *siatest.TestGroup, skykeyType skykey.SkykeyType) {
	r := tg.Renters()[0]
	encKeyName := "large-file-encryption-test-key-" + skykeyType.ToString()

	// Create some data to upload as a skyfile.
	data := fastrand.Bytes(5 * int(modules.SectorSize))
	// Need it to be a reader.
	reader := bytes.NewReader(data)
	// Call the upload skyfile client call.
	filename := "testEncryptLarge-" + skykeyType.ToString()
	uploadSiaPath, err := modules.NewSiaPath(filename)
	if err != nil {
		t.Fatal(err)
	}
	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     0640, // Intentionally does not match any defaults.
		},
		Reader:     reader,
		SkykeyName: encKeyName,
	}

	_, err = r.SkykeyCreateKeyPost(encKeyName, skykeyType)
	if err != nil {
		t.Fatal(err)
	}

	skylink, sfMeta, err := r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}

	if sfMeta.Skylink != skylink {
		t.Log(sfMeta.Skylink)
		t.Log(skylink)
		t.Fatal("Expected metadata skylink to match returned skylink")
	}

	// Try to download the file behind the skylink.
	fetchedData, metadata, err := r.SkynetSkylinkGet(sfMeta.Skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}
	if metadata.Mode != 0640 {
		t.Error("bad mode")
	}
	if metadata.Filename != filename {
		t.Error("bad filename")
	}

	// Pin the encrypted Skyfile.
	pinSiaPath, err := modules.NewSiaPath("testEncryptedPinPath" + skykeyType.ToString())
	if err != nil {
		t.Fatal(err)
	}
	pinLUP := modules.SkyfilePinParameters{
		SiaPath:             pinSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
	}
	err = r.SkynetSkylinkPinPost(skylink, pinLUP)
	if err != nil {
		t.Fatal(err)
	}

	// See if the file is present.
	fullPinSiaPath, err := modules.SkynetFolder.Join(pinSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	pinnedFile, err := r.RenterFileRootGet(fullPinSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedFile.File.Skylinks) != 1 {
		t.Fatal("expecting 1 skylink")
	}
	if pinnedFile.File.Skylinks[0] != skylink {
		t.Fatal("skylink mismatch")
	}
}

// testSkynetDefaultPath tests whether defaultPath metadata parameter works
// correctly
func testSkynetDefaultPath(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]
	fc1 := "File1Contents"
	fc2 := "File2Contents"
	indexJs := "index.js"
	invalidPath := "invalid.js"

	// TEST: Contains index.html but doesn't specify a default path.
	// It should return the content of index.html.
	filename := "index.html_nil"
	files := make(map[string][]byte)
	files["index.html"] = []byte(fc1)
	files["about.html"] = []byte(fc2)
	skylink, _, _, err := r.UploadNewMultipartSkyfileBlocking(filename, files, nil, false)
	if err != nil {
		t.Fatal("Failed to upload multipart file.", err)
	}
	content, _, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, files["index.html"]) {
		t.Fatalf("Expected to get content '%s', instead got '%s'", files["index.html"], string(content))
	}

	// TEST: Contains index.html but specifies a different default path.
	// Contains index.js and specifies "index.js" as default path.
	// It should return the content of index.js.
	filename = "index.html_index.js"
	files = make(map[string][]byte)
	files["index.html"] = []byte(fc1)
	files["index.js"] = []byte(fc2)
	skylink, _, _, err = r.UploadNewMultipartSkyfileBlocking(filename, files, &indexJs, false)
	if err != nil {
		t.Fatal("Failed to upload multipart file.", err)
	}
	content, _, err = r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, files["index.js"]) {
		t.Fatalf("Expected to get content '%s', instead got '%s'", files["index.js"], string(content))
	}

	// TEST: Contains index.html but specifies a different INVALID default path.
	// This should fail on upload with "invalid default path provided".
	filename = "index.html_invalid"
	files = make(map[string][]byte)
	files["index.html"] = []byte(fc1)
	files["about.html"] = []byte(fc2)
	_, _, _, err = r.UploadNewMultipartSkyfileBlocking(filename, files, &invalidPath, false)
	if err == nil || !strings.Contains(err.Error(), api.ErrInvalidDefaultPath.Error()) {
		t.Fatalf("Expected error 'invalid default path provided', got '%+v'", err)
	}

	// TEST: Does not contain "index.html".
	// Contains index.js and specifies "index.js" as default path.
	// It should return the content of index.js.
	filename = "index.js_index.js"
	files = make(map[string][]byte)
	files["index.js"] = []byte(fc1)
	files["about.html"] = []byte(fc2)
	skylink, _, _, err = r.UploadNewMultipartSkyfileBlocking(filename, files, &indexJs, false)
	if err != nil {
		t.Fatal("Failed to upload multipart file.", err)
	}
	content, _, err = r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, files["index.js"]) {
		t.Fatalf("Expected to get content '%s', instead got '%s'", files["index.js"], string(content))
	}

	// TEST: Does not contain index.html and specifies an INVALID default path.
	// This should fail on upload with "invalid default path provided".
	filename = "index.js_invalid"
	files = make(map[string][]byte)
	files["index.js"] = []byte(fc1)
	files["about.html"] = []byte(fc2)
	_, _, _, err = r.UploadNewMultipartSkyfileBlocking(filename, files, &invalidPath, false)
	if err == nil || !strings.Contains(err.Error(), api.ErrInvalidDefaultPath.Error()) {
		t.Fatalf("Expected error 'invalid default path provided', got '%+v'", err)
	}

	// TEST: Does not contain "index.html".
	// Contains a single file and doesn't specify a default path.
	// It should return the content of index.js.
	filename = "index.js"
	files = make(map[string][]byte)
	files["index.js"] = []byte(fc1)
	skylink, _, _, err = r.UploadNewMultipartSkyfileBlocking(filename, files, nil, false)
	if err != nil {
		t.Fatal("Failed to upload multipart file.", err)
	}
	content, _, err = r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, files["index.js"]) {
		t.Fatalf("Expected to get content '%s', instead got '%s'", files["index.js"], string(content))
	}
}

// testSkynetSingleFileNoSubfiles ensures that a single file uploaded as a
// skyfile will not have `subfiles` defined in its metadata. This is required by
// the `defaultPath` and `redirect` logic.
func testSkynetSingleFileNoSubfiles(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]

	skylink, sup, _, err := r.UploadNewSkyfileBlocking("file.name", modules.SectorSize, false)
	if err != nil {
		t.Fatal("Failed to upload a single file.", err)
	}
	if sup.FileMetadata.Subfiles != nil {
		t.Fatal("Expected empty subfiles on upload, got", sup.FileMetadata.Subfiles)
	}
	_, metadata, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Subfiles != nil {
		t.Fatal("Expected empty subfiles on download, got", sup.FileMetadata.Subfiles)
	}
}

// fileMap is a helper type that maps filenames onto the raw file data
type fileMap map[string][]byte

// readTarArchive is a helper function that takes a reader containing a tar
// archive and returns an fileMap, which is a small helper struct that maps the
// filename to the data.
func readTarArchive(r io.Reader) (fileMap, error) {
	a := make(fileMap)
	tr := tar.NewReader(r)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		data, err := ioutil.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		a[header.Name] = data
	}
	return a, nil
}

// readZipArchive is a helper function that takes a reader containing a zip
// archive and returns an fileMap, which is a small helper struct that maps the
// filename to the data.
func readZipArchive(r io.Reader) (fileMap, error) {
	a := make(fileMap)

	// copy all data to a buffer (this is necessary because the zipreader
	// expects a `ReaderAt`)
	buff := bytes.NewBuffer([]byte{})
	size, err := io.Copy(buff, r)
	if err != nil {
		return nil, err
	}
	reader := bytes.NewReader(buff.Bytes())
	zr, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, err
	}

	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}

		data, err := ioutil.ReadAll(rc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		err = rc.Close()
		if err != nil {
			return nil, err
		}
		a[f.FileHeader.Name] = data
	}
	return a, nil
}
