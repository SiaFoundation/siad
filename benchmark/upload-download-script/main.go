package main

import (
	"fmt"
	"io"
	"log"
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
	logFilename      = "upload-download-script.log"          // Log filename in working directory
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

// file status enum
const (
	initialized fileStatus = iota
	uploading
	uploaded
	downloading
)

var (
	workDir string // Working directory
	upDir   string // Uploads working directory
	downDir string // Downloads working directory
	logPath string // Path to log file

	c *client.Client // Sia client for uploads and downloads

	wg sync.WaitGroup // Wait group to wait for all goroutines to finish

	upChan   = make(chan struct{}, maxConcurrUploads)   // Channel with a buffer to limit number of max concurrent uploads
	downChan = make(chan struct{}, maxConcurrDownloads) // Channel with a buffer to limit number of max concurrent downloads

	uploadTimes   []time.Duration // Slice of file upload durations
	downloadTimes []time.Duration // Slice of file download durations

	filesMap = files{
		m: make(map[string]fileStatus),
	}
)

// files is a map of files with its data
type files struct {
	m              map[string]fileStatus
	downloadsCount int
	mu             sync.Mutex
}

// fileStatus is a type to represent a current status of a file
type fileStatus int

// This script concurrently uploads and then downloads files. Before execution
// be sure to have enough storage for concurrent upload and download files.
func main() {
	// Init, create, clean dirs
	initDirs()

	// Init log to file
	f := initLog()
	defer f.Close()

	// Log dirs, files
	log.Println("=== Starting upload/download script")
	log.Println("Working   dir was set to:", workDir)
	log.Println("Uploads   dir was set to:", upDir)
	log.Println("Downloads dir was set to:", downDir)
	log.Println("Logs are stored in:      ", logPath)
	log.Println()

	// Print overview
	totalData := formatFileSize(nFiles*int(fileSize), " ")
	fileSizeStr := formatFileSize(fileSize, " ")
	log.Printf("Upload total of %s data in %d files per %s files\n", totalData, nFiles, fileSizeStr)

	totalData = formatFileSize(nTotalDownloads*int(fileSize), " ")
	log.Printf("Download total of %s data in %d downloads per %s files\n", totalData, nTotalDownloads, fileSizeStr)
	log.Println()

	// Init download and upload clients
	log.Println("=== Init client")
	initClient(siadPort)

	// Set allowance
	log.Println("=== Check/set allowance")
	setAllowance()

	// Wait for renter to be upload ready
	log.Println("=== Wait for renter to be ready to upload")
	waitForRenterIsUploadReady()

	// Init files map with files to be uploaded
	log.Println("=== Init files map")
	initFilesMap()

	// Upload files
	log.Println("=== Upload files concurrently")
	for i := 0; i < maxConcurrUploads; i++ {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		wg.Add(1)
		go threadedCreateAndUploadFiles(i, timestamp)
	}

	// Download files with
	log.Println("=== Download files concurrently")
	for i := 0; i < maxConcurrDownloads; i++ {
		wg.Add(1)
		go threadedDownloadFiles(i)
	}

	// Wait for all downloads finish
	wg.Wait()

	// Delete upload and download directories
	log.Println("=== Delete upload and download directories")
	cleanUpDirs()

	// Log durations
	log.Printf("Filesize was %s", fileSizeStr)
	log.Printf("Average upload time was %v", averageDuration(uploadTimes))
	log.Printf("Average upload speed was %v", averageSpeed(uploadTimes))
	log.Printf("Average download time was %v", averageDuration(downloadTimes))
	log.Printf("Average download speed was %v", averageSpeed(downloadTimes))
	log.Println("=== Done")
}

// averageDuration calculates average duration from a slice of durations
func averageDuration(durations []time.Duration) time.Duration {
	if len(durations) == 0 {
		log.Fatal("There are no durations logged")
	}

	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	return sum / time.Duration(len(durations))
}

// averageSpeed calculates average file upload/download speed
func averageSpeed(durations []time.Duration) string {
	averageDuration := averageDuration(durations)
	averageSpeedFloat := float64(fileSize) / float64(averageDuration/time.Second)
	averageSpeedString := formatFileSizeFloat(averageSpeedFloat, " ") + "/s"
	return averageSpeedString
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
	log.Printf("%s:%d failed check(err)\n", filepath.Base(file), line)
	log.Fatalln(e)
}

// cleanUpDirs deletes upload and download directories
func cleanUpDirs() {
	// Delete dirs
	err := os.RemoveAll(upDir)
	check(err)
	err = os.RemoveAll(downDir)
	check(err)
}

// createFile creates a file in the upload directory with te given size
func createFile(filename string) {
	start := time.Now()

	log.Printf("Creating file: %s\n", filename)

	// Open file for appending
	path := filepath.Join(upDir, filename)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	check(err)
	defer f.Close()

	// Append to file per parts (avoids out of memory error on large files when
	// created concurrently)
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
	log.Printf("Created file: %s in: %s\n", filename, elapsed)
}

// deleteLocalFile deletes a local file, in case of error exits execution via
// check()
func deleteLocalFile(filepath string) {
	log.Println("Deleting a local file:", filepath)
	err := os.Remove(filepath)
	check(err)
}

// formatFileSize formats int value to filesize string
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

// formatFileSizeFloat formats float64 value to filesize string
func formatFileSizeFloat(size float64, separator string) string {
	if size > 1e12 {
		return fmt.Sprintf("%.2f%s%s", size/1e12, separator, "TB")
	} else if size > 1e9 {
		return fmt.Sprintf("%.2f%s%s", size/1e9, separator, "GB")
	} else if size > 1e6 {
		return fmt.Sprintf("%.2f%s%s", size/1e6, separator, "MB")
	} else if size > 1e3 {
		return fmt.Sprintf("%.2f%s%s", size/1e3, separator, "kB")
	} else {
		return fmt.Sprintf("%.2f%s%s", size, separator, "B")
	}
}

// getFileCountByStatus returns count of files with given status
func getFileCountByStatus(fs fileStatus) int {
	n := 0
	for _, status := range filesMap.m {
		if status == fs {
			n++
		}
	}
	return n
}

// getFirstFileByStatus returns the first file with the given status and ok
// flag if found or not
func getFirstFileByStatus(fs fileStatus) (string, bool) {
	for filename, status := range filesMap.m {
		if status == fs {
			return filename, true
		}
	}

	return "", false
}

// getRandomFileByStatus returns random file with the given status and ok flag
// if found or not
func getRandomFileByStatus(fs fileStatus) (string, bool) {
	n := getFileCountByStatus(fs)
	if n == 0 {
		// No file found
		return "", false
	}

	rand.Seed(time.Now().UnixNano())
	randomIndex := rand.Intn(n)

	i := 0
	for filename, status := range filesMap.m {
		if status != fs {
			continue
		}
		if i == randomIndex {
			return filename, true
		}
		i++
	}
	return "", false
}

// initClient initializes a Sia http client
func initClient(port int) {
	opts, err := client.DefaultOptions()
	check(err)
	opts.Address = "localhost:" + strconv.Itoa(port)
	c = client.New(opts)
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
	cleanUpDirs()

	// Create dirs
	err = os.MkdirAll(upDir, os.ModePerm)
	check(err)
	err = os.MkdirAll(downDir, os.ModePerm)
	check(err)
}

// initFilesMap initializes a map of files to be created, uploaded and
// downloaded
func initFilesMap() {
	// Preapare file name parts
	sizeStr := formatFileSize(fileSize, "")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	for i := 0; i < nFiles; i++ {
		fileIndex := fmt.Sprintf("%03d", i)

		// Create filename and full path
		filename := "Randfile" + fileIndex + "_" + sizeStr + "_" + timestamp
		filesMap.m[filename] = initialized
	}
}

// initLog initializes logging to the file
func initLog() *os.File {
	logPath = filepath.Join(workDir, logFilename)
	file, err := os.Create(logPath)
	if err != nil {
		log.Fatal(err)
	}

	mw := io.MultiWriter(os.Stdout, file)
	log.SetOutput(mw)

	return file
}

// setAllowance sets default allowance if no allowance is set
func setAllowance() {
	rg, err := c.RenterGet()
	if !rg.Settings.Allowance.Active() {
		log.Println("=== Setting default allowance")
		err = c.RenterPostAllowance(modules.DefaultAllowance)
		check(err)
	}
}

// threadedCreateAndUploadFiles is a worker that creates and uploads files
func threadedCreateAndUploadFiles(workerIndex int, timestamp string) {
	for {
		// Check if there are some files to be uploaded
		filesMap.mu.Lock()
		filename, ok := getFirstFileByStatus(initialized)
		if !ok {
			// No more files, finish
			log.Printf("Upload worker #%d finished", workerIndex)
			filesMap.mu.Unlock()
			return
		}
		filesMap.m[filename] = uploading
		filesMap.mu.Unlock()

		// Create a local file
		createFile(filename)

		// Upload the file file to Sia
		uploadFile(filename)

		// Delete uploaded file
		path := filepath.Join(upDir, filename)
		deleteLocalFile(path)

		// Update file status
		filesMap.mu.Lock()
		filesMap.m[filename] = uploaded
		filesMap.mu.Unlock()

		wg.Done()
	}
}

// threadedDownloadFiles is a worker that downloads files
func threadedDownloadFiles(workerIndex int) {
	for {
		// Check if there are files to be downloaded
		// Each file should be downloaded at least once
		filesMap.mu.Lock()
		if getFileCountByStatus(downloading) == nFiles {
			// All files are downloaded or being downloaded, finish
			filesMap.mu.Unlock()
			return
		}

		// Select a file to download
		// Download a file, that has not yet been downloaded
		filename, ok := getFirstFileByStatus(uploaded)
		if ok {
			// We have file not yet downoaded to be downloaded, update status
			filesMap.m[filename] = downloading
		} else {
			// There is no file, that is uploaded and has not been downloaded yet
			// Download a random file that is already downloaded or is being
			// downloaded
			filename, ok = getRandomFileByStatus(downloading)
		}
		if !ok {
			// There is no uploaded file yet, has to wait
			time.Sleep(1 * time.Second)
			continue
		}
		downloadIndex := filesMap.downloadsCount
		filesMap.downloadsCount++
		filesMap.mu.Unlock()

		// Download file
		log.Printf("Download #%03d, downloading file: %s\n", downloadIndex, filename)

		// Use unique local filename because one file can be downloaded concurrently multiple times
		localFilename := filename + fmt.Sprintf("_#%03d", downloadIndex)
		localPath := filepath.Join(downDir, localFilename)
		siaPath, err := modules.NewSiaPath(filepath.Join(siaDir, filename))
		check(err)

		start := time.Now()

		_, err = c.RenterDownloadFullGet(siaPath, localPath, false)
		check(err)

		// Log duration
		elapsed := time.Now().Sub(start)
		log.Printf("Download #%02d, downloaded file %s in %s", downloadIndex, filename, elapsed)
		downloadTimes = append(downloadTimes, elapsed)

		// Delete downloaded file
		deleteLocalFile(localPath)

		wg.Done()
	}
}

// uploadFile uploads file to Sia
func uploadFile(filename string) {
	start := time.Now()

	log.Printf("Uploading file: %s\n", filename)

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

	// Log duration
	elapsed := time.Now().Sub(start)
	log.Printf("Uploaded file: %s in: %s\n", filename, elapsed)
	uploadTimes = append(uploadTimes, elapsed)
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
	log.Printf("It took %s for renter to be ready to upload.\n", elapsed)
}
