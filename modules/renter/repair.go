package renter

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

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

// BubbleHealth calculates the health of a directory and updates the siadir
// metadata on disk then moves to the parent directory and repeats until the top
// level renter files directory is reached
//
// Note: health = 0 is full redundancy, health <= 1 is recoverable, health > 1
// cannot be immediately repaired using only the online hosts.
func (r *Renter) BubbleHealth(siaPath string) error {
	for {
		// Grab the siadir and lock it
		siaDir, err := r.staticDirSet.OpenLockedSiaDirSetEntry(siaPath)
		if err != nil {
			return err
		}

		// Calculate the health of the directory
		worstHealth, worstStuckHealth, lastHealthCheckTime, err := r.managedCalculateDirectoryHealth(siaPath)
		if err != nil {
			return err
		}

		// Update directory metadata with the health information
		err = siaDir.UpdateHealth(worstHealth, worstStuckHealth, lastHealthCheckTime)
		if err != nil {
			r.log.Printf("WARN: Could not update the health of the directory %v: %v\n", filepath.Join(r.filesDir, siaPath), err)
			return err
		}

		// Close and release the lock on the siadir
		if err = siaDir.CloseLockedSiaDirSetEntry(); err != nil {
			return err
		}

		// If siaPath is equal to "" then break as we are in the root files
		// directory of the renter
		if siaPath == "" {
			break
		}
		// Move to parent directory
		siaPath = filepath.Dir(siaPath)
		if siaPath == "." {
			siaPath = ""
		}
	}
	return nil
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
