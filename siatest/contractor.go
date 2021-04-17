package siatest

import (
	"fmt"
	"math/big"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/types"
)

// CheckBalanceVsSpending checks the renters confirmed siacoin balance in their
// wallet against their reported spending
func CheckBalanceVsSpending(r *TestNode, initialBalance types.Currency) error {
	// Getting initial financial metrics
	// Setting variables to easier reference
	rg, err := r.RenterGet()
	if err != nil {
		return err
	}
	fm := rg.FinancialMetrics

	// Check balance after allowance is set
	wg, err := r.WalletGet()
	if err != nil {
		return err
	}
	expectedBalance := initialBalance.Sub(fm.TotalAllocated).Sub(fm.WithheldFunds).Sub(fm.PreviousSpending)
	if expectedBalance.Cmp(wg.ConfirmedSiacoinBalance) != 0 {
		details := fmt.Sprintf(`Initial balance minus Renter Reported Spending does not equal wallet Confirmed Siacoin Balance
		Expected Balance:   %v
		Wallet Balance:     %v
		Actual difference:  %v
		`, expectedBalance.HumanString(), wg.ConfirmedSiacoinBalance.HumanString(), initialBalance.Sub(wg.ConfirmedSiacoinBalance).HumanString())
		var diff string
		if expectedBalance.Cmp(wg.ConfirmedSiacoinBalance) > 0 {
			diff = fmt.Sprintf("Under reported by:  %v\n", expectedBalance.Sub(wg.ConfirmedSiacoinBalance).HumanString())
		} else {
			diff = fmt.Sprintf("Over reported by:   %v\n", wg.ConfirmedSiacoinBalance.Sub(expectedBalance).HumanString())
		}
		err := details + diff
		return errors.New(err)
	}
	return nil
}

// CheckContractVsReportedSpending confirms that the spending recorded in the
// renter's contracts matches the reported spending for the renter. Renewed
// contracts should be the renter's active contracts and oldContracts should be
// the renter's inactive and expired contracts
func CheckContractVsReportedSpending(r *TestNode, WindowSize types.BlockHeight, oldContracts, renewedContracts []api.RenterContract) error {
	// Get Current BlockHeight
	cg, err := r.ConsensusGet()
	if err != nil {
		return err
	}

	// Getting financial metrics after uploads, downloads, and
	// contract renewal
	rg, err := r.RenterGet()
	if err != nil {
		return err
	}

	fm := rg.FinancialMetrics
	totalSpent := fm.ContractFees.Add(fm.UploadSpending).
		Add(fm.DownloadSpending).Add(fm.StorageSpending).Add(fm.FundAccountSpending).Add(fm.MaintenanceSpending.Sum())
	total := totalSpent.Add(fm.Unspent)
	allowance := rg.Settings.Allowance

	// Check that renter financial metrics add up to allowance
	if total.Cmp(allowance.Funds) != 0 {
		return fmt.Errorf(`Combined Total of reported spending and unspent funds not equal to allowance:
			total:     %v
			allowance: %v
			`, total.HumanString(), allowance.Funds.HumanString())
	}

	// Check renter financial metrics against contract spending
	var spending modules.ContractorSpending
	for _, contract := range oldContracts {
		if contract.StartHeight >= rg.CurrentPeriod {
			// Calculate ContractFees
			spending.ContractFees = spending.ContractFees.Add(contract.Fees)
			// Calculate TotalAllocated
			spending.TotalAllocated = spending.TotalAllocated.Add(contract.TotalCost)
			// Calculate Spending
			spending.DownloadSpending = spending.DownloadSpending.Add(contract.DownloadSpending)
			spending.FundAccountSpending = spending.FundAccountSpending.Add(contract.FundAccountSpending)
			spending.MaintenanceSpending = spending.MaintenanceSpending.Add(contract.MaintenanceSpending)
			spending.UploadSpending = spending.UploadSpending.Add(contract.UploadSpending)
			spending.StorageSpending = spending.StorageSpending.Add(contract.StorageSpending)
		} else if contract.EndHeight+WindowSize+types.MaturityDelay > cg.Height {
			// Calculated funds that are being withheld in contracts
			spending.WithheldFunds = spending.WithheldFunds.Add(contract.RenterFunds)
			// Record the largest window size for worst case when reporting the spending
			if contract.EndHeight+WindowSize+types.MaturityDelay >= spending.ReleaseBlock {
				spending.ReleaseBlock = contract.EndHeight + WindowSize + types.MaturityDelay
			}
			// Calculate Previous spending
			spending.PreviousSpending = spending.PreviousSpending.Add(contract.Fees).
				Add(contract.DownloadSpending).Add(contract.UploadSpending).Add(contract.StorageSpending).Add(contract.FundAccountSpending).Add(contract.MaintenanceSpending.Sum())
		} else {
			// Calculate Previous spending
			spending.PreviousSpending = spending.PreviousSpending.Add(contract.Fees).
				Add(contract.DownloadSpending).Add(contract.UploadSpending).Add(contract.StorageSpending).Add(contract.FundAccountSpending).Add(contract.MaintenanceSpending.Sum())
		}
	}
	for _, contract := range renewedContracts {
		if contract.GoodForRenew {
			// Calculate ContractFees
			spending.ContractFees = spending.ContractFees.Add(contract.Fees)
			// Calculate TotalAllocated
			spending.TotalAllocated = spending.TotalAllocated.Add(contract.TotalCost)
			// Calculate Spending
			spending.DownloadSpending = spending.DownloadSpending.Add(contract.DownloadSpending)
			spending.FundAccountSpending = spending.FundAccountSpending.Add(contract.FundAccountSpending)
			spending.MaintenanceSpending = spending.MaintenanceSpending.Add(contract.MaintenanceSpending)
			spending.UploadSpending = spending.UploadSpending.Add(contract.UploadSpending)
			spending.StorageSpending = spending.StorageSpending.Add(contract.StorageSpending)
		}
	}

	// Compare contract fees
	if fm.ContractFees.Cmp(spending.ContractFees) != 0 {
		return fmt.Errorf(`Fees not equal:
			Financial Metrics Fees: %v
			Contract Fees:          %v
			`, fm.ContractFees.HumanString(), spending.ContractFees.HumanString())
	}
	// Compare Total Allocated
	if fm.TotalAllocated.Cmp(spending.TotalAllocated) != 0 {
		return fmt.Errorf(`Total Allocated not equal:
			Financial Metrics TA: %v
			Contract TA:          %v
			`, fm.TotalAllocated.HumanString(), spending.TotalAllocated.HumanString())
	}
	// Compare Upload Spending
	if fm.UploadSpending.Cmp(spending.UploadSpending) != 0 {
		return fmt.Errorf(`Upload spending not equal:
			Financial Metrics US: %v
			Contract US:          %v
			`, fm.UploadSpending.HumanString(), spending.UploadSpending.HumanString())
	}
	// Compare Download Spending
	if fm.DownloadSpending.Cmp(spending.DownloadSpending) != 0 {
		return fmt.Errorf(`Download spending not equal:
			Financial Metrics DS: %v
			Contract DS:          %v
			`, fm.DownloadSpending.HumanString(), spending.DownloadSpending.HumanString())
	}
	// Compare FundAccount Spending
	if fm.FundAccountSpending.Cmp(spending.FundAccountSpending) != 0 {
		return fmt.Errorf(`FundAccount spending not equal:
			Financial Metrics DS: %v
			Contract DS:          %v
			`, fm.FundAccountSpending.HumanString(), spending.FundAccountSpending.HumanString())
	}
	// Compare Maintenance Spending
	if fm.MaintenanceSpending.Sum().Cmp(spending.MaintenanceSpending.Sum()) != 0 {
		return fmt.Errorf(`Maintenance spending not equal:
			Financial Metrics DS: %v
			Contract DS:          %v
			`, fm.MaintenanceSpending.Sum().HumanString(), spending.MaintenanceSpending.Sum().HumanString())
	}
	// Compare Storage Spending
	if fm.StorageSpending.Cmp(spending.StorageSpending) != 0 {
		return fmt.Errorf(`Storage spending not equal:
			Financial Metrics SS: %v
			Contract SS:          %v
			`, fm.StorageSpending.HumanString(), spending.StorageSpending.HumanString())
	}
	// Compare Withheld Funds
	if fm.WithheldFunds.Cmp(spending.WithheldFunds) != 0 {
		return fmt.Errorf(`Withheld Funds not equal:
			Financial Metrics WF: %v
			Contract WF:          %v
			`, fm.WithheldFunds.HumanString(), spending.WithheldFunds.HumanString())
	}
	// Compare Release Block
	if fm.ReleaseBlock != spending.ReleaseBlock {
		return fmt.Errorf(`Release Block not equal:
			Financial Metrics RB: %v
			Contract RB:          %v
			`, fm.ReleaseBlock, spending.ReleaseBlock)
	}
	// Compare Previous Spending
	if fm.PreviousSpending.Cmp(spending.PreviousSpending) != 0 {
		return fmt.Errorf(`Previous spending not equal:
			Financial Metrics PS: %v
			Contract PS:          %v
			`, fm.PreviousSpending.HumanString(), spending.PreviousSpending.HumanString())
	}

	return nil
}

// CheckExpectedNumberOfContracts confirms that the renter has the expected
// number of each type of contract
func CheckExpectedNumberOfContracts(r *TestNode, numActive, numPassive, numRefreshed, numDisabled, numExpired, numExpiredRefreshed int) error {
	rc, err := r.RenterAllContractsGet()
	if err != nil {
		return err
	}
	var combinedError error
	if len(rc.ActiveContracts) != numActive {
		combinedError = errors.Compose(combinedError, fmt.Errorf("Expected %v active contracts, got %v", numActive, len(rc.ActiveContracts)))
	}
	if len(rc.PassiveContracts) != numPassive {
		combinedError = errors.Compose(combinedError, fmt.Errorf("Expected %v passive contracts, got %v", numPassive, len(rc.PassiveContracts)))
	}
	if len(rc.RefreshedContracts) != numRefreshed {
		combinedError = errors.Compose(combinedError, fmt.Errorf("Expected %v refreshed contracts, got %v", numRefreshed, len(rc.RefreshedContracts)))
	}
	if len(rc.DisabledContracts) != numDisabled {
		combinedError = errors.Compose(combinedError, fmt.Errorf("Expected %v disabled contracts, got %v", numDisabled, len(rc.DisabledContracts)))
	}
	if len(rc.ExpiredContracts) != numExpired {
		combinedError = errors.Compose(combinedError, fmt.Errorf("Expected %v expired contracts, got %v", numExpired, len(rc.ExpiredContracts)))
	}
	if len(rc.ExpiredRefreshedContracts) != numExpiredRefreshed {
		combinedError = errors.Compose(combinedError, fmt.Errorf("Expected %v expired refreshed contracts, got %v", numExpiredRefreshed, len(rc.ExpiredRefreshedContracts)))
	}
	return combinedError
}

// CheckRenewedContractIDs confirms that contracts are renewed as expected with
// hosts and no duplicate IDs
func CheckRenewedContractIDs(oldContracts, renewedContracts []api.RenterContract) error {
	// Create Maps for comparison
	initialContractIDMap := make(map[types.FileContractID]struct{})
	initialContractKeyMap := make(map[crypto.Hash]struct{})
	for _, c := range oldContracts {
		initialContractIDMap[c.ID] = struct{}{}
		initialContractKeyMap[crypto.HashBytes(c.HostPublicKey.Key)] = struct{}{}
	}

	for _, c := range renewedContracts {
		// Verify that all the contracts marked as GoodForRenew
		// were renewed
		if _, ok := initialContractIDMap[c.ID]; ok {
			return errors.New("ID from renewedContracts found in oldContracts")
		}
		// Verifying that Renewed Contracts have the same HostPublicKey
		// as an initial contract
		if _, ok := initialContractKeyMap[crypto.HashBytes(c.HostPublicKey.Key)]; !ok {
			return errors.New("Host Public Key from renewedContracts not found in oldContracts")
		}
	}
	return nil
}

// CheckRenewedContractsSpending confirms that renewed contracts show no
// spending has occurred yet. Renewed contracts should be the renter's active
// contracts
func CheckRenewedContractsSpending(renewedContracts []api.RenterContract) error {
	for _, c := range renewedContracts {
		if !c.UploadSpending.IsZero() && c.GoodForUpload {
			return fmt.Errorf("Upload spending on renewed contract equal to %v, expected zero", c.UploadSpending.HumanString())
		}
		if !c.DownloadSpending.IsZero() {
			return fmt.Errorf("Download spending on renewed contract equal to %v, expected zero", c.DownloadSpending.HumanString())
		}
		if !c.FundAccountSpending.IsZero() {
			return fmt.Errorf("FundAccount spending on renewed contract equal to %v, expected zero", c.DownloadSpending.HumanString())
		}
		if !c.MaintenanceSpending.Sum().IsZero() {
			return fmt.Errorf("Maintenance spending on renewed contract equal to %v, expected zero", c.MaintenanceSpending.Sum().HumanString())
		}
	}
	return nil
}

// DrainContractsByUploading uploads files until the contracts renew due to
// running out of funds
//
// NOTE: in order to use this helper method the renter must use the dependency
// DependencyDisableUploadGougingCheck so that the uploads succeed
func DrainContractsByUploading(renter *TestNode, tg *TestGroup) (startingUploadSpend types.Currency, err error) {
	// Sanity check
	if len(tg.Hosts()) == 1 {
		return types.ZeroCurrency, errors.New("uploads will fail with only 1 host")
	}

	// Renew contracts by running out of funds
	// Set upload price to max price
	maxStoragePrice := types.SiacoinPrecision.Mul64(3e6).Div(modules.BlockBytesPerMonthTerabyte)
	maxUploadPrice := maxStoragePrice.Mul64(100 * uint64(types.BlocksPerMonth))
	hosts := tg.Hosts()
	for _, h := range hosts {
		err := h.HostModifySettingPost(client.HostParamMinUploadBandwidthPrice, maxUploadPrice)
		if err != nil {
			return types.ZeroCurrency, errors.AddContext(err, "could not set Host Upload Price")
		}
	}

	// Waiting for nodes to sync
	m := tg.Miners()[0]
	if err := m.MineBlock(); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "error mining block")
	}
	if err := tg.Sync(); err != nil {
		return types.ZeroCurrency, err
	}

	// Set upload parameters.
	dataPieces := uint64(1)
	parityPieces := uint64(1)
	chunkSize := ChunkSize(dataPieces, crypto.TypeDefaultRenter)

	// Upload once to show upload spending
	_, _, err = renter.UploadNewFileBlocking(int(chunkSize), dataPieces, parityPieces, false)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "failed to upload first file in DrainContractsByUploading")
	}

	// Get current upload spend, previously contracts had zero upload spend
	rc, err := renter.RenterContractsGet()
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "could not get renter active contracts")
	}
	startingUploadSpend = rc.ActiveContracts[0].UploadSpending

	// Upload files to force contract renewal due to running out of funds
LOOP:
	for {
		// To protect against contracts not renewing during uploads
		for _, c := range rc.ActiveContracts {
			percentRemaining, _ := big.NewRat(0, 1).SetFrac(c.RenterFunds.Big(), c.TotalCost.Big()).Float64()
			if percentRemaining < contractor.MinContractFundRenewalThreshold {
				break LOOP
			}
		}
		_, _, err = renter.UploadNewFileBlocking(int(chunkSize), dataPieces, parityPieces, false)
		if err != nil {
			pr, _ := big.NewRat(0, 1).SetFrac(rc.ActiveContracts[0].RenterFunds.Big(), rc.ActiveContracts[0].TotalCost.Big()).Float64()
			s := fmt.Sprintf("failed to upload file in renewContractsBySpending loop, percentRemaining: %v", pr)
			return types.ZeroCurrency, errors.AddContext(err, s)
		}

		rc, err = renter.RenterContractsGet()
		if err != nil {
			return types.ZeroCurrency, errors.AddContext(err, "could not get renter active contracts")
		}
	}
	if err = m.MineBlock(); err != nil {
		return startingUploadSpend, err
	}
	if err := tg.Sync(); err != nil {
		return types.ZeroCurrency, err
	}
	return startingUploadSpend, nil
}

// RenewContractsByRenewWindow mines blocks to force contract renewal
func RenewContractsByRenewWindow(renter *TestNode, tg *TestGroup) error {
	rg, err := renter.RenterGet()
	if err != nil {
		return err
	}
	cg, err := renter.ConsensusGet()
	if err != nil {
		return err
	}
	rc, err := renter.RenterContractsGet()
	if err != nil {
		return err
	}
	if len(rc.ActiveContracts) == 0 {
		return errors.New("No Active Contracts")
	}
	endHeight := ^types.BlockHeight(0)
	for _, c := range rc.ActiveContracts {
		if c.EndHeight < endHeight {
			endHeight = c.EndHeight
		}
	}

	blocksToMine := endHeight - rg.Settings.Allowance.RenewWindow - cg.Height
	m := tg.Miners()[0]
	for i := 0; i < int(blocksToMine); i++ {
		if err = m.MineBlock(); err != nil {
			return err
		}
	}

	// Waiting for nodes to sync
	if err = tg.Sync(); err != nil {
		return err
	}
	return nil
}

// RenterContractsStable verifies that the renter's contracts are in a stable
// state and no contracts are renewing to avoid errors.
func RenterContractsStable(renter *TestNode, tg *TestGroup) error {
	rg, err := renter.RenterGet()
	if err != nil {
		return err
	}
	renewWindow := int(rg.Settings.Allowance.RenewWindow)
	numContracts := int(rg.Settings.Allowance.Hosts)
	if len(tg.Hosts()) < numContracts {
		numContracts = len(tg.Hosts())
	}
	miner := tg.Miners()[0]
	count := 1
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations
		if count%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		count++

		// Get consensus and renter contracts
		cg, err := renter.ConsensusGet()
		if err != nil {
			return err
		}
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}

		// Check for active contracts
		if len(rc.ActiveContracts) != numContracts {
			return errors.New("Not enough Active Contracts")
		}

		// Check if any of the active contracts are in the renew window
		for _, contract := range rc.ActiveContracts {
			inRenewWindow := int(contract.EndHeight)-renewWindow-int(cg.Height) <= 0
			if inRenewWindow {
				return errors.New("contract in renew window")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Waiting for nodes to sync
	return tg.Sync()
}
