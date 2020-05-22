package renter

import (
	"bytes"
	"sort"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/contractor"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

type (
	// jobUploadSnapshot is a job for the worker to upload a snapshot to its
	// respective host.
	jobUploadSnapshot struct {
		staticMetadata modules.UploadedBackup
		staticSiaFileData []byte

		staticCancelChan chan struct{}
		staticResponseChan chan *jobUploadSnapshotResponse
	}

	// jobUploadSnapshotQueue contains the set of snapshots that need to be
	// uploaded.
	jobUploadSnapshotQueue struct {
		killed bool
		jobs []*jobUploadSnapshot

		cooldownUntil time.Time
		consecutiveFailures uint64

		staticWorker *worker
		mu sync.Mutex
	}

	// jobUploa;dSnapshotResponse contains the response to an upload snapshot
	// job.
	jobUploadSnapshotResponse struct {
		staticErr error
	}
)

// staticCanceled returns whether or not the job has been canceled.
func (j *jobUploadSnapshot) staticCanceled() bool {
	select {
	case <-j.staticCancelChan:
		return true
	default:
		return false
	}
}

// callAdd will add an upload snapshot job to the queue.
func (jq *jobUploadSnapshotQueue) callAdd(j *jobUploadSnapshot) {
	jq.mu.Lock()
	jq.jobs = append(jq.jobs, j)
	jq.mu.Unlock()
	jq.staticWorker.staticWake()
	return
}

// managedHasUploadSnapshotJob will return true if there is a snapshot upload
// job in the worker's queue.
func (w *worker) managedHasUploadSnapshotJob() bool {
	jq := w.staticJobUploadSnapshotQueue
	jq.mu.Lock()
	defer jq.mu.Unlock()
	if time.Now().Before(jq.cooldownUntil) {
		return false
	}
	return len(jq.jobs) > 0
}

// managedJobUploadSnapshot will perform an upload snapshot job for the worker.
func (w *worker) managedJobUploadSnapshot() {
	// Get the latest job.
	var job *jobUploadSnapshot
	jq := w.staticJobUploadSnapshotQueue
	jq.mu.Lock()
	for {
		// If there are no jobs, nothing to do.
		if len(jq.jobs) == 0 {
			jq.mu.Unlock()
			return
		}

		// Grab the next job>
		job = jq.jobs[0]
		jq.jobs = jq.jobs[1:]

		// Move onto the next job if this job has been canceled.
		if job.staticCanceled() {
			continue
		}
		break
	}
	jq.mu.Unlock()

	// Defer a function to send the result down a channel.
	var err error
	defer func() {
		// Return an error to the caller.
		resp := &jobUploadSnapshotResponse{
			staticErr: err,
		}
		w.renter.tg.Launch(func() {
			job.staticResponseChan <- resp
		})
	}()

	// Check that the worker is good for upload.
	if !w.staticCache().staticContractUtility.GoodForUpload {
		println("skipping worker b/c not gfu")
		err = errors.New("snapshot was not uploaded because the worker is not good for upload")
		return
	}

	// TODO: Check for price gouging.

	// Perform the actual upload.
	sess, err := w.renter.hostContractor.Session(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		println("still didn't get the session:", err.Error())
		w.renter.log.Debugln("unable to grab a session to perform an upload snapshot job:", err)
		err = errors.AddContext(err, "unable to get host session")
		return
	}
	defer func() {
		closeErr := sess.Close()
		if closeErr != nil {
			w.renter.log.Println("error while closing session:", closeErr)
		}
		err = errors.Compose(err, closeErr)
	}()

	// TODO: This function doesn't take any cancel channels or thread groups.
	err = w.renter.managedUploadSnapshotHost(job.staticMetadata, job.staticSiaFileData, sess)
	if err != nil {
		println("the actual upload part failed:", err.Error())
		w.renter.log.Debugln("uploading a snapshot to a host failed:", err)
		err = errors.AddContext(err, "uploading a snapshot to a host failed")
		return
	}
}

// initJobUploadSnapshotQueue will initialize the upload snapshot job queue for
// the worker.
func (w *worker) initJobUploadSnapshotQueue() {
	if w.staticJobUploadSnapshotQueue != nil {
		w.renter.log.Critical("should not be double initializng the upload snapshot queue")
		return
	}

	w.staticJobUploadSnapshotQueue = &jobUploadSnapshotQueue{
		staticWorker: w,
	}
}

// managedUploadSnapshotHost uploads a snapshot to a single host.
func (r *Renter) managedUploadSnapshotHost(meta modules.UploadedBackup, dotSia []byte, host contractor.Session) error {
	// Get the wallet seed.
	ws, _, err := r.w.PrimarySeed()
	if err != nil {
		return errors.AddContext(err, "failed to get wallet's primary seed")
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := proto.DeriveRenterSeed(ws)
	defer fastrand.Read(rs[:])
	// Derive the secret and wipe it afterwards.
	secret := crypto.HashAll(rs, snapshotKeySpecifier)
	defer fastrand.Read(secret[:])

	// split the snapshot .sia file into sectors
	var sectors [][]byte
	for buf := bytes.NewBuffer(dotSia); buf.Len() > 0; {
		sector := make([]byte, modules.SectorSize)
		copy(sector, buf.Next(len(sector)))
		sectors = append(sectors, sector)
	}
	if len(sectors) > 4 {
		return errors.New("snapshot is too large")
	}

	// upload the siafile, creating a snapshotEntry
	var name [96]byte
	copy(name[:], meta.Name)
	entry := snapshotEntry{
		Name:         name,
		UID:          meta.UID,
		CreationDate: meta.CreationDate,
		Size:         meta.Size,
	}
	for j, piece := range sectors {
		root, err := host.Upload(piece)
		if err != nil {
			return errors.AddContext(err, "could not perform host upload")
		}
		entry.DataSectors[j] = root
	}

	// download the current entry table
	entryTable, err := r.managedDownloadSnapshotTable(host)
	if err != nil {
		return errors.AddContext(err, "could not download the snapshot table")
	}
	shouldOverwrite := len(entryTable) != 0 // only overwrite if the sector already contained an entryTable
	entryTable = append(entryTable, entry)

	// if entryTable is too large to fit in a sector, repeatedly remove the
	// oldest entry until it fits
	id := r.mu.Lock()
	sort.Slice(r.persist.UploadedBackups, func(i, j int) bool {
		return r.persist.UploadedBackups[i].CreationDate > r.persist.UploadedBackups[j].CreationDate
	})
	r.mu.Unlock(id)
	c, _ := crypto.NewSiaKey(crypto.TypeThreefish, secret[:])
	for len(encoding.Marshal(entryTable)) > int(modules.SectorSize) {
		entryTable = entryTable[:len(entryTable)-1]
	}

	// encode and encrypt the table
	newTable := make([]byte, modules.SectorSize)
	copy(newTable[:16], snapshotTableSpecifier[:])
	copy(newTable[16:], encoding.Marshal(entryTable))
	tableSector := c.EncryptBytes(newTable)

	// swap the new entry table into index 0 and delete the old one
	// (unless it wasn't an entry table)
	if _, err := host.Replace(tableSector, 0, shouldOverwrite); err != nil {
		// Sometimes during the siatests, this will fail with 'write to host
		// failed; connection reset by peer. This error is very consistent in
		// TestRemoteBackup, but occurs after everything else has succeeded so
		// the test doesn't fail.
		return errors.AddContext(err, "could not perform sector replace for the snapshot")
	}
	println("THE REPLACE DID SUCCEED")
	return nil
}
