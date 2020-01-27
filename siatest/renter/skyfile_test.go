package renter

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkyfile provides basic end-to-end testing for uploading skyfiles and
// downloading the resulting skylinks.
func TestSkyfile(t *testing.T) {
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

	/*
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
		lup := modules.LinkfileUploadParameters{
			SiaPath:             uploadSiaPath,
			Force:               force,
			BaseChunkRedundancy: 2,
			FileMetadata: modules.LinkfileMetadata{
				Filename:   filename,
				Executable: false,
			},

			Reader: reader,
		}
		skylink, err := r.SkynetSkyfilePost(lup)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("Example skylink:", skylink)

		// Try to download the file behind the skylink.
		fetchedData, err := r.RenterSkylinkGet(skylink)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fetchedData, data) {
			t.Error("upload and download doesn't match")
			t.Log(data)
			t.Log(fetchedData)
		}
	*/

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
	largeLup := modules.LinkfileUploadParameters{
		SiaPath:             largeSiaPath,
		Force:               force2,
		BaseChunkRedundancy: 2,
		FileMetadata: modules.LinkfileMetadata{
			Filename: largeFilename,
			// Remaining fields intentionally left to default
		},

		Reader: largeReader,
	}
	largeSkylink, err := r.SkynetSkyfilePost(largeLup)
	if err != nil {
		t.Fatal(err)
	}
	largeFetchedData, err := r.SkynetSkylinkGet(largeSkylink)
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
	if len(largeRenterFile.File.Sialinks) != 1 {
		t.Fatal("expecting one skylink:", len(largeRenterFile.File.Sialinks))
	}
	if largeRenterFile.File.Sialinks[0] != largeSkylink {
		t.Error("skylinks should match")
		t.Log(largeRenterFile.File.Sialinks[0])
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
		lup = modules.LinkfileUploadParameters{
			SiaPath:             uploadSiaPath2,
			Force:               !force,
			BaseChunkRedundancy: 2,
			FileMetadata: modules.LinkfileMetadata{
				Executable: true,
				Filename:   filename2,
			},
		}

		skylink2, err := r.RenterConvertSiafileToSkyfilePost(lup, remoteFile.SiaPath())
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
