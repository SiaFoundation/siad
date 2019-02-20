package renter

// The following describes the work flow of how Sia repairs files
//
// There are 3 main functions that work together to make up Sia's file repair
// mechanism, threadedUpdateRenterHealth, threadedUploadLoop, and
// threadedStuckFileLoop. These 3 functions will be referred to as the health
// loop, the repair loop, and the stuck loop respectively.
//
// The health loop is responsible for ensuring that the health of the renter's
// file directory is updated periodically. The health information for a
// directory is stored in the .siadir metadata file and is the worst values for
// any of the files and sub directories. This is true for all directories which
// means the health of top level directory of the renter is the health of the
// worst file in the renter. For health and stuck health the worst value is the
// highest value, for timestamp values the oldest timestamp is the worst value,
// and for aggregate values (ie NumStuckChunks) it will be the sum of all the
// files and sub directories.  The health loop keeps the renter file directory
// updated by following the path of oldest LastHealthCheckTime and then calling
// threadedBubbleHealth, to be referred to as bubble, on that directory. When a
// directory is bubbled, the health information is recalculated and saved to
// disk and then bubble is called on the parent directory until the top level
// directory is reached. If during a bubble a file is found that meets the
// threshold health for repair, then a signal is sent to the repair loop. If a
// stuck chunk is found then a signal is sent to the stuck loop. Once the entire
// renter's directory has been updated within the healthCheckInterval the health
// loop sleeps until the time interval has passed.
//
// The repair loop is responsible for repairing the renter's files, this
// includes uploads. The repair loop follows the path of worst health and then
// adds the files from the directory with the worst health to the repair heap
// and begins repairing. If no directories are unhealthy enough to require
// repair the repair loop sleeps until a new upload triggers it to start or it
// is triggered by a bubble finding a file that requires repair. While there are
// files to repair, the repair loop will continue to work through the renter's
// directory finding the worst health directories and adding them to the repair
// heap. The rebuildChunkHeapInterval is used to make sure the repair heap
// doesn't get stuck on repairing a set of chunks for too long. Once the
// rebuildChunkheapInterval passes, the repair loop will continue in it's search
// for files that need repair. As chunks are repaired, they will call bubble on
// their directory to ensure that the renter directory gets updated.
//
// The stuck loop is responsible for targeting chunks that didn't get repaired
// properly. The stuck loop randomly finds a directory containing stuck chunks
// and adds those to the repair heap. The repair heap will randomly add one
// stuck chunk to the heap at a time. Stuck chunks are priority in the heap, so
// limiting it to 1 stuck chunk at a time prevents the heap from being saturated
// with stuck chunks that potentially cannot be repaired which would cause no
// other files to be repaired. If the repair of a stuck chunk is successful, a
// signal is sent to the stuck loop and another stuck chunk is added to the
// heap. If the repair wasn't successful, the stuck loop will wait for the
// repairStuckChunkInterval to pass and then try another random stuck chunk. If
// the stuck loop doesn't find any stuck chunks, it will sleep until a bubble
// triggers it by finding a stuck chunk.

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// errNoStuckFiles is a helper to indicate that there are no stuck files in
	// the renter's directory
	errNoStuckFiles = errors.New("no stuck files")
)

type (
	// fileHealth is a helper struct that contains health metadata information
	// about a SiaFile
	fileHealth struct {
		health              float64
		stuckHealth         float64
		lastHealthCheckTime time.Time
		numStuckChunks      uint64
		recentRepairTime    time.Time
	}
)

// bubbleStatus indicates the status of a bubble being executed on a
// directory
type bubbleStatus int

// bubbleError, bubbleInit, bubbleActive, and bubblePending are the constants
// used to determine the status of a bubble being executed on a directory
const (
	bubbleError bubbleStatus = iota
	bubbleInit
	bubbleActive
	bubblePending
)

// managedBubbleNeeded checks if a bubble is needed for a directory, updates the
// renter's bubbleUpdates map and returns a bool
func (r *Renter) managedBubbleNeeded(siaPath string) (bool, error) {
	r.bubbleUpdatesMu.Lock()
	defer r.bubbleUpdatesMu.Unlock()

	// Check for bubble in bubbleUpdate map
	status, ok := r.bubbleUpdates[siaPath]
	if !ok {
		status = bubbleInit
		r.bubbleUpdates[siaPath] = status
	}

	// Update the bubble status
	var err error
	switch status {
	case bubblePending:
	case bubbleActive:
		r.bubbleUpdates[siaPath] = bubblePending
	case bubbleInit:
		r.bubbleUpdates[siaPath] = bubbleActive
		return true, nil
	default:
		err = errors.New("WARN: invalid bubble status")
	}
	return false, err
}

// managedCalculateDirectoryHealth calculates the health of all the siafiles in
// a siadir and returns the worst health, worst stuck health, and oldest
// lastHealthCheckTime of any of the siafiles and any of the sub-directories
func (r *Renter) managedCalculateDirectoryHealth(siaPath string) (siadir.SiaDirHealth, error) {
	// Set health to DefaultDirHealth to avoid falsely identifying the most in
	// need file
	worstHealth := siadir.SiaDirHealth{
		Health:              siadir.DefaultDirHealth,
		StuckHealth:         siadir.DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		NumStuckChunks:      0,
	}
	// Read directory
	path := filepath.Join(r.staticFilesDir, siaPath)
	fileinfos, err := ioutil.ReadDir(path)
	if err != nil {
		r.log.Printf("WARN: Error in reading files in directory %v : %v\n", path, err)
		return siadir.SiaDirHealth{}, err
	}

	// Iterate over directory
	for _, fi := range fileinfos {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return siadir.SiaDirHealth{}, err
		default:
		}

		var health, stuckHealth float64
		var numStuckChunks uint64
		var lastHealthCheckTime time.Time
		ext := filepath.Ext(fi.Name())
		// Check for SiaFiles and Directories
		if ext == siafile.ShareExtension {
			// SiaFile found, calculate the health of the siafile
			fName := strings.TrimSuffix(fi.Name(), siafile.ShareExtension)
			fileHealth, err := r.managedFileHealth(filepath.Join(siaPath, fName))
			if err != nil {
				return siadir.SiaDirHealth{}, err
			}
			if time.Since(fileHealth.recentRepairTime) >= fileRepairInterval {
				// If the file has not recently been repaired then consider the
				// health of the file
				health = fileHealth.health
			}
			lastHealthCheckTime = fileHealth.lastHealthCheckTime
			stuckHealth = fileHealth.stuckHealth
			numStuckChunks = fileHealth.numStuckChunks
		} else if fi.IsDir() {
			// Directory is found, read the directory metadata file
			dirHealth, err := r.managedDirectoryHealth(filepath.Join(siaPath, fi.Name()))
			if err != nil {
				return siadir.SiaDirHealth{}, err
			}
			health = dirHealth.Health
			stuckHealth = dirHealth.StuckHealth
			lastHealthCheckTime = dirHealth.LastHealthCheckTime
			numStuckChunks = dirHealth.NumStuckChunks
		} else {
			// Ignore everthing that is not a SiaFile or a directory
			continue
		}
		// Update Health and Stuck Health
		if health > worstHealth.Health {
			worstHealth.Health = health
		}
		if stuckHealth > worstHealth.StuckHealth {
			worstHealth.StuckHealth = stuckHealth
		}
		// Update LastHealthCheckTime
		if lastHealthCheckTime.Before(worstHealth.LastHealthCheckTime) {
			worstHealth.LastHealthCheckTime = lastHealthCheckTime
		}
		worstHealth.NumStuckChunks += numStuckChunks
	}

	return worstHealth, nil
}

// managedCompleteBubbleUpdate completes the bubble update and updates and/or
// removes it from the renter's bubbleUpdates.
func (r *Renter) managedCompleteBubbleUpdate(siaPath string) error {
	r.bubbleUpdatesMu.Lock()
	defer r.bubbleUpdatesMu.Unlock()

	// Check current status
	status, ok := r.bubbleUpdates[siaPath]
	if !ok {
		// Bubble not found in map, treat as developer error as this should not happen
		build.Critical("bubble status not found in bubble update map")
	}

	// Update status and call new bubble or remove from bubbleUpdates and save
	switch status {
	case bubblePending:
		r.bubbleUpdates[siaPath] = bubbleInit
		defer func() {
			go r.threadedBubbleHealth(siaPath)
		}()
	case bubbleActive:
		delete(r.bubbleUpdates, siaPath)
	default:
		return errors.New("WARN: invalid bubble status")
	}

	return r.saveBubbleUpdates()
}

// managedDirectoryHealth reads the directory metadata and returns the health,
// the DefaultDirHealth will be returned in the event of an error or if a path
// to a file is past in
func (r *Renter) managedDirectoryHealth(siaPath string) (siadir.SiaDirHealth, error) {
	// Check for bad paths and files
	fullPath := filepath.Join(r.staticFilesDir, siaPath)
	fi, err := os.Stat(fullPath)
	if err != nil {
		return siadir.SiaDirHealth{}, err
	}
	if !fi.IsDir() {
		return siadir.SiaDirHealth{}, fmt.Errorf("%v is not a directory", siaPath)
	}

	//  Open SiaDir
	siaDir, err := r.staticDirSet.Open(siaPath)
	if os.IsNotExist(err) {
		// Remember initial Error
		initError := err
		// Metadata file does not exists, check if directory is empty
		fileInfos, err := ioutil.ReadDir(fullPath)
		if err != nil {
			return siadir.SiaDirHealth{}, err
		}
		// If the directory is empty and is not the root directory, assume it
		// was deleted so do not create a metadata file
		if len(fileInfos) == 0 && siaPath != "" {
			return siadir.SiaDirHealth{}, initError
		}
		// If we are at the root directory or the directory is not empty, create
		// a metadata file
		siaDir, err = r.staticDirSet.NewSiaDir(siaPath)
	}
	if err != nil {
		return siadir.SiaDirHealth{}, err
	}
	defer siaDir.Close()

	return siaDir.Health(), nil
}

// managedFileHealth calculates the health of a siafile. Health is defined as
// the percent of parity pieces remaining.
//
// health = 0 is full redundancy, health <= 1 is recoverable, health > 1 needs
// to be repaired from disk or repair by upload streaming
func (r *Renter) managedFileHealth(siaPath string) (fileHealth, error) {
	// Load the Siafile.
	sf, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return fileHealth{}, err
	}
	defer sf.Close()

	// Calculate file health
	hostOfflineMap, hostGoodForRenewMap, _ := r.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{sf})
	health, stuckHealth, numStuckChunks := sf.Health(hostOfflineMap, hostGoodForRenewMap)
	if err := sf.UpdateLastHealthCheckTime(); err != nil {
		return fileHealth{}, err
	}
	// Check if local file is missing and redundancy is less than one
	if _, err := os.Stat(sf.LocalPath()); os.IsNotExist(err) && sf.Redundancy(hostOfflineMap, hostGoodForRenewMap) < 1 {
		r.log.Debugln("File not found on disk and possibly unrecoverable:", sf.LocalPath())
	}
	return fileHealth{
		health:              health,
		stuckHealth:         stuckHealth,
		lastHealthCheckTime: sf.LastHealthCheckTime(),
		numStuckChunks:      numStuckChunks,
		recentRepairTime:    sf.RecentRepairTime(),
	}, nil
}

// managedOldestHealthCheckTime finds the lowest level directory that has a
// LastHealthCheckTime that is outside the healthCheckInterval
func (r *Renter) managedOldestHealthCheckTime() (string, time.Time, error) {
	// Check the siadir metadata for the root files directory
	siaPath := ""
	health, err := r.managedDirectoryHealth(siaPath)
	if err != nil {
		return "", time.Time{}, err
	}

	// Find the lowest level directory that has a LastHealthCheckTime outside
	// the healthCheckInterval
	for time.Since(health.LastHealthCheckTime) > healthCheckInterval {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return "", time.Time{}, err
		default:
		}

		// Check for sub directories
		subDirSiaPaths, err := r.managedSubDirectories(siaPath)
		if err != nil {
			return "", time.Time{}, err
		}
		// If there are no sub directories, return
		if len(subDirSiaPaths) == 0 {
			return siaPath, health.LastHealthCheckTime, nil
		}

		// Find the oldest LastHealthCheckTime of the sub directories
		updated := false
		for _, subDirPath := range subDirSiaPaths {
			// Check lastHealthCheckTime of sub directory
			subHealth, err := r.managedDirectoryHealth(subDirPath)
			if err != nil {
				return "", time.Time{}, err
			}

			// If lastCheck is after current lastHealthCheckTime continue since
			// we are already in a directory with an older timestamp
			if subHealth.LastHealthCheckTime.After(health.LastHealthCheckTime) {
				continue
			}

			// Update lastHealthCheckTime and follow older path
			updated = true
			health.LastHealthCheckTime = subHealth.LastHealthCheckTime
			siaPath = subDirPath
		}

		// If the values were never updated with any of the sub directory values
		// then return as we are in the directory we are looking for
		if !updated {
			return siaPath, health.LastHealthCheckTime, nil
		}
	}

	return siaPath, health.LastHealthCheckTime, nil
}

// managedStuckDirectory randomly finds a directory that contains stuck chunks
func (r *Renter) managedStuckDirectory() (string, error) {
	// Iterating of the renter direcotry until randomly ending up in a
	// directory, break and return that directory
	siaPath := ""
	for {
		select {
		// Check to make sure renter hasn't been shutdown
		case <-r.tg.StopChan():
			return "", nil
		default:
		}

		directories, files, err := r.DirList(siaPath)
		if err != nil {
			return "", err
		}
		// Sanity check that there is at least the current directory
		if len(directories) == 0 {
			build.Critical("No directories returned from DirList")
		}
		// Sanity check that we didn't end up in an empty directory with stuck
		// chunks. The expection is the root files directory as this is the case
		// for new renters until a file is uploaded
		emptyDir := len(directories) == 1 && len(files) == 0
		if emptyDir && directories[0].NumStuckChunks != 0 && siaPath != "" {
			build.Critical("Empty directory found with stuck chunks:", siaPath)
		}
		// Sanity check if there are stuck chunks in this directory, this is the
		// case when it is a healthy renter's directory
		if directories[0].NumStuckChunks == 0 {
			// Sanity check that we are at the root directory
			if siaPath != "" {
				build.Critical("ended up in directory with no stuck chunks that is not root directory:", siaPath)
			}
			return siaPath, errNoStuckFiles
		}
		// Check if we have reached a directory with only files
		if len(directories) == 1 {
			return siaPath, nil
		}

		// Get random int
		rand := fastrand.Intn(int(directories[0].NumStuckChunks))

		// Use rand to decide which directory to go into. Work backwards over
		// the slice of directories. Since the first element is the current
		// directory that means that it is the sum of all the files and
		// directories.  We can chose a directory by subtracting the number of
		// stuck chunks a directory has from rand and if rand gets to 0 or less
		// we choose that direcotry
		for i := len(directories) - 1; i >= 0; i-- {
			// If we are on the last iteration then return the current directory
			if i == 0 {
				return siaPath, nil
			}

			// Skip directories with no stuck chunks
			if directories[i].NumStuckChunks == uint64(0) {
				continue
			}

			// If we make it to the last iteration double check that the current
			// directory has files
			if i == 0 && len(files) == 0 {
				break
			}

			rand = rand - int(directories[i].NumStuckChunks)
			siaPath = directories[i].SiaPath
			// If rand is less than 0 break out of the loop and continue into
			// that directory
			if rand <= 0 {
				break
			}
		}
	}
}

// managedSubDirectories reads a directory and returns a slice of all the sub
// directory SiaPaths
func (r *Renter) managedSubDirectories(siaPath string) ([]string, error) {
	// Read directory
	fileinfos, err := ioutil.ReadDir(filepath.Join(r.staticFilesDir, siaPath))
	if err != nil {
		return []string{}, err
	}
	// Find all sub directory SiaPaths
	folders := make([]string, 0, len(fileinfos))
	for _, fi := range fileinfos {
		if fi.IsDir() {
			folders = append(folders, filepath.Join(siaPath, fi.Name()))
		}
	}
	return folders, nil
}

// managedWorstHealthDirectory follows the path of worst health to the lowest
// level possible
func (r *Renter) managedWorstHealthDirectory() (string, float64, error) {
	// Check the health of the root files directory
	siaPath := ""
	health, err := r.managedDirectoryHealth(siaPath)
	if err != nil {
		return "", 0, err
	}

	// Follow the path of worst health to the lowest level. We only want to find
	// directories with a health worse than the repairHealthThreshold to save
	// resources
	for health.Health >= RemoteRepairDownloadThreshold {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return "", 0, errors.New("could not find worst health directory due to shutdown")
		default:
		}
		// Check for subdirectories
		subDirSiaPaths, err := r.managedSubDirectories(siaPath)
		if err != nil {
			return "", 0, err
		}
		// If there are no sub directories, return
		if len(subDirSiaPaths) == 0 {
			return siaPath, health.Health, nil
		}

		// Check sub directory healths to find the worst health
		updated := false
		for _, subDirPath := range subDirSiaPaths {
			// Check health of sub directory
			subHealth, err := r.managedDirectoryHealth(subDirPath)
			if err != nil {
				return "", 0, err
			}

			// If the health of the sub directory is better than the current
			// worst health continue
			if subHealth.Health < health.Health {
				continue
			}

			// Update Health and worst health path
			updated = true
			health.Health = subHealth.Health
			siaPath = subDirPath
		}

		// If the values were never updated with any of the sub directory values
		// then return as we are in the directory we are looking for
		if !updated {
			return siaPath, health.Health, nil
		}
	}

	return siaPath, health.Health, nil
}

// threadedBubbleHealth calculates the health of a directory and updates the
// siadir metadata on disk then calls threadedBubbleHealth on the parent
// directory
//
// Note: health = 0 is full redundancy, health <= 1 is recoverable, health > 1
// cannot be immediately repaired using only the online hosts.
func (r *Renter) threadedBubbleHealth(siaPath string) {
	if err := r.tg.Add(); err != nil {
		return
	}
	defer r.tg.Done()

	// Check if bubble is needed
	needed, err := r.managedBubbleNeeded(siaPath)
	if err != nil {
		r.log.Println("WARN: error in checking if bubble is needed:", err)
		return
	}
	if !needed {
		return
	}

	// Calculate the health of the directory
	health, err := r.managedCalculateDirectoryHealth(siaPath)
	if err != nil {
		r.log.Printf("WARN: Could not calculate the health of directory %v: %v\n", filepath.Join(r.staticFilesDir, siaPath), err)
		return
	}

	// Check for files in need of repair or stuck chunks and trigger the
	// appropriate repair loop
	if health.Health >= RemoteRepairDownloadThreshold {
		select {
		case r.uploadHeap.repairNeeded <- struct{}{}:
		default:
		}
	}
	if health.NumStuckChunks > 0 {
		select {
		case r.uploadHeap.stuckChunkFound <- struct{}{}:
		default:
		}
	}

	// Update directory metadata with the health information
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		r.log.Printf("WARN: Could not open directory %v: %v\n", filepath.Join(r.staticFilesDir, siaPath), err)
		return
	}
	err = siaDir.UpdateHealth(health)
	if err != nil {
		r.log.Printf("WARN: Could not update the health of the directory %v: %v\n", filepath.Join(r.staticFilesDir, siaPath), err)
		return
	}

	// Complete bubble
	err = r.managedCompleteBubbleUpdate(siaPath)
	if err != nil {
		r.log.Println("WARN: error in completing bubble:", err)
		return
	}

	// If siaPath is equal to "" then return as we are in the root files
	// directory of the renter
	if siaPath == "" {
		return
	}
	// Move to parent directory
	siaPath = filepath.Dir(siaPath)
	if siaPath == "." {
		siaPath = ""
	}
	go r.threadedBubbleHealth(siaPath)
	return
}

// threadedStuckFileLoop go through the renter directory and finds the stuck
// chunks and tries to repair them
func (r *Renter) threadedStuckFileLoop() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()
	// Loop until the renter has shutdown or until there are no stuck chunks
	for {
		// Wait until the renter is online to proceed.
		if !r.managedBlockUntilOnline() {
			// The renter shut down before the internet connection was restored.
			fmt.Println("renter shutdown before internet connection")
			return
		}

		// Randomly get directory with stuck files
		siaPath, err := r.managedStuckDirectory()
		if err != nil && err != errNoStuckFiles {
			r.log.Debugln("WARN: error getting random stuck directory:", err)
			continue
		}
		if err == errNoStuckFiles {
			// Block until new work is required.
			select {
			case <-r.uploadHeap.stuckChunkFound:
				// Health Loop found stuck chunk
			case <-r.tg.StopChan():
				// The renter has shut down.
				return
			}
			continue
		}

		// Refresh the worker pool and get the set of hosts that are currently
		// useful for uploading.
		hosts := r.managedRefreshHostsAndWorkers()

		// Add stuck chunk to upload heap
		r.managedBuildChunkHeap(siaPath, hosts, targetStuckChunks)

		// Try and repair stuck chunk. Since the heap prioritizes stuck chunks
		// the first chunk popped off will be the stuck chunk.
		r.log.Println("Attempting to repair stuck chunks from", siaPath)
		r.managedRepairLoop(hosts)

		// Call bubble once all chunks have been popped off heap
		r.threadedBubbleHealth(siaPath)

		// Sleep until it is time to try and repair another stuck chunk
		rebuildStuckHeapSignal := time.After(repairStuckChunkInterval)
		select {
		case <-r.tg.StopChan():
			// Return if the return has been shutdown
			return
		case <-rebuildStuckHeapSignal:
			// Time to find another random chunk
		case <-r.uploadHeap.stuckChunkSuccess:
			// Stuck chunk was successfully repaired, continue to repair stuck
			// chunks
		}
	}
}

// threadedUpdateRenterHealth reads all the siafiles in the renter, calculates
// the health of each file and updates the folder metadata
func (r *Renter) threadedUpdateRenterHealth() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()
	// Loop until the renter has shutdown or until the renter's top level files
	// directory has a LasHealthCheckTime within the healthCheckInterval
	for {
		select {
		// Check to make sure renter hasn't been shutdown
		case <-r.tg.StopChan():
			return
		default:
		}
		// Follow path of oldest time, return directory and timestamp
		siaPath, lastHealthCheckTime, err := r.managedOldestHealthCheckTime()
		if err != nil {
			r.log.Debug("WARN: Could not find oldest health check time:", err)
			continue
		}

		// If lastHealthCheckTime is within the healthCheckInterval block
		// until it is time to check again
		healthCheckSignal := time.After(healthCheckInterval - time.Since(lastHealthCheckTime))
		select {
		case <-r.tg.StopChan():
			return
		case <-healthCheckSignal:
			// Bubble directory
			r.threadedBubbleHealth(siaPath)
		}
	}
}
