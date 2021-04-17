package renter

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem/siadir"
	"go.sia.tech/siad/modules/renter/filesystem/siafile"
)

// bubbledSiaDirMetadata is a wrapper for siadir.Metadata that also contains the
// siapath for convenience.
type bubbledSiaDirMetadata struct {
	sp modules.SiaPath
	siadir.Metadata
}

// bubbledSiaFileMetadata is a wrapper for siafile.BubbledMetadata that also
// contains the siapath for convenience.
type bubbledSiaFileMetadata struct {
	sp modules.SiaPath
	bm siafile.BubbledMetadata
}

// callCalculateDirectoryMetadata calculates the new values for the
// directory's metadata and tracks the value, either worst or best, for each to
// be bubbled up
func (r *Renter) callCalculateDirectoryMetadata(siaPath modules.SiaPath) (siadir.Metadata, error) {
	// Set default metadata values to start
	now := time.Now()
	metadata := siadir.Metadata{
		AggregateHealth:              siadir.DefaultDirHealth,
		AggregateLastHealthCheckTime: now,
		AggregateMinRedundancy:       math.MaxFloat64,
		AggregateModTime:             time.Time{},
		AggregateNumFiles:            uint64(0),
		AggregateNumStuckChunks:      uint64(0),
		AggregateNumSubDirs:          uint64(0),
		AggregateRemoteHealth:        siadir.DefaultDirHealth,
		AggregateRepairSize:          uint64(0),
		AggregateSize:                uint64(0),
		AggregateStuckHealth:         siadir.DefaultDirHealth,
		AggregateStuckSize:           uint64(0),

		Health:              siadir.DefaultDirHealth,
		LastHealthCheckTime: now,
		MinRedundancy:       math.MaxFloat64,
		ModTime:             time.Time{},
		NumFiles:            uint64(0),
		NumStuckChunks:      uint64(0),
		NumSubDirs:          uint64(0),
		RemoteHealth:        siadir.DefaultDirHealth,
		RepairSize:          uint64(0),
		Size:                uint64(0),
		StuckHealth:         siadir.DefaultDirHealth,
		StuckSize:           uint64(0),
	}
	// Read directory
	fileinfos, err := r.staticFileSystem.ReadDir(siaPath)
	if err != nil {
		r.log.Printf("WARN: Error in reading files in directory %v : %v\n", siaPath.String(), err)
		return siadir.Metadata{}, err
	}

	// Iterate over directory and collect the file and dir siapaths.
	var fileSiaPaths, dirSiaPaths []modules.SiaPath
	for _, fi := range fileinfos {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return siadir.Metadata{}, err
		default:
		}
		// Sort by file and dirs.
		ext := filepath.Ext(fi.Name())
		if ext == modules.SiaFileExtension {
			// SiaFile found.
			fName := strings.TrimSuffix(fi.Name(), modules.SiaFileExtension)
			fileSiaPath, err := siaPath.Join(fName)
			if err != nil {
				r.log.Println("unable to join siapath with dirpath while calculating directory metadata:", err)
				continue
			}
			fileSiaPaths = append(fileSiaPaths, fileSiaPath)
		} else if fi.IsDir() {
			// Directory is found, read the directory metadata file
			dirSiaPath, err := siaPath.Join(fi.Name())
			if err != nil {
				r.log.Println("unable to join siapath with dirpath while calculating directory metadata:", err)
				continue
			}
			dirSiaPaths = append(dirSiaPaths, dirSiaPath)
		}
	}

	// Grab the Files' bubbleMetadata from the cached metadata first.
	//
	// Note: We don't need to abort on error. It's likely that only one or a few
	// files failed and that the remaining metadatas are good to use.
	bubbledMetadatas, err := r.managedCachedFileMetadatas(fileSiaPaths)
	if err != nil {
		r.log.Printf("failed to calculate file metadata: %v", err)
	}

	// Get all the Directory Metadata
	//
	// Note: We don't need to abort on error. It's likely that only one or a few
	// directories failed and that the remaining metadatas are good to use.
	dirMetadatas, err := r.managedDirectoryMetadatas(dirSiaPaths)
	if err != nil {
		r.log.Printf("failed to calculate file metadata: %v", err)
	}

	for len(bubbledMetadatas)+len(dirMetadatas) > 0 {
		// Aggregate Fields
		var aggregateHealth, aggregateRemoteHealth, aggregateStuckHealth, aggregateMinRedundancy float64
		var aggregateLastHealthCheckTime, aggregateModTime time.Time
		if len(bubbledMetadatas) > 0 {
			// Get next file's metadata.
			bubbledMetadata := bubbledMetadatas[0]
			bubbledMetadatas = bubbledMetadatas[1:]
			fileSiaPath := bubbledMetadata.sp
			fileMetadata := bubbledMetadata.bm
			// If 75% or more of the redundancy is missing, register an alert
			// for the file.
			uid := string(fileMetadata.UID)
			if maxHealth := math.Max(fileMetadata.Health, fileMetadata.StuckHealth); maxHealth >= AlertSiafileLowRedundancyThreshold {
				r.staticAlerter.RegisterAlert(modules.AlertIDSiafileLowRedundancy(uid), AlertMSGSiafileLowRedundancy,
					AlertCauseSiafileLowRedundancy(fileSiaPath, maxHealth, fileMetadata.Redundancy),
					modules.SeverityWarning)
			} else {
				r.staticAlerter.UnregisterAlert(modules.AlertIDSiafileLowRedundancy(uid))
			}

			// If the file's LastHealthCheckTime is still zero, set it as now since it
			// it currently being checked.
			//
			// The LastHealthCheckTime is not a field that is initialized when a file
			// is created, so we can reach this point by one of two ways. If a file is
			// created in the directory after the health loop has decided it needs to
			// be bubbled, or a file is created in a directory that gets a bubble
			// called on it outside of the health loop before the health loop as been
			// able to set the LastHealthCheckTime.
			if fileMetadata.LastHealthCheckTime.IsZero() {
				fileMetadata.LastHealthCheckTime = time.Now()
			}

			// Update repair fields
			metadata.AggregateRepairSize += fileMetadata.RepairBytes
			metadata.AggregateStuckSize += fileMetadata.StuckBytes
			metadata.RepairSize += fileMetadata.RepairBytes
			metadata.StuckSize += fileMetadata.StuckBytes

			// Record Values that compare against sub directories
			aggregateHealth = fileMetadata.Health
			aggregateStuckHealth = fileMetadata.StuckHealth
			aggregateMinRedundancy = fileMetadata.Redundancy
			aggregateLastHealthCheckTime = fileMetadata.LastHealthCheckTime
			aggregateModTime = fileMetadata.ModTime
			if !fileMetadata.OnDisk {
				aggregateRemoteHealth = fileMetadata.Health
			}

			// Update aggregate fields.
			metadata.AggregateNumFiles++
			metadata.AggregateNumStuckChunks += fileMetadata.NumStuckChunks
			metadata.AggregateSize += fileMetadata.Size

			// Update siadir fields.
			metadata.Health = math.Max(metadata.Health, fileMetadata.Health)
			if fileMetadata.LastHealthCheckTime.Before(metadata.LastHealthCheckTime) {
				metadata.LastHealthCheckTime = fileMetadata.LastHealthCheckTime
			}
			if fileMetadata.Redundancy != -1 {
				metadata.MinRedundancy = math.Min(metadata.MinRedundancy, fileMetadata.Redundancy)
			}
			if fileMetadata.ModTime.After(metadata.ModTime) {
				metadata.ModTime = fileMetadata.ModTime
			}
			metadata.NumFiles++
			metadata.NumStuckChunks += fileMetadata.NumStuckChunks
			if !fileMetadata.OnDisk {
				metadata.RemoteHealth = math.Max(metadata.RemoteHealth, fileMetadata.Health)
			}
			metadata.Size += fileMetadata.Size
			metadata.StuckHealth = math.Max(metadata.StuckHealth, fileMetadata.StuckHealth)
		} else if len(dirMetadatas) > 0 {
			// Get next dir's metadata.
			dirMetadata := dirMetadatas[0]
			dirMetadatas = dirMetadatas[1:]

			// Check if the directory's AggregateLastHealthCheckTime is Zero. If so
			// set the time to now and call bubble on that directory to try and fix
			// the directories metadata.
			//
			// The LastHealthCheckTime is not a field that is initialized when
			// a directory is created, so we can reach this point if a directory is
			// created and gets a bubble called on it outside of the health loop
			// before the health loop has been able to set the LastHealthCheckTime.
			if dirMetadata.AggregateLastHealthCheckTime.IsZero() {
				dirMetadata.AggregateLastHealthCheckTime = time.Now()
				// Check for the dependency to disable the LastHealthCheckTime
				// correction, (LHCT = LastHealthCheckTime).
				if !r.deps.Disrupt("DisableLHCTCorrection") {
					// Queue a bubble to bubble the directory, ignore the return channel
					// as we do not want to block on this update.
					r.log.Debugf("Found zero time for ALHCT at '%v'", dirMetadata.sp)
					_ = r.staticBubbleScheduler.callQueueBubble(dirMetadata.sp)
				}
			}

			// Record Values that compare against files
			aggregateHealth = dirMetadata.AggregateHealth
			aggregateStuckHealth = dirMetadata.AggregateStuckHealth
			aggregateMinRedundancy = dirMetadata.AggregateMinRedundancy
			aggregateLastHealthCheckTime = dirMetadata.AggregateLastHealthCheckTime
			aggregateModTime = dirMetadata.AggregateModTime
			aggregateRemoteHealth = dirMetadata.AggregateRemoteHealth

			// Update aggregate fields.
			metadata.AggregateNumFiles += dirMetadata.AggregateNumFiles
			metadata.AggregateNumStuckChunks += dirMetadata.AggregateNumStuckChunks
			metadata.AggregateNumSubDirs += dirMetadata.AggregateNumSubDirs
			metadata.AggregateRepairSize += dirMetadata.AggregateRepairSize
			metadata.AggregateSize += dirMetadata.AggregateSize
			metadata.AggregateStuckSize += dirMetadata.AggregateStuckSize

			// Add 1 to the AggregateNumSubDirs to account for this subdirectory.
			metadata.AggregateNumSubDirs++

			// Update siadir fields
			metadata.NumSubDirs++
		}
		// Track the max value of aggregate health values
		metadata.AggregateHealth = math.Max(metadata.AggregateHealth, aggregateHealth)
		metadata.AggregateRemoteHealth = math.Max(metadata.AggregateRemoteHealth, aggregateRemoteHealth)
		metadata.AggregateStuckHealth = math.Max(metadata.AggregateStuckHealth, aggregateStuckHealth)
		// Track the min value for AggregateMinRedundancy
		if aggregateMinRedundancy != -1 {
			metadata.AggregateMinRedundancy = math.Min(metadata.AggregateMinRedundancy, aggregateMinRedundancy)
		}
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
	// then set it to -1 to indicate an empty directory
	if metadata.AggregateMinRedundancy == math.MaxFloat64 {
		metadata.AggregateMinRedundancy = -1
	}
	if metadata.MinRedundancy == math.MaxFloat64 {
		metadata.MinRedundancy = -1
	}

	return metadata, nil
}

// managedCachedFileMetadata returns the cached metadata information of
// a siafiles that needs to be bubbled.
func (r *Renter) managedCachedFileMetadata(siaPath modules.SiaPath) (bubbledSiaFileMetadata, error) {
	// Open SiaFile in a read only state so that it doesn't need to be
	// closed
	sf, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return bubbledSiaFileMetadata{}, err
	}
	defer func() {
		err = errors.Compose(err, sf.Close())
	}()

	// Grab the metadata to pull the cached information from
	md := sf.Metadata()

	// Check if original file is on disk
	_, err = os.Stat(sf.LocalPath())
	onDisk := err == nil
	if !onDisk && md.CachedRedundancy < 1 {
		r.log.Debugf("File not found on disk and possibly unrecoverable: LocalPath %v; SiaPath %v", sf.LocalPath(), siaPath.String())
	}

	// Return the metadata
	return bubbledSiaFileMetadata{
		sp: siaPath,
		bm: siafile.BubbledMetadata{
			Health:              md.CachedHealth,
			LastHealthCheckTime: sf.LastHealthCheckTime(),
			ModTime:             sf.ModTime(),
			NumStuckChunks:      md.CachedNumStuckChunks,
			OnDisk:              onDisk,
			Redundancy:          md.CachedRedundancy,
			RepairBytes:         md.CachedRepairBytes,
			Size:                sf.Size(),
			StuckHealth:         md.CachedStuckHealth,
			StuckBytes:          md.CachedStuckBytes,
			UID:                 sf.UID(),
		},
	}, nil
}

// managedCachedFileMetadatas returns the cahced metadata information of
// multiple siafiles that need to be bubbled. Usually the return value of
// a method is ignored when the returned error != nil. For
// managedCachedFileMetadatas we make an exception. The caller can decide
// themselves whether to use the output in case of an error or not.
func (r *Renter) managedCachedFileMetadatas(siaPaths []modules.SiaPath) (_ []bubbledSiaFileMetadata, err error) {
	// Define components
	mds := make([]bubbledSiaFileMetadata, 0, len(siaPaths))
	siaPathChan := make(chan modules.SiaPath, numBubbleWorkerThreads)
	var errs error
	var errMu, mdMu sync.Mutex

	// Create function for loading SiaFiles and calculating the metadata
	metadataWorker := func() {
		for siaPath := range siaPathChan {
			md, err := r.managedCachedFileMetadata(siaPath)
			if err != nil {
				errMu.Lock()
				errs = errors.Compose(errs, err)
				errMu.Unlock()
				continue
			}
			mdMu.Lock()
			mds = append(mds, md)
			mdMu.Unlock()
		}
	}

	// Launch Metadata workers
	var wg sync.WaitGroup
	for i := 0; i < numBubbleWorkerThreads; i++ {
		wg.Add(1)
		go func() {
			metadataWorker()
			wg.Done()
		}()
	}
	for _, siaPath := range siaPaths {
		select {
		case siaPathChan <- siaPath:
		case <-r.tg.StopChan():
			close(siaPathChan)
			wg.Wait()
			return nil, errors.AddContext(errs, "renter shutdown")
		}
	}
	close(siaPathChan)
	wg.Wait()
	return mds, errs
}

// managedDirectoryMetadatas returns all the metadatas of the SiaDirs for the
// provided siaPaths
func (r *Renter) managedDirectoryMetadatas(siaPaths []modules.SiaPath) ([]bubbledSiaDirMetadata, error) {
	// Define components
	mds := make([]bubbledSiaDirMetadata, 0, len(siaPaths))
	siaPathChan := make(chan modules.SiaPath, numBubbleWorkerThreads)
	var errs error
	var errMu, mdMu sync.Mutex

	// Create function for getting the directory metadata
	metadataWorker := func() {
		for siaPath := range siaPathChan {
			md, err := r.managedDirectoryMetadata(siaPath)
			if err != nil {
				errMu.Lock()
				errs = errors.Compose(errs, err)
				errMu.Unlock()
				continue
			}
			mdMu.Lock()
			mds = append(mds, bubbledSiaDirMetadata{
				siaPath,
				md,
			})
			mdMu.Unlock()
		}
	}

	// Launch Metadata workers
	var wg sync.WaitGroup
	for i := 0; i < numBubbleWorkerThreads; i++ {
		wg.Add(1)
		go func() {
			metadataWorker()
			wg.Done()
		}()
	}
	for _, siaPath := range siaPaths {
		select {
		case siaPathChan <- siaPath:
		case <-r.tg.StopChan():
			close(siaPathChan)
			wg.Wait()
			return nil, errors.AddContext(errs, "renter shutdown")
		}
	}
	close(siaPathChan)
	wg.Wait()
	return mds, errs
}

// managedDirectoryMetadata reads the directory metadata and returns the bubble
// metadata
func (r *Renter) managedDirectoryMetadata(siaPath modules.SiaPath) (_ siadir.Metadata, err error) {
	// Check for bad paths and files
	fi, err := r.staticFileSystem.Stat(siaPath)
	if err != nil {
		return siadir.Metadata{}, err
	}
	if !fi.IsDir() {
		return siadir.Metadata{}, fmt.Errorf("%v is not a directory", siaPath)
	}

	//  Open SiaDir
	siaDir, err := r.staticFileSystem.OpenSiaDirCustom(siaPath, true)
	if err != nil {
		return siadir.Metadata{}, err
	}
	defer func() {
		err = errors.Compose(err, siaDir.Close())
	}()

	// Grab the metadata.
	return siaDir.Metadata()
}

// managedUpdateLastHealthCheckTime updates the LastHealthCheckTime and
// AggregateLastHealthCheckTime fields of the directory metadata by reading all
// the subdirs of the directory.
func (r *Renter) managedUpdateLastHealthCheckTime(siaPath modules.SiaPath) error {
	// Read directory
	fileinfos, err := r.staticFileSystem.ReadDir(siaPath)
	if err != nil {
		r.log.Printf("WARN: Error in reading files in directory %v : %v\n", siaPath.String(), err)
		return err
	}

	// Iterate over directory and find the oldest AggregateLastHealthCheckTime
	aggregateLastHealthCheckTime := time.Now()
	for _, fi := range fileinfos {
		// Check to make sure renter hasn't been shutdown
		select {
		case <-r.tg.StopChan():
			return err
		default:
		}
		// Check for SiaFiles and Directories
		if fi.IsDir() {
			// Directory is found, read the directory metadata file
			dirSiaPath, err := siaPath.Join(fi.Name())
			if err != nil {
				return err
			}
			dirMetadata, err := r.managedDirectoryMetadata(dirSiaPath)
			if err != nil {
				return err
			}
			// Update AggregateLastHealthCheckTime.
			if dirMetadata.AggregateLastHealthCheckTime.Before(aggregateLastHealthCheckTime) {
				aggregateLastHealthCheckTime = dirMetadata.AggregateLastHealthCheckTime
			}
		} else {
			// Ignore everything that is not a directory since files should be updated
			// already by the ongoing bubble.
			continue
		}
	}

	// Write changes to disk.
	entry, err := r.staticFileSystem.OpenSiaDir(siaPath)
	if err != nil {
		return err
	}
	err = entry.UpdateLastHealthCheckTime(aggregateLastHealthCheckTime, time.Now())
	return errors.Compose(err, entry.Close())
}
