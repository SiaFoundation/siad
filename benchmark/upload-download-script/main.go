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
		m: make(map[string]fileData),
	}
)

// files is a map of files with its data
type files struct {
	m  map[string]fileData
	mu sync.Mutex
}

// fileData stores download data for a specific file
type fileData struct {
	downloading bool
	nDownloads  int
}

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
	log.Println("=== Checking allowance")
	setAllowance()

	// Wait for renter to be upload ready
	log.Println("=== Wait for renter to be ready to upload")
	waitForRenterIsUploadReady()

	// Init filesMap
	log.Println("=== Init list of files to upload/download")
	initFilesMap()

	// Upload files
	log.Println("=== Upload files concurrently")
	// xxx
	// for i := 0; i < nFiles; i++ {
	// 	wg.Add(1)
	// 	go XXXcreateAndUploadFile(i)
	// }
	for i := 0; i < maxConcurrUploads; i++ {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		wg.Add(1)
		go threadedCreateAndUploadFiles(i, timestamp)
	}

	// Wait for all uploads to finish to avoid "not enough workers" error
	wg.Wait()

	// Download files
	log.Println("=== Download files concurrently")
	for i := 0; i < nTotalDownloads; i++ {
		wg.Add(1)
		go downloadFile(i)
	}

	// Wait for all downloads finish
	wg.Wait()
	log.Println("=== Done")
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
		fd := fileData{}
		filesMap.m[filename] = fd
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

// createAndUploadFile creates a local file and uploads it to Sia
func XXXcreateAndUploadFile(i int) {
	// Occupy one spot in the uploads channel buffer to limit concurrent uploads
	upChan <- struct{}{}

	// Create a local file to upload
	f := XXXcreateFile(i)

	// Upload a file to Sia
	XXXuploadFile(f, i)

	// Delete local file
	filePath := filepath.Join(upDir, f)
	XXXdeleteLocalFile(filePath)

	// Free one spot in the uploads channel buffer
	_ = <-upChan

	wg.Done()
}

// createFile creates a file in the upload directory with te given size in bytes
func XXXcreateFile(i int) string {
	start := time.Now()

	// Preapare file name parts
	fileIndex := fmt.Sprintf("%03d", i)
	sizeStr := formatFileSize(fileSize, "")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Create filename and full path
	filename := "Randfile" + fileIndex + "_" + sizeStr + "_" + timestamp
	path := filepath.Join(upDir, filename)

	// Log
	log.Printf("File #%03d, creating file: %s\n", i, filename)

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
	log.Printf("File #%03d, created file: %s in: %s\n", i, filename, elapsed)

	return filename
}

// uploadFile uploads a file to Sia and adds a file to files map
func XXXuploadFile(filename string, i int) {
	start := time.Now()

	// Log
	log.Printf("File #%03d, uploading file: %s\n", i, filename)

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

	filesMap.mu.Lock()
	defer filesMap.mu.Unlock()
	fd := fileData{}
	filesMap.m[filename] = fd

	// Log duration
	elapsed := time.Now().Sub(start)
	log.Printf("File #%03d, uploaded file: %s in: %s\n", i, filename, elapsed)
	uploadTimes = append(uploadTimes, elapsed)
}

// deleteLocalFile deletes a local file, in case of error exits execution via
// check()
func XXXdeleteLocalFile(filepath string) {
	log.Println("Deleting a local file:", filepath)
	err := os.Remove(filepath)
	check(err)
}

// downloadFile downloads a file from Sia
func downloadFile(iDownload int) {
	// Occupy one spot in the downloads channel buffer to limit concurrent uploads
	downChan <- struct{}{}

	start := time.Now()

	// Select a file not yet downloading to be sure to download each file at
	// least once
	var filename string
	filesMap.mu.Lock()
	for fn, fd := range filesMap.m {
		if fd.downloading {
			continue
		}
		filename = fn
	}

	// Select random file if all files are already downloading
	if filename == "" {
		rand.Seed(time.Now().UnixNano())
		fi := rand.Intn(len(filesMap.m))
		i := 0
		for fn := range filesMap.m {
			if i == fi {
				filename = fn
				break
			}
			i++
		}
	}
	filesMap.mu.Unlock()

	log.Printf("Download #%03d, downloading file: %s\n", iDownload, filename)

	// Download file
	// Use unique local filename because one file can be downloaded concurrently multiple times
	localFilename := filename + fmt.Sprintf("_#%03d", iDownload)
	localPath := filepath.Join(downDir, localFilename)
	siaPath, err := modules.NewSiaPath(filepath.Join(siaDir, filename))
	check(err)
	_, err = c.RenterDownloadFullGet(siaPath, localPath, false)
	check(err)

	// Delete downloaded file
	XXXdeleteLocalFile(localPath)

	// Update files map
	filesMap.mu.Lock()
	nDownloads := filesMap.m[filename].nDownloads
	fd := fileData{
		downloading: true,
		nDownloads:  nDownloads + 1,
	}
	filesMap.m[filename] = fd
	filesMap.mu.Unlock()

	// Log duration
	elapsed := time.Now().Sub(start)
	log.Printf("Download #%02d, downloaded file %s in %s", iDownload, filename, elapsed)
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
		log.Println("=== Setting default allowance")
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
	log.Printf("It took %s for renter to be ready to upload.\n", elapsed)
}

// threadedCreateAndUploadFiles is a worker that creates and uploads files
func threadedCreateAndUploadFiles(workerIndex int, timestamp string) {
	// Check if there are some files to be uploaded
	filesMap.mu.Lock()
	n := len(filesMap.m)
	if n == nFiles {
		// No more files, finish
		log.Printf("Upload worker #%d finished", workerIndex)
		filesMap.mu.Unlock()
		return
	}

	// Create filename
	fileIndex := fmt.Sprintf("%03d", n)
	sizeStr := formatFileSize(fileSize, "")
	filename := "Randfile" + fileIndex + "_" + sizeStr + "_" + timestamp

	// Update files map with file to be created
	filesMap.m[filename] = fileData{}
	filesMap.mu.Unlock()

	// Create a local file
	createFile(n, filename)
}

// createFile creates a file in the upload directory with te given size
func createFile(fileIndex int, filename string) {
	start := time.Now()

	log.Printf("File #%03d, creating file: %s\n", fileIndex, filename)

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
	log.Printf("File #%03d, created file: %s in: %s\n", fileIndex, filename, elapsed)
}

//xxx
func uploadFile(fileIndex int, filename string) {
	start := time.Now()

	log.Printf("File #%03d, uploading file: %s\n", fileIndex, filename)

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

	filesMap.mu.Lock()
	defer filesMap.mu.Unlock()
	fd := fileData{}
	filesMap.m[filename] = fd

	// Log duration
	elapsed := time.Now().Sub(start)
	log.Printf("File #%03d, uploaded file: %s in: %s\n", fileIndex, filename, elapsed)
	uploadTimes = append(uploadTimes, elapsed)
}
