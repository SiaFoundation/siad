package renter

import (
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/consensus"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/modules/miner"
	"gitlab.com/NebulousLabs/Sia/modules/renter/contractor"
	"gitlab.com/NebulousLabs/Sia/modules/renter/hostdb"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/Sia/modules/wallet"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// renterTester contains all of the modules that are used while testing the renter.
type renterTester struct {
	cs        modules.ConsensusSet
	gateway   modules.Gateway
	miner     modules.TestMiner
	tpool     modules.TransactionPool
	wallet    modules.Wallet
	walletKey crypto.CipherKey

	renter *Renter
	dir    string
}

// Close shuts down the renter tester.
func (rt *renterTester) Close() error {
	rt.wallet.Lock()
	rt.cs.Close()
	rt.gateway.Close()
	return nil
}

// addRenter adds a renter to the renter tester and then make sure there is
// money in the wallet
func (rt *renterTester) addRenter(r *Renter) error {
	rt.renter = r
	// Mine blocks until there is money in the wallet.
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		_, err := rt.miner.AddBlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// createTestFileOnDisk creates a 0 byte file on disk so that a Stat of the
// local path won't return an error
func (rt *renterTester) createTestFileOnDisk() (string, error) {
	path := filepath.Join(rt.renter.staticFilesDir, persist.RandomSuffix())
	err := ioutil.WriteFile(path, []byte{}, 0600)
	if err != nil {
		return "", err
	}
	return path, nil
}

// newRenterTester creates a ready-to-use renter tester with money in the
// wallet.
func newRenterTester(name string) (*renterTester, error) {
	testdir := build.TempDir("renter", name)
	rt, err := newRenterTesterNoRenter(testdir)
	if err != nil {
		return nil, err
	}
	r, err := New(rt.gateway, rt.cs, rt.wallet, rt.tpool, filepath.Join(testdir, modules.RenterDir))
	if err != nil {
		return nil, err
	}
	err = rt.addRenter(r)
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// newRenterTesterNoRenter creates all the modules for the renter tester except
// the renter. A renter will need to be added and blocks mined to add money to
// the wallet.
func newRenterTesterNoRenter(testdir string) (*renterTester, error) {
	// Create the modules.
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, err := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		return nil, err
	}
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, err
	}
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	_, err = w.Encrypt(key)
	if err != nil {
		return nil, err
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, err
	}

	// Assemble all pieces into a renter tester.
	return &renterTester{
		cs:      cs,
		gateway: g,
		miner:   m,
		tpool:   tp,
		wallet:  w,

		dir: testdir,
	}, nil
}

// newRenterTesterWithDependency creates a ready-to-use renter tester with money in the
// wallet.
func newRenterTesterWithDependency(name string, deps modules.Dependencies) (*renterTester, error) {
	testdir := build.TempDir("renter", name)
	rt, err := newRenterTesterNoRenter(testdir)
	if err != nil {
		return nil, err
	}
	r, err := newRenterWithDependency(rt.gateway, rt.cs, rt.wallet, rt.tpool, filepath.Join(testdir, modules.RenterDir), deps)
	if err != nil {
		return nil, err
	}
	err = rt.addRenter(r)
	if err != nil {
		return nil, err
	}
	return rt, nil
}

// newRenterWithDependency creates a Renter with custom dependency
func newRenterWithDependency(g modules.Gateway, cs modules.ConsensusSet, wallet modules.Wallet, tpool modules.TransactionPool, persistDir string, deps modules.Dependencies) (*Renter, error) {
	hdb, err := hostdb.New(g, cs, tpool, persistDir)
	if err != nil {
		return nil, err
	}
	hc, err := contractor.New(cs, wallet, tpool, hdb, persistDir)
	if err != nil {
		return nil, err
	}
	return NewCustomRenter(g, cs, tpool, hdb, wallet, hc, persistDir, deps)
}

// stubHostDB is the minimal implementation of the hostDB interface. It can be
// embedded in other mock hostDB types, removing the need to re-implement all
// of the hostDB's methods on every mock.
type stubHostDB struct{}

func (stubHostDB) ActiveHosts() []modules.HostDBEntry   { return nil }
func (stubHostDB) AllHosts() []modules.HostDBEntry      { return nil }
func (stubHostDB) AverageContractPrice() types.Currency { return types.Currency{} }
func (stubHostDB) Close() error                         { return nil }
func (stubHostDB) Filter() (modules.FilterMode, map[string]types.SiaPublicKey) {
	return 0, make(map[string]types.SiaPublicKey)
}
func (stubHostDB) SetFilterMode(fm modules.FilterMode, hosts []types.SiaPublicKey) error { return nil }
func (stubHostDB) IsOffline(modules.NetAddress) bool                                     { return true }
func (stubHostDB) RandomHosts(int, []types.SiaPublicKey) ([]modules.HostDBEntry, error) {
	return []modules.HostDBEntry{}, nil
}
func (stubHostDB) EstimateHostScore(modules.HostDBEntry, modules.Allowance) (modules.HostScoreBreakdown, error) {
	return modules.HostScoreBreakdown{}, nil
}
func (stubHostDB) Host(types.SiaPublicKey) (modules.HostDBEntry, bool) {
	return modules.HostDBEntry{}, false
}
func (stubHostDB) ScoreBreakdown(modules.HostDBEntry) (modules.HostScoreBreakdown, error) {
	return modules.HostScoreBreakdown{}, nil
}

// stubContractor is the minimal implementation of the hostContractor
// interface.
type stubContractor struct{}

func (stubContractor) SetAllowance(modules.Allowance) error { return nil }
func (stubContractor) Allowance() modules.Allowance         { return modules.Allowance{} }
func (stubContractor) Contract(modules.NetAddress) (modules.RenterContract, bool) {
	return modules.RenterContract{}, false
}
func (stubContractor) Contracts() []modules.RenterContract                    { return nil }
func (stubContractor) CurrentPeriod() types.BlockHeight                       { return 0 }
func (stubContractor) IsOffline(modules.NetAddress) bool                      { return false }
func (stubContractor) Editor(types.FileContractID) (contractor.Editor, error) { return nil, nil }
func (stubContractor) Downloader(types.FileContractID) (contractor.Downloader, error) {
	return nil, nil
}

type pricesStub struct {
	stubHostDB

	dbEntries []modules.HostDBEntry
}

func (pricesStub) InitialScanComplete() (bool, error) { return true, nil }
func (pricesStub) IPViolationsCheck() bool            { return true }

func (ps pricesStub) RandomHosts(_ int, _, _ []types.SiaPublicKey) ([]modules.HostDBEntry, error) {
	return ps.dbEntries, nil
}
func (ps pricesStub) RandomHostsWithAllowance(_ int, _, _ []types.SiaPublicKey, _ modules.Allowance) ([]modules.HostDBEntry, error) {
	return ps.dbEntries, nil
}
func (ps pricesStub) SetIPViolationCheck(enabled bool) { return }

// TestRenterPricesDivideByZero verifies that the Price Estimation catches
// divide by zero errors.
func TestRenterPricesDivideByZero(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Confirm price estimation returns error if there are no hosts available
	_, _, err = rt.renter.PriceEstimation(modules.Allowance{})
	if err == nil {
		t.Fatal("Expected error due to no hosts")
	}

	// Create a stubbed hostdb, add an entry.
	hdb := &pricesStub{}
	id := rt.renter.mu.Lock()
	rt.renter.hostDB = hdb
	rt.renter.mu.Unlock(id)
	dbe := modules.HostDBEntry{}
	dbe.ContractPrice = types.SiacoinPrecision
	dbe.DownloadBandwidthPrice = types.SiacoinPrecision
	dbe.UploadBandwidthPrice = types.SiacoinPrecision
	dbe.StoragePrice = types.SiacoinPrecision
	pk := fastrand.Bytes(crypto.EntropySize)
	dbe.PublicKey = types.SiaPublicKey{Key: pk}
	hdb.dbEntries = append(hdb.dbEntries, dbe)

	// Confirm price estimation does not return an error now that there is a
	// host available
	_, _, err = rt.renter.PriceEstimation(modules.Allowance{})
	if err != nil {
		t.Fatal(err)
	}

	// Set allowance funds and host contract price such that the allowance funds
	// are not sufficient to cover the contract price
	allowance := modules.Allowance{
		Funds:       types.SiacoinPrecision,
		Hosts:       1,
		Period:      12096,
		RenewWindow: 4032,
	}
	dbe.ContractPrice = allowance.Funds.Mul64(2)

	// Confirm price estimation returns error because of the contract and
	// funding prices
	_, _, err = rt.renter.PriceEstimation(allowance)
	if err == nil {
		t.Fatal("Expected error due to allowance funds inefficient")
	}
}

// TestRenterPricesVolatility verifies that the renter caches its price
// estimation, and subsequent calls result in non-volatile results.
func TestRenterPricesVolatility(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// create a stubbed hostdb, query it with one contract, add another, verify
	// the price estimation remains constant until the timeout has passed.
	hdb := &pricesStub{}
	id := rt.renter.mu.Lock()
	rt.renter.hostDB = hdb
	rt.renter.mu.Unlock(id)
	dbe := modules.HostDBEntry{}
	dbe.ContractPrice = types.SiacoinPrecision
	dbe.DownloadBandwidthPrice = types.SiacoinPrecision
	dbe.UploadBandwidthPrice = types.SiacoinPrecision
	dbe.StoragePrice = types.SiacoinPrecision
	// Add 4 host entries in the database with different public keys.
	for len(hdb.dbEntries) < modules.PriceEstimationScope {
		pk := fastrand.Bytes(crypto.EntropySize)
		dbe.PublicKey = types.SiaPublicKey{Key: pk}
		hdb.dbEntries = append(hdb.dbEntries, dbe)
	}
	allowance := modules.Allowance{}
	initial, _, err := rt.renter.PriceEstimation(allowance)
	if err != nil {
		t.Fatal(err)
	}
	// Changing the contract price should be enough to trigger a change
	// if the hosts are not cached.
	dbe.ContractPrice = dbe.ContractPrice.Mul64(2)
	pk := fastrand.Bytes(crypto.EntropySize)
	dbe.PublicKey = types.SiaPublicKey{Key: pk}
	hdb.dbEntries = append(hdb.dbEntries, dbe)
	after, _, err := rt.renter.PriceEstimation(allowance)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial, after) {
		t.Log(initial)
		t.Log(after)
		t.Fatal("expected renter price estimation to be constant")
	}
}
