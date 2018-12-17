package renter

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
func (r *Renter) managedCalculateDirectoryHealth(siaPath string) (float64, float64, time.Time, error) {
	// Set health to DefaultDirHealth to avoid falsely identifying the most in
	// need file
	worstHealth := siadir.DefaultDirHealth
	worstStuckHealth := siadir.DefaultDirHealth
	lastHealthCheckTime := time.Now()
	// Read directory
	path := filepath.Join(r.filesDir, siaPath)
	fileinfos, err := ioutil.ReadDir(path)
	if err != nil {
		r.log.Printf("WARN: Error in reading files in directory %v : %v\n", path, err)
		return 0, 0, time.Time{}, err
	}

	// Iterate over directory
	for _, fi := range fileinfos {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return 0, 0, time.Time{}, err
		default:
		}

		var health, stuckHealth float64
		var numStuckChunks uint64
		lastCheck := time.Now()
		ext := filepath.Ext(fi.Name())
		if ext == siadir.SiaDirExtension || ext == siadir.SiaDirExtension+"_temp" {
			// ignore siadir metadata files
			continue
		} else if ext == siafile.ShareExtension {
			fName := strings.TrimSuffix(fi.Name(), siafile.ShareExtension)
			// calculate the health of the siafile
			health, numStuckChunks, err = r.managedFileHealth(filepath.Join(siaPath, fName))
			if err != nil {
				return 0, 0, time.Time{}, err
			}
			if numStuckChunks > 0 && health > worstStuckHealth {
				worstStuckHealth = health
			}
			lastCheck = time.Now()
		} else {
			// Directory is found, read the directory metadata file
			health, stuckHealth, lastCheck, err = r.managedDirectoryHealth(filepath.Join(siaPath, fi.Name()))
			if err != nil {
				return 0, 0, time.Time{}, err
			}
			if stuckHealth > worstStuckHealth {
				worstStuckHealth = stuckHealth
			}
		}

		if health > worstHealth {
			worstHealth = health
		}
		if lastCheck.Before(lastHealthCheckTime) {
			lastHealthCheckTime = lastCheck
		}
	}

	return worstHealth, worstStuckHealth, lastHealthCheckTime, nil
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
func (r *Renter) managedDirectoryHealth(siaPath string) (float64, float64, time.Time, error) {
	// Check for bad paths and files
	fi, err := os.Stat(filepath.Join(r.filesDir, siaPath))
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	if !fi.IsDir() {
		return 0, 0, time.Time{}, fmt.Errorf("%v is not a directory", siaPath)
	}

	//  Open SiaDir
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		return 0, 0, time.Time{}, err
	}
	defer siaDir.Close()

	// Return the siadir health
	health, stuckHealth, lastHealthChecktime := siaDir.Health()
	return health, stuckHealth, lastHealthChecktime, nil
}

// managedFileHealth calculates the health of a siafile. Health is defined as
// the percent of parity pieces remaining.
//
// health = 0 is full redundancy, health <= 1 is recoverable, health > 1 needs
// to be repaired from disk or repair by upload streaming
func (r *Renter) managedFileHealth(siaPath string) (float64, uint64, error) {
	// Load the Siafile.
	sf, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return 0, 0, err
	}
	defer sf.Close()

	// Calculate file health
	hostOfflineMap, _, _ := r.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{sf})
	fileHealth, numStuckChunks := sf.Health(hostOfflineMap)
	return fileHealth, numStuckChunks, nil
}

// managedOldestHealthCheckTime finds the lowest level directory that has a
// LastHealthCheckTime that is outside the healthCheckInterval
func (r *Renter) managedOldestHealthCheckTime() (string, time.Time, error) {
	// Check the siadir metadata for the root files directory
	siaPath := ""
	_, _, lastHealthCheckTime, err := r.managedDirectoryHealth(siaPath)
	if err != nil {
		return "", time.Time{}, err
	}

	// Find the lowest level directory that has a LastHealthCheckTime outside
	// the healthCheckInterval
	for time.Since(lastHealthCheckTime) > healthCheckInterval {
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
			return siaPath, lastHealthCheckTime, nil
		}

		// Find the oldest LastHealthCheckTime of the sub directories
		for _, subDirPath := range subDirSiaPaths {
			// Check lastHealthCheckTime of sub directory
			_, _, lastCheck, err := r.managedDirectoryHealth(subDirPath)
			if err != nil {
				return "", time.Time{}, err
			}

			// If lastCheck is after current lastHealthCheckTime continue since
			// we are already in a directory with an older timestamp
			if lastCheck.After(lastHealthCheckTime) {
				continue
			}

			// Update lastHealthCheckTime and follow older path
			lastHealthCheckTime = lastCheck
			siaPath = subDirPath
		}
	}

	return siaPath, lastHealthCheckTime, nil
}

// managedSubDirectories reads a directory and returns a slice of all the sub
// directory SiaPaths
func (r *Renter) managedSubDirectories(siaPath string) ([]string, error) {
	// Read directory
	fileinfos, err := ioutil.ReadDir(filepath.Join(r.filesDir, siaPath))
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
	worstHealth, worstStuckHealth, lastHealthCheckTime, err := r.managedCalculateDirectoryHealth(siaPath)
	if err != nil {
		r.log.Printf("WARN: Could not calculate the health of directory %v: %v\n", filepath.Join(r.filesDir, siaPath), err)
		return
	}

	// Update directory metadata with the health information
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		r.log.Printf("WARN: Could not open directory %v: %v\n", filepath.Join(r.filesDir, siaPath), err)
		return
	}
	err = siaDir.UpdateHealth(worstHealth, worstStuckHealth, lastHealthCheckTime)
	if err != nil {
		r.log.Printf("WARN: Could not update the health of the directory %v: %v\n", filepath.Join(r.filesDir, siaPath), err)
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
		healthCheckSignal := time.After(healthCheckInterval - time.Since(lastHealthCheckTime))

		// If lastHealthCheckTime is within the healthCheckInterval block
		// until it is time to check again
		select {
		case <-healthCheckSignal:
			// Bubble directory
			go r.threadedBubbleHealth(siaPath)
		}
	}
}
