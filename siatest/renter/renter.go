package renter

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// The following are helper functions for the renter tests

// checkBalanceVsSpending checks the renters confirmed siacoin balance in their
// wallet against their reported spending
func checkBalanceVsSpending(r *siatest.TestNode, initialBalance types.Currency) error {
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
		ExpectedBalance:    %v
		walletBalance:      %v
		`, expectedBalance.HumanString(), wg.ConfirmedSiacoinBalance.HumanString(), initialBalance.Sub(wg.ConfirmedSiacoinBalance).HumanString(),
			expectedBalance.HumanString(), wg.ConfirmedSiacoinBalance.HumanString())
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

// checkContracts confirms that contracts are renewed as expected, renewed
// contracts should be the renter's active contracts and oldContracts should be
// the renter's inactive and expired contracts
func checkContracts(numHosts, numRenewals int, oldContracts, renewedContracts []api.RenterContract) error {
	if len(renewedContracts) != numHosts {
		return fmt.Errorf("Incorrect number of Active contracts: have %v expected %v", len(renewedContracts), numHosts)
	}
	if len(oldContracts) == 0 && numRenewals == 0 {
		return nil
	}
	// Confirm contracts were renewed, this will also mean there are old contracts
	// Verify there are not more renewedContracts than there are oldContracts
	// This would mean contracts are not getting archived
	if len(oldContracts) < len(renewedContracts) {
		return errors.New("Too many renewed contracts")
	}
	if len(oldContracts) != numHosts*numRenewals {
		return fmt.Errorf("Incorrect number of Old contracts: have %v expected %v", len(oldContracts), numHosts*numRenewals)
	}

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

// checkContractVsReportedSpending confirms that the spending recorded in the
// renter's contracts matches the reported spending for the renter. Renewed
// contracts should be the renter's active contracts and oldContracts should be
// the renter's inactive and expired contracts
func checkContractVsReportedSpending(r *siatest.TestNode, WindowSize types.BlockHeight, oldContracts, renewedContracts []api.RenterContract) error {
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
		Add(fm.DownloadSpending).Add(fm.StorageSpending)
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
				Add(contract.DownloadSpending).Add(contract.UploadSpending).Add(contract.StorageSpending)
		} else {
			// Calculate Previous spending
			spending.PreviousSpending = spending.PreviousSpending.Add(contract.Fees).
				Add(contract.DownloadSpending).Add(contract.UploadSpending).Add(contract.StorageSpending)
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

// checkRenewedContracts confirms that renewed contracts have zero upload and
// download spending. Renewed contracts should be the renter's active contracts
func checkRenewedContracts(renewedContracts []api.RenterContract) error {
	for _, c := range renewedContracts {
		if c.UploadSpending.Cmp(types.ZeroCurrency) != 0 && c.GoodForUpload {
			return fmt.Errorf("Upload spending on renewed contract equal to %v, expected zero", c.UploadSpending.HumanString())
		}
		if c.DownloadSpending.Cmp(types.ZeroCurrency) != 0 {
			return fmt.Errorf("Download spending on renewed contract equal to %v, expected zero", c.DownloadSpending.HumanString())
		}
	}
	return nil
}

// copyFile is a helper function to copy a file to a destination.
func copyFile(fromPath, toPath string) error {
	err := os.MkdirAll(filepath.Dir(toPath), 0700)
	if err != nil {
		return err
	}
	from, err := os.Open(fromPath)
	if err != nil {
		return err
	}
	to, err := os.OpenFile(toPath, os.O_RDWR|os.O_CREATE, 0700)
	if err != nil {
		return err
	}
	_, err = io.Copy(to, from)
	if err != nil {
		return err
	}
	if err = from.Close(); err != nil {
		return err
	}
	if err = to.Close(); err != nil {
		return err
	}
	return nil
}

// deleteDuringDownloadAndStream will download and stream a file in parallel, it
// will then sleep to ensure the download and stream have downloaded some data,
// then it will delete the file
func deleteDuringDownloadAndStream(r *siatest.TestNode, rf *siatest.RemoteFile, t *testing.T, wg *sync.WaitGroup, sleep time.Duration) {
	defer wg.Done()
	wgDelete := new(sync.WaitGroup)
	// Download the file
	wgDelete.Add(1)
	go func() {
		defer wgDelete.Done()
		_, err := r.DownloadToDisk(rf, false)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Stream the File
	wgDelete.Add(1)
	go func() {
		defer wgDelete.Done()
		_, err := r.Stream(rf)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Delete the file
	wgDelete.Add(1)
	go func() {
		defer wgDelete.Done()
		// Wait to ensure download and stream have started
		time.Sleep(sleep)
		err := r.RenterDeletePost(rf.SiaPath())
		if err != nil {
			t.Error(err)
		}
	}()

	// Wait for the method's go routines to finish
	wgDelete.Wait()

}

// renameDuringDownloadAndStream will download and stream a file in parallel, it
// will then sleep to ensure the download and stream have downloaded some data,
// then it will rename the file
func renameDuringDownloadAndStream(r *siatest.TestNode, rf *siatest.RemoteFile, t *testing.T, wg *sync.WaitGroup, sleep time.Duration) {
	defer wg.Done()
	wgRename := new(sync.WaitGroup)
	// Download the file
	wgRename.Add(1)
	go func() {
		defer wgRename.Done()
		_, err := r.DownloadToDisk(rf, false)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Stream the File
	wgRename.Add(1)
	go func() {
		defer wgRename.Done()
		_, err := r.Stream(rf)
		if err != nil {
			t.Fatal(err)
		}
	}()
	// Rename the file
	wgRename.Add(1)
	go func() {
		defer wgRename.Done()
		// Wait to ensure download and stream have started
		time.Sleep(sleep)
		var err error
		rf, err = r.Rename(rf, modules.RandomSiaPath())
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Wait for the method's go routines to finish
	wgRename.Wait()
}

// renewContractByRenewWindow mines blocks to force contract renewal
func renewContractsByRenewWindow(renter *siatest.TestNode, tg *siatest.TestGroup) error {
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
	blocksToMine := rc.ActiveContracts[0].EndHeight - rg.Settings.Allowance.RenewWindow - cg.Height
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

// drainContractsByUploading uploads files until the contracts renew due to
// running out of funds
func drainContractsByUploading(renter *siatest.TestNode, tg *siatest.TestGroup, maxPercentageRemaining float64) (startingUploadSpend types.Currency, err error) {
	// Renew contracts by running out of funds
	// Set upload price to max price
	maxStoragePrice := types.SiacoinPrecision.Mul64(3e6).Div(modules.BlockBytesPerMonthTerabyte)
	maxUploadPrice := maxStoragePrice.Mul64(100 * 4320)
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
	chunkSize := siatest.ChunkSize(dataPieces, crypto.TypeDefaultRenter)

	// Upload once to show upload spending
	_, _, err = renter.UploadNewFileBlocking(int(chunkSize), dataPieces, parityPieces, false)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "failed to upload first file in renewContractsBySpending")
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
			if percentRemaining < maxPercentageRemaining {
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
