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
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// errNoStuckFiles is a helper to indicate that there are no stuck files in
	// the renter's directory
	errNoStuckFiles = errors.New("no stuck files")
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
		files, err := r.FileList(siaPath, false, false)
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
