package benchmark

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/fastrand"
)

// Config
const (
	// Directories and log
	workDirPart      = "nebulous/sia-upload-download-benchmark" // Working directory under user home
	uploadsDirPart   = "uploads"                                // Uploads in working directory
	downloadsDirPart = "downloads"                              // Downloads in working directory
	testGroupDirPart = "test-group"
	logFilename      = "upload-download-benchmark.log" // Log filename in working directory
	siaDir           = "upload-download-benchmark"     // Sia directory to upload files to

	// Uploads and downloads
	nFiles              = 50  // Number of files to upload
	fileSize            = 0   // File size of a file to be uploaded in bytes, use 0 to use default testing sector size
	maxConcurrUploads   = 10  // Max number of files to be concurrently uploaded
	maxConcurrDownloads = 10  // Max number of files to be concurrently downloaded
	nTotalDownloads     = 500 // Total number of file downloads. There will be nFiles, a single file can be downloaded x-times

	// siad
	siadPort     = "9980" // Port of siad node (if not using a test group)
	useTestGroup = true   // Whether to use a test group (true) or Sia network (false)
)

// file status enum
const (
	initialized fileStatus = iota
	uploading
	uploaded
	downloading
)

var (
	workDir      string // Working directory
	upDir        string // Uploads working directory
	downDir      string // Downloads working directory
	testGroupDir string // Test group working directory
	logPath      string // Path to log file

	actualFileSize int // Actual file size to be used

	c  *client.Client     // Sia client for uploads and downloads
	tg *siatest.TestGroup // Test group (if used)

	uploadWG   sync.WaitGroup // Wait group to wait for all upload goroutines to finish
	downloadWG sync.WaitGroup // Wait group to wait for all download goroutines to finish

	uploadTimes   []time.Duration // Slice of file upload durations
	downloadTimes []time.Duration // Slice of file download durations

	uploadTimesMu   sync.Mutex // Upload durations mutex
	downloadTimesMu sync.Mutex // Download durations mutex

	filesMap = files{}
)

// files is a map of files with its data
type files struct {
	m              map[string]fileStatus
	downloadsCount int
	mu             sync.Mutex
}

// fileStatus is a type to represent a current status of a file
type fileStatus int

// TestSiaUploadsDownloads concurrently uploads and then downloads files. Before execution
// be sure to have enough storage for concurrent upload and download files.
func TestSiaUploadsDownloads(t *testing.T) {
	// Init, create, clean dirs
	initDirs()

	// Init log to file
	f := initLog()
	defer f.Close()

	// Init actual file size
	initFileSize()

	// Log dirs, files
	log.Println("=== Starting upload/download benchmark")
	log.Println("Working   dir was set to:", workDir)
	log.Println("Uploads   dir was set to:", upDir)
	log.Println("Downloads dir was set to:", downDir)
	log.Println("Logs are stored in:      ", logPath)
	log.Println()

	// Print overview
	totalData := formatFileSize(nFiles*int(actualFileSize), " ")
	fileSizeStr := formatFileSize(actualFileSize, " ")
	log.Printf("Upload total of %s data in %d files per %s\n", totalData, nFiles, fileSizeStr)

	totalData = formatFileSize(nTotalDownloads*int(actualFileSize), " ")
	log.Printf("Download total of %s data in %d downloads per %s\n", totalData, nTotalDownloads, fileSizeStr)
	log.Println()

	// Init test group
	if useTestGroup {
		log.Println("=== Init test group")
		tg = initTestGroup()
		defer func() {
			if err := tg.Close(); err != nil {
				check(err)
			}
		}()
	}

	// Init download and upload clients
	log.Println("=== Init client")
	initClient()

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
		uploadWG.Add(1)
		go threadedCreateAndUploadFiles(i)
	}

	// Wait for uploads to finish. When we start massively downloading while
	// uploads are in progress, uploads halt, because they have lower priority
	uploadWG.Wait()

	// Download files with
	log.Println("=== Download files concurrently")
	for i := 0; i < maxConcurrDownloads; i++ {
		downloadWG.Add(1)
		go threadedDownloadFiles(i)
	}

	// Wait for all downloads finish
	downloadWG.Wait()

	// Delete upload, download (and test group) directories
	log.Println("=== Delete upload, download (and test group) directories")
	cleanUpDirs()

	// Log durations
	log.Printf("Filesize was %s", fileSizeStr)
	averageDuration, averageSpeed := averages(uploadTimes)
	log.Printf("Average upload time was %v", averageDuration)
	log.Printf("Average upload speed was %v", averageSpeed)
	averageDuration, averageSpeed = averages(downloadTimes)
	log.Printf("Average download time was %v", averageDuration)
	log.Printf("Average download speed was %v", averageSpeed)
	log.Println("=== Done")
}

// averages calculates average duration and average file upload/download speed
// from a slice of durations
func averages(durations []time.Duration) (time.Duration, string) {
	if len(durations) == 0 {
		log.Fatal("There are no durations logged")
	}

	// Calculate average duration
	var sum time.Duration
	for _, d := range durations {
		sum += d
	}
	averageDuration := sum / time.Duration(len(durations))

	// Calculate and format average speed
	averageSpeedFloat := float64(actualFileSize) / float64(averageDuration) * float64(time.Second)
	averageSpeedString := formatFileSizeFloat(averageSpeedFloat, " ") + "/s"

	return averageDuration, averageSpeedString
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
	if _, err := os.Stat(testGroupDir); err == nil {
		err = os.RemoveAll(testGroupDir)
		check(err)
	}
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
	remaining := int(actualFileSize)
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
	}
	err = f.Sync()
	check(err)

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
	switch {
	case size > 1e12:
		return strconv.Itoa(int(size)/1e12) + separator + "TB"
	case size > 1e9:
		return strconv.Itoa(int(size)/1e9) + separator + "GB"
	case size > 1e6:
		return strconv.Itoa(int(size)/1e6) + separator + "MB"
	case size > 1e3:
		return strconv.Itoa(int(size)/1e3) + separator + "kB"
	default:
		return strconv.Itoa(size) + separator + "B"
	}
}

// formatFileSizeFloat formats float64 value to filesize string
func formatFileSizeFloat(size float64, separator string) string {
	switch {
	case size > 1e12:
		return fmt.Sprintf("%.2f%s%s", size/1e12, separator, "TB")
	case size > 1e9:
		return fmt.Sprintf("%.2f%s%s", size/1e9, separator, "GB")
	case size > 1e6:
		return fmt.Sprintf("%.2f%s%s", size/1e6, separator, "MB")
	case size > 1e3:
		return fmt.Sprintf("%.2f%s%s", size/1e3, separator, "kB")
	default:
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

	randomIndex := fastrand.Intn(n)

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
func initClient() {
	if useTestGroup {
		c = &tg.Renters()[0].Client
		log.Println("Renter address:", tg.Renters()[0].APIAddress())
		return
	}

	opts, err := client.DefaultOptions()
	check(err)
	opts.Address = "localhost:" + siadPort
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
	if useTestGroup {
		testGroupDir = filepath.Join(workDir, testGroupDirPart)
	}

	// Delete dirs
	cleanUpDirs()

	// Create dirs
	err = os.MkdirAll(upDir, modules.DefaultDirPerm)
	check(err)
	err = os.MkdirAll(downDir, modules.DefaultDirPerm)
	check(err)
	if useTestGroup {
		err = os.MkdirAll(testGroupDir, modules.DefaultDirPerm)
		check(err)
	}
}

// initFilesMap initializes a map of files to be created, uploaded and
// downloaded
func initFilesMap() {
	// Init map
	filesMap.m = make(map[string]fileStatus)

	// Prepare file name parts
	sizeStr := formatFileSize(actualFileSize, "")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	for i := 0; i < nFiles; i++ {
		fileIndex := fmt.Sprintf("%03d", i)

		// Create filename and full path
		filename := "Randfile" + fileIndex + "_" + sizeStr + "_" + timestamp
		filesMap.m[filename] = initialized
	}
}

// initFileSize initializes actual filesize to the given filesize or to default
// sector size if filesize is set to 0
func initFileSize() {
	if fileSize == 0 {
		actualFileSize = int(modules.SectorSize)
	} else {
		actualFileSize = fileSize
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

// initTestGroup initializes test group
func initTestGroup() *siatest.TestGroup {
	// Create a test group
	groupDir := filepath.Join(workDir, "test-group")
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(groupDir, groupParams)
	check(err)
	return tg
}

// setAllowance sets default allowance if no allowance is set
func setAllowance() {
	rg, err := c.RenterGet()
	check(err)
	if !rg.Settings.Allowance.Active() {
		log.Println("=== Setting default allowance")
		err = c.RenterPostAllowance(modules.DefaultAllowance)
		check(err)
	}
}

// threadedCreateAndUploadFiles is a worker that creates and uploads files
func threadedCreateAndUploadFiles(workerIndex int) {
	for {
		// Check if there are some files to be uploaded
		filesMap.mu.Lock()
		filename, ok := getFirstFileByStatus(initialized)
		if !ok {
			// No more files, finish
			filesMap.mu.Unlock()
			break
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
	}
	log.Printf("Upload worker #%d finished", workerIndex)
	uploadWG.Done()
}

// threadedDownloadFiles is a worker that downloads files
func threadedDownloadFiles(workerIndex int) {
	for {
		// Check if there are files to be downloaded
		// Each file should be downloaded at least once
		filesMap.mu.Lock()
		if getFileCountByStatus(downloading) == nFiles && filesMap.downloadsCount >= nTotalDownloads {
			// All files are downloaded or being downloaded and we have reached
			// target download count,finish
			filesMap.mu.Unlock()
			break
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
			filesMap.mu.Unlock()
			time.Sleep(1 * time.Second)
			continue
		}
		filesMap.downloadsCount++
		downloadIndex := filesMap.downloadsCount
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
		downloadTimesMu.Lock()
		downloadTimes = append(downloadTimes, elapsed)
		downloadTimesMu.Unlock()

		// Delete downloaded file
		deleteLocalFile(localPath)
	}
	log.Printf("Upload worker #%d finished", workerIndex)
	downloadWG.Done()
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

		if f.File.MaxHealthPercent == 100 {
			break
		}

		time.Sleep(500 * time.Millisecond)
	}

	// Log duration
	elapsed := time.Now().Sub(start)
	log.Printf("Uploaded file: %s in: %s\n", filename, elapsed)
	uploadTimesMu.Lock()
	uploadTimes = append(uploadTimes, elapsed)
	uploadTimesMu.Unlock()
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
