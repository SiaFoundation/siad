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
var uploadMap = make(map[string]time.Time)
var uploadMapLock sync.Mutex
var createSpeeds, uploadSpeeds, downloadSpeeds []time.Duration

// main overview
//
// The purpose of this function is to the sia network by uploading a large
// number of files and data. This functions uses the client package to access
// the API and ushers a number of timed tests
func main() {
	// Creating client for renter module
	opts, err := client.DefaultOptions()
	check(err)
	opts.Address = "localhost:9980"
	client := client.New(opts)

	var wg sync.WaitGroup
	c := make(chan struct{})

	// File Creation variables
	size := 200e9                                                         // 200GB
	chunk := 200e6                                                        // 200MB
	remainingData := size * 50                                            // 50 is number of files to be uploaded to get 10TB
	var file string                                                       // initializing file variable
	home, err := os.UserHomeDir()                                         // user home dir
	workDir := filepath.Join(home, "nebulous/sia-upload-download-script") // script's working directory
	filesDir := filepath.Join(workDir, "files")                           // path to directory where created files will be stored
	downloadsDir := filepath.Join(workDir, "downloads")                   // path to the directory where downloaded files will be stored

	//xxx for dev
	size = 200e6
	chunk = 200e3
	remainingData = size * 3

	// File upload variables
	var dataPieces, parityPieces uint64 = 10, 20
	// redundancy := (dataPieces + parityPieces) / dataPieces
	redundancy := 2.5

	// Initializing logs
	os.MkdirAll(workDir, os.ModePerm)
	logFilename := "files.log"
	f, err := os.Create(filepath.Join(workDir, logFilename))
	check(err)
	defer f.Close()
	w := bufio.NewWriter(f)

	// Setting default allowance is allowance is not set
	_, err = fmt.Fprintf(w, "*********************************************.\n")
	check(err)
	_, err = fmt.Fprintf(w, "CHECKING ALLOWANCE.\n\n")
	check(err)
	rg, err := client.RenterGet()
	allowanceIsSet := rg.Settings.Allowance.Active()
	if !allowanceIsSet {
		_, err = fmt.Fprintf(w, "SETTING DEFAULT ALLOWANCE.\n\n")
		check(err)
		err = client.RenterPostAllowance(modules.DefaultAllowance)
		check(err)
	}

	// Waiting for upload ready
	_, err = fmt.Fprintf(w, "*********************************************.\n")
	check(err)
	_, err = fmt.Fprintf(w, "WAITING READY TO UPLOAD.\n\n")
	check(err)
	w.Flush()

	start := time.Now()
	for {
		rur, e := client.RenterUploadReadyGet(dataPieces, parityPieces)
		check(e)
		if rur.Ready {
			elapse := time.Now().Sub(start)
			_, err = fmt.Fprintf(w, "It took %s for renter to be ready to upload.\n", elapse)
			check(err)
			w.Flush()
			break
		}
		time.Sleep(1 * time.Second)
	}
	_, err = fmt.Fprintf(w, "*********************************************.\n")
	check(err)
	w.Flush()

	// Starting Tracking thread
	wg.Add(1)
	os.MkdirAll(filesDir, os.ModePerm)
	go checkRedundancy(client, &wg, c, redundancy, w, filesDir)

	// Repeating file creation, upload, and deletion cycle
	_, err = fmt.Fprintf(w, "Attempting to upload %6.2fTB of data in %2.0f %6.2fGB files.\n", remainingData/1e12, remainingData/size, size/1e9)
	check(err)
	w.Flush()
	i := 0
	for remainingData > 0 {
		// Only uploading one file at a time to get accurate upload speeds
		if len(uploadMap) < 1 {
			file = "Randfile" + strconv.Itoa(i) + "_" + strconv.Itoa(int(size/1e9)) + "GB" + strconv.FormatInt(time.Now().Unix(), 10)
			path := filepath.Join(filesDir, file)
			createFile(path, int(size), int(chunk), w)

			upload(client, path, dataPieces, parityPieces, w)

			remainingData = remainingData - size
			i++
		}

		// Uploading is the bottleneck, delaying this for loop as it does not impact the metrics we are looking at
		time.Sleep(10 * time.Second)
	}

	close(c)
	w.Flush()
	wg.Wait()

	_, err = fmt.Fprintf(w, "*********************************************.\n")
	check(err)
	_, err = fmt.Fprintf(w, "UPLOADS COMPLETE.\n")
	check(err)
	avgCreateSpeed := average(createSpeeds)
	avgUploadSpeed := average(uploadSpeeds)

	// Call download files
	rf, err := client.RenterFilesGet(false)
	check(err)
	_, err = fmt.Fprintf(w, "The average time to create a %6.2fGB file was %s for an average write speed of %fMB/s \n", size/1e9, avgCreateSpeed, size/float64(avgCreateSpeed)*1e3)
	check(err)
	_, err = fmt.Fprintf(w, "The average time to upload a %6.2fGB file was %s for an average upload speed of %fMB/s\n", size/1e9, avgUploadSpeed, size/float64(avgUploadSpeed)*1e3)
	check(err)

	_, err = fmt.Fprintf(w, "*********************************************.\n")
	check(err)
	_, err = fmt.Fprintf(w, "BEGINNING DOWNLOADS.\n")
	check(err)

	w.Flush()

	os.MkdirAll(downloadsDir, os.ModePerm)
	for _, fi := range rf.Files {
		// calling download not async so it will be blocking
		downloadFile(client, fi.SiaPath, downloadsDir, false, w, uint64(size))

		// delete downloaded file
		filename := filepath.Base(fi.SiaPath.Path)
		path := filepath.Join(downloadsDir, filename)
		deleteLocalFile(path, w)
	}

	_, err = fmt.Fprintf(w, "*********************************************.\n")
	check(err)
	_, err = fmt.Fprintf(w, "DOWNLOADS COMPLETE\n")
	avgDownloadSpeed := average(downloadSpeeds)
	_, err = fmt.Fprintf(w, "The average time to download a %6.2fGB file was %s for an average download speed of %fMB/s\n", size/1e9, avgDownloadSpeed, size/float64(avgDownloadSpeed)*1e3)
	check(err)
	w.Flush()
}

// average calculates the average duration for a set of time durations
func average(times []time.Duration) time.Duration {
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
		_, file, line, _ := runtime.Caller(0)
		fmt.Printf("%s:%d failed check(err)\n", filepath.Base(file), line)
		fmt.Println(e)
		os.Exit(1)
	}
}

// createFile creates a local file on disk
func createFile(path string, size int, chunk int, w *bufio.Writer) {
	// Start Time
	startTime := time.Now()

	// Open a file for writing.
	f, err := os.Create(path)
	check(err)
	defer f.Close()

	// create data for file (fastrand)
	for i := 0; i < size/chunk; i++ {
		data := fastrand.Bytes(chunk)

		// Write data to file.
		_, err := f.Write(data)
		check(err)
	}

	// End time
	endTime := time.Now()
	elapse := endTime.Sub(startTime)
	createSpeeds = append(createSpeeds, elapse)

	_, file, line, _ := runtime.Caller(0)
	_, err = fmt.Fprintf(w, "%s:%d Created a %4.2fGB file, %s, in %s\n", filepath.Base(file), line, float64(size)/1e9, path, elapse)
	check(err)
	w.Flush()

	// Flush writes to stable storage.
	f.Sync()
}

// deleteLocalFile deletes a local file on disk
func deleteLocalFile(path string, w *bufio.Writer) {
	err := os.RemoveAll(path)
	check(err)
	_, file, line, _ := runtime.Caller(0)
	_, err = fmt.Fprintf(w, "%s:%d %s deleted.\n", filepath.Base(file), line, path)
	w.Flush()
}

// Upload uses the node to upload the file.
func upload(c *client.Client, path string, dataPieces, parityPieces uint64, w *bufio.Writer) {
	// Get absolute file path for RenterUploadPost
	abs, err := filepath.Abs(path)
	check(err)

	// Submit upload
	siaFolder := "upload-download-script"
	siaPath, err := modules.NewSiaPath(filepath.Join(siaFolder, filepath.Base(path)))
	check(err)
	// err = c.RenterUploadPost(abs, filepath.Join("/", filepath.Base(path)), dataPieces, parityPieces)
	err = c.RenterUploadPost(abs, siaPath, dataPieces, parityPieces)
	check(err)

	// Add to uploadMap
	uploadMapLock.Lock()
	//xxx uploadMap[filepath.Base(path)] = time.Now()
	uploadMap[filepath.Join(siaFolder, filepath.Base(path))] = time.Now()
	uploadMapLock.Unlock()
}

// checkRedundancy pings the files API endpoint to see if a file has been fully
// uploaded
func checkRedundancy(client *client.Client, wg *sync.WaitGroup, c chan struct{}, r float64, w *bufio.Writer, dir string) {
	for {
		uploadMapLock.Lock()
		select {
		case <-c:
			// stop
			if len(uploadMap) == 0 {
				wg.Done()
				uploadMapLock.Unlock()
				return
			}
		default:
			// continue
		}
		rf, err := client.RenterFilesGet(false)
		check(err)
		for _, fi := range rf.Files {
			if _, ok := uploadMap[fi.SiaPath.Path]; ok && fi.Redundancy >= r {
				// Deleting local copy of file
				filename := filepath.Base(fi.SiaPath.Path)
				path := filepath.Join(dir, filename)
				deleteLocalFile(path, w)

				// Max redundancy reached, calucalating elapsed time
				elapse := time.Now().Sub(uploadMap[fi.SiaPath.Path])
				uploadSpeeds = append(uploadSpeeds, elapse)

				// logging result
				_, err := fmt.Fprintf(w, "File: %s uploaded in: %s\n", filepath.Base(fi.SiaPath.Path), elapse)
				check(err)
				w.Flush()

				// Remove from map
				delete(uploadMap, fi.SiaPath.Path)
			}
		}
		uploadMapLock.Unlock()
		time.Sleep(1 * time.Second)
	}
}

// downloadFile uses the download API endpoint to download a file from the Sia
// network
func downloadFile(c *client.Client, siaPath modules.SiaPath, destination string, async bool, w *bufio.Writer, size uint64) {
	abs, err := filepath.Abs(filepath.Join(destination, siaPath.Path))
	check(err)

	start := time.Now()

	_, err = c.RenterDownloadGet(siaPath, abs, 0, size, false, true)
	check(err)

	elapse := time.Now().Sub(start)
	downloadSpeeds = append(downloadSpeeds, elapse)

	// logging result
	_, err = fmt.Fprintf(w, "File: %s downloaded in: %s\n", filepath.Base(siaPath.Path), elapse)
	check(err)
	w.Flush()
}
