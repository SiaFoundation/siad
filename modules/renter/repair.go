package renter

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem"
	"go.sia.tech/siad/types"
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

// managedAddRandomStuckChunks will try and add up to
// maxRandomStuckChunksAddToHeap random stuck chunks to the upload heap
func (r *Renter) managedAddRandomStuckChunks(hosts map[string]struct{}) ([]modules.SiaPath, error) {
	var dirSiaPaths []modules.SiaPath
	// Remember number of stuck chunks we are starting with
	prevNumStuckChunks, prevNumRandomStuckChunks := r.uploadHeap.managedNumStuckChunks()
	// Check if there is space in the heap. There is space if the number of
	// random stuck chunks has not exceeded maxRandomStuckChunksInHeap and the
	// total number of stuck chunks as not exceeded maxStuckChunksInHeap
	spaceInHeap := prevNumRandomStuckChunks < maxRandomStuckChunksInHeap && prevNumStuckChunks < maxStuckChunksInHeap
	for i := 0; i < maxRandomStuckChunksAddToHeap && spaceInHeap; i++ {
		// Randomly get directory with stuck files
		dirSiaPath, err := r.managedStuckDirectory()
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to get random stuck directory")
		}

		// Get Random stuck file from directory
		siaPath, err := r.managedStuckFile(dirSiaPath)
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to get random stuck file in dir "+dirSiaPath.String())
		}

		// Add stuck chunk to upload heap and signal repair needed
		err = r.managedBuildAndPushRandomChunk(siaPath, hosts, targetStuckChunks, r.repairMemoryManager)
		if err != nil {
			return dirSiaPaths, errors.AddContext(err, "unable to push random stuck chunk from '"+siaPath.String()+"' of '"+dirSiaPath.String()+"'")
		}

		// Sanity check that stuck chunks were added
		currentNumStuckChunks, currentNumRandomStuckChunks := r.uploadHeap.managedNumStuckChunks()
		if currentNumRandomStuckChunks <= prevNumRandomStuckChunks {
			// If the number of stuck chunks in the heap is not increasing
			// then break out of this loop in order to prevent getting stuck
			// in an infinite loop
			break
		}

		// Remember the directory so bubble can be called on it at the end of
		// the iteration
		dirSiaPaths = append(dirSiaPaths, dirSiaPath)
		r.repairLog.Printf("Added %v stuck chunks from %s", currentNumRandomStuckChunks-prevNumRandomStuckChunks, dirSiaPath.String())
		prevNumStuckChunks = currentNumStuckChunks
		prevNumRandomStuckChunks = currentNumRandomStuckChunks
		spaceInHeap = prevNumRandomStuckChunks < maxRandomStuckChunksInHeap && prevNumStuckChunks < maxStuckChunksInHeap
	}
	return dirSiaPaths, nil
}

// managedAddStuckChunksFromStuckStack will try and add up to
// maxStuckChunksInHeap stuck chunks to the upload heap from the files in the
// stuck stack.
func (r *Renter) managedAddStuckChunksFromStuckStack(hosts map[string]struct{}) ([]modules.SiaPath, error) {
	var dirSiaPaths []modules.SiaPath
	offline, goodForRenew, _ := r.managedContractUtilityMaps()
	numStuckChunks, _ := r.uploadHeap.managedNumStuckChunks()
	for r.stuckStack.managedLen() > 0 && numStuckChunks < maxStuckChunksInHeap {
		// Pop the first file SiaPath
		siaPath := r.stuckStack.managedPop()

		// Add stuck chunks to uploadHeap
		err := r.managedAddStuckChunksToHeap(siaPath, hosts, offline, goodForRenew)
		if err != nil && !errors.Contains(err, errNoStuckChunks) {
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
		numStuckChunks, _ = r.uploadHeap.managedNumStuckChunks()
	}
	return dirSiaPaths, nil
}

// managedAddStuckChunksToHeap tries to add as many stuck chunks from a siafile
// to the upload heap as possible
func (r *Renter) managedAddStuckChunksToHeap(siaPath modules.SiaPath, hosts map[string]struct{}, offline, goodForRenew map[string]bool) (err error) {
	// Open File
	sf, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return fmt.Errorf("unable to open siafile %v, error: %v", siaPath, err)
	}
	defer func() {
		err = errors.Compose(err, sf.Close())
	}()

	// Check if there are still stuck chunks to repair
	if sf.NumStuckChunks() == 0 {
		return errNoStuckChunks
	}

	// Build unfinished stuck chunks
	var allErrors error
	unfinishedStuckChunks := r.managedBuildUnfinishedChunks(sf, hosts, targetStuckChunks, offline, goodForRenew, r.repairMemoryManager)
	defer func() {
		// Close out remaining file entries
		for _, chunk := range unfinishedStuckChunks {
			allErrors = errors.Compose(allErrors, chunk.fileEntry.Close())
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
		pushed, err := r.managedPushChunkForRepair(chunk, chunkTypeLocalChunk)
		if err != nil {
			return errors.Compose(allErrors, err, chunk.fileEntry.Close())
		}
		if !pushed {
			// Stuck chunk unable to be added. Close the file entry of that
			// chunk
			allErrors = errors.Compose(allErrors, chunk.fileEntry.Close())
			continue
		}
		stuckChunksAdded++
	}
	if stuckChunksAdded > 0 {
		r.repairLog.Printf("Added %v stuck chunks from %s to the repair heap", stuckChunksAdded, siaPath.String())
	}

	// check if there are more stuck chunks in the file
	if len(unfinishedStuckChunks) > 0 {
		r.stuckStack.managedPush(siaPath)
	}
	return allErrors
}

// managedOldestHealthCheckTime finds the lowest level directory tree that
// contains the oldest LastHealthCheckTime.
func (r *Renter) managedOldestHealthCheckTime() (modules.SiaPath, time.Time, error) {
	// Check the siadir metadata for the root files directory
	siaPath := modules.RootSiaPath()
	metadata, err := r.managedDirectoryMetadata(siaPath)
	if err != nil {
		return modules.SiaPath{}, time.Time{}, err
	}

	// Follow the path of oldest LastHealthCheckTime to the lowest level directory
	// tree defined by the batch constants
	for (metadata.AggregateNumSubDirs > healthLoopNumBatchSubDirs || metadata.AggregateNumFiles > healthLoopNumBatchFiles) && metadata.NumSubDirs > 0 {
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

			// If the AggregateLastHealthCheckTime for the sub directory is after the
			// current directory's AggregateLastHealthCheckTime then we will want to
			// continue since we want to follow the path of oldest
			// AggregateLastHealthCheckTime
			isOldestAggregate := subMetadata.AggregateLastHealthCheckTime.After(metadata.AggregateLastHealthCheckTime)
			// Whenever the node stops there is a chance the directory tree is not
			// fully updated if there are bubbles pending. With this in mind we also
			// want to confirm that the current directory's LastHealthCheckTime is
			// older than the sub directory's AggregateLastHealthCheckTime as well.
			isOldestDirectory := subMetadata.AggregateLastHealthCheckTime.After(metadata.LastHealthCheckTime)
			// The isOldestDirectory condition is only a valid check if we have not
			// already updated the metadata for a sub directory. As soon as we have
			// updated the metadata once, we have confirmed we are not going to get an
			// incorrect LastHealthCheckTime due to the metadatas being out of date
			// from a shutdown when there were pending bubbles.
			if isOldestAggregate && (isOldestDirectory || updated) {
				continue
			}

			// Update the metadata and siaPath to follow older path. We do not break
			// out of the loop just because we have updated these values as we might
			// find an even older path to follow.
			updated = true
			metadata = subMetadata
			siaPath = subDirPath
		}

		// If the values were never updated with any of the sub directory values
		// then return as we are in the directory we are looking for
		if !updated {
			// We return the LastHealthCheckTime here because at this point we should
			// actually be in the oldest directory.
			r.log.Debugf("Health Loop found LHCT OldestDir %v, LHCT %v, ALHCT %v", siaPath, metadata.LastHealthCheckTime, metadata.AggregateLastHealthCheckTime)
			return siaPath, metadata.LastHealthCheckTime, nil
		}
	}

	// Returning the AggregateLastHealthCheckTime here because we stopped
	// traversing the filesystem via the above for loop. This doesn't mean that we
	// have necessarily ended up in the oldest directory but we are on the oldest
	// subtree so return the AggregateLastHealthCheckTime
	r.log.Debugf("Health Loop found ALHCT OldestDir %v, LHCT %v, ALHCT %v", siaPath, metadata.LastHealthCheckTime, metadata.AggregateLastHealthCheckTime)
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

		directories, err := r.managedDirList(siaPath)
		if err != nil {
			return modules.SiaPath{}, err
		}
		// Sanity check that there is at least the current directory
		if len(directories) == 0 {
			build.Critical("No directories returned from DirList", siaPath.String())
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
				r.log.Println("WARN: ended up in directory with no stuck chunks that is not root directory:", siaPath)
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
	siaDir, err := r.staticFileSystem.OpenSiaDir(dirSiaPath)
	if err != nil {
		return modules.SiaPath{}, errors.AddContext(err, "unable to open siaDir "+dirSiaPath.String())
	}
	defer func() {
		err = errors.Compose(err, siaDir.Close())
	}()
	metadata, err := siaDir.Metadata()
	if err != nil {
		return modules.SiaPath{}, err
	}
	aggregateNumStuckChunks := metadata.AggregateNumStuckChunks
	numStuckChunks := metadata.NumStuckChunks
	numFiles := metadata.NumFiles
	if aggregateNumStuckChunks == 0 || numStuckChunks == 0 || numFiles == 0 {
		// If the number of stuck chunks or number of files is zero then this
		// directory should not have been used to find a stuck file.
		//
		// Queue a bubble to bubble the directory, ignore the return channel as we
		// do not want to block on this update.
		_ = r.staticBubbleScheduler.callQueueBubble(dirSiaPath)
		err = fmt.Errorf("managedStuckFile should not have been called on %v, AggregateNumStuckChunks: %v, NumStuckChunks: %v, NumFiles: %v", dirSiaPath.String(), aggregateNumStuckChunks, numStuckChunks, numFiles)
		return modules.SiaPath{}, err
	}

	// Use rand to decide which file to select. We can chose a file by
	// subtracting the number of stuck chunks a file has from rand and if rand
	// gets to 0 or less we choose that file
	rand := fastrand.Intn(int(aggregateNumStuckChunks))

	// Read the directory, using ReadDir so we don't read all the siafiles
	// unless we need to
	fileinfos, err := r.staticFileSystem.ReadDir(dirSiaPath)
	if err != nil {
		return modules.SiaPath{}, errors.AddContext(err, "unable to open siadir: "+dirSiaPath.String())
	}
	// Iterate over the fileinfos
	for _, fi := range fileinfos {
		// Check for SiaFile
		if fi.IsDir() || filepath.Ext(fi.Name()) != modules.SiaFileExtension {
			continue
		}

		// Get SiaPath
		sp, err := dirSiaPath.Join(strings.TrimSuffix(fi.Name(), modules.SiaFileExtension))
		if err != nil {
			return modules.SiaPath{}, errors.AddContext(err, "unable to join the siapath with the file: "+fi.Name())
		}

		// Open SiaFile, grab the number of stuck chunks and close the file
		f, err := r.staticFileSystem.OpenSiaFile(sp)
		if err != nil {
			return modules.SiaPath{}, errors.AddContext(err, "could not open siafileset for "+sp.String())
		}
		numStuckChunks := int(f.NumStuckChunks())
		if err := f.Close(); err != nil {
			return modules.SiaPath{}, errors.AddContext(err, "failed to close filenode "+sp.String())
		}

		// Check if stuck
		if numStuckChunks == 0 {
			continue
		}

		// Decrement rand and check if we have decremented fully
		rand = rand - numStuckChunks
		siapath = sp
		if rand < 0 {
			break
		}
	}
	if siapath.IsEmpty() {
		// If no files were selected from the directory than there is a mismatch
		// between the file metadata and the directory metadata.
		//
		// Queue a bubble to bubble the directory, ignore the return channel as we
		// do not want to block on this update.
		_ = r.staticBubbleScheduler.callQueueBubble(dirSiaPath)
		return modules.SiaPath{}, errors.New("no files selected from directory " + dirSiaPath.String())
	}
	return siapath, nil
}

// managedSubDirectories reads a directory and returns a slice of all the sub
// directory SiaPaths
func (r *Renter) managedSubDirectories(siaPath modules.SiaPath) ([]modules.SiaPath, error) {
	// Read directory
	fileinfos, err := r.staticFileSystem.ReadDir(siaPath)
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
			r.log.Println("renter shutdown before internet connection")
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
			r.repairLog.Println("WARN: error adding stuck chunks to repair heap from files with previously successful stuck repair jobs:", err)
		}
		dirSiaPaths = append(dirSiaPaths, stuckStackDirSiaPaths...)

		// Try add random stuck chunks to upload heap
		randomDirSiaPaths, err := r.managedAddRandomStuckChunks(hosts)
		if err != nil {
			r.repairLog.Println("WARN: error adding random stuck chunks to upload heap:", err)
		}
		dirSiaPaths = append(dirSiaPaths, randomDirSiaPaths...)

		// Check if any stuck chunks were added to the upload heap
		numStuckChunks, _ := r.uploadHeap.managedNumStuckChunks()
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
		bubblePaths := r.newUniqueRefreshPaths()
		for _, dirSiaPath := range dirSiaPaths {
			err = bubblePaths.callAdd(dirSiaPath)
			if err != nil {
				r.repairLog.Printf("Error adding refresh path of %s: %v", dirSiaPath.String(), err)
			}
		}
		bubblePaths.callRefreshAllBlocking()
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
			r.log.Println("WARN: Could not find oldest health check time:", err)
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
			r.log.Printf("Health loop sleeping for %v, lastHealthCheckTime %v, directory %v", sleepDuration, lastHealthCheckTime, siaPath)
			wakeSignal := time.After(sleepDuration)
			select {
			case <-r.tg.StopChan():
				return
			case <-wakeSignal:
			}
		}

		// Prepare the subtree for being bubbled
		r.log.Debugf("Preparing subtree '%v' for bubble", siaPath)
		urp, err := r.callPrepareForBubble(siaPath, false)
		if err != nil {
			// Log the error
			r.log.Println("Error calling callPrepareForBubble on `", siaPath.String(), "`:", err)

			// Check if urp is nil. This should only happen if the first call to Add
			// the Root dir fails.
			if urp == nil {
				// Sleep and continue
				select {
				case <-time.After(healthLoopErrorSleepDuration):
				case <-r.tg.StopChan():
					return
				}
				continue
			}
		}

		// Sanity check that we have both a urp and it has directories listed in its
		// childDir map.
		if urp == nil || urp.callNumChildDirs() == 0 {
			// This should never happen, build.Critical and sleep to prevent potential
			// rapid cycling.
			msg := fmt.Sprintf("WARN: No refresh paths returned from '%v'", siaPath)
			build.Critical(msg)
			select {
			case <-time.After(healthLoopErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}
		r.log.Printf("Calling bubble on the subtree '%v', # bubbles %v", siaPath, urp.callNumChildDirs())
		urp.callRefreshAllBlocking()
	}
}

// callPrepareForBubble prepares a directory for the Health Loop to call bubble
// on and returns a uniqueRefreshPaths including all the paths of the
// directories in the subtree that need to be updated. This includes updating
// the LastHealthCheckTime for the supplied root directory.
//
// This method will at a minimum return a uniqueRefreshPaths with the rootDir
// added.
//
// If the force boolean is supplied, the LastHealthCheckTime of the directories
// will be ignored so all directories will be considered.
func (r *Renter) callPrepareForBubble(rootDir modules.SiaPath, force bool) (*uniqueRefreshPaths, error) {
	// Initiate helpers
	urp := r.newUniqueRefreshPaths()
	aggregateLastHealthCheckTime := time.Now()

	// Add the rootDir to urp.
	err := urp.callAdd(rootDir)
	if err != nil {
		return nil, errors.AddContext(err, "unable to add initial rootDir to uniqueRefreshPaths")
	}

	// Define DirectoryInfo function
	var mu sync.Mutex
	dlf := func(di modules.DirectoryInfo) {
		mu.Lock()
		defer mu.Unlock()

		// Skip any directories that have been updated recently
		if !force && time.Since(di.LastHealthCheckTime) < healthCheckInterval {
			// Track the LastHealthCheckTime of the skipped directory
			if di.LastHealthCheckTime.Before(aggregateLastHealthCheckTime) {
				aggregateLastHealthCheckTime = di.LastHealthCheckTime
			}
			return
		}
		// Add the directory to uniqueRefreshPaths
		addErr := urp.callAdd(di.SiaPath)
		if addErr != nil {
			r.log.Printf("WARN: unable to add siapath `%v` to uniqueRefreshPaths; err: %v", di.SiaPath, addErr)
			err = errors.Compose(err, addErr)
			return
		}
	}

	// Execute the function on the FileSystem
	errList := r.staticFileSystem.CachedList(rootDir, true, func(modules.FileInfo) {}, dlf)
	if errList != nil {
		err = errors.Compose(err, errList)
		// Still return the uniqueRefreshPaths as we added at least the root dir and
		// we should return.
		return urp, errors.AddContext(err, "unable to get cached list of sub directories")
	}

	// Update the root directory's LastHealthCheckTime to signal that this sub
	// tree has been updated
	entry, openErr := r.staticFileSystem.OpenSiaDir(rootDir)
	if openErr != nil {
		return urp, errors.Compose(err, openErr)
	}
	return urp, errors.Compose(err, entry.UpdateLastHealthCheckTime(aggregateLastHealthCheckTime, time.Now()), entry.Close())
}

// managedUpdateFileMetadatasParams updates the metadata of all siafiles within
// a dir with the provided parameters.  This can be very expensive for large
// directories and should therefore only happen sparingly.
func (r *Renter) managedUpdateFileMetadatasParams(dirSiaPath modules.SiaPath, offlineMap map[string]bool, goodForRenewMap map[string]bool, contracts map[string]modules.RenterContract, used []types.SiaPublicKey) error {
	// Read the fileinfos from the directory
	fis, err := r.staticFileSystem.ReadDir(dirSiaPath)
	if err != nil {
		return errors.AddContext(err, "managedUpdateFileMetadatas: failed to read dir")
	}

	// Define common variables
	var errs error
	var errMU sync.Mutex
	fileSiaPathChan := make(chan modules.SiaPath, numBubbleWorkerThreads)

	// Define the fileWorker
	fileWorker := func() {
		for fileSiaPath := range fileSiaPathChan {
			err := func() error {
				sf, err := r.staticFileSystem.OpenSiaFile(fileSiaPath)
				if err != nil {
					return err
				}
				err = r.managedUpdateFileMetadata(sf, offlineMap, goodForRenewMap, contracts, used)
				return errors.Compose(err, sf.Close())
			}()
			errMU.Lock()
			errs = errors.Compose(errs, err)
			errMU.Unlock()
		}
	}

	// Launch file workers
	var wg sync.WaitGroup
	for i := 0; i < numBubbleWorkerThreads; i++ {
		wg.Add(1)
		go func() {
			fileWorker()
			wg.Done()
		}()
	}

	// Update the file metadatas
	for _, fi := range fis {
		ext := filepath.Ext(fi.Name())
		if ext != modules.SiaFileExtension {
			continue
		}
		fName := strings.TrimSuffix(fi.Name(), modules.SiaFileExtension)
		fileSiaPath, err := dirSiaPath.Join(fName)
		if err != nil {
			r.log.Println("managedUpdateFileMetadatas: unable to join siapath with dirpath", err)
			continue
		}
		// Send fileSiaPath to the file workers
		select {
		case fileSiaPathChan <- fileSiaPath:
		case <-r.tg.StopChan():
			close(fileSiaPathChan)
			wg.Wait()
			return errors.AddContext(errs, "renter shutdown")
		}
	}

	// Close the chan and wait for the workers to finish
	close(fileSiaPathChan)
	wg.Wait()
	return errs
}

// managedUpdateFileMetadata updates the metadata of a siafile.
func (r *Renter) managedUpdateFileMetadata(sf *filesystem.FileNode, offlineMap, goodForRenew map[string]bool, contracts map[string]modules.RenterContract, used []types.SiaPublicKey) (err error) {
	// Update the siafile's used hosts.
	if err := sf.UpdateUsedHosts(used); err != nil {
		return errors.AddContext(err, "WARN: Could not update used hosts")
	}
	// Update cached redundancy values.
	_, _, err = sf.Redundancy(offlineMap, goodForRenew)
	if err != nil {
		return errors.AddContext(err, "WARN: Could not update cached redundancy")
	}
	// Update cached health values.
	_, _, _, _, _, _, _ = sf.Health(offlineMap, goodForRenew)
	// Set the LastHealthCheckTime
	sf.SetLastHealthCheckTime()
	// Update the cached expiration of the siafile.
	_ = sf.Expiration(contracts)
	// Save the metadata.
	err = sf.SaveMetadata()
	if err != nil {
		return err
	}
	return nil
}
