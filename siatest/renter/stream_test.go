package renter

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/siatest"

	"gitlab.com/NebulousLabs/fastrand"
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
	downloadedData, err := renter.DownloadByStream(remoteFile)
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
