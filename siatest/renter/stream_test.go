package renter

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// TestRenterDownloadStreamCache checks that the download stream caching is
// functioning correctly - that there are no rough edges around weirdly sized
// files or alignments, and that the cache serves data correctly.
func TestRenterDownloadStreamCache(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup with a renter.
	groupParams := siatest.GroupParams{
		Hosts:   3,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(renterTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := tg.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Upload a file to the renter.
	fileSize := 123456
	renter := tg.Renters()[0]
	localFile, remoteFile, err := renter.UploadNewFileBlocking(fileSize, 2, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// Download that file using a download stream.
	_, downloadedData, err := renter.DownloadByStream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	err = localFile.Equal(downloadedData)
	if err != nil {
		t.Fatal(err)
	}

	// Test downloading a bunch of random partial streams. Generally these will
	// not be aligned at all.
	for i := 0; i < 25; i++ {
		// Get random values for 'from' and 'to'.
		from := fastrand.Intn(fileSize)
		to := fastrand.Intn(fileSize - from)
		to += from
		if to == from {
			continue
		}

		// Stream some data.
		streamedPartialData, err := renter.StreamPartial(remoteFile, localFile, uint64(from), uint64(to))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Compare(streamedPartialData, downloadedData[from:to]) != 0 {
			t.Error("Read range returned the wrong data")
		}
	}

	// Test downloading a bunch of partial streams that start from 0.
	for i := 0; i < 25; i++ {
		// Get random values for 'from' and 'to'.
		from := 0
		to := fastrand.Intn(fileSize - from)
		if to == from {
			continue
		}

		// Stream some data.
		streamedPartialData, err := renter.StreamPartial(remoteFile, localFile, uint64(from), uint64(to))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Compare(streamedPartialData, downloadedData[from:to]) != 0 {
			t.Error("Read range returned the wrong data")
		}
	}

	// Test a series of chosen values to have specific alignments.
	for i := 0; i < 5; i++ {
		for j := 0; j < 3; j++ {
			// Get random values for 'from' and 'to'.
			from := 0 + j
			to := 8190 + i
			if to == from {
				continue
			}

			// Stream some data.
			streamedPartialData, err := renter.StreamPartial(remoteFile, localFile, uint64(from), uint64(to))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Compare(streamedPartialData, downloadedData[from:to]) != 0 {
				t.Error("Read range returned the wrong data")
			}
		}
	}
	for i := 0; i < 5; i++ {
		for j := 0; j < 5; j++ {
			// Get random values for 'from' and 'to'.
			from := 8190 + j
			to := 16382 + i
			if to == from {
				continue
			}

			// Stream some data.
			streamedPartialData, err := renter.StreamPartial(remoteFile, localFile, uint64(from), uint64(to))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Compare(streamedPartialData, downloadedData[from:to]) != 0 {
				t.Error("Read range returned the wrong data")
			}
		}
	}
	for i := 0; i < 3; i++ {
		// Get random values for 'from' and 'to'.
		from := fileSize - i
		to := fileSize
		if to == from {
			continue
		}

		// Stream some data.
		streamedPartialData, err := renter.StreamPartial(remoteFile, localFile, uint64(from), uint64(to))
		if err != nil {
			t.Fatal(err, from, to)
		}
		if bytes.Compare(streamedPartialData, downloadedData[from:to]) != 0 {
			t.Error("Read range returned the wrong data")
		}
	}
}

// TestRenterStream executes a number of subtests using the same TestGroup to
// save time on initialization
func TestRenterStream(t *testing.T) {
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
		{"TestStreamLargeFile", testStreamLargeFile},
		{"TestStreamRepair", testStreamRepair},
		{"TestUploadStreaming", testUploadStreaming},
		{"TestUploadStreamingWithBadDeps", testUploadStreamingWithBadDeps},
	}

	// Run tests
	if err := runRenterTests(t, groupParams, subTests); err != nil {
		t.Fatal(err)
	}
}

// testStreamLargeFile tests that using the streaming endpoint to download
// multiple chunks works.
func testStreamLargeFile(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	renter := tg.Renters()[0]
	// Upload file, creating a piece for each host in the group
	dataPieces := uint64(2)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	ct := crypto.TypeDefaultRenter
	fileSize := int(10 * siatest.ChunkSize(dataPieces, ct))
	localFile, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
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

// testStreamRepair tests if repairing a file using the streaming endpoint
// works.
func testStreamRepair(t *testing.T, tg *siatest.TestGroup) {
	// Grab the first of the group's renters
	r := tg.Renters()[0]

	// Check that we have enough hosts for this test.
	if len(tg.Hosts()) < 2 {
		t.Fatal("This test requires at least 2 hosts")
	}

	// Set fileSize and redundancy for upload
	fileSize := int(5*modules.SectorSize) + siatest.Fuzz()
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces

	// Upload file
	localFile, remoteFile, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Move the file locally to make sure the repair loop can't find it.
	if err := localFile.Move(); err != nil {
		t.Fatal("failed to delete local file", err)
	}

	// Take down all of the hosts and check if redundancy decreases.
	hostsRemoved := 0
	for i := uint64(0); i < parityPieces+dataPieces; i++ {
		if err := tg.RemoveNode(tg.Hosts()[0]); err != nil {
			t.Fatal("Failed to shutdown host", err)
		}
		hostsRemoved++
	}
	if err := r.WaitForDecreasingRedundancy(remoteFile, 0); err != nil {
		t.Fatal("Redundancy isn't decreasing", err)
	}
	// Bring up hosts to replace the ones that went offline.
	for hostsRemoved > 0 {
		hostsRemoved--
		_, err = tg.AddNodes(node.HostTemplate)
		if err != nil {
			t.Fatal("Failed to create a new host", err)
		}
	}
	// Use the streaming endpoint to repair the file. It should always reach 100%.
	b, err := ioutil.ReadFile(localFile.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RenterUploadStreamRepairPost(bytes.NewReader(b), remoteFile.SiaPath()); err != nil {
		t.Fatal(err)
	}
	if err := r.WaitForUploadHealth(remoteFile); err != nil {
		t.Fatal("File wasn't repaired", err)
	}
	// We should be able to download
	if _, _, err := r.DownloadByStream(remoteFile); err != nil {
		t.Fatal("Failed to download file", err)
	}
	// Repair the file again to make sure we don't get stuck on chunks that are
	// already repaired. Datapieces and paritypieces can be set to 0 as long as
	// repair is true.
	if err := r.RenterUploadStreamRepairPost(bytes.NewReader(b), remoteFile.SiaPath()); err != nil {
		t.Fatal(err)
	}
}

// testUploadStreaming uploads random data using the upload streaming API.
func testUploadStreaming(t *testing.T, tg *siatest.TestGroup) {
	if len(tg.Renters()) == 0 {
		t.Fatal("Test requires at least 1 renter")
	}
	// Create some random data to write.
	fileSize := fastrand.Intn(2*int(modules.SectorSize)) + siatest.Fuzz() + 2 // between 1 and 2*SectorSize + 3 bytes
	data := fastrand.Bytes(fileSize)
	d := bytes.NewReader(data)

	// Upload the data.
	siaPath, err := modules.NewSiaPath("/foo")
	if err != nil {
		t.Fatal(err)
	}
	r := tg.Renters()[0]
	err = r.RenterUploadStreamPost(d, siaPath, 1, uint64(len(tg.Hosts())-1), false)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the file reached full redundancy.
	err = build.Retry(100, 600*time.Millisecond, func() error {
		rfg, err := r.RenterFileGet(siaPath)
		if err != nil {
			return err
		}
		if rfg.File.Redundancy < float64(len(tg.Hosts())) {
			return fmt.Errorf("expected redundancy %v but was %v",
				len(tg.Hosts()), rfg.File.Redundancy)
		}
		if rfg.File.Filesize != uint64(len(data)) {
			return fmt.Errorf("expected uploaded file to have size %v but was %v",
				len(data), rfg.File.Filesize)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Download the file again.
	_, downloadedData, err := r.RenterDownloadHTTPResponseGet(siaPath, 0, uint64(len(data)), true)
	if err != nil {
		t.Fatal(err)
	}
	// Compare downloaded data to original one.
	if !bytes.Equal([]byte(data), downloadedData) {
		t.Log("originalData:", data)
		t.Log("downloadedData:", downloadedData)
		t.Fatal("Downloaded data doesn't match uploaded data")
	}
}

// testUploadStreamingWithBadDeps uploads random data using the upload streaming
// API, depending on a disrupt to cause a failure. This is a regression test
// that would have caused a production build panic.
func testUploadStreamingWithBadDeps(t *testing.T, tg *siatest.TestGroup) {
	// Create a custom renter with a dependency and remove it after the test is
	// done.
	renterParams := node.Renter(filepath.Join(renterTestDir(t.Name()), "renter"))
	renterParams.RenterDeps = &dependencies.DependencyFailUploadStreamFromReader{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]
	defer tg.RemoveNode(renter)

	// Create some random data to write.
	fileSize := fastrand.Intn(2*int(modules.SectorSize)) + siatest.Fuzz() + 2 // between 1 and 2*SectorSize + 3 bytes
	data := fastrand.Bytes(fileSize)
	d := bytes.NewReader(data)

	// Upload the data.
	siaPath, err := modules.NewSiaPath("/foo")
	if err != nil {
		t.Fatal(err)
	}
	r := tg.Renters()[0]
	err = r.RenterUploadStreamPost(d, siaPath, 1, uint64(len(tg.Hosts())-1), false)
	if err == nil {
		t.Fatal("dependency injection should have caused the upload to fail")
	}
}
