package renter

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestLinkfile provides basic end-to-end testing for uploading linkfiles and
// downloading the resulting sialinks.
func TestLinkfile(t *testing.T) {
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
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	r := tg.Renters()[0]

	// Create some data to upload as a linkfile.
	data := fastrand.Bytes(100 + siatest.Fuzz())
	// Need it to be a reader.
	reader := bytes.NewReader(data)
	// Call the upload linkfile client call.
	filename := "testOne"
	uploadSiaPath, err := modules.NewSiaPath("testOnePath")
	if err != nil {
		t.Fatal(err)
	}
	// Quick fuzz on the force value so that sometimes it is set, sometimes it
	// is not.
	var force bool
	if fastrand.Intn(2) == 0 {
		force = true
	}
	lup := modules.LinkfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               force,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.LinkfileMetadata{
			Name:       filename,
			Mode:       0600, // intentionally not the default
			CreateTime: 1e6,  // intentionally before current time
		},

		Reader: reader,
	}
	sialink, err := r.RenterLinkfilePost(lup)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Example sialink:", sialink)

	// Try to download the file behind the sialink.
	fetchedData, err := r.RenterSialinkGet(sialink)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, data) {
		t.Error("upload and download doesn't match")
		t.Log(data)
		t.Log(fetchedData)
	}

	// Check the metadata of the siafile, see that the metadata of the siafile
	// has the sialink referenced.
	linkfilePath, err := modules.LinkfileSiaFolder.Join(uploadSiaPath.String())
	if err != nil {
		t.Fatal(err)
	}
	renterFile, err := r.RenterFileRootGet(linkfilePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(renterFile.File.Sialinks) != 1 {
		t.Fatal("expecting one sialink:", len(renterFile.File.Sialinks))
	}
	if renterFile.File.Sialinks[0] != sialink {
		t.Error("sialinks should match")
		t.Log(renterFile.File.Sialinks[0])
		t.Log(sialink)
	}

	// TODO: Need to verify the mode, name, and create-time. At this time, I'm
	// not sure how we can feed those out of the API. They aren't going to be
	// the same as the siafile values, because the siafile was created
	// separately.
	//
	// Maybe this can be accomplished by tagging a flag to the API which has the
	// layout and metadata streamed as the first bytes? Maybe there is some
	// easier way.

	// Upload a siafile that will then be converted to a linkfile. The siafile
	// needs at least 2 sectors.
	localFile, remoteFile, err := r.UploadNewFileBlocking(int(modules.SectorSize*2)+siatest.Fuzz(), 1, 1, false)
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
	lup = modules.LinkfileUploadParameters{
		SiaPath:             uploadSiaPath2,
		Force:               !force,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.LinkfileMetadata{
			Name:       filename2,
			Mode:       0600, // intentionally not the default
			CreateTime: 1e6,  // intentionally before current time
		},
	}
	sialink2, err := r.RenterConvertSiafileToLinkfilePost(lup, remoteFile.SiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// Try to download the sialink.
	fetchedData, err = r.RenterSialinkGet(sialink2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fetchedData, localData) {
		t.Error("upload and download doesn't match")
	}

	// TODO: Need to verify the mode, name, and create-time. At this time, I'm
	// not sure how we can feed those out of the API. They aren't going to be
	// the same as the siafile values, because the siafile was created
	// separately.
	//
	// Maybe this can be accomplished by tagging a flag to the API which has the
	// layout and metadata streamed as the first bytes? Maybe there is some
	// easier way.
}
