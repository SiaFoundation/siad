package benchmark

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest"
)

// Config
const (
	// Directories and log
	uploadsDirPart   = "uploads"   // Uploads in working directory
	downloadsDirPart = "downloads" // Downloads in working directory
	testGroupDirPart = "test-group"
	logFilename      = "upload-download-benchmark.log" // Log filename in working directory
	siaDir           = "upload-download-benchmark"     // Sia directory to upload files to
	keepLog          = false                           // Keep log after benchmark is finished

	// Uploads and downloads
	nFiles              = 50               // Number of files to upload
	fileSize            = 0                // File size of a file to be uploaded in bytes, use 0 to use default testing sector size
	maxConcurrUploads   = 10               // Max number of files to be concurrently uploaded
	maxConcurrDownloads = 10               // Max number of files to be concurrently downloaded
	nTotalDownloads     = 500              // Total number of file downloads. There will be nFiles, a single file can be downloaded x-times
	renterReadyTimeout  = 10 * time.Second // Timeout in seconds for renter to become upload ready
	uploadTimeout       = 60 * time.Second // Timeout in seconds to upload a file

	// siad
	siadPort     = "9980" // Port of siad node (if not using a test group)
	useTestGroup = true   // Whether to use a test group (true) or Sia network (false)
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

	// List of files that were uploaded, but not yet downloaded, a file started
	// to be downloaded is removed from the list, count represents total
	// number of files uploaded even when they were removed from the list
	createdNotDownloadedFiles files

	// List of files that were started to be downloaded, count represents total
	// number of downloads, a file can be downloaded multiple times
	downloadingFiles files

	uploadTimes   []time.Duration // Slice of file upload durations
	downloadTimes []time.Duration // Slice of file download durations

	uploadTimesMu   sync.Mutex // Upload durations mutex
	downloadTimesMu sync.Mutex // Download durations mutex
)

// files struct contains a slice of filenames with count field. Count usage is
// described in variables createdNotDownloadedFiles and downloadingFiles that
// use files struct
type files struct {
	filenames []string
	count     int
	mu        sync.Mutex
}

// TestSiaUploadsDownloads concurrently uploads and then downloads files. Before execution
// be sure to have enough storage for concurrent upload and download files.
func TestSiaUploadsDownloads(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}

	// Init, create, clean dirs
	initDirs(t)

	// Init log to file
	f := initLog()
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Init actual file size
	initFileSize()

	// Log dirs, files
	log.Println("=== Starting upload/download benchmark")
	log.Println("Working dir was set to:  ", workDir)
	log.Println("Uploads dir was set to:  ", upDir)
	log.Println("Downloads dir was set to:", downDir)
	log.Println("Logs are stored in:      ", logPath)
	log.Println()

	// Print overview
	totalData := modules.FilesizeUnits(uint64(nFiles * actualFileSize))
	fileSizeStr := modules.FilesizeUnits(uint64(actualFileSize))
	log.Printf("Upload total of %s data in %d files per %s\n", totalData, nFiles, fileSizeStr)

	totalData = modules.FilesizeUnits(uint64(nTotalDownloads * actualFileSize))
	log.Printf("Download total of %s data in %d downloads per %s\n", totalData, nTotalDownloads, fileSizeStr)
	log.Println()

	// Init test group
	if useTestGroup {
		log.Println("=== Init test group")
		tg = initTestGroup()
		defer func() {
			if tg == nil {
				return
			}
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

	// Upload files
	log.Println("=== Upload files concurrently")
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	for i := 0; i < maxConcurrUploads; i++ {
		uploadWG.Add(1)
		go threadedCreateAndUploadFiles(timestamp, i)
	}

	// Wait for uploads to finish. When we start massively downloading while
	// uploads are in progress, uploads halt, because they have lower priority
	// TODO: https://gitlab.com/NebulousLabs/Sia/issues/4242
	// Once RHP is fully functioning, do not wait for all uploads finished,
	// download concurrently with uploads
	uploadWG.Wait()

	// Download files with
	log.Println("=== Download files concurrently")
	for i := 0; i < maxConcurrDownloads; i++ {
		downloadWG.Add(1)
		go threadedDownloadFiles(i)
	}

	// Wait for all downloads finish
	downloadWG.Wait()

	// Stop test group
	if useTestGroup {
		log.Println("=== Stop test group")
		if err := tg.Close(); err != nil {
			check(err)
		}
		tg = nil
	}

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
	averageSpeedString := modules.FilesizeUnits(uint64(averageSpeedFloat)) + "/s"

	return averageDuration, averageSpeedString
}

// benchmarkTestDir creates a temporary Sia testing directory for a benchmark
// test, removing any files or directories that previously existed at that
// location. This should only every be called once per test. Otherwise it will
// delete the directory again.
func benchmarkTestDir(testName string) string {
	path := siatest.TestDir("benchmark", testName)
	err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest)
	check(err)
	return path
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

// checkMaxHealthReached checks sia file health and returns nil when 100%
// health is reached
func checkMaxHealthReached(c *client.Client, siaPath modules.SiaPath) error {
	f, err := c.RenterFileGet(siaPath)
	if err != nil {
		return err
	}

	if f.File.MaxHealthPercent != 100 {
		return errors.New("sia file max health did not not reach 100%")
	}

	return nil
}

// cleanUpDirs deletes upload and download directories
func cleanUpDirs() {
	// Delete all dirs incl. log file
	if !keepLog {
		err := os.RemoveAll(workDir)
		check(err)
		return
	}

	// Delete working dirs, but keep log file
	err := os.RemoveAll(upDir)
	check(err)
	err = os.RemoveAll(downDir)
	check(err)
	err = os.RemoveAll(testGroupDir)
	check(err)
}

// createFile creates a file in the upload directory with the given size
func createFile(filename string) {
	start := time.Now()

	log.Printf("Creating file: %s\n", filename)

	// Create file
	path := filepath.Join(upDir, filename)
	f, err := os.Create(path)
	check(err)
	defer func() {
		if err := f.Close(); err != nil {
			check(err)
		}
	}()

	_, err = io.CopyN(f, fastrand.Reader, int64(actualFileSize))
	check(err)
	err = f.Sync()
	check(err)

	// Log duration
	elapsed := time.Since(start)
	log.Printf("Created file: %s in: %s\n", filename, elapsed)
}

// deleteLocalFile deletes a local file, in case of error exits execution via
// check()
func deleteLocalFile(filepath string) {
	log.Println("Deleting a local file:", filepath)
	err := os.Remove(filepath)
	check(err)
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
func initDirs(t *testing.T) {
	// Set dir paths
	workDir = benchmarkTestDir(t.Name())
	upDir = filepath.Join(workDir, uploadsDirPart)
	downDir = filepath.Join(workDir, downloadsDirPart)
	if useTestGroup {
		testGroupDir = filepath.Join(workDir, testGroupDirPart)
	}

	// Create dirs
	err := os.MkdirAll(upDir, modules.DefaultDirPerm)
	check(err)
	err = os.MkdirAll(downDir, modules.DefaultDirPerm)
	check(err)
	if useTestGroup {
		err = os.MkdirAll(testGroupDir, modules.DefaultDirPerm)
		check(err)
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
func threadedCreateAndUploadFiles(timestamp string, workerIndex int) {
	for {
		// Create filename
		fileIndex, ok := createdNotDownloadedFiles.managedIncCountLimit(nFiles)
		if !ok {
			// No more files to create and upload, finish
			break
		}
		fileIndexStr := fmt.Sprintf("%03d", fileIndex)
		sizeStr := modules.FilesizeUnits(uint64(actualFileSize))
		filename := "Randfile" + fileIndexStr + "_" + sizeStr + "_" + timestamp
		filename = strings.ReplaceAll(filename, " ", "")

		// Create a local file
		createFile(filename)

		// Upload the file file to Sia
		uploadFile(filename)

		// Delete the uploaded file
		path := filepath.Join(upDir, filename)
		deleteLocalFile(path)

		// Update files to be downloaded
		createdNotDownloadedFiles.managedAddFile(filename)
	}
	log.Printf("Upload worker #%d finished", workerIndex)
	uploadWG.Done()
}

// threadedDownloadFiles is a worker that downloads files
func threadedDownloadFiles(workerIndex int) {
	for {
		// Check if there are files to be downloaded
		// Each file should be downloaded at least once
		count, length := createdNotDownloadedFiles.managedCountLen()
		allFilesDownloading := count == nFiles && length == 0
		downloadCountReached := downloadingFiles.managedCount() >= nTotalDownloads
		if allFilesDownloading && downloadCountReached {
			// All files are downloaded or being downloaded and we have reached
			// target download count, finish
			break
		}

		// Select a file to download
		// Download a file, that has not yet been downloaded
		filename, ok := createdNotDownloadedFiles.managedRemoveFirstFile()
		if !ok {
			// Fallback to using a file that's already downloading
			filename, ok = downloadingFiles.managedRandomFile()
		}
		if !ok {
			// There is no uploaded file yet, has to wait
			time.Sleep(time.Second)
			continue
		}

		// Dowload file
		downloadIndex := downloadingFiles.managedIncCount()
		downloadingFiles.managedAddFile(filename)
		log.Printf("Download #%03d, downloading file: %s\n", downloadIndex, filename)

		// Use unique local filename because one file can be downloaded concurrently multiple times
		localFilename := filename + fmt.Sprintf("_#%03d", downloadIndex)
		localPath := filepath.Join(downDir, localFilename)
		siaPath, err := modules.NewSiaPath(filepath.Join(siaDir, filename))
		check(err)

		start := time.Now()

		_, err = c.RenterDownloadFullGet(siaPath, localPath, false, false)
		check(err)

		// Log duration
		elapsed := time.Since(start)
		log.Printf("Download #%02d, downloaded file %s in %s", downloadIndex, filename, elapsed)
		downloadTimesMu.Lock()
		downloadTimes = append(downloadTimes, elapsed)
		downloadTimesMu.Unlock()

		// Delete downloaded file
		deleteLocalFile(localPath)
	}
	log.Printf("Download worker #%d finished", workerIndex)
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
	waitTimeMilis := 500 * time.Millisecond
	tries := int(uploadTimeout * 1000 / waitTimeMilis)
	err = build.Retry(tries, waitTimeMilis, func() error {
		return checkMaxHealthReached(c, siaPath)
	})
	check(err)

	// Log duration
	elapsed := time.Since(start)
	log.Printf("Uploaded file: %s in: %s\n", filename, elapsed)
	uploadTimesMu.Lock()
	uploadTimes = append(uploadTimes, elapsed)
	uploadTimesMu.Unlock()
}

// waitForRenterIsUploadReady waits till renter is ready to upload
func waitForRenterIsUploadReady() {
	start := time.Now()

	// Wait for renter upload ready
	waitTime := time.Second
	tries := int(renterReadyTimeout / waitTime)
	err := build.Retry(tries, waitTime, func() error {
		rur, err := c.RenterUploadReadyDefaultGet()
		if err != nil {
			return err
		}
		if !rur.Ready {
			return errors.New("renter is not upload ready")
		}
		return nil
	})
	check(err)

	elapsed := time.Since(start)
	log.Printf("It took %s for renter to be ready to upload.\n", elapsed)
}

// managedAddFile adds filename to the slice of files
func (f *files) managedAddFile(filename string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, file := range f.filenames {
		if file == filename {
			// File with this name already exists
			return
		}
	}
	f.filenames = append(f.filenames, filename)
}

// managedCount gets count
func (f *files) managedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

// managedIncCount increases count and return new already increased count
func (f *files) managedIncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	return f.count
}

// managedIncCountLimit increases count upto a given limit, return new already
// increased count and ok = true if limit count has not yet been reached,
// returns existing count and ok = false if the count has already been reached
func (f *files) managedIncCountLimit(limit int) (count int, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.count >= limit {
		return f.count, false
	}
	f.count++
	return f.count, true
}

// managedCountLen returns count and length of files slice
func (f *files) managedCountLen() (count, length int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, len(f.filenames)
}

// managedRandomFile gets a random filename from the files slice
func (f *files) managedRandomFile() (filename string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.filenames) == 0 {
		return "", false
	}
	n := len(f.filenames)
	return f.filenames[fastrand.Intn(n)], true
}

// managedRemoveFirstFile returns and removes first filename from the files
// slice, if files slice is empty, returns empty string and ok = false
func (f *files) managedRemoveFirstFile() (filename string, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.filenames) == 0 {
		return "", false
	}
	filename = f.filenames[0]
	f.filenames = f.filenames[1:]
	return filename, true
}
