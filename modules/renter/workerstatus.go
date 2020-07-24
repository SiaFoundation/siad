package renter

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// callStatus returns the status of the worker.
func (w *worker) callStatus() modules.WorkerStatus {
	w.mu.Lock()
	defer w.mu.Unlock()

	downloadOnCoolDown := w.onDownloadCooldown()
	uploadOnCoolDown, uploadCoolDownTime := w.onUploadCooldown()

	var uploadCoolDownErr string
	if w.uploadRecentFailureErr != nil {
		uploadCoolDownErr = w.uploadRecentFailureErr.Error()
	}

	var maintenanceCoolDownErr string
	if w.maintenanceRecentErr != nil {
		maintenanceCoolDownErr = w.maintenanceRecentErr.Error()
	}
	var maintenaceCoolDownTime time.Duration
	if w.managedOnMaintenanceCooldown() {
		maintenaceCoolDownTime = w.maintenanceCooldownUntil.Sub(time.Now())
	}

	// Update the worker cache before returning a status.
	w.staticTryUpdateCache()
	cache := w.staticCache()
	return modules.WorkerStatus{
		// Contract Information
		ContractID:      cache.staticContractID,
		ContractUtility: cache.staticContractUtility,
		HostPubKey:      w.staticHostPubKey,

		// Download information
		DownloadOnCoolDown: downloadOnCoolDown,
		DownloadQueueSize:  len(w.downloadChunks),
		DownloadTerminated: w.downloadTerminated,

		// Upload information
		UploadCoolDownError: uploadCoolDownErr,
		UploadCoolDownTime:  uploadCoolDownTime,
		UploadOnCoolDown:    uploadOnCoolDown,
		UploadQueueSize:     len(w.unprocessedChunks),
		UploadTerminated:    w.uploadTerminated,

		// Job Queues
		BackupJobQueueSize:       w.staticFetchBackupsJobQueue.managedLen(),
		DownloadRootJobQueueSize: w.staticJobQueueDownloadByRoot.managedLen(),

		// Maintenance Cooldown Information
		MaintenanceOnCooldown:    w.managedOnMaintenanceCooldown(),
		MaintenanceCoolDownError: maintenanceCoolDownErr,
		MaintenanceCoolDownTime:  maintenaceCoolDownTime,

		// Account Information
		AccountBalanceTarget: w.staticBalanceTarget,
		AccountStatus:        w.staticAccount.managedStatus(),

		// Price Table Information
		PriceTableStatus: w.staticPriceTableStatus(),

		// Read Job Information
		ReadJobsStatus: w.callReadJobStatus(),

		// HasSector Job Information
		HasSectorJobsStatus: w.callHasSectorJobStatus(),
	}
}

// staticPriceTableStatus returns the status of the worker's price table
func (w *worker) staticPriceTableStatus() modules.WorkerPriceTableStatus {
	pt := w.staticPriceTable()

	var recentErrStr string
	if pt.staticRecentErr != nil {
		recentErrStr = pt.staticRecentErr.Error()
	}

	return modules.WorkerPriceTableStatus{
		ExpiryTime: pt.staticExpiryTime,
		UpdateTime: pt.staticUpdateTime,

		Active: time.Now().Before(pt.staticExpiryTime),

		RecentErr:     recentErrStr,
		RecentErrTime: pt.staticRecentErrTime,
	}
}

// callReadJobStatus returns the status of the read job queue
func (w *worker) callReadJobStatus() modules.WorkerReadJobsStatus {
	jrq := w.staticJobReadQueue
	status := jrq.callStatus()

	var recentErrString string
	if status.recentErr != nil {
		recentErrString = status.recentErr.Error()
	}

	avgJobTimeInMs := func(l uint64) uint64 {
		if d := jrq.callAverageJobTime(l); d > 0 {
			return uint64(d.Milliseconds())
		}
		return 0
	}

	return modules.WorkerReadJobsStatus{
		AvgJobTime64k:       avgJobTimeInMs(1 << 16),
		AvgJobTime1m:        avgJobTimeInMs(1 << 20),
		AvgJobTime4m:        avgJobTimeInMs(1 << 22),
		ConsecutiveFailures: status.consecutiveFailures,
		JobQueueSize:        status.size,
		RecentErr:           recentErrString,
		RecentErrTime:       status.recentErrTime,
	}
}

// callHasSectorJobStatus returns the status of the has sector job queue
func (w *worker) callHasSectorJobStatus() modules.WorkerHasSectorJobsStatus {
	hsq := w.staticJobHasSectorQueue
	status := hsq.callStatus()

	var recentErrStr string
	if status.recentErr != nil {
		recentErrStr = status.recentErr.Error()
	}

	var avgJobTimeInMs uint64 = 0
	if d := hsq.callAverageJobTime(); d > 0 {
		avgJobTimeInMs = uint64(d.Milliseconds())
	}

	return modules.WorkerHasSectorJobsStatus{
		AvgJobTime:          avgJobTimeInMs,
		ConsecutiveFailures: status.consecutiveFailures,
		JobQueueSize:        status.size,
		RecentErr:           recentErrStr,
		RecentErrTime:       status.recentErrTime,
	}
}
