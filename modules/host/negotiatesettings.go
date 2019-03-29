package host

import (
	"net"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// capacity returns the amount of storage still available on the machine. The
// amount can be negative if the total capacity was reduced to below the active
// capacity.
func (h *Host) capacity() (total, remaining uint64) {
	// Total storage can be computed by summing the size of all the storage
	// folders.
	sfs := h.StorageFolders()
	for _, sf := range sfs {
		total += sf.Capacity
		remaining += sf.CapacityRemaining
	}
	return total, remaining
}

// externalSettings compiles and returns the external settings for the host.
func (h *Host) externalSettings() modules.HostExternalSettings {
	// Increment the revision number for the external settings
	h.revisionNumber++

	totalStorage, remainingStorage := h.capacity()
	var netAddr modules.NetAddress
	if h.settings.NetAddress != "" {
		netAddr = h.settings.NetAddress
	} else {
		netAddr = h.autoAddress
	}

	// Calculate contract price
	_, maxFee := h.tpool.FeeEstimation()
	contractPrice := maxFee.Mul64(modules.EstimatedFileContractRevisionAndProofTransactionSetSize)
	if contractPrice.Cmp(h.settings.MinContractPrice) < 0 {
		contractPrice = h.settings.MinContractPrice
	}

	// If the host's wallet is locked report that it is not accepting contracts.
	acceptingContracts := h.settings.AcceptingContracts
	if unlocked, err := h.wallet.Unlocked(); err != nil || !unlocked {
		acceptingContracts = false
	}
	// If the host's wallet cannot afford to put MaxCollateral coins into a
	// contract, reduce its advertised MaxCollateral.
	maxCollateral := h.settings.MaxCollateral
	balance, _, _, err := h.wallet.ConfirmedBalance()
	if err != nil {
		maxCollateral = types.ZeroCurrency
	}
	if balance.Cmp(maxCollateral) < 0 {
		maxCollateral = balance
	}
	if h.settings.CollateralBudget.Cmp(h.financialMetrics.LockedStorageCollateral) < 0 {
		maxCollateral = types.ZeroCurrency
	} else if h.financialMetrics.LockedStorageCollateral.Add(maxCollateral).Cmp(h.settings.CollateralBudget) > 0 {
		maxCollateral = h.settings.CollateralBudget.Sub(h.financialMetrics.LockedStorageCollateral)
	}

	return modules.HostExternalSettings{
		AcceptingContracts:   acceptingContracts,
		MaxDownloadBatchSize: h.settings.MaxDownloadBatchSize,
		MaxDuration:          h.settings.MaxDuration,
		MaxReviseBatchSize:   h.settings.MaxReviseBatchSize,
		NetAddress:           netAddr,
		RemainingStorage:     remainingStorage,
		SectorSize:           modules.SectorSize,
		TotalStorage:         totalStorage,
		UnlockHash:           h.unlockHash,
		WindowSize:           h.settings.WindowSize,

		Collateral:    h.settings.Collateral,
		MaxCollateral: maxCollateral,

		BaseRPCPrice:           h.settings.MinBaseRPCPrice,
		ContractPrice:          contractPrice,
		DownloadBandwidthPrice: h.settings.MinDownloadBandwidthPrice,
		SectorAccessPrice:      h.settings.MinSectorAccessPrice,
		StoragePrice:           h.settings.MinStoragePrice,
		UploadBandwidthPrice:   h.settings.MinUploadBandwidthPrice,

		RevisionNumber: h.revisionNumber,
		Version:        build.Version,
	}
}

// managedRPCSettings is an rpc that returns the host's settings.
func (h *Host) managedRPCSettings(conn net.Conn) error {
	// Set the negotiation deadline.
	conn.SetDeadline(time.Now().Add(modules.NegotiateSettingsTime))

	// The revision number is updated so that the renter can be certain that
	// they have the most recent copy of the settings. The revision number and
	// signature can be compared against other settings objects that the renter
	// may have, and if the new revision number is not higher the renter can
	// suspect foul play. Largely, the revision number is in place to enable
	// renters to share host settings with each other, a feature that has not
	// yet been implemented.
	//
	// While updating the revision number, also grab the secret key and
	// external settings.
	var hes modules.HostExternalSettings
	var secretKey crypto.SecretKey
	h.mu.Lock()
	secretKey = h.secretKey
	hes = h.externalSettings()
	h.mu.Unlock()

	// Convert the settings to the pre-v1.4.0 version.
	settings := modules.HostOldExternalSettings{
		AcceptingContracts:     hes.AcceptingContracts,
		MaxDownloadBatchSize:   hes.MaxDownloadBatchSize,
		MaxDuration:            hes.MaxDuration,
		MaxReviseBatchSize:     hes.MaxReviseBatchSize,
		NetAddress:             hes.NetAddress,
		RemainingStorage:       hes.RemainingStorage,
		SectorSize:             hes.SectorSize,
		TotalStorage:           hes.TotalStorage,
		UnlockHash:             hes.UnlockHash,
		WindowSize:             hes.WindowSize,
		Collateral:             hes.Collateral,
		MaxCollateral:          hes.MaxCollateral,
		ContractPrice:          hes.ContractPrice,
		DownloadBandwidthPrice: hes.DownloadBandwidthPrice,
		StoragePrice:           hes.StoragePrice,
		UploadBandwidthPrice:   hes.UploadBandwidthPrice,
		RevisionNumber:         hes.RevisionNumber,
		Version:                hes.Version,
	}

	// Write the settings to the renter. If the write fails, return a
	// connection error.
	err := crypto.WriteSignedObject(conn, settings, secretKey)
	if err != nil {
		return ErrorConnection("failed WriteSignedObject during RPCSettings: " + err.Error())
	}
	return nil
}
