package renter

import (
	"fmt"
	"strings"
	"sync/atomic"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// workertools.go provides a set of high level tools for workers to use when
// performing jobs. The goal is to enable the jobs to worry about the high level
// tasks that need to be accomplished, while allowing the toolkit to worry about
// lower level concerns such as monitoring failure rates and host performance.

// checkDownloadPriceProtections looks at the current renter allowance and the
// active settings for a host and determines whether a download by root job
// should be halted due to price gouging.
func checkDownloadPriceProtections(allowance modules.Allowance, hostSettings modules.HostExternalSettings) error {
	// Check whether the base RPC price is too high.
	if !allowance.MaxRPCPrice.IsZero() && allowance.MaxRPCPrice.Cmp(hostSettings.BaseRPCPrice) < 0 {
		errStr := fmt.Sprintf("rpc price of host is above the allowance max: %v vs %v", hostSettings.BaseRPCPrice, allowance.MaxRPCPrice)
		return errors.New(errStr)
	}
	// Check whether the download bandwidth price is too high.
	if !allowance.MaxDownloadBandwidthPrice.IsZero() && allowance.MaxDownloadBandwidthPrice.Cmp(hostSettings.DownloadBandwidthPrice) < 0 {
		dbp := hostSettings.DownloadBandwidthPrice
		max := allowance.MaxDownloadBandwidthPrice
		errStr := fmt.Sprintf("download bandwidth price of host is above the allowance max: %v vs %v", dbp, max)
		return errors.New(errStr)
	}
	// Check whether the sector access price is too high.
	if !allowance.MaxSectorAccessPrice.IsZero() && allowance.MaxSectorAccessPrice.Cmp(hostSettings.SectorAccessPrice) < 0 {
		sap := hostSettings.SectorAccessPrice
		max := allowance.MaxSectorAccessPrice
		errStr := fmt.Sprintf("sector access price of host is above the allowance max: %v vs %v", sap, max)
		return errors.New(errStr)
	}

	return nil
}

// errCausedByRevisionMismatch returns true if (we suspect) the given error is
// caused by a revision number mismatch. Unfortunately we can not know this for
// sure, because hosts before v1.4.12 did not perform the revision number check
// as the very first check when validating a revision.
func errCausedByRevisionMismatch(err error) bool {
	return err != nil &&
		(strings.Contains(err.Error(), "bad revision number") ||
			strings.Contains(err.Error(), "unexpected number of outputs") ||
			strings.Contains(err.Error(), "high paying renter valid output"))
}

// Download will fetch data from a host, first checking any price protections
// that are in place.
func (w *worker) Download(root crypto.Hash, offset, length uint64) ([]byte, error) {
	// Fetch a session to use in retrieving the sector.
	downloader, err := w.renter.hostContractor.Downloader(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		return nil, errors.AddContext(err, "unable to open downloader for download")
	}
	defer downloader.Close()

	// Check for price gouging before completing the job.
	allowance := w.renter.hostContractor.Allowance()
	hostSettings := downloader.HostSettings()
	err = checkDownloadPriceProtections(allowance, hostSettings)
	if err != nil {
		return nil, errors.AddContext(err, "price protections are blocking download")
	}

	// Fetch the data. Need to ensure that the length is a factor of 64, need to
	// add and remove padding.
	//
	// NOTE: This padding is a requirement of the current Downloader, when the
	// MDM gets deployed and used, this download operation shouldn't need any
	// padding anymore.
	padding := crypto.SegmentSize - length%crypto.SegmentSize
	if padding == crypto.SegmentSize {
		padding = 0
	}
	sectorData, err := downloader.Download(root, uint32(offset), uint32(length+padding))
	if err != nil {
		return nil, errors.AddContext(err, "download failed")
	}
	return sectorData[:length], nil
}

// managedTryFixRevisionNumberMismatch attempts to fix a mismatch in revision
// numbers, it does so by instantiating a session, which has a handshake where
// revisions are exchanged and we learn the host's revision number, and goes on
// to try and sync them if they do not match.
func (w *worker) managedTryFixRevisionNumberMismatch() {
	// Make sure to unset the flag.
	defer atomic.StoreUint64(&w.atomicSuspectRevisionNumberMismatch, 0)

	// Initiate a session, this performs a handshake with the host and syncs up
	// the revision if necessary.
	session, err := w.renter.hostContractor.Session(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		w.renter.log.Printf("could not fix revision number mismatch, could not retrieve a session with host %v, err: %v", w.staticHostPubKeyStr, err)
		return
	}

	// Immediately close the session.
	err = session.Close()
	if err != nil {
		w.renter.log.Printf("could not close session with host %v, err: %v", w.staticHostPubKeyStr, err)
		return
	}
}

// staticSetSuspectRevisionNumberMismatch sets the
// atomicSuspectRevisionNumberMismatch flag.
func (w *worker) staticSetSuspectRevisionNumberMismatch() {
	atomic.StoreUint64(&w.atomicSuspectRevisionNumberMismatch, 1)
}

// staticSetSuspectRevisionNumberMismatch returns whether or not the
// atomicSuspectRevisionNumberMismatch flag has been set.
func (w *worker) staticSuspectRevisionNumberMismatch() bool {
	return atomic.LoadUint64(&w.atomicSuspectRevisionNumberMismatch) == 1
}
