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
	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// errNoStuckFiles is a helper to indicate that there are no stuck files in
	// the renter's directory
	errNoStuckFiles = errors.New("no stuck files")
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

// managedAddStuckChunksToHeap adds all the stuck chunks in a file to the repair
// heap
func (r *Renter) managedAddStuckChunksToHeap(siaPath modules.SiaPath) error {
	// Open File
	sf, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return fmt.Errorf("unable to open siafile %v, error: %v", siaPath, err)
	}
	defer sf.Close()
	// Add stuck chunks from file to repair heap
	files := []*siafile.SiaFileSetEntry{sf}
	hosts := r.managedRefreshHostsAndWorkers()
	offline, goodForRenew, _ := r.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{sf})
	r.managedBuildAndPushChunks(files, hosts, targetStuckChunks, offline, goodForRenew)
	return nil
}

// managedBubbleNeeded checks if a bubble is needed for a directory, updates the
// renter's bubbleUpdates map and returns a bool
func (r *Renter) managedBubbleNeeded(siaPath modules.SiaPath) (bool, error) {
	r.bubbleUpdatesMu.Lock()
	defer r.bubbleUpdatesMu.Unlock()

	// Check for bubble in bubbleUpdate map
	siaPathStr := siaPath.String()
	status, ok := r.bubbleUpdates[siaPathStr]
	if !ok {
		status = bubbleInit
		r.bubbleUpdates[siaPathStr] = status
	}

	// Update the bubble status
	var err error
	switch status {
	case bubblePending:
	case bubbleActive:
		r.bubbleUpdates[siaPathStr] = bubblePending
	case bubbleInit:
		r.bubbleUpdates[siaPathStr] = bubbleActive
		return true, nil
	default:
		err = errors.New("WARN: invalid bubble status")
	}
	return false, err
}

// managedCalculateDirectoryMetadata calculates the new values for the
// directory's metadata and tracks the value, either worst or best, for each to
// be bubbled up
func (r *Renter) managedCalculateDirectoryMetadata(siaPath modules.SiaPath) (siadir.Metadata, error) {
	// Set default metadata values to start
	metadata := siadir.Metadata{
		AggregateNumFiles:   uint64(0),
		Health:              siadir.DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		ModTime:             time.Time{},
		MinRedundancy:       math.MaxFloat64,
		NumFiles:            uint64(0),
		NumStuckChunks:      uint64(0),
		NumSubDirs:          uint64(0),
		AggregateSize:       uint64(0),
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

		var health, stuckHealth, redundancy float64
		var numStuckChunks uint64
		var lastHealthCheckTime, modTime time.Time
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
			fileMetadata, err := r.managedCalculateFileMetadata(fileSiaPath)
			if err != nil {
				r.log.Printf("failed to calculate file metadata %v: %v", fi.Name(), err)
				continue
			}

			health = fileMetadata.Health
			stuckHealth = fileMetadata.StuckHealth
			redundancy = fileMetadata.Redundancy
			numStuckChunks = fileMetadata.NumStuckChunks
			modTime = fileMetadata.ModTime

			// Update aggregate fields.
			metadata.NumFiles++
			metadata.AggregateNumFiles++
			metadata.AggregateSize += fileMetadata.Size
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

			health = dirMetadata.Health
			stuckHealth = dirMetadata.StuckHealth
			redundancy = dirMetadata.MinRedundancy
			numStuckChunks = dirMetadata.NumStuckChunks
			lastHealthCheckTime = dirMetadata.LastHealthCheckTime
			modTime = dirMetadata.ModTime

			// Update aggregate fields.
			metadata.AggregateNumFiles += dirMetadata.AggregateNumFiles
			metadata.AggregateSize += dirMetadata.AggregateSize
			metadata.NumSubDirs++
		} else {
			// Ignore everything that is not a SiaFile or a directory
			continue
		}

		// Update Health and Stuck Health
		if health > metadata.Health {
			metadata.Health = health
		}
		if stuckHealth > metadata.StuckHealth {
			metadata.StuckHealth = stuckHealth
		}
		// Update MinRedundancy
		if redundancy < metadata.MinRedundancy {
			metadata.MinRedundancy = redundancy
		}
		// Update ModTime
		if modTime.After(metadata.ModTime) {
			metadata.ModTime = modTime
		}
		// Increment NumStuckChunks
		metadata.NumStuckChunks += numStuckChunks
		// Update LastHealthCheckTime if the file or sub directory
		// lastHealthCheckTime is older (before) the current lastHealthCheckTime
		if lastHealthCheckTime.Before(metadata.LastHealthCheckTime) || lastHealthCheckTime.Equal(metadata.LastHealthCheckTime) {
			metadata.LastHealthCheckTime = lastHealthCheckTime
		}
		metadata.NumStuckChunks += numStuckChunks
	}
	// Sanity check on ModTime. If mod time is still zero it means there were no
	// files or subdirectories. Set ModTime to now since we just updated this
	// directory
	if metadata.ModTime.IsZero() {
		metadata.ModTime = time.Now()
	}

	// Sanity check on Redundancy. If MinRedundancy is still math.MaxFloat64
	// then set it to 0
	if metadata.MinRedundancy == math.MaxFloat64 {
		metadata.MinRedundancy = 0
	}

	return metadata, nil
}

// managedCalculateFileMetadata calculates and returns the necessary metadata
// information of a siafile that needs to be bubbled
func (r *Renter) managedCalculateFileMetadata(siaPath modules.SiaPath) (siafile.BubbledMetadata, error) {
	// Load the Siafile.
	sf, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return siafile.BubbledMetadata{}, err
	}
	defer sf.Close()

	// Mark sure that healthy chunks are not marked as stuck
	hostOfflineMap, hostGoodForRenewMap, _ := r.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{sf})
	// TODO: This 'MarkAllHealthyChunksAsUnstuck' function may not be necessary
	// in the long term. I believe that it was/is useful because other parts of
	// the process for marking and handling stuck chunks was not complete.
	err = sf.MarkAllHealthyChunksAsUnstuck(hostOfflineMap, hostGoodForRenewMap)
	if err != nil {
		return siafile.BubbledMetadata{}, errors.AddContext(err, "unable to mark healthy chunks as unstuck")
	}
	// Calculate file health
	health, stuckHealth, numStuckChunks := sf.Health(hostOfflineMap, hostGoodForRenewMap)
	// Update the LastHealthCheckTime
	if err := sf.UpdateLastHealthCheckTime(); err != nil {
		return siafile.BubbledMetadata{}, err
	}
	// Calculate file Redundancy and check if local file is missing and
	// redundancy is less than one
	redundancy := sf.Redundancy(hostOfflineMap, hostGoodForRenewMap)
	if _, err := os.Stat(sf.LocalPath()); os.IsNotExist(err) && redundancy < 1 {
		r.log.Debugln("File not found on disk and possibly unrecoverable:", sf.LocalPath())
	}
	metadata := siafile.CachedHealthMetadata{
		Health:      health,
		Redundancy:  redundancy,
		StuckHealth: stuckHealth,
	}
	return siafile.BubbledMetadata{
		Health:              health,
		LastHealthCheckTime: sf.LastHealthCheckTime(),
		ModTime:             sf.ModTime(),
		NumStuckChunks:      numStuckChunks,
		Redundancy:          redundancy,
		Size:                sf.Size(),
		StuckHealth:         stuckHealth,
	}, sf.UpdateCachedHealthMetadata(metadata)
}

// managedCompleteBubbleUpdate completes the bubble update and updates and/or
// removes it from the renter's bubbleUpdates.
func (r *Renter) managedCompleteBubbleUpdate(siaPath modules.SiaPath) error {
	r.bubbleUpdatesMu.Lock()
	defer r.bubbleUpdatesMu.Unlock()

	// Check current status
	siaPathStr := siaPath.String()
	status, ok := r.bubbleUpdates[siaPathStr]
	if !ok {
		// Bubble not found in map, nothing to do.
		return nil
	}

	// Update status and call new bubble or remove from bubbleUpdates and save
	switch status {
	case bubblePending:
		r.bubbleUpdates[siaPathStr] = bubbleInit
		defer func() {
			go r.threadedBubbleMetadata(siaPath)
		}()
	case bubbleActive:
		delete(r.bubbleUpdates, siaPathStr)
	default:
		return errors.New("WARN: invalid bubble status")
	}

	return r.saveBubbleUpdates()
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
	if os.IsNotExist(err) {
		// Remember initial Error
		initError := err
		// Metadata file does not exists, check if directory is empty
		fileInfos, err := ioutil.ReadDir(siaPath.SiaDirSysPath(r.staticFilesDir))
		if err != nil {
			return siadir.Metadata{}, err
		}
		// If the directory is empty and is not the root directory, assume it
		// was deleted so do not create a metadata file
		if len(fileInfos) == 0 && !siaPath.IsRoot() {
			return siadir.Metadata{}, initError
		}
		// If we are at the root directory or the directory is not empty, create
		// a metadata file
		siaDir, err = r.staticDirSet.NewSiaDir(siaPath)
	}
	if err != nil {
		return siadir.Metadata{}, err
	}
	defer siaDir.Close()

	return siaDir.Metadata(), nil
}

// managedOldestHealthCheckTime finds the lowest level directory with the oldest
// LastHealthCheckTime
func (r *Renter) managedOldestHealthCheckTime() (modules.SiaPath, time.Time, error) {
	// Check the siadir metadata for the root files directory
	siaPath := modules.RootSiaPath()
	metadata, err := r.managedDirectoryMetadata(siaPath)
	if err != nil {
		return modules.SiaPath{}, time.Time{}, err
	}

	// Follow the path of oldest LastHealthCheckTime to the lowest level
	// directory
	for metadata.NumSubDirs > 0 {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return modules.SiaPath{}, time.Time{}, errors.New("Renter shutdown before oldestHealthCheckTime could be found")
		default:
		}

		// Check for sub directories
		subDirSiaPaths, err := r.managedSubDirectories(siaPath)
		if err != nil {
			return modules.SiaPath{}, time.Time{}, err
		}

		// Find the oldest LastHealthCheckTime of the sub directories
		updated := false
		for _, subDirPath := range subDirSiaPaths {
			// Check to make sure renter hasn't been shutdown
			select {
			case <-r.tg.StopChan():
				return modules.SiaPath{}, time.Time{}, errors.New("Renter shutdown before oldestHealthCheckTime could be found")
			default:
			}

			// Check lastHealthCheckTime of sub directory
			subMetadata, err := r.managedDirectoryMetadata(subDirPath)
			if err != nil {
				return modules.SiaPath{}, time.Time{}, err
			}

			// If the LastHealthCheckTime is after current LastHealthCheckTime
			// continue since we are already in a directory with an older
			// timestamp
			if subMetadata.LastHealthCheckTime.After(metadata.LastHealthCheckTime) {
				continue
			}

			// Update LastHealthCheckTime and follow older path
			updated = true
			metadata = subMetadata
			siaPath = subDirPath
		}

		// If the values were never updated with any of the sub directory values
		// then return as we are in the directory we are looking for
		if !updated {
			return siaPath, metadata.LastHealthCheckTime, nil
		}
	}

	return siaPath, metadata.LastHealthCheckTime, nil
}

// managedStuckDirectory randomly finds a directory that contains stuck chunks
func (r *Renter) managedStuckDirectory() (modules.SiaPath, error) {
	// Iterating of the renter directory until randomly ending up in a
	// directory, break and return that directory
	siaPath := modules.RootSiaPath()
	for {
		select {
		// Check to make sure renter hasn't been shutdown
		case <-r.tg.StopChan():
			return modules.SiaPath{}, nil
		default:
		}

		directories, files, err := r.DirList(siaPath)
		if err != nil {
			return modules.SiaPath{}, err
		}
		// Sanity check that there is at least the current directory
		if len(directories) == 0 {
			build.Critical("No directories returned from DirList")
		}
		// Check if we are in an empty Directory. This will be the case before
		// any files have been uploaded so the root directory is empty. Also it
		// could happen if the only file in a directory was stuck and was very
		// recently deleted so the health of the directory has not yet been
		// updated.
		emptyDir := len(directories) == 1 && len(files) == 0
		if emptyDir {
			return siaPath, errNoStuckFiles
		}
		// Check if there are stuck chunks in this directory
		if directories[0].AggregateNumStuckChunks == 0 {
			// Log error if we are not at the root directory
			if !siaPath.IsRoot() {
				r.log.Debugln("WARN: ended up in directory with no stuck chunks that is not root directory:", siaPath)
			}
			return siaPath, errNoStuckFiles
		}
		// Check if we have reached a directory with only files
		if len(directories) == 1 {
			return siaPath, nil
		}

		// Get random int
		rand := fastrand.Intn(int(directories[0].AggregateNumStuckChunks))

		// Use rand to decide which directory to go into. Work backwards over
		// the slice of directories. Since the first element is the current
		// directory that means that it is the sum of all the files and
		// directories.  We can chose a directory by subtracting the number of
		// stuck chunks a directory has from rand and if rand gets to 0 or less
		// we choose that directory
		for i := len(directories) - 1; i >= 0; i-- {
			// If we make it to the last iteration double check that the current
			// directory has files
			if i == 0 && len(files) == 0 {
				break
			}

			// If we are on the last iteration and the directory does have files
			// then return the current directory
			if i == 0 {
				siaPath = directories[0].SiaPath
				return siaPath, nil
			}

			// Skip directories with no stuck chunks
			if directories[i].AggregateNumStuckChunks == uint64(0) {
				continue
			}

			rand = rand - int(directories[i].AggregateNumStuckChunks)
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
func (r *Renter) managedSubDirectories(siaPath modules.SiaPath) ([]modules.SiaPath, error) {
	// Read directory
	fileinfos, err := ioutil.ReadDir(siaPath.SiaDirSysPath(r.staticFilesDir))
	if err != nil {
		return nil, err
	}
	// Find all sub directory SiaPaths
	folders := make([]modules.SiaPath, 0, len(fileinfos))
	for _, fi := range fileinfos {
		if fi.IsDir() {
			subDir, err := siaPath.Join(fi.Name())
			if err != nil {
				return nil, err
			}
			folders = append(folders, subDir)
		}
	}
	return folders, nil
}

// managedWorstHealthDirectory follows the path of worst health to the lowest
// level possible
func (r *Renter) managedWorstHealthDirectory() (modules.SiaPath, float64, error) {
	// Check the health of the root files directory
	siaPath := modules.RootSiaPath()
	health, err := r.managedDirectoryMetadata(siaPath)
	if err != nil {
		return modules.SiaPath{}, 0, err
	}

	// Follow the path of worst health to the lowest level. We only want to find
	// directories with a health worse than the repairHealthThreshold to save
	// resources
	for health.Health >= siafile.RemoteRepairDownloadThreshold {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return modules.SiaPath{}, 0, errors.New("could not find worst health directory due to shutdown")
		default:
		}
		// Check for subdirectories
		subDirSiaPaths, err := r.managedSubDirectories(siaPath)
		if err != nil {
			return modules.SiaPath{}, 0, err
		}
		// If there are no sub directories, return
		if len(subDirSiaPaths) == 0 {
			return siaPath, health.Health, nil
		}

		// Check sub directory healths to find the worst health
		updated := false
		for _, subDirPath := range subDirSiaPaths {
			// Check health of sub directory
			subHealth, err := r.managedDirectoryMetadata(subDirPath)
			if err != nil {
				return modules.SiaPath{}, 0, err
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

// managedBubbleMetadata calculates the updated values of a directory's metadata
// and updates the siadir metadata on disk then calls threadedBubbleMetadata on
// the parent directory so that it is only blocking for the current directory
func (r *Renter) managedBubbleMetadata(siaPath modules.SiaPath) error {
	// Check if bubble is needed
	needed, err := r.managedBubbleNeeded(siaPath)
	if err != nil {
		return errors.AddContext(err, "error in checking if bubble is needed")
	}
	if !needed {
		return nil
	}

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
		if metadata.Health >= siafile.RemoteRepairDownloadThreshold {
			select {
			case r.uploadHeap.repairNeeded <- struct{}{}:
			default:
			}
		}
		if metadata.NumStuckChunks > 0 {
			select {
			case r.uploadHeap.stuckChunkFound <- struct{}{}:
			default:
			}
		}
	}
	return err
}

// threadedStuckFileLoop go through the renter directory and finds the stuck
// chunks and tries to repair them
func (r *Renter) threadedStuckFileLoop() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	if r.deps.Disrupt("DisableRepairAndHealthLoops") {
		return
	}

	// Loop until the renter has shutdown or until there are no stuck chunks
	for {
		// Wait until the renter is online to proceed.
		if !r.managedBlockUntilOnline() {
			// The renter shut down before the internet connection was restored.
			r.log.Debugln("renter shutdown before internet connection")
			return
		}

		// Randomly get directory with stuck files
		dirSiaPath, err := r.managedStuckDirectory()
		if err != nil && err != errNoStuckFiles {
			r.log.Debugln("WARN: error getting random stuck directory:", err)
			continue
		}
		if err == errNoStuckFiles {
			// Block until new work is required.
			select {
			case <-r.tg.StopChan():
				// The renter has shut down.
				return
			case <-r.uploadHeap.stuckChunkFound:
				// Health Loop found stuck chunk
			case siaPath := <-r.uploadHeap.stuckChunkSuccess:
				// Stuck chunk was successfully repaired. Add the rest of the file
				// to the heap
				err := r.managedAddStuckChunksToHeap(siaPath)
				if err != nil {
					r.log.Debugln("WARN: unable to add stuck chunks from file", siaPath, "to heap:", err)
				}
			}
			continue
		}

		// Refresh the worker pool and get the set of hosts that are currently
		// useful for uploading.
		hosts := r.managedRefreshHostsAndWorkers()

		// Add stuck chunk to upload heap and signal repair needed
		r.managedBuildChunkHeap(dirSiaPath, hosts, targetStuckChunks)
		r.log.Debugf("Attempting to repair stuck chunks from directory `%s`", dirSiaPath)
		select {
		case r.uploadHeap.repairNeeded <- struct{}{}:
		default:
		}

		// Sleep until it is time to try and repair another stuck chunk
		rebuildStuckHeapSignal := time.After(repairStuckChunkInterval)
		select {
		case <-r.tg.StopChan():
			// Return if the return has been shutdown
			return
		case <-rebuildStuckHeapSignal:
			// Time to find another random chunk
		case siaPath := <-r.uploadHeap.stuckChunkSuccess:
			// Stuck chunk was successfully repaired. Add the rest of the file
			// to the heap
			err := r.managedAddStuckChunksToHeap(siaPath)
			if err != nil {
				r.log.Debugln("WARN: unable to add stuck chunks from file", siaPath, "to heap:", err)
			}
		}

		// Call bubble before continuing on next iteration to ensure filesystem
		// is up to date. We do not use the upload heap's channel since bubble
		// is called when a chunk is done with its repair and since this loop
		// only typically adds one chunk at a time call bubble before the next
		// iteration is sufficient.
		r.managedBubbleMetadata(dirSiaPath)
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

	if r.deps.Disrupt("DisableRepairAndHealthLoops") {
		return
	}

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
			// If there is an error getting the lastHealthCheckTime sleep for a
			// little bit before continuing
			r.log.Debug("WARN: Could not find oldest health check time:", err)
			select {
			case <-time.After(healthLoopErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}

		// Check if the time since the last check on the least recently checked
		// folder is inside the health check interval. If so, the whole
		// filesystem has been checked recently, and we can sleep until the
		// least recent check is outside the check interval.
		timeSinceLastCheck := time.Since(lastHealthCheckTime)
		if timeSinceLastCheck < healthCheckInterval {
			// Sleep unitl the least recent check is outside the check interval.
			sleepDuration := healthCheckInterval - timeSinceLastCheck
			wakeSignal := time.After(sleepDuration)
			select {
			case <-r.tg.StopChan():
				return
			case <-wakeSignal:
			}
		}
		r.managedBubbleMetadata(siaPath)
	}
}
