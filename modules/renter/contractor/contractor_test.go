package contractor

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// newStub is used to test the New function. It implements all of the contractor's
// dependencies.
type newStub struct{}

// consensus set stubs
func (newStub) ConsensusSetSubscribe(modules.ConsensusSetSubscriber, modules.ConsensusChangeID, <-chan struct{}) error {
	return nil
}
func (newStub) Synced() bool                               { return true }
func (newStub) Unsubscribe(modules.ConsensusSetSubscriber) { return }
func (newStub) TryTransactionSet([]types.Transaction) (modules.ConsensusChange, error) {
	return modules.ConsensusChange{}, nil
}

// wallet stubs
func (newStub) NextAddress() (uc types.UnlockConditions, err error) { return }
func (newStub) PrimarySeed() (modules.Seed, uint64, error)          { return modules.Seed{}, 0, nil }
func (newStub) RegisterTransaction(types.Transaction, []types.Transaction) (modules.TransactionBuilder, error) {
	return nil, nil
}
func (newStub) StartTransaction() (tb modules.TransactionBuilder, err error) { return }
func (newStub) Unlocked() (bool, error)                                      { return true, nil }

// transaction pool stubs
func (newStub) AcceptTransactionSet([]types.Transaction) error      { return nil }
func (newStub) FeeEstimation() (a types.Currency, b types.Currency) { return }

// hdb stubs
func (newStub) AllHosts() ([]modules.HostDBEntry, error)    { return nil, nil }
func (newStub) ActiveHosts() ([]modules.HostDBEntry, error) { return nil, nil }
func (newStub) CheckForIPViolations([]types.SiaPublicKey) ([]types.SiaPublicKey, error) {
	return nil, nil
}
func (newStub) Filter() (modules.FilterMode, map[string]types.SiaPublicKey, error) {
	return 0, make(map[string]types.SiaPublicKey), nil
}
func (newStub) SetFilterMode(fm modules.FilterMode, hosts []types.SiaPublicKey) error      { return nil }
func (newStub) Host(types.SiaPublicKey) (settings modules.HostDBEntry, ok bool, err error) { return }
func (newStub) IncrementSuccessfulInteractions(key types.SiaPublicKey) error               { return nil }
func (newStub) IncrementFailedInteractions(key types.SiaPublicKey) error                   { return nil }
func (newStub) InitialScanComplete() (complete bool, err error) {
	return true, nil
}
func (newStub) RandomHosts(int, []types.SiaPublicKey, []types.SiaPublicKey) ([]modules.HostDBEntry, error) {
	return nil, nil
}
func (newStub) ScoreBreakdown(modules.HostDBEntry) (modules.HostScoreBreakdown, error) {
	return modules.HostScoreBreakdown{}, nil
}
func (newStub) SetAllowance(allowance modules.Allowance) error { return nil }
func (newStub) UpdateContracts([]modules.RenterContract) error { return nil }

// TestNew tests the New function.
func TestNew(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Using a stub implementation of the dependencies is fine, as long as its
	// non-nil.
	var stub newStub
	dir := build.TempDir("contractor", t.Name())

	// Sane values.
	_, errChan := New(stub, stub, stub, stub, dir)
	if err := <-errChan; err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// Nil consensus set.
	_, errChan = New(nil, stub, stub, stub, dir)
	if err := <-errChan; err != errNilCS {
		t.Fatalf("expected %v, got %v", errNilCS, err)
	}

	// Nil wallet.
	_, errChan = New(stub, nil, stub, stub, dir)
	if err := <-errChan; err != errNilWallet {
		t.Fatalf("expected %v, got %v", errNilWallet, err)
	}

	// Nil transaction pool.
	_, errChan = New(stub, stub, nil, stub, dir)
	if err := <-errChan; err != errNilTpool {
		t.Fatalf("expected %v, got %v", errNilTpool, err)
	}

	// Bad persistDir.
	_, errChan = New(stub, stub, stub, stub, "")
	if err := <-errChan; !os.IsNotExist(err) {
		t.Fatalf("expected invalid directory, got %v", err)
	}
}

// TestAllowance tests the Allowance method.
func TestAllowance(t *testing.T) {
	c := &Contractor{
		allowance: modules.Allowance{
			Funds:  types.NewCurrency64(1),
			Period: 2,
			Hosts:  3,
		},
	}
	a := c.Allowance()
	if a.Funds.Cmp(c.allowance.Funds) != 0 ||
		a.Period != c.allowance.Period ||
		a.Hosts != c.allowance.Hosts {
		t.Fatal("Allowance did not return correct allowance:", a, c.allowance)
	}
}

// stubHostDB mocks the hostDB dependency using zero-valued implementations of
// its methods.
type stubHostDB struct{}

func (stubHostDB) AllHosts() (hs []modules.HostDBEntry, err error)    { return }
func (stubHostDB) ActiveHosts() (hs []modules.HostDBEntry, err error) { return }
func (stubHostDB) CheckForIPViolations([]types.SiaPublicKey) ([]types.SiaPublicKey, error) {
	return nil, nil
}
func (stubHostDB) Filter() (modules.FilterMode, map[string]types.SiaPublicKey, error) {
	return 0, make(map[string]types.SiaPublicKey), nil
}
func (stubHostDB) SetFilterMode(fm modules.FilterMode, hosts []types.SiaPublicKey) error { return nil }
func (stubHostDB) Host(types.SiaPublicKey) (h modules.HostDBEntry, ok bool, err error)   { return }
func (stubHostDB) IncrementSuccessfulInteractions(key types.SiaPublicKey) error          { return nil }
func (stubHostDB) IncrementFailedInteractions(key types.SiaPublicKey) error              { return nil }
func (stubHostDB) PublicKey() (spk types.SiaPublicKey)                                   { return }

func (stubHostDB) InitialScanComplete() (complete bool, err error) {
	return true, nil
}
func (stubHostDB) RandomHosts(int, []types.SiaPublicKey, []types.SiaPublicKey) (hs []modules.HostDBEntry, _ error) {
	return
}
func (stubHostDB) ScoreBreakdown(modules.HostDBEntry) (modules.HostScoreBreakdown, error) {
	return modules.HostScoreBreakdown{}, nil
}
func (stubHostDB) SetAllowance(allowance modules.Allowance) error { return nil }
func (stubHostDB) UpdateContracts([]modules.RenterContract) error { return nil }

// TestIntegrationSetAllowance tests the SetAllowance method.
func TestIntegrationSetAllowance(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// create a siamux
	testdir := build.TempDir("contractor", t.Name())
	mux, err := modules.NewSiaMux(testdir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	// create testing trio
	h, c, m, err := newTestingTrioCustom(t.Name(), testdir, mux)
	if err != nil {
		t.Fatal(err)
	}

	// this test requires two hosts: create another one
	h, err = newTestingHost(build.TempDir("hostdata", ""), c.cs.(modules.ConsensusSet), c.tpool.(modules.TransactionPool), mux)
	if err != nil {
		t.Fatal(err)
	}

	// announce the extra host
	err = h.Announce()
	if err != nil {
		t.Fatal(err)
	}

	// mine a block, processing the announcement
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// wait for hostdb to scan
	hosts, err := c.hdb.RandomHosts(1, nil, nil)
	if err != nil {
		t.Fatal("failed to get hosts", err)
	}
	for i := 0; i < 100 && len(hosts) == 0; i++ {
		time.Sleep(time.Millisecond * 50)
	}

	// cancel allowance
	var a modules.Allowance
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}

	// bad args
	a.Hosts = 1
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroFunds {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroFunds, err)
	}
	a.Funds = types.SiacoinPrecision
	a.Hosts = 0
	err = c.SetAllowance(a)
	if err != ErrAllowanceNoHosts {
		t.Errorf("expected %q, got %q", ErrAllowanceNoHosts, err)
	}
	a.Hosts = 1
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroPeriod {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroPeriod, err)
	}
	a.Period = 20
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroWindow {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroWindow, err)
	}
	a.RenewWindow = 20
	err = c.SetAllowance(a)
	if err != errAllowanceWindowSize {
		t.Errorf("expected %q, got %q", errAllowanceWindowSize, err)
	}
	a.RenewWindow = 10
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroExpectedStorage {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroExpectedStorage, err)
	}
	a.ExpectedStorage = modules.DefaultAllowance.ExpectedStorage
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroExpectedUpload {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroExpectedUpload, err)
	}
	a.ExpectedUpload = modules.DefaultAllowance.ExpectedUpload
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroExpectedDownload {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroExpectedDownload, err)
	}
	a.ExpectedDownload = modules.DefaultAllowance.ExpectedDownload
	err = c.SetAllowance(a)
	if err != ErrAllowanceZeroExpectedRedundancy {
		t.Errorf("expected %q, got %q", ErrAllowanceZeroExpectedRedundancy, err)
	}
	a.ExpectedRedundancy = modules.DefaultAllowance.ExpectedRedundancy
	a.MaxPeriodChurn = modules.DefaultAllowance.MaxPeriodChurn

	// reasonable values; should succeed
	a.Funds = types.SiacoinPrecision.Mul64(100)
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) != 1 {
			return errors.New("allowance forming seems to have failed")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// set same allowance; should no-op
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	clen := c.staticContracts.Len()
	if clen != 1 {
		t.Fatal("expected 1 contract, got", clen)
	}

	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// set allowance with Hosts = 2; should only form one new contract
	a.Hosts = 2
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) != 2 {
			return errors.New("allowance forming seems to have failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// set allowance with Funds*2; should trigger renewal of both contracts
	a.Funds = a.Funds.Mul64(2)
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) != 2 {
			return errors.New("allowance forming seems to have failed")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// delete one of the contracts and set allowance with Funds*2; should
	// trigger 1 renewal and 1 new contract
	c.mu.Lock()
	ids := c.staticContracts.IDs()
	contract, _ := c.staticContracts.Acquire(ids[0])
	c.staticContracts.Delete(contract)
	c.mu.Unlock()
	a.Funds = a.Funds.Mul64(2)
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) != 2 {
			return errors.New("allowance forming seems to have failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestHostMaxDuration tests that a host will not be used if their max duration
// is not sufficient when renewing contracts
func TestHostMaxDuration(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create testing trio
	h, c, m, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Set host's MaxDuration to 5 to test if host will be skipped when contract
	// is formed
	settings := h.InternalSettings()
	settings.MaxDuration = types.BlockHeight(5)
	if err := h.SetInternalSettings(settings); err != nil {
		t.Fatal(err)
	}
	// Let host settings permeate
	err = build.Retry(50, 100*time.Millisecond, func() error {
		host, _, err := c.hdb.Host(h.PublicKey())
		if err != nil {
			return err
		}
		if settings.MaxDuration != host.MaxDuration {
			return fmt.Errorf("host max duration not set, expected %v, got %v", settings.MaxDuration, host.MaxDuration)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create allowance
	a := modules.Allowance{
		Funds:              types.SiacoinPrecision.Mul64(100),
		Hosts:              1,
		Period:             30,
		RenewWindow:        20,
		ExpectedStorage:    modules.DefaultAllowance.ExpectedStorage,
		ExpectedUpload:     modules.DefaultAllowance.ExpectedUpload,
		ExpectedDownload:   modules.DefaultAllowance.ExpectedDownload,
		ExpectedRedundancy: modules.DefaultAllowance.ExpectedRedundancy,
		MaxPeriodChurn:     modules.DefaultAllowance.MaxPeriodChurn,
	}
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for and confirm no Contract creation
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) == 0 {
			return errors.New("no contract created")
		}
		return nil
	})
	if err == nil {
		t.Fatal("Contract should not have been created")
	}

	// Set host's MaxDuration to 50 to test if host will now form contract
	settings = h.InternalSettings()
	settings.MaxDuration = types.BlockHeight(50)
	if err := h.SetInternalSettings(settings); err != nil {
		t.Fatal(err)
	}
	// Let host settings permeate
	err = build.Retry(50, 100*time.Millisecond, func() error {
		host, _, err := c.hdb.Host(h.PublicKey())
		if err != nil {
			return err
		}
		if settings.MaxDuration != host.MaxDuration {
			return fmt.Errorf("host max duration not set, expected %v, got %v", settings.MaxDuration, host.MaxDuration)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Wait for Contract creation
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if len(c.Contracts()) != 1 {
			return errors.New("no contract created")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// Set host's MaxDuration to 5 to test if host will be skipped when contract
	// is renewed
	settings = h.InternalSettings()
	settings.MaxDuration = types.BlockHeight(5)
	if err := h.SetInternalSettings(settings); err != nil {
		t.Fatal(err)
	}
	// Let host settings permeate
	err = build.Retry(50, 100*time.Millisecond, func() error {
		host, _, err := c.hdb.Host(h.PublicKey())
		if err != nil {
			return err
		}
		if settings.MaxDuration != host.MaxDuration {
			return fmt.Errorf("host max duration not set, expected %v, got %v", settings.MaxDuration, host.MaxDuration)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine blocks to renew contract
	for i := types.BlockHeight(0); i <= c.allowance.Period-c.allowance.RenewWindow; i++ {
		_, err = m.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Confirm Contract is not renewed
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.OldContracts()) == 0 {
			return errors.New("no contract renewed")
		}
		return nil
	})
	if err == nil {
		t.Fatal("Contract should not have been renewed")
	}
}

// TestLinkedContracts tests that the contractors maps are updated correctly
// when renewing contracts
func TestLinkedContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create testing trio
	h, c, m, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create allowance
	a := modules.Allowance{
		Funds:              types.SiacoinPrecision.Mul64(100),
		Hosts:              1,
		Period:             20,
		RenewWindow:        10,
		ExpectedStorage:    modules.DefaultAllowance.ExpectedStorage,
		ExpectedUpload:     modules.DefaultAllowance.ExpectedUpload,
		ExpectedDownload:   modules.DefaultAllowance.ExpectedDownload,
		ExpectedRedundancy: modules.DefaultAllowance.ExpectedRedundancy,
		MaxPeriodChurn:     modules.DefaultAllowance.MaxPeriodChurn,
	}
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for Contract creation
	numRetries := 0
	err = build.Retry(200, 100*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			if _, err := m.AddBlock(); err != nil {
				return err
			}
		}
		numRetries++
		if len(c.Contracts()) != 1 {
			return errors.New("no contract created")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// Confirm that maps are empty
	if len(c.renewedFrom) != 0 {
		t.Fatal("renewedFrom map should be empty")
	}
	if len(c.renewedTo) != 0 {
		t.Fatal("renewedTo map should be empty")
	}

	// Set host's uploadbandwidthprice to zero to test divide by zero check when
	// contracts are renewed
	settings := h.InternalSettings()
	settings.MinUploadBandwidthPrice = types.ZeroCurrency
	if err := h.SetInternalSettings(settings); err != nil {
		t.Fatal(err)
	}

	// Mine blocks to renew contract
	for i := types.BlockHeight(0); i < c.allowance.Period-c.allowance.RenewWindow; i++ {
		_, err = m.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Confirm Contracts got renewed
	err = build.Retry(200, 100*time.Millisecond, func() error {
		if len(c.Contracts()) != 1 {
			return errors.New("no contract")
		}
		if len(c.OldContracts()) != 1 {
			return errors.New("no old contract")
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}

	// Confirm maps are updated as expected
	if len(c.renewedFrom) != 1 {
		t.Fatalf("renewedFrom map should have 1 entry but has %v", len(c.renewedFrom))
	}
	if len(c.renewedTo) != 1 {
		t.Fatalf("renewedTo map should have 1 entry but has %v", len(c.renewedTo))
	}
	if c.renewedFrom[c.Contracts()[0].ID] != c.OldContracts()[0].ID {
		t.Fatalf(`Map assignment incorrect,
		expected:
		map[%v:%v]
		got:
		%v`, c.Contracts()[0].ID, c.OldContracts()[0].ID, c.renewedFrom)
	}
	if c.renewedTo[c.OldContracts()[0].ID] != c.Contracts()[0].ID {
		t.Fatalf(`Map assignment incorrect,
		expected:
		map[%v:%v]
		got:
		%v`, c.OldContracts()[0].ID, c.Contracts()[0].ID, c.renewedTo)
	}
}

// testWalletShim is used to test the walletBridge type.
type testWalletShim struct {
	nextAddressCalled bool
	startTxnCalled    bool
}

// These stub implementations for the walletShim interface set their respective
// booleans to true, allowing tests to verify that they have been called.
func (ws *testWalletShim) NextAddress() (types.UnlockConditions, error) {
	ws.nextAddressCalled = true
	return types.UnlockConditions{}, nil
}
func (ws *testWalletShim) PrimarySeed() (modules.Seed, uint64, error) {
	return modules.Seed{}, 0, nil
}
func (ws *testWalletShim) StartTransaction() (modules.TransactionBuilder, error) {
	ws.startTxnCalled = true
	return nil, nil
}

func (ws *testWalletShim) RegisterTransaction(types.Transaction, []types.Transaction) (modules.TransactionBuilder, error) {
	return nil, nil
}

func (ws *testWalletShim) Unlocked() (bool, error) { return true, nil }

// TestWalletBridge tests the walletBridge type.
func TestWalletBridge(t *testing.T) {
	shim := new(testWalletShim)
	bridge := WalletBridge{shim}
	bridge.NextAddress()
	if !shim.nextAddressCalled {
		t.Error("NextAddress was not called on the shim")
	}
	bridge.StartTransaction()
	if !shim.startTxnCalled {
		t.Error("StartTransaction was not called on the shim")
	}
}
