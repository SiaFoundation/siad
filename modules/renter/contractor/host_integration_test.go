package contractor

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/siamux"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/consensus"
	"go.sia.tech/siad/modules/gateway"
	"go.sia.tech/siad/modules/host"
	"go.sia.tech/siad/modules/miner"
	"go.sia.tech/siad/modules/renter/hostdb"
	"go.sia.tech/siad/modules/transactionpool"
	modWallet "go.sia.tech/siad/modules/wallet"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// newTestingWallet is a helper function that creates a ready-to-use wallet
// and mines some coins into it.
func newTestingWallet(testdir string, cs modules.ConsensusSet, tp modules.TransactionPool) (modules.Wallet, closeFn, error) {
	w, err := modWallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, nil, err
	}
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	encrypted, err := w.Encrypted()
	if err != nil {
		return nil, nil, err
	}
	if !encrypted {
		_, err = w.Encrypt(key)
		if err != nil {
			return nil, nil, err
		}
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, nil, err
	}
	// give it some money
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, nil, err
	}
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		_, err := m.AddBlock()
		if err != nil {
			return nil, nil, err
		}
	}

	cf := func() error {
		return errors.Compose(m.Close(), w.Close())
	}
	return w, cf, nil
}

// newCustomTestingHost is a helper function that creates a ready-to-use host.
func newCustomTestingHost(testdir string, cs modules.ConsensusSet, tp modules.TransactionPool, mux *siamux.SiaMux, deps modules.Dependencies) (modules.Host, closeFn, error) {
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, nil, err
	}
	w, walletCF, err := newTestingWallet(testdir, cs, tp)
	if err != nil {
		return nil, nil, err
	}
	h, err := host.NewCustomHost(deps, cs, g, tp, w, mux, "localhost:0", filepath.Join(testdir, modules.HostDir))
	if err != nil {
		return nil, nil, err
	}

	// configure host to accept contracts
	settings := h.InternalSettings()
	settings.AcceptingContracts = true
	err = h.SetInternalSettings(settings)
	if err != nil {
		return nil, nil, err
	}

	// add storage to host
	storageFolder := filepath.Join(testdir, "storage")
	err = os.MkdirAll(storageFolder, 0700)
	if err != nil {
		return nil, nil, err
	}
	err = h.AddStorageFolder(storageFolder, modules.SectorSize*64)
	if err != nil {
		return nil, nil, err
	}

	cf := func() error {
		return errors.Compose(h.Close(), walletCF(), g.Close())
	}
	return h, cf, nil
}

// newTestingHost is a helper function that creates a ready-to-use host.
func newTestingHost(testdir string, cs modules.ConsensusSet, tp modules.TransactionPool, mux *siamux.SiaMux) (modules.Host, closeFn, error) {
	return newCustomTestingHost(testdir, cs, tp, mux, modules.ProdDependencies)
}

// newTestingContractor is a helper function that creates a ready-to-use
// contractor.
func newTestingContractor(testdir string, g modules.Gateway, cs modules.ConsensusSet, tp modules.TransactionPool, rl *ratelimit.RateLimit, deps modules.Dependencies) (*Contractor, closeFn, error) {
	w, walletCF, err := newTestingWallet(testdir, cs, tp)
	if err != nil {
		return nil, nil, err
	}
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, nil, err
	}
	hdb, errChan := hostdb.New(g, cs, tp, mux, filepath.Join(testdir, "hostdb"))
	if err := <-errChan; err != nil {
		return nil, nil, err
	}
	contractor, errChan := newWithDeps(cs, w, tp, hdb, rl, filepath.Join(testdir, "contractor"), deps)
	err = <-errChan
	if err != nil {
		return nil, nil, err
	}
	cf := func() error {
		return errors.Compose(contractor.Close(), hdb.Close(), mux.Close(), walletCF())
	}
	return contractor, cf, <-errChan
}

// newTestingTrio creates a Host, Contractor, and TestMiner that can be
// used for testing host/renter interactions.
func newTestingTrio(name string) (modules.Host, *Contractor, modules.TestMiner, closeFn, error) {
	return newTestingTrioWithContractorDeps(name, modules.ProdDependencies)
}

// newTestingTrioWithContractorDeps creates a Host, Contractor, and TestMiner
// that can be used for testing host/renter interactions.
func newTestingTrioWithContractorDeps(name string, deps modules.Dependencies) (modules.Host, *Contractor, modules.TestMiner, closeFn, error) {
	testdir := build.TempDir("contractor", name)

	// create mux
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return newCustomTestingTrio(name, mux, modules.ProdDependencies, deps)
}

// newCustomTestingTrio creates a Host, Contractor, and TestMiner that can be
// used for testing host/renter interactions. It allows to pass a custom siamux.
func newCustomTestingTrio(name string, mux *siamux.SiaMux, hdeps, cdeps modules.Dependencies) (modules.Host, *Contractor, modules.TestMiner, closeFn, error) {
	testdir := build.TempDir("contractor", name)

	// create miner
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, nil, nil, nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	w, err := modWallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	encrypted, err := w.Encrypted()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !encrypted {
		_, err = w.Encrypt(key)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// create host and contractor, using same consensus set and gateway
	h, hostCF, err := newCustomTestingHost(filepath.Join(testdir, "Host"), cs, tp, mux, hdeps)
	if err != nil {
		return nil, nil, nil, nil, build.ExtendErr("error creating testing host", err)
	}
	c, contractorCF, err := newTestingContractor(filepath.Join(testdir, "Contractor"), g, cs, tp, ratelimit.NewRateLimit(0, 0, 0), cdeps)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// announce the host
	err = h.Announce()
	if err != nil {
		return nil, nil, nil, nil, build.ExtendErr("error announcing host", err)
	}

	// mine a block, processing the announcement
	_, err = m.AddBlock()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// wait for hostdb to scan host
	err = build.Retry(100, 100*time.Millisecond, func() error {
		activeHosts, err := c.hdb.ActiveHosts()
		if err != nil {
			return err
		}
		if len(activeHosts) == 0 {
			return errors.New("no active hosts")
		}
		complete, scanCheckErr := c.hdb.InitialScanComplete()
		if scanCheckErr != nil {
			return scanCheckErr
		}
		if !complete {
			return errors.New("initial scan not complete")
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	cf := func() error {
		return errors.Compose(mux.Close(), m.Close(), contractorCF(), hostCF(), w.Close(), tp.Close(), cs.Close(), g.Close())
	}
	return h, c, m, cf, nil
}

// TestIntegrationFormContract tests that the contractor can form contracts
// with the host module.
func TestIntegrationFormContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	h, c, _, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// acquire the contract maintenance lock for the duration of the test. This
	// prevents theadedContractMaintenance from running.
	c.maintenanceLock.Lock()
	defer c.maintenanceLock.Unlock()

	// get the host's entry from the db
	hostEntry, ok, err := c.hdb.Host(h.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no entry for host in db")
	}

	// set an allowance but don't use SetAllowance to avoid automatic contract
	// formation.
	c.mu.Lock()
	c.allowance = modules.DefaultAllowance
	c.mu.Unlock()

	// form a contract with the host
	_, _, err = c.managedNewContract(hostEntry, types.SiacoinPrecision.Mul64(50), c.blockHeight+100)
	if err != nil {
		t.Fatal(err)
	}
}

// TestFormContractSmallAllowance tests to make sure that a contract doesn't
// form when there are insufficient funds in the allowance
func TestFormContractSmallAllowance(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	h, c, _, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// get the host's entry from the db
	hostEntry, ok, err := c.hdb.Host(h.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no entry for host in db")
	}

	// set an allowance but don't use SetAllowance to avoid automatic contract
	// formation. Setting funds to 1SC to mimic bug report found in production.
	// Using production number of hosts as well
	c.mu.Lock()
	c.allowance = modules.DefaultAllowance
	c.allowance.Funds = types.SiacoinPrecision.Mul64(1)
	c.allowance.Hosts = uint64(50)
	initialContractFunds := c.allowance.Funds.Div64(c.allowance.Hosts).Div64(3)
	c.mu.Unlock()

	// try to form a contract with the host
	_, _, err = c.managedNewContract(hostEntry, initialContractFunds, c.blockHeight+100)
	if err == nil {
		t.Fatal("Expected underflow error for insufficient funds")
	}
}

// TestIntegrationReviseContract tests that the contractor can revise a
// contract previously formed with a host.
func TestIntegrationReviseContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	h, c, _, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// acquire the contract maintenance lock for the duration of the test. This
	// prevents theadedContractMaintenance from running.
	c.maintenanceLock.Lock()
	defer c.maintenanceLock.Unlock()

	// get the host's entry from the db
	hostEntry, ok, err := c.hdb.Host(h.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no entry for host in db")
	}

	// set an allowance but don't use SetAllowance to avoid automatic contract
	// formation.
	c.mu.Lock()
	c.allowance = modules.DefaultAllowance
	c.mu.Unlock()

	// form a contract with the host
	_, contract, err := c.managedNewContract(hostEntry, types.SiacoinPrecision.Mul64(50), c.blockHeight+100)
	if err != nil {
		t.Fatal(err)
	}

	// revise the contract
	editor, err := c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := fastrand.Bytes(int(modules.SectorSize))
	_, err = editor.Upload(data)
	if err != nil {
		t.Fatal(err)
	}
	err = editor.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationUploadDownload tests that the contractor can upload data to
// a host and download it intact.
func TestIntegrationUploadDownload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	h, c, _, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// get the host's entry from the db
	hostEntry, ok, err := c.hdb.Host(h.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no entry for host in db")
	}

	// set an allowance but don't use SetAllowance to avoid automatic contract
	// formation.
	c.mu.Lock()
	c.allowance = modules.DefaultAllowance
	c.mu.Unlock()

	// form a contract with the host
	_, contract, err := c.managedNewContract(hostEntry, types.SiacoinPrecision.Mul64(50), c.blockHeight+100)
	if err != nil {
		t.Fatal(err)
	}

	// revise the contract
	editor, err := c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := fastrand.Bytes(int(modules.SectorSize))
	root, err := editor.Upload(data)
	if err != nil {
		t.Fatal(err)
	}
	err = editor.Close()
	if err != nil {
		t.Fatal(err)
	}

	// download the data
	downloader, err := c.Downloader(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	retrieved, err := downloader.Download(root, 0, uint32(modules.SectorSize))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, retrieved) {
		t.Fatal("downloaded data does not match original")
	}
	err = downloader.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationRenew tests that the contractor can renew a previously-
// formed file contract.
func TestIntegrationRenew(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	_, c, m, cf, err := newTestingTrioWithContractorDeps(t.Name(), &dependencies.DependencyLegacyRenew{})
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// set an allowance and wait for a contract to be formed.
	a := modules.DefaultAllowance
	a.Hosts = 1
	if err := c.SetAllowance(a); err != nil {
		t.Fatal(err)
	}
	numRetries := 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			if _, err := m.AddBlock(); err != nil {
				return err
			}
		}
		numRetries++
		// Check for number of contracts and number of pubKeys as there is a
		// slight delay between the contract being added to the contract set and
		// the pubkey being added to the contractor map
		c.mu.Lock()
		numPubKeys := len(c.pubKeysToContractID)
		c.mu.Unlock()
		numContracts := len(c.Contracts())
		if numContracts != 1 {
			return fmt.Errorf("Expected 1 contracts, found %v", numContracts)
		}
		if numPubKeys != 1 {
			return fmt.Errorf("Expected 1 pubkey, found %v", numPubKeys)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// get the contract
	contract := c.Contracts()[0]

	// revise the contract
	editor, err := c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := fastrand.Bytes(int(modules.SectorSize))
	// insert the sector
	root, err := editor.Upload(data)
	if err != nil {
		t.Fatal(err)
	}
	err = editor.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Grab the host settings.
	hostSettings := editor.HostSettings()

	// renew the contract
	err = c.managedAcquireAndUpdateContractUtility(contract.ID, modules.ContractUtility{GoodForRenew: true})
	if err != nil {
		t.Fatal(err)
	}
	contract, err = c.managedRenew(contract.ID, contract.HostPublicKey, types.SiacoinPrecision.Mul64(50), c.blockHeight+200, hostSettings)
	if err != nil {
		t.Fatal(err)
	}

	// check renewed contract
	if contract.EndHeight != c.blockHeight+200 {
		t.Fatal(contract.EndHeight)
	}

	// download the renewed contract
	downloader, err := c.Downloader(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	retrieved, err := downloader.Download(root, 0, uint32(modules.SectorSize))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, retrieved) {
		t.Fatal("downloaded data does not match original")
	}
	err = downloader.Close()
	if err != nil {
		t.Fatal(err)
	}

	// renew to a lower height
	err = c.managedAcquireAndUpdateContractUtility(contract.ID, modules.ContractUtility{GoodForRenew: true})
	if err != nil {
		t.Fatal(err)
	}
	contract, err = c.managedRenew(contract.ID, contract.HostPublicKey, types.SiacoinPrecision.Mul64(50), c.blockHeight+100, hostSettings)
	if err != nil {
		t.Fatal(err)
	}
	if contract.EndHeight != c.blockHeight+100 {
		t.Fatal(contract.EndHeight)
	}

	// revise the contract
	editor, err = c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	data = fastrand.Bytes(int(modules.SectorSize))
	// insert the sector
	_, err = editor.Upload(data)
	if err != nil {
		t.Fatal(err)
	}
	err = editor.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// TestIntegrationDownloaderCaching tests that downloaders are properly cached
// by the contractor. When two downloaders are requested for the same
// contract, only one underlying downloader should be created.
func TestIntegrationDownloaderCaching(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	_, c, m, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// set an allowance and wait for a contract to be formed.
	if err := c.SetAllowance(modules.DefaultAllowance); err != nil {
		t.Fatal(err)
	}
	if err := build.Retry(10, time.Second, func() error {
		_, err := m.AddBlock()
		if err != nil {
			return err
		}
		if len(c.Contracts()) == 0 {
			return errors.New("no contracts were formed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// get the contract
	contract := c.Contracts()[0]

	// create a downloader
	d1, err := c.Downloader(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// create another downloader
	d2, err := c.Downloader(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// downloaders should match
	if d1 != d2 {
		t.Fatal("downloader was not cached")
	}

	// close one of the downloaders; it should not fully close, since d1 is
	// still using it
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}

	c.mu.RLock()
	_, ok := c.downloaders[contract.ID]
	_, sok := c.sessions[contract.ID]
	c.mu.RUnlock()
	if !ok && !sok {
		t.Fatal("expected downloader to still be present")
	}

	// create another downloader
	d3, err := c.Downloader(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// downloaders should match
	if d3 != d1 {
		t.Fatal("closing one client should not fully close the downloader")
	}

	// close both downloaders
	if err := d1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}

	c.mu.RLock()
	_, ok = c.downloaders[contract.ID]
	_, sok = c.sessions[contract.ID]
	c.mu.RUnlock()
	if ok || sok {
		t.Fatal("did not expect downloader to still be present")
	}

	// create another downloader
	d4, err := c.Downloader(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// downloaders should match
	if d4 == d1 {
		t.Fatal("downloader should not have been cached after all clients were closed")
	}
	d4.Close()
}

// TestIntegrationEditorCaching tests that editors are properly cached
// by the contractor. When two editors are requested for the same
// contract, only one underlying editor should be created.
func TestIntegrationEditorCaching(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	_, c, m, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// set an allowance and wait for a contract to be formed.
	if err := c.SetAllowance(modules.DefaultAllowance); err != nil {
		t.Fatal(err)
	}
	numRetries := 0
	if err := build.Retry(2000, 100*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			if _, err := m.AddBlock(); err != nil {
				return err
			}
		}
		numRetries++
		if len(c.Contracts()) == 0 {
			return errors.New("no contracts were formed")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// get the contract
	contract := c.Contracts()[0]

	// create an editor
	var d1 Editor
	err = build.Retry(100, 100*time.Millisecond, func() error {
		d1, err = c.Editor(contract.HostPublicKey, nil)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// create another editor
	d2, err := c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// editors should match
	if d1 != d2 {
		t.Fatal("editor was not cached")
	}

	// close one of the editors; it should not fully close, since d1 is
	// still using it
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}

	c.mu.RLock()
	_, ok := c.editors[contract.ID]
	_, sok := c.sessions[contract.ID]
	c.mu.RUnlock()
	if !ok && !sok {
		t.Fatal("expected editor to still be present")
	}

	// create another editor
	d3, err := c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// editors should match
	if d3 != d1 {
		t.Fatal("closing one client should not fully close the editor")
	}

	// close both editors
	if err := d1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d2.Close(); err != nil {
		t.Fatal(err)
	}

	c.mu.RLock()
	_, ok = c.editors[contract.ID]
	_, sok = c.sessions[contract.ID]
	c.mu.RUnlock()
	if ok || sok {
		t.Fatal("did not expect editor to still be present")
	}

	// create another editor
	d4, err := c.Editor(contract.HostPublicKey, nil)
	if err != nil {
		t.Fatal(err)
	}

	// editors should match
	if d4 == d1 {
		t.Fatal("editor should not have been cached after all clients were closed")
	}
	d4.Close()
}

// TestContractPresenceLeak tests that a renter can not tell from the response
// of the host to RPCs if the host has the contract if the renter doesn't
// own this contract. See https://gitlab.com/NebulousLabs/Sia/issues/2327.
func TestContractPresenceLeak(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	h, c, _, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// get the host's entry from the db
	hostEntry, ok, err := c.hdb.Host(h.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no entry for host in db")
	}

	// set an allowance but don't use SetAllowance to avoid automatic contract
	// formation.
	c.mu.Lock()
	c.allowance = modules.DefaultAllowance
	c.mu.Unlock()

	// form a contract with the host
	_, contract, err := c.managedNewContract(hostEntry, types.SiacoinPrecision.Mul64(10), c.blockHeight+100)
	if err != nil {
		t.Fatal(err)
	}

	// Connect with bad challenge response. Try correct
	// and incorrect contract IDs. Compare errors.
	wrongID := contract.ID
	wrongID[0] ^= 0x01
	fcids := []types.FileContractID{contract.ID, wrongID}
	var errors []error

	for _, fcid := range fcids {
		var challenge crypto.Hash
		var signature crypto.Signature
		conn, err := net.Dial("tcp", string(hostEntry.NetAddress))
		if err != nil {
			t.Fatalf("Couldn't dial tpc connection with host @ %v: %v.", string(hostEntry.NetAddress), err)
		}
		if err := encoding.WriteObject(conn, modules.RPCDownload); err != nil {
			t.Fatalf("Couldn't initiate RPC: %v.", err)
		}
		if err := encoding.WriteObject(conn, fcid); err != nil {
			t.Fatalf("Couldn't send fcid: %v.", err)
		}
		if err := encoding.ReadObject(conn, &challenge, 32); err != nil {
			t.Fatalf("Couldn't read challenge: %v.", err)
		}
		if err := encoding.WriteObject(conn, signature); err != nil {
			t.Fatalf("Couldn't send signature: %v.", err)
		}
		err = modules.ReadNegotiationAcceptance(conn)
		if err == nil {
			t.Fatal("Expected an error, got success.")
		}
		errors = append(errors, err)
	}
	if errors[0].Error() != errors[1].Error() {
		t.Fatalf("Expected to get equal errors, got %q and %q.", errors[0], errors[1])
	}
}
