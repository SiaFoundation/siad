package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/fastrand"
)

// Map for tracking file uploads
var uploadMap = make(map[modules.SiaPath]time.Time)
var uploadMapLock sync.Mutex
var uploadSpeeds, downloadSpeeds []time.Duration

// main overview
//
// The purpose of this function is to the sia network by uploading a large
// number of files and data. This functions uses the client package to access
// the API and ushers a number of timed tests
func main() {
	// Create client with default options
	opts, err := client.DefaultOptions()
	check(err)
	opts.Address = "localhost:9980"
	client := client.New(opts)

	var wg sync.WaitGroup
	c := make(chan struct{})

	// File Creation variables
	size := 200e6                                                         // 200MB
	remainingData := size * 50                                            // 50 is number of files to be uploaded to get 10GB
	var file string                                                       // initializing file variable
	home, err := os.UserHomeDir()                                         // user home dir
	workDir := filepath.Join(home, "nebulous/sia-upload-download-script") // script's working directory
	filesDir := filepath.Join(workDir, "files")                           // path to directory where created files will be stored
	downloadsDir := filepath.Join(workDir, "downloads")                   // path to the directory where downloaded files will be stored
	siaDir := "upload-download-script"                                    // folder in Sia to upload files to

	//xxx for dev
	remainingData = size * 3

	// Initializing logs
	os.MkdirAll(workDir, os.ModePerm)
	logFilename := "files.log"
	f, err := os.Create(filepath.Join(workDir, logFilename))
	check(err)
	defer f.Close()
	w := bufio.NewWriter(f)

	// Setting default allowance is allowance is not set
	_, err = fmt.Fprintln(w, "== CHECKING ALLOWANCE")
	check(err)
	rg, err := client.RenterGet()
	if !rg.Settings.Allowance.Active() {
		_, err = fmt.Fprintln(w, "== SETTING DEFAULT ALLOWANCE")
		check(err)
		err = client.RenterPostAllowance(modules.DefaultAllowance)
		check(err)
	}

	// Waiting for upload ready
	_, err = fmt.Fprintln(w, "== WAITING READY TO UPLOAD")
	check(err)
	w.Flush()
	start := time.Now()
	for {
		rur, e := client.RenterUploadReadyDefaultGet()
		check(e)
		if rur.Ready {
			break
		}
		time.Sleep(1 * time.Second)
	}
	elapse := time.Now().Sub(start)
	_, err = fmt.Fprintf(w, "It took %s for renter to be ready to upload.\n", elapse)
	check(err)
	w.Flush()

	// Starting Tracking thread
	wg.Add(1)
	os.MkdirAll(filesDir, os.ModePerm)
	go threadedDeleteLocalFiles(client, &wg, c, w, filesDir)

	// Repeating file creation, upload, and deletion cycle
	// TODO - move this to a threaded function so that we can be downloading in
	// parallel
	_, err = fmt.Fprintf(w, "Attempting to upload %6.2fGB of data in %2.0f %6.2fMB files.\n", remainingData/1e9, remainingData/size, size/1e6)
	check(err)
	w.Flush()
	i := 0
	for remainingData > 0 {
		// Only uploading one file at a time to get accurate upload speeds
		if len(uploadMap) < 1 {
			file = "Randfile" + strconv.Itoa(i) + "_" + strconv.Itoa(int(size/1e9)) + "GB" + strconv.FormatInt(time.Now().Unix(), 10)
			path := filepath.Join(filesDir, file)
			createFile(path, int(size), w)

			upload(client, siaDir, path, w)

			remainingData = remainingData - size
			i++
		}

		// Uploading is the bottleneck, delaying this for loop as it does not impact the metrics we are looking at
		time.Sleep(10 * time.Second)
	}

	close(c)
	w.Flush()
	wg.Wait()

	_, err = fmt.Fprintln(w, "== UPLOADS COMPLETE")
	check(err)
	avgUploadSpeed := average(uploadSpeeds)

	_, err = fmt.Fprintf(w, "The average time to upload a %6.2fMB file was %s for an average upload speed of %fMB/s\n", size/1e6, avgUploadSpeed, size/float64(avgUploadSpeed)*1e3)
	check(err)
	// End of code that should be included in threaded upload function

	// Call download files
	// TODO - move to threaded download loop
	_, err = fmt.Fprintln(w, "== BEGINNING DOWNLOADS")
	check(err)
	w.Flush()

	// Get all files
	rf, err := client.RenterFilesGet(false)
	check(err)

	// TODO - this will be updated to trying to download files as soon as they are
	// available. Will need a map to track which files have been downloaded to
	// ensure we hit all the files by the end of the test
	fullDownloadsDir := filepath.Join(downloadsDir, siaDir)
	os.MkdirAll(fullDownloadsDir, os.ModePerm)
	for _, fi := range rf.Files {
		// calling download not async so it will be blocking
		downloadFile(client, fi.SiaPath, downloadsDir, w)

		// delete downloaded file
		filename := filepath.Base(fi.SiaPath.Path)
		path := filepath.Join(downloadsDir, filename)
		deleteLocalFile(path, w)
	}

	_, err = fmt.Fprintln(w, "== DOWNLOADS COMPLETE")
	check(err)
	avgDownloadSpeed := average(downloadSpeeds)
	_, err = fmt.Fprintf(w, "The average time to download a %6.2fMB file was %s for an average download speed of %fMB/s\n", size/1e6, avgDownloadSpeed, size/float64(avgDownloadSpeed)*1e3)
	check(err)
	w.Flush()
}

// average calculates the average duration for a set of time durations
func average(times []time.Duration) time.Duration {
	if len(times) == 0 {
		panic("no times submitted to average function")
	}

	var total time.Duration
	for _, t := range times {
		total += t
	}
	return total / time.Duration(len(times))
}

// check logs an error the log file and then causes the program to exit if there
// is an error
func check(e error) {
	if e != nil {
		_, file, line, _ := runtime.Caller(1)
		fmt.Fprintf(os.Stderr, "%s:%d failed check(err)\n", filepath.Base(file), line)
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

// createFile creates a local file on disk
func createFile(path string, size int, w *bufio.Writer) {
	// Open a file for writing.
	f, err := os.Create(path)
	check(err)
	defer f.Close()

	// create data for file (fastrand)
	data := fastrand.Bytes(size)

	// Write data to file.  and Flush writes to stable storage.
	_, err = f.Write(data)
	check(err)
	f.Sync()
}

// deleteLocalFile deletes a local file on disk
func deleteLocalFile(path string, w *bufio.Writer) {
	err := os.RemoveAll(path)
	check(err)
	_, file, line, _ := runtime.Caller(0)
	_, err = fmt.Fprintf(w, "%s:%d %s deleted.\n", filepath.Base(file), line, path)
	check(err)
	w.Flush()
}

// Upload uses the node to upload the file.
func upload(c *client.Client, siaFolder string, path string, w *bufio.Writer) {
	// Get absolute file path for RenterUploadPost
	abs, err := filepath.Abs(path)
	check(err)

	// Submit upload
	siaPath, err := modules.NewSiaPath(filepath.Join(siaFolder, filepath.Base(path)))
	check(err)
	err = c.RenterUploadDefaultPost(abs, siaPath)
	check(err)

	// Add to uploadMap
	uploadMapLock.Lock()
	uploadMap[siaPath] = time.Now()
	uploadMapLock.Unlock()
}

// threadedDeleteLocalFiles pings the files API endpoint and removes the local
// file from disk if a file has been fully uploaded
func threadedDeleteLocalFiles(client *client.Client, wg *sync.WaitGroup, c chan struct{}, w *bufio.Writer, dir string) {
	for {
		uploadMapLock.Lock()
		uploadMapLen := len(uploadMap)
		uploadMapLock.Unlock()
		select {
		case <-c:
			// stop
			// TODO - this seems like it will unnecessarily block the program from
			// stopping
			if uploadMapLen == 0 {
				wg.Done()
				return
			}
		default:
			// continue
		}

		rf, err := client.RenterFilesGet(false)
		check(err)
		uploadMapLock.Lock()
		for _, fi := range rf.Files {
			_, ok := uploadMap[fi.SiaPath]
			if !ok || fi.MaxHealthPercent != 100 {
				continue
			}

			// Deleting local copy of file
			filename := filepath.Base(fi.SiaPath.Path)
			path := filepath.Join(dir, filename)
			deleteLocalFile(path, w)

			// Max redundancy reached, calucalating elapsed time
			elapse := time.Now().Sub(uploadMap[fi.SiaPath])
			uploadSpeeds = append(uploadSpeeds, elapse)

			// logging result
			_, err := fmt.Fprintf(w, "File: %s uploaded in: %s\n", filepath.Base(fi.SiaPath.Path), elapse)
			check(err)
			w.Flush()

			// Remove from map
			delete(uploadMap, fi.SiaPath)
		}
		uploadMapLock.Unlock()
		time.Sleep(1 * time.Second)
	}
}

// downloadFile uses the download API endpoint to download a file from the Sia
// network
func downloadFile(c *client.Client, siaPath modules.SiaPath, destination string, w *bufio.Writer) {
	abs, err := filepath.Abs(filepath.Join(destination, siaPath.Path))
	check(err)

	start := time.Now()

	_, err = c.RenterDownloadFullGet(siaPath, abs, false)
	check(err)

	elapse := time.Now().Sub(start)
	downloadSpeeds = append(downloadSpeeds, elapse)

	// logging result
	_, err = fmt.Fprintf(w, "File: %s downloaded in: %s\n", filepath.Base(siaPath.Path), elapse)
	check(err)
	w.Flush()
}
