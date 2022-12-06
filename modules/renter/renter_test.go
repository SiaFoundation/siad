package renter

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/siamux"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/consensus"
	"go.sia.tech/siad/modules/gateway"
	"go.sia.tech/siad/modules/host"
	"go.sia.tech/siad/modules/miner"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/modules/renter/hostdb"
	"go.sia.tech/siad/modules/renter/proto"
	"go.sia.tech/siad/modules/transactionpool"
	"go.sia.tech/siad/modules/wallet"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

// renterTester contains all of the modules that are used while testing the renter.
type renterTester struct {
	cs      modules.ConsensusSet
	gateway modules.Gateway
	miner   modules.TestMiner
	tpool   modules.TransactionPool
	wallet  modules.Wallet

	mux *siamux.SiaMux

	renter *Renter
	dir    string
}

// Close shuts down the renter tester.
func (rt *renterTester) Close() error {
	err1 := rt.cs.Close()
	err2 := rt.gateway.Close()
	err3 := rt.miner.Close()
	err4 := rt.tpool.Close()
	err5 := rt.wallet.Close()
	err6 := rt.mux.Close()
	err7 := rt.renter.Close()
	return errors.Compose(err1, err2, err3, err4, err5, err6, err7)
}

// addCustomHost adds a host to the test group so that it appears in the host db
func (rt *renterTester) addCustomHost(testdir string, deps modules.Dependencies) (modules.Host, error) {
	// create a siamux for this particular host
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, err
	}

	h, err := host.NewCustomHost(deps, rt.cs, rt.gateway, rt.tpool, rt.wallet, mux, "localhost:0", filepath.Join(testdir, modules.HostDir))
	if err != nil {
		return nil, err
	}

	// configure host to accept contracts and to have a registry.
	settings := h.InternalSettings()
	settings.AcceptingContracts = true
	settings.RegistrySize = 640 * modules.RegistryEntrySize
	err = h.SetInternalSettings(settings)
	if err != nil {
		return nil, err
	}

	// add storage to host
	storageFolder := filepath.Join(testdir, "storage")
	err = os.MkdirAll(storageFolder, 0700)
	if err != nil {
		return nil, err
	}
	err = h.AddStorageFolder(storageFolder, modules.SectorSize*64)
	if err != nil {
		return nil, err
	}

	// announce the host
	err = h.Announce()
	if err != nil {
		return nil, build.ExtendErr("error announcing host", err)
	}

	// mine a block, processing the announcement
	_, err = rt.miner.AddBlock()
	if err != nil {
		return nil, err
	}

	// wait for hostdb to scan host
	activeHosts, err := rt.renter.ActiveHosts()
	if err != nil {
		return nil, err
	}
	for i := 0; i < 50 && len(activeHosts) == 0; i++ {
		time.Sleep(time.Millisecond * 100)
	}
	activeHosts, err = rt.renter.ActiveHosts()
	if err != nil {
		return nil, err
	}
	if len(activeHosts) == 0 {
		return nil, errors.New("host did not make it into the contractor hostdb in time")
	}

	return h, nil
}

// addHost adds a host to the test group so that it appears in the host db
func (rt *renterTester) addHost(name string) (modules.Host, error) {
	return rt.addCustomHost(filepath.Join(rt.dir, name), modules.ProdDependencies)
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

// createZeroByteFileOnDisk creates a 0 byte file on disk so that a Stat of the
// local path won't return an error
func (rt *renterTester) createZeroByteFileOnDisk() (string, error) {
	path := filepath.Join(rt.renter.staticFileSystem.Root(), persist.RandomSuffix())
	err := ioutil.WriteFile(path, []byte{}, 0600)
	if err != nil {
		return "", err
	}
	return path, nil
}

// reloadRenter closes the given renter and then re-adds it, effectively
// reloading the renter.
func (rt *renterTester) reloadRenter(r *Renter) (*Renter, error) {
	return rt.reloadRenterWithDependency(r, r.deps)
}

// reloadRenterWithDependency closes the given renter and recreates it using the
// given dependency, it then re-adds the renter on the renter tester effectively
// reloading it.
func (rt *renterTester) reloadRenterWithDependency(r *Renter, deps modules.Dependencies) (*Renter, error) {
	err := r.Close()
	if err != nil {
		return nil, err
	}

	r, err = newRenterWithDependency(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, filepath.Join(rt.dir, modules.RenterDir), deps)
	if err != nil {
		return nil, err
	}

	err = rt.addRenter(r)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// newRenterTester creates a ready-to-use renter tester with money in the
// wallet.
func newRenterTester(name string) (*renterTester, error) {
	testdir := build.TempDir("renter", name)
	rt, err := newRenterTesterNoRenter(testdir)
	if err != nil {
		return nil, err
	}

	rl := ratelimit.NewRateLimit(0, 0, 0)
	r, errChan := New(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, rl, filepath.Join(testdir, modules.RenterDir))
	if err := <-errChan; err != nil {
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
	// Create the siamux
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, err
	}

	// Create the modules.
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
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
		mux: mux,

		cs:      cs,
		gateway: g,
		miner:   m,
		tpool:   tp,
		wallet:  w,

		dir: testdir,
	}, nil
}

// newRenterTesterWithDependency creates a ready-to-use renter tester with money
// in the wallet.
func newRenterTesterWithDependency(name string, deps modules.Dependencies) (*renterTester, error) {
	testdir := build.TempDir("renter", name)
	rt, err := newRenterTesterNoRenter(testdir)
	if err != nil {
		return nil, err
	}

	// Create the siamux
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, _, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0", "localhost:0")
	if err != nil {
		return nil, err
	}

	r, err := newRenterWithDependency(rt.gateway, rt.cs, rt.wallet, rt.tpool, mux, filepath.Join(testdir, modules.RenterDir), deps)
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
func newRenterWithDependency(g modules.Gateway, cs modules.ConsensusSet, wallet modules.Wallet, tpool modules.TransactionPool, mux *siamux.SiaMux, persistDir string, deps modules.Dependencies) (*Renter, error) {
	hdb, errChan := hostdb.NewCustomHostDB(g, cs, tpool, mux, persistDir, deps)
	if err := <-errChan; err != nil {
		return nil, err
	}
	rl := ratelimit.NewRateLimit(0, 0, 0)
	contractSet, err := proto.NewContractSet(filepath.Join(persistDir, "contracts"), rl, modules.ProdDependencies)
	if err != nil {
		return nil, err
	}

	logger, err := persist.NewFileLogger(filepath.Join(persistDir, "contractor.log"))
	if err != nil {
		return nil, err
	}

	hc, errChan := contractor.NewCustomContractor(cs, wallet, tpool, hdb, persistDir, contractSet, logger, deps)
	if err := <-errChan; err != nil {
		return nil, err
	}
	renter, errChan := NewCustomRenter(g, cs, tpool, hdb, wallet, hc, mux, persistDir, rl, deps)
	return renter, <-errChan
}

// TestRenterCanAccessEphemeralAccountHostSettings verifies that the renter has
// access to the host's external settings and that they include the new
// ephemeral account setting fields.
func TestRenterCanAccessEphemeralAccountHostSettings(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a host to the test group
	h, err := rt.addHost(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	hostEntry, found, err := rt.renter.hostDB.Host(h.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Expected the newly added host to be found in the hostDB")
	}

	if hostEntry.EphemeralAccountExpiry != modules.DefaultEphemeralAccountExpiry {
		t.Fatal("Unexpected account expiry")
	}

	if !hostEntry.MaxEphemeralAccountBalance.Equals(modules.DefaultMaxEphemeralAccountBalance) {
		t.Fatal("Unexpected max account balance")
	}
}

// TestRenterPricesDivideByZero verifies that the Price Estimation catches
// divide by zero errors.
func TestRenterPricesDivideByZero(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Confirm price estimation returns error if there are no hosts available
	_, _, err = rt.renter.PriceEstimation(modules.Allowance{})
	if err == nil {
		t.Fatal("Expected error due to no hosts")
	}

	// Add a host to the test group
	_, err = rt.addHost(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Confirm price estimation does not return an error now that there is a
	// host available
	_, _, err = rt.renter.PriceEstimation(modules.Allowance{})
	if err != nil {
		t.Fatal(err)
	}
}
