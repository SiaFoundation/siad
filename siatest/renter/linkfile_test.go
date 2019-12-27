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
	if fastrand.Intn(1) == 0 {
		force = true
	}
	lup := modules.LinkfileUploadParameters{
		SiaPath:             uploadSiaPath,
		Force:               true, // Even there is no file to replace, try setting 'force' to true.
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
}
