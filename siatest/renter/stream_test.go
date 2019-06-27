package renter

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/siatest"
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
