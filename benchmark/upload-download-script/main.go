package main

import (
	"fmt"
	"io"
	"math/rand"
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

// Config
const (
	// Directories and log
	workDirPart      = "nebulous/sia-upload-download-script" // Working directory under user home
	uploadsDirPart   = "uploads"                             // Uploads in working directory
	downloadsDirPart = "downloads"                           // Downloads in working directory
	logFilename      = "files.log"                           // Log filename in working directory
	siaDir           = "upload-download-script"              // Sia directory to upload files to

	// Uploads and downloads
	nFiles              = 50    // Number of files to upload
	fileSize            = 200e6 // File size of a file to be uploaded in bytes
	maxConcurrUploads   = 10    // Max number of files to be concurrently uploaded
	maxConcurrDownloads = 10    // Max number of files to be concurrently downloaded
	nTotalDownloads     = 500   // Total number of file downloads. There will be nFiles, a single file can be downloaded x-times

	// siad
	siadPort      = 9980 // Port of siad node
	minRedundancy = 2.0  // Minimum redundancy to consider a file uploaded
)

var (
	w       = io.Writer(os.Stdout) // Multi writer to stdout and to file after initialization
	workDir string                 // Working directory
	upDir   string                 // Uploads working directory
	downDir string                 // Downloads working directory
	logPath string                 // Path to log file

	c *client.Client = &client.Client{} // Sia client for uploads and downloads

	wg sync.WaitGroup // Wait group to wait for all goroutines to finish

	upChan   = make(chan struct{}, maxConcurrUploads)   // Channel with a buffer to limit number of max concurrent uploads
	downChan = make(chan struct{}, maxConcurrDownloads) // Channel with a buffer to limit number of max concurrent downloads

	createTimes   []time.Duration // Slice of file creation durations
	uploadTimes   []time.Duration // Slice of file upload durations
	downloadTimes []time.Duration // Slice of file download durations
)

// files is a map of files with its data
type files struct {
	m   map[string]fileData
	mux sync.Mutex
}

// fileData stores download data for a specific file
type fileData struct {
	downloading bool
	nDownloads  int
}

// This script concurrently uploads and then downloads files. Before execution
// be sure to have enough storage for concurrent upload and download files.
func main() {
	var files = files{
		m: make(map[string]fileData),
	}

	// Init, create, clean dirs
	initDirs()

	// Init log to file
	f := initLog()
	defer f.Close()

	// Log dirs, files
	printLog("Working   dir was set to:", workDir)
	printLog("Uploads   dir was set to:", upDir)
	printLog("Downloads dir was set to:", downDir)
	printLog("Logs are stored in:      ", logPath)
	printLog()

	// Print overview
	totalData := formatFileSize(nFiles*int(fileSize), " ")
	fileSizeStr := formatFileSize(fileSize, " ")
	msg := fmt.Sprintf("Upload total of %s data in %d files per %s files", totalData, nFiles, fileSizeStr)
	printLog(msg)

	totalData = formatFileSize(nTotalDownloads*int(fileSize), " ")
	msg = fmt.Sprintf("Download total of %s data in %d downloads per %s files", totalData, nTotalDownloads, fileSizeStr)
	printLog(msg)
	printLog()

	// Init download and upload clients
	printLog("=== Init client")
	initClient(siadPort)

	// Set allowance
	printLog("=== Checking allowance")
	setAllowance()

	// Wait for renter to be upload ready
	printLog("=== Wait for renter to be ready to upload")
	waitForRenterIsUploadReady()

	// Upload files
	printLog("=== Upload files concurrently")
	for i := 0; i < nFiles; i++ {
		wg.Add(1)
		go createAndUploadFile(&files, i)
	}

	// Wait for all uploads to finish to avoid "not enough workers" error
	wg.Wait()

	// Download files
	printLog("=== Download files concurrently")
	for i := 0; i < nTotalDownloads; i++ {
		wg.Add(1)
		go downloadFile(&files, i)
	}

	// Wait for all downloads finish
	wg.Wait()
	printLog("=== Done")
}

// initDirs sets working, uploads and downloads directory paths from config
// and cleans (deletes and creates) them
func initDirs() {
	home, err := os.UserHomeDir()
	check(err)

	// Set dir paths
	workDir = filepath.Join(home, workDirPart)
	upDir = filepath.Join(workDir, uploadsDirPart)
	downDir = filepath.Join(workDir, downloadsDirPart)

	// Delete dirs
	err = os.RemoveAll(upDir)
	check(err)
	err = os.RemoveAll(downDir)
	check(err)

	// Create dirs
	err = os.MkdirAll(upDir, os.ModePerm)
	check(err)
	err = os.MkdirAll(downDir, os.ModePerm)
	check(err)
}

// initLog initializes logging to the file
func initLog() *os.File {
	logPath = filepath.Join(workDir, logFilename)
	f, err := os.Create(logPath)
	check(err)
	w = io.MultiWriter(os.Stdout, f)
	return f
}

// check logs error to our print log
func check(e error) {
	// No error
	if e == nil {
		return
	}

	// Get error location in the file
	_, file, line, _ := runtime.Caller(1)

	// Log error
	msg := fmt.Sprintf("%s:%d failed check(err)\n", filepath.Base(file), line)
	printLog(msg)
	printLog(e)
	os.Exit(1)
}

// printLog logs message to stdout and to the given writer
func printLog(ss ...interface{}) {
	msg := fmt.Sprint(ss...)
	_, err := fmt.Fprintln(w, msg)
	if err != nil {
		fmt.Println("Error writing to log:", err)
	}
}

// createAndUploadFile creates a local file and uploads it to Sia
func createAndUploadFile(files *files, i int) {
	// Occupy one spot in the uploads channel buffer to limit concurrent uploads
	upChan <- struct{}{}

	// Create a local file to upload
	f := createFile(files, i)

	// Upload a file to Sia
	uploadFile(files, f, i)

	// Delete local file
	filePath := filepath.Join(upDir, f)
	deleteLocalFile(filePath)

	// Free one spot in the uploads channel buffer
	_ = <-upChan

	wg.Done()
}

// createFile creates a file in the upload directory with te given size in bytes
func createFile(files *files, i int) string {
	start := time.Now()

	// Preapare file name parts
	fileIndex := fmt.Sprintf("%03d", i)
	sizeStr := formatFileSize(fileSize, "")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Create filename and full path
	filename := "Randfile" + fileIndex + "_" + sizeStr + "_" + timestamp
	path := filepath.Join(upDir, filename)

	// Log
	msg := fmt.Sprintf("File #%03d, creating file: %s", i, filename)
	printLog(msg)

	// Open file for appending
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	check(err)
	defer f.Close()

	// Append to file per parts (avoids out of memory error on large files)
	remaining := int(fileSize)
	for {
		n := int(1e6)
		if remaining == 0 {
			break
		} else if remaining < n {
			n = remaining
			remaining = 0
		} else {
			remaining -= n
		}

		// Create data string for file (fastrand)
		data := fastrand.Bytes(n)

		// Write data to file and Flush writes to stable storage
		_, err = f.Write(data)
		check(err)
		f.Sync()
	}

	// Log duration
	elapsed := time.Now().Sub(start)
	msg = fmt.Sprintf("File #%03d, created file: %s in: %s", i, filename, elapsed)
	printLog(msg)

	createTimes = append(createTimes, elapsed)

	return filename
}

// uploadFile uploads a file to Sia and adds a file to files map
func uploadFile(filesMap *files, filename string, i int) {
	start := time.Now()

	// Log
	msg := fmt.Sprintf("File #%03d, uploading file: %s", i, filename)
	printLog(msg)

	// Upload file to Sia
	siaPath, err := modules.NewSiaPath(filepath.Join(siaDir, filename))
	check(err)
	localPath := filepath.Join(upDir, filename)
	err = c.RenterUploadDefaultPost(localPath, siaPath)
	check(err)

	// Wait for file upload finished
	for {
		f, err := c.RenterFileGet(siaPath)
		check(err)

		if f.File.Redundancy >= minRedundancy {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	filesMap.mux.Lock()
	defer filesMap.mux.Unlock()
	fd := fileData{}
	filesMap.m[filename] = fd

	// Log duration
	elapsed := time.Now().Sub(start)
	msg = fmt.Sprintf("File #%03d, uploaded file: %s in: %s", i, filename, elapsed)
	printLog(msg)

	uploadTimes = append(uploadTimes, elapsed)
}

// deleteLocalFile deletes a local file, in case of error exits execution via
// check()
func deleteLocalFile(filepath string) {
	printLog("Deleting a local file: ", filepath)
	err := os.Remove(filepath)
	check(err)
}

// downloadFile downloads a file from Sia
func downloadFile(files *files, iDownload int) {
	// Occupy one spot in the downloads channel buffer to limit concurrent uploads
	downChan <- struct{}{}

	start := time.Now()

	// Select a file not yet downloading to be sure to download each file at
	// least once
	var filename string
	files.mux.Lock()
	for fn, fd := range files.m {
		if fd.downloading {
			continue
		}
		filename = fn
	}

	// Select random file if all files are already downloading
	if filename == "" {
		rand.Seed(time.Now().UnixNano())
		fi := rand.Intn(len(files.m))
		i := 0
		for fn := range files.m {
			if i == fi {
				filename = fn
				break
			}
			i++
		}
	}
	files.mux.Unlock()

	msg := fmt.Sprintf("Download #%03d, downloading file: %s", iDownload, filename)
	printLog(msg)

	// Download file
	// Use unique local filename because one file can be downloaded concurrently multiple times
	localFilename := filename + fmt.Sprintf("_#%03d", iDownload)
	localPath := filepath.Join(downDir, localFilename)
	siaPath, err := modules.NewSiaPath(filepath.Join(siaDir, filename))
	check(err)
	_, err = c.RenterDownloadFullGet(siaPath, localPath, false)
	check(err)

	// Delete downloaded file
	deleteLocalFile(localPath)

	// Update files map
	files.mux.Lock()
	nDownloads := files.m[filename].nDownloads
	fd := fileData{
		downloading: true,
		nDownloads:  nDownloads + 1,
	}
	files.m[filename] = fd
	files.mux.Unlock()

	// Log duration
	elapsed := time.Now().Sub(start)
	msg = fmt.Sprintf("Download #%02d, downloaded file %s in %s", iDownload, filename, elapsed)
	printLog(msg)

	downloadTimes = append(downloadTimes, elapsed)

	// Free one spot in the downloads channel buffer
	_ = <-downChan

	wg.Done()
}

// formatFileSize format int value to filesize string
func formatFileSize(size int, separator string) string {
	if size > 1e12 {
		return strconv.Itoa(int(size)/1e12) + separator + "TB"
	} else if size > 1e9 {
		return strconv.Itoa(int(size)/1e9) + separator + "GB"
	} else if size > 1e6 {
		return strconv.Itoa(int(size)/1e6) + separator + "MB"
	} else if size > 1e3 {
		return strconv.Itoa(int(size)/1e3) + separator + "kB"
	} else {
		return strconv.Itoa(size) + separator + "B"
	}
}

// initClient initializes a Sia http client
func initClient(port int) {
	opts, err := client.DefaultOptions()
	check(err)
	opts.Address = "localhost:" + strconv.Itoa(port)
	c = client.New(opts)
}

// setAllowance sets default allowance if no allowance is set
func setAllowance() {
	rg, err := c.RenterGet()
	if !rg.Settings.Allowance.Active() {
		printLog("=== Setting default allowance")
		err = c.RenterPostAllowance(modules.DefaultAllowance)
		check(err)
	}
}

// waitForRenterIsUploadReady waits till renter is ready to upload
func waitForRenterIsUploadReady() {
	start := time.Now()
	for {
		rur, err := c.RenterUploadReadyDefaultGet()
		check(err)
		if rur.Ready {
			break
		}
		time.Sleep(1 * time.Second)
	}
	elapsed := time.Now().Sub(start)
	msg := fmt.Sprintf("It took %s for renter to be ready to upload.\n", elapsed)
	printLog(msg)
}
