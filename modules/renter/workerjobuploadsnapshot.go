package renter

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/contractor"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// snapshotUploadGougingFractionDenom sets the fraction to 1/100 because
	// uploading backups is important, so there is less sensitivity to gouging.
	// Also, this is a rare operation.
	snapshotUploadGougingFractionDenom = 100
)

type (
	// jobUploadSnapshot is a job for the worker to upload a snapshot to its
	// respective host.
	jobUploadSnapshot struct {
		staticSiaFileData []byte

		staticResponseChan chan *jobUploadSnapshotResponse

		*jobGeneric
	}

	// jobUploadSnapshotQueue contains the set of snapshots that need to be
	// uploaded.
	jobUploadSnapshotQueue struct {
		*jobGenericQueue
	}

	// jobUploadSnapshotResponse contains the response to an upload snapshot
	// job.
	jobUploadSnapshotResponse struct {
		staticErr error
	}
)

// checkUploadSnapshotGouging looks at the current renter allowance and the
// active settings for a host and determines whether a snapshot upload should be
// halted due to price gouging.
func checkUploadSnapshotGouging(allowance modules.Allowance, hostSettings modules.HostExternalSettings) error {
	// Check whether the base RPC price is too high.
	if !allowance.MaxRPCPrice.IsZero() && allowance.MaxRPCPrice.Cmp(hostSettings.BaseRPCPrice) < 0 {
		errStr := fmt.Sprintf("rpc price of host is %v, which is above the maximum allowed by the allowance: %v", hostSettings.BaseRPCPrice, allowance.MaxRPCPrice)
		return errors.New(errStr)
	}
	// Check whether the upload bandwidth price is too high.
	if !allowance.MaxUploadBandwidthPrice.IsZero() && allowance.MaxUploadBandwidthPrice.Cmp(hostSettings.UploadBandwidthPrice) < 0 {
		errStr := fmt.Sprintf("upload bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v", hostSettings.UploadBandwidthPrice, allowance.MaxUploadBandwidthPrice)
		return errors.New(errStr)
	}
	// Check whether the storage price is too high.
	if !allowance.MaxStoragePrice.IsZero() && allowance.MaxStoragePrice.Cmp(hostSettings.StoragePrice) < 0 {
		errStr := fmt.Sprintf("storage price of host is %v, which is above the maximum allowed by the allowance: %v", hostSettings.StoragePrice, allowance.MaxStoragePrice)
		return errors.New(errStr)
	}

	// If there is no allowance, general price gouging checks have to be
	// disabled, because there is no baseline for understanding what might count
	// as price gouging.
	if allowance.Funds.IsZero() {
		return nil
	}

	// Check that the combined prices make sense in the context of the overall
	// allowance. The general idea is to compute the total cost of performing
	// the same action repeatedly until a fraction of the desired total resource
	// consumption established by the allowance has been reached. The fraction
	// is determined on a case-by-case basis. If the host is too expensive to
	// even satisfy a faction of the user's total desired resource consumption,
	// the action will be blocked for price gouging.
	singleUploadCost := hostSettings.BaseRPCPrice.Add(hostSettings.UploadBandwidthPrice.Mul64(modules.StreamDownloadSize)).Add(hostSettings.StoragePrice.Mul64(uint64(allowance.Period)).Mul64(modules.SectorSize))
	fullCostPerByte := singleUploadCost.Div64(modules.SectorSize)
	allowanceStorageCost := fullCostPerByte.Mul64(allowance.ExpectedStorage)
	reducedCost := allowanceStorageCost.Div64(snapshotUploadGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined fetch backups pricing of host yields %v, which is more than the renter is willing to pay for storage: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}

// callDiscard will discard this job, sending an error down the response
// channel.
func (j *jobUploadSnapshot) callDiscard(err error) {
	resp := &jobUploadSnapshotResponse{
		staticErr: errors.Extend(err, ErrJobDiscarded),
	}
	w := j.staticQueue.staticWorker()
	errLaunch := w.renter.tg.Launch(func() {
		select {
		case j.staticResponseChan <- resp:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
	if errLaunch != nil {
		w.renter.log.Print("callDiscard: launch failed", err)
	}
}

// callExecute will perform an upload snapshot job for the worker.
func (j *jobUploadSnapshot) callExecute() {
	w := j.staticQueue.staticWorker()

	// Defer a function to send the result down a channel.
	var err error
	defer func() {
		// Return the error to the caller, error may be nil.
		resp := &jobUploadSnapshotResponse{
			staticErr: err,
		}
		errLaunch := w.renter.tg.Launch(func() {
			select {
			case j.staticResponseChan <- resp:
			case <-j.staticCtx.Done():
			case <-w.renter.tg.StopChan():
			}
		})
		if errLaunch != nil {
			w.renter.log.Print("callExecute: launch failed", err)
		}

		// Report a failure to the queue if this job had an error.
		if err != nil {
			j.staticQueue.callReportFailure(err)
		} else {
			j.staticQueue.callReportSuccess()
		}
	}()

	// Check that the worker is good for upload.
	if !w.staticCache().staticContractUtility.GoodForUpload {
		err = errors.New("snapshot was not uploaded because the worker is not good for upload")
		return
	}

	// Perform the actual upload.
	var sess contractor.Session
	sess, err = w.renter.hostContractor.Session(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
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

	allowance := w.renter.hostContractor.Allowance()
	hostSettings := sess.HostSettings()
	err = checkUploadSnapshotGouging(allowance, hostSettings)
	if err != nil {
		err = errors.AddContext(err, "snapshot upload blocked because potential price gouging was detected")
		return
	}

	// Safe cast the metadata to the expected type
	meta, ok := j.staticMetadata.(modules.UploadedBackup)
	if !ok {
		build.Critical("unable to cast job metadata") // sanity check
		return
	}

	// Upload the snapshot to the host.
	err = w.renter.managedUploadSnapshotHost(meta, j.staticSiaFileData, sess, w)
	if err != nil {
		w.renter.log.Debugln("uploading a snapshot to a host failed:", err)
		err = errors.AddContext(err, "uploading a snapshot to a host failed")
		return
	}
}

// callExpectedBandwidth returns the amount of bandwidth this job is expected to
// consume.
func (j *jobUploadSnapshot) callExpectedBandwidth() (ul, dl uint64) {
	// Estimate 50kb in overhead for upload and download, and then 4 MiB
	// necessary to send the actual full sector payload.
	return 50e3 + 1<<22, 50e3
}

// initJobUploadSnapshotQueue will initialize the upload snapshot job queue for
// the worker.
func (w *worker) initJobUploadSnapshotQueue() {
	if w.staticJobUploadSnapshotQueue != nil {
		w.renter.log.Critical("should not be double initializng the upload snapshot queue")
		return
	}

	w.staticJobUploadSnapshotQueue = &jobUploadSnapshotQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}

// managedUploadSnapshotHost uploads a snapshot to a single host.
func (r *Renter) managedUploadSnapshotHost(meta modules.UploadedBackup, dotSia []byte, host contractor.Session, w *worker) error {
	// Get the wallet seed.
	ws, _, err := r.w.PrimarySeed()
	if err != nil {
		return errors.AddContext(err, "failed to get wallet's primary seed")
	}
	// Derive the renter seed and wipe the memory once we are done using it.
	rs := modules.DeriveRenterSeed(ws)
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

	// download the snapshot table
	entryTable, err := r.managedDownloadSnapshotTable(w)
	if err != nil && !errors.Contains(err, errEmptyContract) {
		return errors.AddContext(err, "could not download the snapshot table")
	}

	// check if the table already contains the entry.
	for _, existingEntry := range entryTable {
		if existingEntry.UID == meta.UID {
			return nil // host already contains entry
		}
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
	return nil
}

// UploadSnapshot is a helper method to run a UploadSnapshot job on a worker.
func (w *worker) UploadSnapshot(ctx context.Context, meta modules.UploadedBackup, dotSia []byte) error {
	uploadSnapshotRespChan := make(chan *jobUploadSnapshotResponse)
	jus := &jobUploadSnapshot{
		staticSiaFileData:  dotSia,
		staticResponseChan: uploadSnapshotRespChan,

		jobGeneric: newJobGeneric(ctx, w.staticJobUploadSnapshotQueue, meta),
	}

	// Add the job to the queue.
	if !w.staticJobUploadSnapshotQueue.callAdd(jus) {
		return errors.New("worker unavailable")
	}

	// Wait for the response.
	var resp *jobUploadSnapshotResponse
	select {
	case <-ctx.Done():
		return errors.New("UploadSnapshot interrupted")
	case resp = <-uploadSnapshotRespChan:
	}
	return resp.staticErr
}
