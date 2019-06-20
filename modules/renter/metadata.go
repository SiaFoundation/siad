package renter

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
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
	bubbleActive
	bubblePending
)

// managedPrepareBubble will add a bubble to the bubble map. If 'true' is returned, the
// caller should proceed by calling bubble. If 'false' is returned, the caller
// should not bubble, another thread will handle running the bubble.
func (r *Renter) managedPrepareBubble(siaPath modules.SiaPath) bool {
	r.bubbleUpdatesMu.Lock()
	defer r.bubbleUpdatesMu.Unlock()

	// Check for bubble in bubbleUpdate map
	siaPathStr := siaPath.String()
	status, ok := r.bubbleUpdates[siaPathStr]
	if !ok {
		r.bubbleUpdates[siaPathStr] = bubbleActive
		return true
	}
	if status != bubbleActive && status != bubblePending {
		build.Critical("bubble status set to bubbleError")
	}
	r.bubbleUpdates[siaPathStr] = bubblePending
	return false
}

// managedCalculateDirectoryMetadata calculates the new values for the
// directory's metadata and tracks the value, either worst or best, for each to
// be bubbled up
func (r *Renter) managedCalculateDirectoryMetadata(siaPath modules.SiaPath) (siadir.Metadata, error) {
	// Set default metadata values to start
	metadata := siadir.Metadata{
		AggregateHealth:              siadir.DefaultDirHealth,
		AggregateLastHealthCheckTime: time.Now(),
		AggregateMinRedundancy:       math.MaxFloat64,
		AggregateModTime:             time.Time{},
		AggregateNumFiles:            uint64(0),
		AggregateNumStuckChunks:      uint64(0),
		AggregateNumSubDirs:          uint64(0),
		AggregateSize:                uint64(0),
		AggregateStuckHealth:         siadir.DefaultDirHealth,

		Health:              siadir.DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		MinRedundancy:       math.MaxFloat64,
		ModTime:             time.Time{},
		NumFiles:            uint64(0),
		NumStuckChunks:      uint64(0),
		NumSubDirs:          uint64(0),
		Size:                uint64(0),
		StuckHealth:         siadir.DefaultDirHealth,
	}
	// Read directory
	fileinfos, err := ioutil.ReadDir(siaPath.SiaDirSysPath(r.staticFilesDir))
	if err != nil {
		r.log.Printf("WARN: Error in reading files in directory %v : %v\n", siaPath.SiaDirSysPath(r.staticFilesDir), err)
		return siadir.Metadata{}, err
	}

	// Iterate over directory
	for _, fi := range fileinfos {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return siadir.Metadata{}, err
		default:
		}

		// Aggregate Fields
		var aggregateHealth, aggregateStuckHealth, aggregateMinRedundancy float64
		var aggregateLastHealthCheckTime, aggregateModTime time.Time
		var fileMetadata siafile.BubbledMetadata
		ext := filepath.Ext(fi.Name())
		// Check for SiaFiles and Directories
		if ext == modules.SiaFileExtension {
			// SiaFile found, calculate the needed metadata information of the siafile
			fName := strings.TrimSuffix(fi.Name(), modules.SiaFileExtension)
			fileSiaPath, err := siaPath.Join(fName)
			if err != nil {
				r.log.Println("unable to join siapath with dirpath while calculating directory metadata:", err)
				continue
			}
			fileMetadata, err = r.managedCalculateAndUpdateFileMetadata(fileSiaPath)
			if err != nil {
				r.log.Printf("failed to calculate file metadata %v: %v", fi.Name(), err)
				continue
			}

			// Record Values that compare against sub directories
			aggregateHealth = fileMetadata.Health
			aggregateStuckHealth = fileMetadata.StuckHealth
			aggregateMinRedundancy = fileMetadata.Redundancy
			aggregateLastHealthCheckTime = fileMetadata.LastHealthCheckTime
			aggregateModTime = fileMetadata.ModTime

			// Update aggregate fields.
			metadata.AggregateNumFiles++
			metadata.AggregateNumStuckChunks += fileMetadata.NumStuckChunks
			metadata.AggregateSize += fileMetadata.Size

			// Update siadir fields.
			metadata.Health = math.Max(metadata.Health, fileMetadata.Health)
			if fileMetadata.LastHealthCheckTime.Before(metadata.LastHealthCheckTime) {
				metadata.LastHealthCheckTime = fileMetadata.LastHealthCheckTime
			}
			metadata.MinRedundancy = math.Min(metadata.MinRedundancy, fileMetadata.Redundancy)
			if fileMetadata.ModTime.After(metadata.ModTime) {
				metadata.ModTime = fileMetadata.ModTime
			}
			metadata.NumFiles++
			metadata.NumStuckChunks += fileMetadata.NumStuckChunks
			metadata.Size += fileMetadata.Size
			metadata.StuckHealth = math.Max(metadata.StuckHealth, fileMetadata.StuckHealth)
		} else if fi.IsDir() {
			// Directory is found, read the directory metadata file
			dirSiaPath, err := siaPath.Join(fi.Name())
			if err != nil {
				return siadir.Metadata{}, err
			}
			dirMetadata, err := r.managedDirectoryMetadata(dirSiaPath)
			if err != nil {
				return siadir.Metadata{}, err
			}

			// Record Values that compare against files
			aggregateHealth = dirMetadata.AggregateHealth
			aggregateStuckHealth = dirMetadata.AggregateStuckHealth
			aggregateMinRedundancy = dirMetadata.AggregateMinRedundancy
			aggregateLastHealthCheckTime = dirMetadata.AggregateLastHealthCheckTime
			aggregateModTime = dirMetadata.AggregateModTime

			// Update aggregate fields.
			metadata.AggregateNumFiles += dirMetadata.AggregateNumFiles
			metadata.AggregateNumStuckChunks += dirMetadata.AggregateNumStuckChunks
			metadata.AggregateNumSubDirs += dirMetadata.AggregateNumSubDirs
			metadata.AggregateSize += dirMetadata.AggregateSize

			// Update siadir fields
			metadata.NumSubDirs++
		} else {
			// Ignore everything that is not a SiaFile or a directory
			continue
		}
		// Track the max value of AggregateHealth and Aggregate StuckHealth
		metadata.AggregateHealth = math.Max(metadata.AggregateHealth, aggregateHealth)
		metadata.AggregateStuckHealth = math.Max(metadata.AggregateStuckHealth, aggregateStuckHealth)
		// Track the min value for AggregateMinRedundancy
		metadata.AggregateMinRedundancy = math.Min(metadata.AggregateMinRedundancy, aggregateMinRedundancy)
		// Update LastHealthCheckTime
		if aggregateLastHealthCheckTime.Before(metadata.AggregateLastHealthCheckTime) {
			metadata.AggregateLastHealthCheckTime = aggregateLastHealthCheckTime
		}
		// Update ModTime
		if aggregateModTime.After(metadata.AggregateModTime) {
			metadata.AggregateModTime = aggregateModTime
		}
	}
	// Sanity check on ModTime. If mod time is still zero it means there were no
	// files or subdirectories. Set ModTime to now since we just updated this
	// directory
	if metadata.AggregateModTime.IsZero() {
		metadata.AggregateModTime = time.Now()
	}
	if metadata.ModTime.IsZero() {
		metadata.ModTime = time.Now()
	}

	// Sanity check on Redundancy. If MinRedundancy is still math.MaxFloat64
	// then set it to 0
	if metadata.AggregateMinRedundancy == math.MaxFloat64 {
		metadata.AggregateMinRedundancy = 0
	}
	if metadata.MinRedundancy == math.MaxFloat64 {
		metadata.MinRedundancy = 0
	}

	return metadata, nil
}

// managedCalculateAndUpdateFileMetadata calculates and returns the necessary
// metadata information of a siafile that needs to be bubbled. The calculated
// metadata information is also updated and saved to disk
func (r *Renter) managedCalculateAndUpdateFileMetadata(siaPath modules.SiaPath) (siafile.BubbledMetadata, error) {
	// Load the Siafile.
	sf, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return siafile.BubbledMetadata{}, err
	}
	defer sf.Close()

	// Get offline and goodforrenew maps
	hostOfflineMap, hostGoodForRenewMap, _ := r.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{sf})

	// Calculate file health
	health, stuckHealth, numStuckChunks := sf.Health(hostOfflineMap, hostGoodForRenewMap)

	// Set the LastHealthCheckTime
	sf.SetLastHealthCheckTime()

	// Calculate file Redundancy and check if local file is missing and
	// redundancy is less than one
	redundancy := sf.Redundancy(hostOfflineMap, hostGoodForRenewMap)
	if _, err := os.Stat(sf.LocalPath()); os.IsNotExist(err) && redundancy < 1 {
		r.log.Debugln("File not found on disk and possibly unrecoverable:", sf.LocalPath())
	}

	return siafile.BubbledMetadata{
		Health:              health,
		LastHealthCheckTime: sf.LastHealthCheckTime(),
		ModTime:             sf.ModTime(),
		NumStuckChunks:      numStuckChunks,
		Redundancy:          redundancy,
		Size:                sf.Size(),
		StuckHealth:         stuckHealth,
	}, sf.SaveMetadata()
}

// managedCompleteBubbleUpdate completes the bubble update and updates and/or
// removes it from the renter's bubbleUpdates.
//
// TODO: bubbleUpdatesMu is in violation of conventions, needs to be moved to
// its own object to have its own mu.
func (r *Renter) managedCompleteBubbleUpdate(siaPath modules.SiaPath) (err error) {
	r.bubbleUpdatesMu.Lock()
	defer r.bubbleUpdatesMu.Unlock()
	defer func() {
		err = r.saveBubbleUpdates()
	}()

	// Check current status
	siaPathStr := siaPath.String()
	status := r.bubbleUpdates[siaPathStr]

	// If the status is 'bubbleActive', delete the status and return.
	if status == bubbleActive {
		delete(r.bubbleUpdates, siaPathStr)
		return nil
	}
	// If the status is not 'bubbleActive', and the status is also not
	// 'bubblePending', this is an error. There should be a status, and it
	// should either be active or pending.
	if status != bubblePending {
		build.Critical("invalid bubble status", status)
		return nil
	}
	// The status is bubblePending, switch the status to bubbleActive.
	r.bubbleUpdates[siaPathStr] = bubbleActive

	// Launch a thread to do another bubble on this directory, as there was a
	// bubble pending waiting for the current bubble to complete.
	go func() {
		err := r.tg.Add()
		if err != nil {
			return
		}
		defer r.tg.Done()

		r.managedPerformBubbleMetadata(siaPath)
	}()
	return nil
}

// managedDirectoryMetadata reads the directory metadata and returns the bubble
// metadata
func (r *Renter) managedDirectoryMetadata(siaPath modules.SiaPath) (siadir.Metadata, error) {
	// Check for bad paths and files
	fi, err := os.Stat(siaPath.SiaDirSysPath(r.staticFilesDir))
	if err != nil {
		return siadir.Metadata{}, err
	}
	if !fi.IsDir() {
		return siadir.Metadata{}, fmt.Errorf("%v is not a directory", siaPath)
	}

	//  Open SiaDir
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil && err.Error() == siadir.ErrUnknownPath.Error() {
		// If siadir doesn't exist create one
		siaDir, err = r.staticDirSet.NewSiaDir(siaPath)
		if err != nil {
			return siadir.Metadata{}, err
		}
	} else if err != nil {
		return siadir.Metadata{}, err
	}
	defer siaDir.Close()

	return siaDir.Metadata(), nil
}

// threadedBubbleMetadata is the thread safe method used to call
// managedBubbleMetadata when the call does not need to be blocking
func (r *Renter) threadedBubbleMetadata(siaPath modules.SiaPath) {
	if err := r.tg.Add(); err != nil {
		return
	}
	defer r.tg.Done()
	if err := r.managedBubbleMetadata(siaPath); err != nil {
		r.log.Debugln("WARN: error with bubbling metadata:", err)
	}
}

// managedPerformBubbleMetadata will bubble the metadata without checking the
// bubble preparation.
func (r *Renter) managedPerformBubbleMetadata(siaPath modules.SiaPath) (err error) {
	// Make sure we call threadedBubbleMetadata on the parent once we are done.
	defer func() error {
		// Complete bubble
		err = r.managedCompleteBubbleUpdate(siaPath)
		if err != nil {
			return errors.AddContext(err, "error in completing bubble")
		}
		// Continue with parent dir if we aren't in the root dir already.
		if siaPath.IsRoot() {
			return nil
		}
		parentDir, err := siaPath.Dir()
		if err != nil {
			return errors.AddContext(err, "failed to defer threadedBubbleMetadata on parent dir")
		}
		go r.threadedBubbleMetadata(parentDir)
		return nil
	}()

	// Calculate the new metadata values of the directory
	metadata, err := r.managedCalculateDirectoryMetadata(siaPath)
	if err != nil {
		e := fmt.Sprintf("could not calculate the metadata of directory %v", siaPath.SiaDirSysPath(r.staticFilesDir))
		return errors.AddContext(err, e)
	}

	// Update directory metadata with the health information. Don't return here
	// to avoid skipping the repairNeeded and stuckChunkFound signals.
	siaDir, err := r.staticDirSet.Open(siaPath)
	if err != nil {
		e := fmt.Sprintf("could not open directory %v", siaPath.SiaDirSysPath(r.staticFilesDir))
		err = errors.AddContext(err, e)
	} else {
		defer siaDir.Close()
		err = siaDir.UpdateMetadata(metadata)
		if err != nil {
			e := fmt.Sprintf("could not update the metadata of the  directory %v", siaPath.SiaDirSysPath(r.staticFilesDir))
			err = errors.AddContext(err, e)
		}
	}

	// If we are at the root directory then check if any files were found in
	// need of repair or and stuck chunks and trigger the appropriate repair
	// loop. This is only done at the root directory as the repair and stuck
	// loops start at the root directory so there is no point triggering them
	// until the root directory is updated
	if siaPath.IsRoot() {
		if metadata.AggregateHealth >= RepairThreshold {
			select {
			case r.uploadHeap.repairNeeded <- struct{}{}:
			default:
			}
		}
		if metadata.AggregateNumStuckChunks > 0 {
			select {
			case r.uploadHeap.stuckChunkFound <- struct{}{}:
			default:
			}
		}
	}
	return err
}

// managedBubbleMetadata calculates the updated values of a directory's metadata
// and updates the siadir metadata on disk then calls threadedBubbleMetadata on
// the parent directory so that it is only blocking for the current directory
func (r *Renter) managedBubbleMetadata(siaPath modules.SiaPath) error {
	// Check if bubble is needed
	proceedWithBubble := r.managedPrepareBubble(siaPath)
	if !proceedWithBubble {
		return nil
	}
	return r.managedPerformBubbleMetadata(siaPath)
}
