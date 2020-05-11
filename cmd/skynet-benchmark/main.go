package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/node/api/client"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// The SiaPath that will be used by the program to upload and store all of
	// the files when performing test downloads.
	testSiaDir = "var/skynet-benchmark"

	// A range of files of different sizes.
	dir64kb = "64kb"
	dir1mb  = "1mb"
	dir4mb  = "4mb"
	dir10mb = "10mb"

	// The exact sizes of each file. This size is chosen so that when the
	// metadata is added to the file, and then the filesize is converted to a
	// fetch size, the final fetch size is as close as possible to the filesize
	// of the dir without going over.
	exactSize64kb = 61e3
	exactSize1mb  = 982e3
	exactSize4mb  = 3931e3
	exactSize10mb = 10e6 // Once over 4 MB, fetch size doesn't matter, can use exact sizes.

	// Fetch size is the largest fetch size that can be set using the skylink
	// naming standard without exceeding the filesize.
	fetchSize64kb = 61440
	fetchSize1mb  = 983040
	fetchSize4mb  = 3932160
	fetchSize10mb = 10e6 // Once over 4 MB, fetch size doesn't matter, can use exact sizes.

	// The total number of files of each size that we download during testing.
	filesPerDir = 200
)

var (
	c *client.Client
)

func main() {
	fmt.Printf("Skynet performance analysis tool.\n\n")

	// Determine which port to use when talking to siad.
	args := os.Args
	var addr string
	if len(args) == 1 {
		addr = "localhost:9980"
	} else if len(args) == 2 {
		// Parse port.
		num, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Println("Error parsing port:", err)
		}
		if num > 65535 {
			fmt.Println("Invalid port number")
		}
		addr = "localhost:" + args[1]
	} else if len(args) > 2 {
		fmt.Println("Usage: ./skynet-benchmark [optional: port for siad api]")
		return
	}

	// Create the client that will be used to talk to siad.
	opts, err := client.DefaultOptions()
	if err != nil {
		fmt.Println("Unable to get Sia client options:", err)
		return
	}
	opts.Address = addr
	c = client.New(opts)

	// Establish the directories that we will be using for testing.
	dirBasePath, err := modules.NewSiaPath(testSiaDir)
	if err != nil {
		fmt.Println("Could not create siapath for testing directory:", err)
		return
	}
	dir64kbPath, err := dirBasePath.Join(dir64kb)
	if err != nil {
		fmt.Println("Could not create 64kb siapath for testing directory:", err)
		return
	}
	dir1mbPath, err := dirBasePath.Join(dir1mb)
	if err != nil {
		fmt.Println("Could not create 1mb siapath for testing directory:", err)
		return
	}
	dir4mbPath, err := dirBasePath.Join(dir4mb)
	if err != nil {
		fmt.Println("Could not create 4mb siapath for testing directory:", err)
		return
	}
	dir10mbPath, err := dirBasePath.Join(dir10mb)
	if err != nil {
		fmt.Println("Could not create 10mb siapath for testing directory:", err)
		return
	}

	// Upload the files for each dir. The filesize used is slightly smaller than
	// the expected filesize to leave room for metadata overhead. The expected
	// filesize used is the largest filesize that fits inside of the file limits
	// for the metrics collector.
	err = uploadFileSet(dir64kbPath, exactSize64kb, fetchSize64kb)
	if err != nil {
		fmt.Println("Unable to upload 64kb files:", err)
		return
	}
	fmt.Println("64kb files are ready to go.")
	err = uploadFileSet(dir1mbPath, exactSize1mb, fetchSize1mb)
	if err != nil {
		fmt.Println("Unable to upload 1mb files:", err)
		return
	}
	fmt.Println("1mb files are ready to go.")
	err = uploadFileSet(dir4mbPath, exactSize4mb, fetchSize4mb)
	if err != nil {
		fmt.Println("Unable to upload 4mb files:", err)
		return
	}
	fmt.Println("4mb files are ready to go.")
	err = uploadFileSet(dir10mbPath, exactSize10mb, fetchSize10mb)
	if err != nil {
		fmt.Println("Unable to upload 10mb files:", err)
		return
	}
	fmt.Printf("10mb files are ready to go.\n\n")

	fmt.Printf("Beginning download testing. Each test is %v files\n\n", filesPerDir)

	// Download all of the 64kb files.
	threadss := []uint64{1, 4, 16, 64} // threadss is the plural of threads
	downloadStart := time.Now()
	for _, threads := range threadss {
		err = downloadFileSet(dir64kbPath, exactSize64kb, threads)
		if err != nil {
			fmt.Println("Unable to download all 64kb files:", err)
		}
		fmt.Printf("64kb downloads on %v threads finished in %v\n", threads, time.Since(downloadStart))
		downloadStart = time.Now()
		err = downloadFileSet(dir1mbPath, exactSize1mb, threads)
		if err != nil {
			fmt.Println("Unable to download all 1mb files:", err)
		}
		fmt.Printf("1mb downloads on %v threads finished in %v\n", threads, time.Since(downloadStart))
		downloadStart = time.Now()
		err = downloadFileSet(dir4mbPath, exactSize4mb, threads)
		if err != nil {
			fmt.Println("Unable to download all 4mb files:", err)
		}
		fmt.Printf("4mb downloads on %v threads finished in %v\n", threads, time.Since(downloadStart))
		downloadStart = time.Now()
		err = downloadFileSet(dir10mbPath, exactSize10mb, threads)
		if err != nil {
			fmt.Println("Unable to download all 10mb files:", err)
		}
		fmt.Printf("10mb downloads on %v threads finished in %v\n", threads, time.Since(downloadStart))
		downloadStart = time.Now()
		fmt.Println()
	}
}

// downloadFileSet will download all of the files of the expected fetch size in
// a dir.
func downloadFileSet(dir modules.SiaPath, fileSize int, threads uint64) error {
	// Create a thread pool and fill it. Need to grab a struct from the pool
	// before launching a thread, need to drop the object back into the pool
	// when done.
	threadPool := make(chan struct{}, threads)
	for i := uint64(0); i < threads; i++ {
		threadPool <- struct{}{}
	}

	// Loop over every file. Block until there is an object ready in the thread
	// pool, then launch a thread.
	var atomicDownloadErrors uint64
	var wg sync.WaitGroup
	for i := 0; i < filesPerDir; i++ {
		// Get permission to launch a thread.
		<-threadPool

		// Launch the downloading thread.
		wg.Add(1)
		go func(i int) {
			// Make room for the next thread.
			defer func() {
				threadPool <- struct{}{}
			}()
			// Clear the wait group.
			defer wg.Done()

			// Figure out the siapath of the dir.
			siaPath, err := dir.Join(strconv.Itoa(i))
			if err != nil {
				fmt.Println("Dir error:", err)
				atomic.AddUint64(&atomicDownloadErrors, 1)
				return
			}
			// Figure out the skylink for the file.
			rf, err := c.RenterFileRootGet(siaPath)
			if err != nil {
				fmt.Println("Error getting file info:", err)
				atomic.AddUint64(&atomicDownloadErrors, 1)
				return
			}
			// Get a reader / stream for the download.
			reader, err := c.SkynetSkylinkReaderGet(rf.File.Skylinks[0])
			if err != nil {
				fmt.Println("Error getting skylink reader:", err)
				atomic.AddUint64(&atomicDownloadErrors, 1)
				return
			}

			// Download and discard the result, we only care about the speeds,
			// not the data.
			data, err := ioutil.ReadAll(reader)
			if err != nil {
				fmt.Printf("Error performing download, only got %v bytes: %v\n", len(data), err)
				atomic.AddUint64(&atomicDownloadErrors, 1)
				return
			}
			if len(data) != fileSize {
				fmt.Printf("Error performing download, got %v bytes when expecting %v\n", len(data), fileSize)
				atomic.AddUint64(&atomicDownloadErrors, 1)
				return
			}
		}(i)
	}
	wg.Wait()

	// Don't need to use atomics, all threads have returned.
	if atomicDownloadErrors != 0 {
		return fmt.Errorf("there were %v errors while downloading", atomicDownloadErrors)
	}
	return nil
}

// getMissingFiles will fetch a map of all the files that are missing or don't
// have skylinks
func getMissingFiles(dir modules.SiaPath, expectedFileSize uint64, expectedFetchSize uint64) (map[int]struct{}, error) {
	// Determine whether the dirs already exist and have files in them for
	// downloading.
	rdg, err := c.RenterDirRootGet(dir)
	if err != nil {
		// If the error is something other than a DNE, abort.
		if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
			return nil, errors.AddContext(err, "could not fetch dir for missing files")
		}
	}

	missingFiles := make(map[int]struct{})
	for i := 0; i < filesPerDir; i++ {
		missingFiles[i] = struct{}{}
	}
	// Loop through the files we have.
	for _, file := range rdg.Files {
		// Check the files that are the right size.
		if !file.Available {
			continue
		}
		if len(file.Skylinks) != 1 {
			continue
		}
		var sl modules.Skylink
		err := sl.LoadString(file.Skylinks[0])
		if err != nil {
			return nil, errors.AddContext(err, "error parsing skylink in testing dir")
		}
		_, fetchSize, err := sl.OffsetAndFetchSize()
		if err != nil {
			return nil, errors.AddContext(err, "error parsing skylink offset and fetch size in testing dir")
		}
		if expectedFetchSize < 4100e3 && fetchSize != expectedFetchSize {
			continue
		} else if fetchSize >= 4100e3 && file.Filesize != expectedFetchSize {
			continue
		}
		cleanName := strings.TrimSuffix(file.SiaPath.Name(), "-extended")
		num, err := strconv.Atoi(cleanName)
		if err != nil {
			continue
		}
		delete(missingFiles, num)
	}
	return missingFiles, nil
}

// uploadFileSet will upload a set of files for testing, skipping over any files
// that already exist.
func uploadFileSet(dir modules.SiaPath, fileSize uint64, expectedFetchSize uint64) error {
	missingFiles, err := getMissingFiles(dir, fileSize, expectedFetchSize)
	if err != nil {
		return errors.AddContext(err, "error assembling set of missing files")
	}
	if len(missingFiles) != 0 {
		fmt.Printf("There are %v missing %v files, uploading now.\n", len(missingFiles), fileSize)
	}

	// Upload files until there are enough.
	for i := range missingFiles {
		// Get the siapath for the file.
		sp, err := dir.Join(strconv.Itoa(i))
		if err != nil {
			return errors.AddContext(err, "error creating filename")
		}
		buf := bytes.NewReader(fastrand.Bytes(int(fileSize)))
		// Fill out the upload parameters.
		sup := modules.SkyfileUploadParameters{
			SiaPath: sp,
			Root:    true,
			Force:   true, // This will overwrite other files in the dir.

			FileMetadata: modules.SkyfileMetadata{
				Filename: strconv.Itoa(i) + ".rand",
				Mode:     modules.DefaultFilePerm,
			},

			Reader: buf,
		}
		// Upload the file.
		_, _, err = c.SkynetSkyfilePost(sup)
		if err != nil {
			return errors.AddContext(err, "error when attempting to upload new file")
		}
	}

	missingFiles, err = getMissingFiles(dir, fileSize, expectedFetchSize)
	if err != nil {
		return errors.AddContext(err, "error assembling set of missing files")
	}
	if len(missingFiles) > 0 {
		fmt.Println("Failed to upload all necessary files:", len(missingFiles), "did not complete")
		return errors.New("Upload appears unsuccessful")
	}
	return nil
}
