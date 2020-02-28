package renter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkynet provides basic end-to-end testing for uploading skyfiles and
// downloading the resulting skylinks.
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
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
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

	// Try to download the file using the ReaderGet method.
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
	err = build.Retry(25, 250*time.Millisecond, func() error {
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

	// Upload a siafile that will then be converted to a skyfile. The siafile
	// needs at least 2 sectors.
	/*
		localFile, remoteFile, err := r.UploadNewFileBlocking(int(modules.SectorSize*2)+siatest.Fuzz(), 2, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		localData, err := localFile.Data()
		if err != nil {
			t.Fatal(err)
		}

		filename2 := "testTwo"
		uploadSiaPath2, err := modules.NewSiaPath("testTwoPath")
		if err != nil {
			t.Fatal(err)
		}
		sup = modules.SkyfileUploadParameters{
			SiaPath:             uploadSiaPath2,
			Force:               !force,
			BaseChunkRedundancy: 2,
			FileMetadata: modules.SkyfileMetadata{
				Executable: true,
				Filename:   filename2,
			},
		}

		skylink2, err := r.RenterConvertSiafileToSkyfilePost(sup, remoteFile.SiaPath())
		if err != nil {
			t.Fatal(err)
		}
		// Try to download the skylink.
		fetchedData, err = r.RenterSkylinkGet(skylink2)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fetchedData, localData) {
			t.Error("upload and download doesn't match")
		}
	*/

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

// TestSkynetMultipartUpload provides end-to-end testing for uploading multiple
// files as a single skyfile using multipart file upload. The uploaded subfiles
// are then retrievable by skylink and their filename.
func TestSkynetMultipartUpload(t *testing.T) {
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
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	r := tg.Renters()[0]

	testMultipartUploadEmpty(t, r)
	testMultipartUploadSmall(t, r)
	testMultipartUploadLarge(t, r)
}

// testMultipartUploadEmpty tests you can perform a multipart upload without
// any subfiles.
func testMultipartUploadEmpty(t *testing.T, r *siatest.TestNode) {
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
}

// testMultipartUploadSmall tests multipart upload for small files, small files
// are files which are smaller than one sector, and thus don't need a fanout.
func testMultipartUploadSmall(t *testing.T, r *siatest.TestNode) {
	var offset uint64

	// create a multipart upload that uploads several files.
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	subfiles := make(modules.SkyfileSubfiles)

	// add a file at root level
	data := []byte("File1Contents")
	subfile := addMultipartFile(writer, data, "files[]", "file1", 0600, &offset)
	subfiles[subfile.Filename] = subfile

	// add a nested file
	data = []byte("File2Contents")
	subfile = addMultipartFile(writer, data, "files[]", "nested/file2", 0640, &offset)
	subfiles[subfile.Filename] = subfile

	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(body.Bytes())

	// Call the upload skyfile client call.
	uploadSiaPath, err := modules.NewSiaPath("TestFolderUpload")
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
	_, fileMetadata, err := r.SkynetSkylinkGet(fmt.Sprintf("%s?format=concat", skylink))
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
}

// testMultipartUploadLarge tests multipart upload for large files, large files
// are files which are larger than one sector, and thus need a fanout streamer.
func testMultipartUploadLarge(t *testing.T, r *siatest.TestNode) {
	var offset uint64

	// create a multipart upload that uploads several files.
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	subfiles := make(modules.SkyfileSubfiles)

	// add a small file at root level
	smallData := []byte("File1Contents")
	subfile := addMultipartFile(writer, smallData, "files[]", "smallfile1.txt", 0600, &offset)
	subfiles[subfile.Filename] = subfile

	// add a large nested file
	largeData := fastrand.Bytes(2 * int(modules.SectorSize))
	subfile = addMultipartFile(writer, largeData, "files[]", "nested/largefile2.txt", 0644, &offset)
	subfiles[subfile.Filename] = subfile

	err := writer.Close()
	if err != nil {
		t.Fatal(err)
	}
	allData := body.Bytes()
	reader := bytes.NewReader(allData)

	// Call the upload skyfile client call.
	uploadSiaPath, err := modules.NewSiaPath("TestFolderUploadLarge")
	if err != nil {
		t.Fatal(err)
	}

	sup := modules.SkyfileMultipartUploadParameters{
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

	largeFetchedData, _, err := r.SkynetSkylinkGet(fmt.Sprintf("%s?format=concat", largeSkylink))
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

var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// escapeQuotes escapes the quotes in the given string.
func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}

// createFormFileHeaders builds a header from the given params. These headers are used when creating the parts in a multi-part form upload.
func createFormFileHeaders(fieldname, filename string, headers map[string]string) textproto.MIMEHeader {
	fieldname = escapeQuotes(fieldname)
	filename = escapeQuotes(filename)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldname, filename))
	for k, v := range headers {
		h.Set(k, v)
	}
	return h
}

// addMultipartField is a helper function to add a file to the multipart form-
// data. Note that the given data will be treated as binary data, and the multi
// part 's ContentType header will be set accordingly.
func addMultipartFile(w *multipart.Writer, filedata []byte, filekey, filename string, filemode uint64, offset *uint64) modules.SkyfileSubfileMetadata {
	h := map[string]string{"mode": fmt.Sprintf("%o", filemode)}
	partHeader := createFormFileHeaders(filekey, filename, h)
	part, err := w.CreatePart(partHeader)
	if err != nil {
		panic(err)
	}

	_, err = part.Write(filedata)
	metadata := modules.SkyfileSubfileMetadata{
		Filename:    filename,
		ContentType: "application/octet-stream",
		Mode:        os.FileMode(filemode),
		Len:         uint64(len(filedata)),
	}

	if offset != nil {
		metadata.Offset = *offset
		*offset += metadata.Len
	}

	return metadata
}

// TestSkynetNoFilename verifies that posting a Skyfile without providing a
// filename fails.
func TestSkynetNoFilename(t *testing.T) {
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
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
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
	subfile := addMultipartFile(writer, data, "files[]", nofilename, 0600, nil)
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

	subfile = addMultipartFile(writer, []byte("File1Contents"), "files[]", "testNoFilenameMultipart", 0600, nil)
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

// TestSkynetSubDirDownload verifies downloading data from a skyfile using a path to download single subfiles or subdirectories
func TestSkynetSubDirDownload(t *testing.T) {
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
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	r := tg.Renters()[0]

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	dataFile1 := []byte("file1.txt")
	dataFile2 := []byte("file2.txt")
	dataFile3 := []byte("file3.txt")
	addMultipartFile(writer, dataFile1, "files[]", "/a/5.f4f8b583.chunk.js", 0600, nil)
	addMultipartFile(writer, dataFile2, "files[]", "/a/5.f4f.chunk.js.map", 0600, nil)
	addMultipartFile(writer, dataFile3, "files[]", "/b/file3.txt", 0640, nil)

	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(body.Bytes())

	uploadSiaPath, err := modules.NewSiaPath("testSkynetSubfileDownload")
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
		Filename:            "testSkynetSubfileDownload",
	}

	skylink, _, err := r.SkynetSkyfileMultiPartPost(mup)
	if err != nil {
		t.Fatal(err)
	}

	// get all data without specifying format, since the file contains multiple
	// subfiles, this should fail
	_, _, err = r.SkynetSkylinkGet(skylink)
	if err == nil || !strings.Contains(err.Error(), "format must be specified") {
		t.Fatal("Expected download to fail because we are downloading a directory and format was not provided, err:", err)
	}

	// now specify the correct format
	allData, _, err := r.SkynetSkylinkGet(fmt.Sprintf("%s?format=concat", skylink))
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

	// get all data for path "/" (equals all data)
	allData, _, err = r.SkynetSkylinkGet(fmt.Sprintf("%s/?format=concat", skylink))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expected, allData) {
		t.Log("expected:", expected)
		t.Log("actual:", allData)
		t.Fatal("Unexpected data for dir A")
	}

	// get all data for path "a"
	dataDirA, _, err := r.SkynetSkylinkGet(fmt.Sprintf("%s/a?format=concat", skylink))
	if err != nil {
		t.Fatal(err)
	}
	expected = append(dataFile1, dataFile2...)
	if !bytes.Equal(expected, dataDirA) {
		t.Log("expected:", expected)
		t.Log("actual:", dataDirA)
		t.Fatal("Unexpected data for dir A")
	}

	// get all data for path "b"
	dataDirB, metadataDirB, err := r.SkynetSkylinkGet(fmt.Sprintf("%s/b?format=concat", skylink))
	if err != nil {
		t.Fatal(err)
	}
	expected = dataFile3
	if !bytes.Equal(expected, dataDirB) {
		t.Log("expected:", expected)
		t.Log("actual:", dataDirB)
		t.Fatal("Unexpected data for dir B")
	}

	if metadataDirB.Filename != "/b" {
		t.Fatal("Expected filename of subdir to be equal to the path")
	}
	mdF3, ok := metadataDirB.Subfiles["/b/file3.txt"]
	if !ok {
		t.Fatal("Expected subfile metadata of file3 to be present")
	}

	mdF3Expected := modules.SkyfileSubfileMetadata{
		Mode:        os.FileMode(0640),
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

	// verify we get a 400 if we don't supply the format parameter
	_, _, err = r.SkynetSkylinkGet(fmt.Sprintf("%s/b", skylink))
	if err == nil || !strings.Contains(err.Error(), "format must be specified") {
		t.Fatal("Expected download to fail because we are downloading a directory and format was not provided, err:", err)
	}

	// verify we get a 400 if we supply an unsupported format parameter
	_, _, err = r.SkynetSkylinkGet(fmt.Sprintf("%s/b?format=raw", skylink))
	if err == nil || !strings.Contains(err.Error(), "unable to parse 'format'") {
		t.Fatal("Expected download to fail because we are downloading a directory and an invalid format was provided, err:", err)
	}
}

// TestSkynetDisableForce verifies the behavior of force and the header that
// allows disabling forcefully uploading a Skyfile
func TestSkynetDisableForce(t *testing.T) {
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
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	r := tg.Renters()[0]

	// Create some data to upload.
	data := fastrand.Bytes(100)
	reader := bytes.NewReader(data)

	// Create the sia path
	uploadSiaPath, err := modules.NewSiaPath("testDisableForce")
	if err != nil {
		t.Fatal(err)
	}

	// Verify normal force behaviour
	sup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               false,
		Root:                false,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: "testDisableForce",
			Mode:     os.FileMode(0640), // Intentionally does not match any defaults.
		},
		Reader: reader,
	}
	skylink, _, err := r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _, err := r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatal("Unexpected data returned for skylink")
	}

	// Upload data to that same siapath again, without setting the force
	// flag, this should result in failure as there already exists a file at
	// that specified path.
	data = fastrand.Bytes(100)
	sup.Reader = bytes.NewReader(data)
	_, _, err = r.SkynetSkyfilePost(sup)
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatal(err)
	}

	// Upload once more, but now use force. It should allow us to
	// overwrite the file at the existing path
	sup.Force = true
	sup.Reader = bytes.NewReader(data)
	skylink, _, err = r.SkynetSkyfilePost(sup)
	if err != nil {
		t.Fatal(err)
	}
	downloaded, _, err = r.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatal("Unexpected data returned for skylink")
	}

	// Upload using the force flag again, however now we set the
	// Skynet-Disable-Force to true, which should prevent us from uploading.
	// Because we have to pass in a custom header, we have to setup the request
	// ourselves and can not use the client.
	req, err := skynetSkyfilePostRequestWithHeaders(r, sup)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Skynet-Disable-Force", "true")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := http.DefaultClient.Do(req)
	if res.StatusCode != 400 {
		t.Log(res)
		t.Fatal("Expected HTTP Bad Request")
	}
	var apiErr api.Error
	if err := json.NewDecoder(res.Body).Decode(&apiErr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apiErr.Error(), "'force' has been disabled") {
		t.Log(res)
		t.Fatal(apiErr)
	}
}

// skynetSkyfilePostRequestWithHeaders is a helper function that turns the given
// SkyfileUploadParameters into a SkynetSkyfilePost request, allowing additional
// headers to be set on the request object
func skynetSkyfilePostRequestWithHeaders(r *siatest.TestNode, sup modules.SkyfileUploadParameters) (*http.Request, error) {
	values := url.Values{}
	values.Set("filename", sup.FileMetadata.Filename)
	forceStr := fmt.Sprintf("%t", sup.Force)
	values.Set("force", forceStr)
	modeStr := fmt.Sprintf("%o", sup.FileMetadata.Mode)
	values.Set("mode", modeStr)
	redundancyStr := fmt.Sprintf("%v", sup.BaseChunkRedundancy)
	values.Set("basechunkredundancy", redundancyStr)
	rootStr := fmt.Sprintf("%t", sup.Root)
	values.Set("root", rootStr)

	resource := fmt.Sprintf("/skynet/skyfile/%s?%s", sup.SiaPath.String(), values.Encode())
	return r.NewRequest("POST", resource, sup.Reader)
}

// TestSkynetBlacklist tests the skynet blacklist module
func TestSkynetBlacklist(t *testing.T) {
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
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	r := tg.Renters()[0]

	// Create skyfile upload params, data should be larger than a sector size to
	// test large file uploads and the deletion of their extended data.
	data := fastrand.Bytes(int(modules.SectorSize) + 100 + siatest.Fuzz())
	reader := bytes.NewReader(data)
	filename := "skyfile"
	uploadSiaPath, err := modules.NewSiaPath("testskyfile")
	if err != nil {
		t.Fatal(err)
	}
	lup := modules.SkyfileUploadParameters{
		SiaPath:             uploadSiaPath,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.SkyfileMetadata{
			Filename: filename,
			Mode:     0640, // Intentionally does not match any defaults.
		},

		Reader: reader,
	}

	// Upload and create a skylink
	skylink, _, err := r.SkynetSkyfilePost(lup)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that the skyfile and its extended info are registered with the
	// renter
	sp, err := modules.SkynetFolder.Join(uploadSiaPath.String())
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

	// Blacklist the skylink
	add := []string{skylink}
	remove := []string{}
	err = r.SkynetBlacklistPost(add, remove)
	if err != nil {
		t.Fatal(err)
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
	lup.Force = true
	lup.Reader = bytes.NewReader(data)
	_, _, err = r.SkynetSkyfilePost(lup)
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
		SiaPath:             uploadSiaPath,
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
}
