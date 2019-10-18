package renter

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// TODO - once bubbling metadata has been updated to be more I/O
// efficient this code should be removed and we should call bubble when
// we clean up the upload chunk after a successful repair.

var (
	// errNoStuckFiles is a helper to indicate that there are no stuck files in
	// the renter's directory
	errNoStuckFiles = errors.New("no stuck files")

	// errNoStuckChunks is a helper to indicate that there are no stuck chunks
	// in a siafile
	errNoStuckChunks = errors.New("no stuck chunks")
)

// managedAddRandomStuckChunks will try and add up to maxStuckChunksInHeap
// random stuck chunks to the upload heap
func (r *Renter) managedAddRandomStuckChunks(hosts map[string]struct{}) ([]modules.SiaPath, error) {
	var dirSiaPaths []modules.SiaPath
	// Remember number of stuck chunks we are starting with
	prevNumStuckChunks := r.uploadHeap.managedNumStuckChunks()
	for prevNumStuckChunks < maxStuckChunksInHeap {
		// Randomly get directory with stuck files
		dirSiaPath, err := r.managedStuckDirectory()
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to get random stuck directory")
		}

		// Get Random stuck file from directory
		siaPath, err := r.managedStuckFile(dirSiaPath)
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to get random stuck file")
		}

		// Add stuck chunk to upload heap and signal repair needed
		err = r.managedBuildAndPushRandomChunk(siaPath, hosts, targetStuckChunks)
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to push random stuck chunk")
		}

		// Sanity check that stuck chunks were added
		currentNumStuckChunks := r.uploadHeap.managedNumStuckChunks()
		if currentNumStuckChunks <= prevNumStuckChunks {
			// If the number of stuck chunks in the heap is not increasing
			// then break out of this loop in order to prevent getting stuck
			// in an infinite loop
			break
		}

		// Remember the directory so bubble can be called on it at the end of
		// the iteration
		dirSiaPaths = append(dirSiaPaths, dirSiaPath)
		r.log.Debugf("Added %v stuck chunks from directory `%s`", currentNumStuckChunks-prevNumStuckChunks, dirSiaPath.String())
		prevNumStuckChunks = currentNumStuckChunks
	}
	return dirSiaPaths, nil
}

// managedAddStuckChunksFromStuckStack will try and add up to
// maxStuckChunksInHeap stuck chunks to the upload heap from the files in the
// stuck stack.
func (r *Renter) managedAddStuckChunksFromStuckStack(hosts map[string]struct{}) ([]modules.SiaPath, error) {
	var dirSiaPaths []modules.SiaPath
	offline, goodForRenew, _ := r.managedContractUtilityMaps()
	for r.stuckStack.managedLen() > 0 && r.uploadHeap.managedNumStuckChunks() < maxStuckChunksInHeap {
		// Pop the first file SiaPath
		siaPath := r.stuckStack.managedPop()

		// Add stuck chunks to uploadHeap
		err := r.managedAddStuckChunksToHeap(siaPath, hosts, offline, goodForRenew)
		if err != nil && err != errNoStuckChunks {
			return dirSiaPaths, errors.AddContext(err, "unable to add stuck chunks to heap")
		}

		// Since we either added stuck chunks to the heap from this file,
		// there are no stuck chunks left in the file, or all the stuck
		// chunks for the file are already being worked on, remember the
		// directory so we can call bubble on it at the end of this
		// iteration of the stuck loop to update the filesystem
		dirSiaPath, err := siaPath.Dir()
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to get directory siapath")
		}
		dirSiaPaths = append(dirSiaPaths, dirSiaPath)
	}
	return dirSiaPaths, nil
}

// managedAddStuckChunksToHeap tries to add as many stuck chunks from a siafile
// to the upload heap as possible
func (r *Renter) managedAddStuckChunksToHeap(siaPath modules.SiaPath, hosts map[string]struct{}, offline, goodForRenew map[string]bool) error {
	// Open File
	sf, err := r.staticFileSet.Open(siaPath)
	if err != nil {
		return fmt.Errorf("unable to open siafile %v, error: %v", siaPath, err)
	}
	defer sf.Close()

	// Check if there are still stuck chunks to repair
	if sf.NumStuckChunks() == 0 {
		return errNoStuckChunks
	}

	// Build unfinished stuck chunks
	var allErrors error
	unfinishedStuckChunks := r.managedBuildUnfinishedChunks(sf, hosts, targetStuckChunks, offline, goodForRenew)
	defer func() {
		// Close out remaining file entries
		for _, chunk := range unfinishedStuckChunks {
			if err = chunk.fileEntry.Close(); err != nil {
				// If there is an error log it and append to the other errors so
				// that we close as many files as possible
				r.log.Println("WARN: unable to close file:", err)
				allErrors = errors.Compose(allErrors, err)
			}
		}
	}()

	// Add up to maxStuckChunksInHeap stuck chunks to the upload heap
	var chunk *unfinishedUploadChunk
	stuckChunksAdded := 0
	for len(unfinishedStuckChunks) > 0 && stuckChunksAdded < maxStuckChunksInHeap {
		chunk = unfinishedStuckChunks[0]
		unfinishedStuckChunks = unfinishedStuckChunks[1:]
		chunk.stuckRepair = true
		chunk.fileRecentlySuccessful = true
		if !r.uploadHeap.managedPush(chunk) {
			// Stuck chunk unable to be added. Close the file entry of that
			// chunk
			if err = chunk.fileEntry.Close(); err != nil {
				// If there is an error log it and append to the other errors so
				// that we close as many files as possible
				r.log.Println("WARN: unable to close file:", err)
				allErrors = errors.Compose(allErrors, err)
			}
			continue
		}
		stuckChunksAdded++
	}

	// check if there are more stuck chunks in the file
	if len(unfinishedStuckChunks) > 0 {
		r.stuckStack.managedPush(siaPath)
	}
	return allErrors
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
			if subMetadata.AggregateLastHealthCheckTime.After(metadata.AggregateLastHealthCheckTime) {
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
			return siaPath, metadata.AggregateLastHealthCheckTime, nil
		}
	}

	return siaPath, metadata.AggregateLastHealthCheckTime, nil
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

		directories, err := r.DirList(siaPath)
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
		emptyDir := len(directories) == 1 && directories[0].NumFiles == 0
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
			if rand < 0 {
				break
			}
		}
	}
}

// managedStuckFile finds a weighted random stuck file from a directory based on
// the number of stuck chunks in the stuck files of the directory
func (r *Renter) managedStuckFile(dirSiaPath modules.SiaPath) (siapath modules.SiaPath, err error) {
	// Grab Aggregate number of stuck chunks from the directory
	//
	// NOTE: using the aggregate number of stuck chunks assumes that the
	// directory and the files within the directory are in sync. This is ok to
	// do as the risks associated with being out of sync are low.
	siaDir, err := r.staticDirSet.Open(dirSiaPath)
	if err != nil {
		return modules.SiaPath{}, err
	}
	defer siaDir.Close()
	metadata := siaDir.Metadata()
	aggregateNumStuckChunks := metadata.AggregateNumStuckChunks
	if aggregateNumStuckChunks == 0 {
		return modules.SiaPath{}, errors.New("No stuck chunks found in stuck files")
	}
	numFiles := metadata.NumFiles
	if numFiles == 0 {
		return modules.SiaPath{}, errors.New("no files in directory")
	}

	// Use rand to decide which file to select. We can chose a file by
	// subtracting the number of stuck chunks a file has from rand and if rand
	// gets to 0 or less we choose that file
	rand := fastrand.Intn(int(aggregateNumStuckChunks))

	// Read the directory, using ReadDir so we don't read all the siafiles
	// unless we need to
	dir := dirSiaPath.SiaDirSysPath(r.staticFilesDir)
	fileinfos, err := ioutil.ReadDir(dir)
	if err != nil {
		return modules.SiaPath{}, err
	}
	// Iterate over the fileinfos
	for _, fi := range fileinfos {
		// Check for SiaFile
		if fi.IsDir() || filepath.Ext(fi.Name()) != modules.SiaFileExtension {
			continue
		}

		// Get SiaPath
		var sp modules.SiaPath
		err = sp.FromSysPath(filepath.Join(dir, fi.Name()), r.staticFilesDir)
		if err != nil {
			return modules.SiaPath{}, err
		}

		// Open SiaFile, grab the number of stuck chunks and close the file
		f, err := r.staticFileSet.Open(sp)
		if err != nil {
			return modules.SiaPath{}, err
		}
		numStuckChunks := int(f.NumStuckChunks())
		err = f.Close()
		if err != nil {
			return modules.SiaPath{}, err
		}

		//Check if stuck
		if numStuckChunks == 0 {
			continue
		}

		// Decrement rand and check if we have decremented fully
		rand = rand - numStuckChunks
		if rand < 0 {
			siapath = sp
			break
		}
	}
	return siapath, nil
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

// threadedStuckFileLoop works through the renter directory and finds the stuck
// chunks and tries to repair them
func (r *Renter) threadedStuckFileLoop() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	// Loop until the renter has shutdown or until there are no stuck chunks
	for {
		// Return if the renter has shut down.
		select {
		case <-r.tg.StopChan():
			return
		default:
		}

		// Wait until the renter is online to proceed.
		if !r.managedBlockUntilOnline() {
			// The renter shut down before the internet connection was restored.
			r.log.Debugln("renter shutdown before internet connection")
			return
		}

		// As we add stuck chunks to the upload heap we want to remember the
		// directories they came from so we can call bubble to update the
		// filesystem
		var dirSiaPaths []modules.SiaPath

		// Refresh the hosts and workers before adding stuck chunks to the
		// upload heap
		hosts := r.managedRefreshHostsAndWorkers()

		// Try and add stuck chunks from the stuck stack. We try and add these
		// first as they will be from files that previously had a successful
		// stuck chunk repair. The previous success gives us more confidence
		// that it is more likely additional stuck chunks from these files will
		// be successful compared to a random stuck chunk from the renter's
		// directory.
		stuckStackDirSiaPaths, err := r.managedAddStuckChunksFromStuckStack(hosts)
		if err != nil {
			r.log.Println("WARN: error adding stuck chunks to upload heap from stuck stack:", err)
		}
		dirSiaPaths = append(dirSiaPaths, stuckStackDirSiaPaths...)

		// Try add random stuck chunks to upload heap
		randomDirSiaPaths, err := r.managedAddRandomStuckChunks(hosts)
		if err != nil {
			r.log.Println("WARN: error adding random stuck chunks to upload heap:", err)
		}
		dirSiaPaths = append(dirSiaPaths, randomDirSiaPaths...)

		// Check if any stuck chunks were added to the upload heap
		numStuckChunks := r.uploadHeap.managedNumStuckChunks()
		if numStuckChunks == 0 {
			// Block until new work is required.
			select {
			case <-r.tg.StopChan():
				// The renter has shut down.
				return
			case <-r.uploadHeap.stuckChunkFound:
				// Health Loop found stuck chunk
			case <-r.uploadHeap.stuckChunkSuccess:
				// Stuck chunk was successfully repaired.
			}
			continue
		}

		// Signal that a repair is needed because stuck chunks were added to the
		// upload heap
		select {
		case r.uploadHeap.repairNeeded <- struct{}{}:
		default:
		}
		r.log.Println(numStuckChunks, "stuck chunks added to the upload heap, repair signal sent")

		// Sleep until it is time to try and repair another stuck chunk
		rebuildStuckHeapSignal := time.After(repairStuckChunkInterval)
		select {
		case <-r.tg.StopChan():
			// Return if the return has been shutdown
			return
		case <-rebuildStuckHeapSignal:
			// Time to find another random chunk
		case <-r.uploadHeap.stuckChunkSuccess:
			// Stuck chunk was successfully repaired.
		}

		// Call bubble before continuing on next iteration to ensure filesystem
		// is updated.
		//
		// TODO - once bubbling metadata has been updated to be more I/O
		// efficient this code should be removed and we should call bubble when
		// we clean up the upload chunk after a successful repair.
		for _, dirSiaPath := range dirSiaPaths {
			err = r.managedBubbleMetadata(dirSiaPath)
			if err != nil {
				r.log.Println("Error calling managedBubbleMetadata on `", dirSiaPath.String(), "`:", err)
				select {
				case <-time.After(stuckLoopErrorSleepDuration):
				case <-r.tg.StopChan():
					return
				}
			}
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
		r.log.Debugln("Checking for oldest health check time")
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
			// Sleep until the least recent check is outside the check interval.
			sleepDuration := healthCheckInterval - timeSinceLastCheck
			r.log.Debugln("Health loop sleeping for", sleepDuration)
			wakeSignal := time.After(sleepDuration)
			select {
			case <-r.tg.StopChan():
				return
			case <-wakeSignal:
			}
		}
		r.log.Debug("Health Loop calling bubble on '", siaPath.String(), "'")
		err = r.managedBubbleMetadata(siaPath)
		if err != nil {
			r.log.Println("Error calling managedBubbleMetadata on `", siaPath.String(), "`:", err)
			select {
			case <-time.After(healthLoopErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
		}
	}
}
