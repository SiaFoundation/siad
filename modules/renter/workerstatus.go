package renter

import (
	"time"

	"go.sia.tech/siad/modules"
)

// callStatus returns the status of the worker.
func (w *worker) callStatus() modules.WorkerStatus {
	downloadQueue := w.staticJobLowPrioReadQueue
	downloadQueue.mu.Lock()
	downloadOnCoolDown := downloadQueue.onCooldown()
	downloadTerminated := downloadQueue.killed
	downloadQueueSize := downloadQueue.jobs.Len()
	downloadCoolDownTime := downloadQueue.cooldownUntil.Sub(time.Now())

	var downloadCoolDownErr string
	if downloadQueue.recentErr != nil {
		downloadCoolDownErr = downloadQueue.recentErr.Error()
	}
	downloadQueue.mu.Unlock()

	w.mu.Lock()
	defer w.mu.Unlock()

	uploadOnCoolDown, uploadCoolDownTime := w.onUploadCooldown()
	var uploadCoolDownErr string
	if w.uploadRecentFailureErr != nil {
		uploadCoolDownErr = w.uploadRecentFailureErr.Error()
	}

	maintenanceOnCooldown, maintenanceCoolDownTime, maintenanceCoolDownErr := w.staticMaintenanceState.managedMaintenanceCooldownStatus()
	var mcdErr string
	if maintenanceCoolDownErr != nil {
		mcdErr = maintenanceCoolDownErr.Error()
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
		DownloadCoolDownError: downloadCoolDownErr,
		DownloadCoolDownTime:  downloadCoolDownTime,
		DownloadOnCoolDown:    downloadOnCoolDown,
		DownloadQueueSize:     downloadQueueSize,
		DownloadTerminated:    downloadTerminated,

		// Upload information
		UploadCoolDownError: uploadCoolDownErr,
		UploadCoolDownTime:  uploadCoolDownTime,
		UploadOnCoolDown:    uploadOnCoolDown,
		UploadQueueSize:     w.unprocessedChunks.Len(),
		UploadTerminated:    w.uploadTerminated,

		// Job Queues
		DownloadSnapshotJobQueueSize: int(w.staticJobDownloadSnapshotQueue.callStatus().size),
		UploadSnapshotJobQueueSize:   int(w.staticJobUploadSnapshotQueue.callStatus().size),

		// Maintenance Cooldown Information
		MaintenanceOnCooldown:    maintenanceOnCooldown,
		MaintenanceCoolDownError: mcdErr,
		MaintenanceCoolDownTime:  maintenanceCoolDownTime,

		// Account Information
		AccountBalanceTarget: w.staticBalanceTarget,
		AccountStatus:        w.staticAccount.managedStatus(),

		// Price Table Information
		PriceTableStatus: w.staticPriceTableStatus(),

		// Read Job Information
		ReadJobsStatus: w.callReadJobStatus(),

		// HasSector Job Information
		HasSectorJobsStatus: w.callHasSectorJobStatus(),

		// ReadRegistry Job Information
		ReadRegistryJobsStatus: w.callReadRegistryJobsStatus(),

		// UpdateRegistry Job Information
		UpdateRegistryJobsStatus: w.callUpdateRegistryJobsStatus(),
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
		if d := jrq.callExpectedJobTime(l); d > 0 {
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

	avgJobTimeInMs := uint64(hsq.callExpectedJobTime().Milliseconds())

	return modules.WorkerHasSectorJobsStatus{
		AvgJobTime:          avgJobTimeInMs,
		ConsecutiveFailures: status.consecutiveFailures,
		JobQueueSize:        status.size,
		RecentErr:           recentErrStr,
		RecentErrTime:       status.recentErrTime,
	}
}

// callGenericWorkerJobStatus returns the status for the generic job queue.
func callGenericWorkerJobStatus(queue *jobGenericQueue) modules.WorkerGenericJobsStatus {
	status := queue.callStatus()

	var recentErrStr string
	if status.recentErr != nil {
		recentErrStr = status.recentErr.Error()
	}

	return modules.WorkerGenericJobsStatus{
		ConsecutiveFailures: status.consecutiveFailures,
		JobQueueSize:        status.size,
		OnCooldown:          time.Now().Before(status.cooldownUntil),
		OnCooldownUntil:     status.cooldownUntil,
		RecentErr:           recentErrStr,
		RecentErrTime:       status.recentErrTime,
	}
}

// callUpdateRegistryJobsStatus returns the status for the ReadRegistry queue.
func (w *worker) callReadRegistryJobsStatus() modules.WorkerReadRegistryJobStatus {
	return modules.WorkerReadRegistryJobStatus{
		WorkerGenericJobsStatus: callGenericWorkerJobStatus(w.staticJobReadRegistryQueue.jobGenericQueue),
	}
}

// callUpdateRegistryJobsStatus returns the status for the UpdateRegistry queue.
func (w *worker) callUpdateRegistryJobsStatus() modules.WorkerUpdateRegistryJobStatus {
	return modules.WorkerUpdateRegistryJobStatus{
		WorkerGenericJobsStatus: callGenericWorkerJobStatus(w.staticJobUpdateRegistryQueue.jobGenericQueue),
	}
}
