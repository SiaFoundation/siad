package contractor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/consensus"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/modules/renter/hostdb"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/Sia/modules/wallet"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// newModules initializes the modules needed to test creating a new contractor
func newModules(testdir string) (modules.ConsensusSet, modules.Wallet, modules.TransactionPool, modules.HostDB, error) {
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
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	hdb, errChanHDB := hostdb.New(g, cs, tp, testdir)
	if err := <-errChanHDB; err != nil {
		return nil, nil, nil, nil, err
	}
	return cs, w, tp, hdb, nil
}

// TestNew tests the New function.
func TestNew(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Create the modules.
	dir := build.TempDir("contractor", t.Name())
	cs, w, tpool, hdb, err := newModules(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Sane values.
	_, errChan := New(cs, w, tpool, hdb, dir)
	if err := <-errChan; err != nil {
		t.Fatalf("expected nil, got %v", err)
	}

	// Nil consensus set.
	_, errChan = New(nil, w, tpool, hdb, dir)
	if err := <-errChan; err != errNilCS {
		t.Fatalf("expected %v, got %v", errNilCS, err)
	}

	// Nil wallet.
	_, errChan = New(cs, nil, tpool, hdb, dir)
	if err := <-errChan; err != errNilWallet {
		t.Fatalf("expected %v, got %v", errNilWallet, err)
	}

	// Nil transaction pool.
	_, errChan = New(cs, w, nil, hdb, dir)
	if err := <-errChan; err != errNilTpool {
		t.Fatalf("expected %v, got %v", errNilTpool, err)
	}
	// Nil hostdb.
	_, errChan = New(cs, w, tpool, nil, dir)
	if err := <-errChan; err != errNilHDB {
		t.Fatalf("expected %v, got %v", errNilHDB, err)
	}

	// Bad persistDir.
	_, errChan = New(cs, w, tpool, hdb, "")
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

// TestIntegrationSetAllowance tests the SetAllowance method.
func TestIntegrationSetAllowance(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// create a siamux
	testdir := build.TempDir("contractor", t.Name())
	siaMuxDir := filepath.Join(testdir, modules.SiaMuxDir)
	mux, err := modules.NewSiaMux(siaMuxDir, testdir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	// create testing trio
	h, c, m, err := newTestingTrio(t.Name())
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

// TestPayment verifies the PaymentProvider interface on the contractor. It does
// this by trying to pay the host using a filecontract and verifying if payment
// can be made successfully.
func TestPayment(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a testing trio
	h, c, _, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// set an allowance and wait for contracts
	err = c.SetAllowance(modules.DefaultAllowance)
	if err != nil {
		t.Fatal(err)
	}
	_ = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) == 0 {
			return errors.New("no contract created")
		}
		return nil
	})

	// check we have a contract with the host
	hpk := h.PublicKey()
	contract, ok := c.ContractByPublicKey(hpk)
	if !ok {
		t.Fatal("No contract with host")
	}

	// create two streams and verify the payment process
	pay := func(payment types.Currency) (paid types.Currency, err error) {
		rStream, hStream := NewTestStreams()
		defer rStream.Close()
		defer hStream.Close()

		var mu sync.Mutex
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			mu.Lock()
			err = errors.Compose(err, c.ProvidePayment(rStream, hpk, types.Specifier{}, payment, 0))
			mu.Unlock()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, pResult, pErr := h.ProcessPayment(hStream)
			if pErr != nil {
				modules.RPCWriteError(hStream, pErr)
				return
			}
			mu.Lock()
			paid = pResult
			mu.Unlock()
		}()
		c := make(chan struct{})
		go func() {
			defer close(c)
			wg.Wait()
		}()
		select {
		case <-c:
		case <-time.After(5 * time.Second):
			mu.Lock()
			err = errors.Compose(err, fmt.Errorf("Timed out"))
			mu.Unlock()
		}
		return paid, err
	}

	// move half of the renter's funds to the host
	initial := contract.RenterFunds
	paid, err := pay(initial.Div64(2))
	if err != nil {
		t.Fatal("Failed to provide payment", err)
	}

	// verify the host received the correct amount
	if !paid.Equals(initial.Div64(2)) {
		t.Fatalf("Expected host to have received half of the funds in the contract, instead it received %s", paid.HumanString())
	}

	// verify the contract was updated
	expected := initial.Sub(initial.Div64(2))
	contract, _ = c.ContractByPublicKey(hpk)
	remaining := contract.RenterFunds
	if !remaining.Equals(expected) {
		t.Fatalf("Expected renter contract to reflect the payment, the renter funds should be %v but were %v", expected.HumanString(), remaining.HumanString())
	}

	// verify we can not spend more than what's in the contract
	_, err = pay(remaining.Add64(1))
	if !errors.Contains(err, errContractInsufficientFunds) {
		t.Fatalf("Expected 'errContractInsufficientFunds', instead error was '%v'", err)
	}

	// verify we can spend what's left in the contract
	paid, err = pay(remaining)
	if err != nil {
		t.Fatal("Failed to provide payment", err)
	}

	// verify the host received the correct amount
	if !paid.Equals(remaining) {
		t.Fatalf("Expected host to have received half of the funds in the contract, instead it received %s", paid.HumanString())
	}

	// verify the contract was updated
	expected = types.ZeroCurrency
	contract, _ = c.ContractByPublicKey(hpk)
	remaining = contract.RenterFunds
	if !remaining.Equals(expected) {
		t.Fatalf("Expected renter contract to reflect the payment, the renter funds should be %v but were %v", expected.HumanString(), remaining.HumanString())
	}

	// verify the host's SO reflects the payments
	var hso modules.StorageObligation
	for _, so := range h.StorageObligations() {
		if so.ObligationId == contract.ID {
			hso = so
			break
		}
	}
	if hso.ObligationId != contract.ID {
		t.Fatal("Expected storage obligation to be found on the host")
	}

	// TODO once we have updated the host to track potential revenue we can
	// extend this test here to verify it
	// hso.PotentialUnknownRevenue == amount transferred
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

// testStream is a helper struct that wraps a net.Conn and implements the
// siamux.Stream interface.
type testStream struct{ c net.Conn }

// NewTestStreams returns two siamux.Stream mock objects.
func NewTestStreams() (client siamux.Stream, server siamux.Stream) {
	var cc, sc net.Conn
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		sc, _ = ln.Accept()
		wg.Done()
	}()
	cc, _ = net.Dial("tcp", ln.Addr().String())
	wg.Wait()

	client = testStream{c: cc}
	server = testStream{c: sc}
	return
}

func (s testStream) Read(b []byte) (n int, err error)  { return s.c.Read(b) }
func (s testStream) Write(b []byte) (n int, err error) { return s.c.Write(b) }
func (s testStream) Close() error                      { return s.c.Close() }

func (s testStream) LocalAddr() net.Addr            { panic("not implemented") }
func (s testStream) RemoteAddr() net.Addr           { panic("not implemented") }
func (s testStream) SetDeadline(t time.Time) error  { panic("not implemented") }
func (s testStream) SetPriority(priority int) error { panic("not implemented") }

func (s testStream) SetReadDeadline(t time.Time) error {
	panic("not implemented")
}
func (s testStream) SetWriteDeadline(t time.Time) error {
	panic("not implemented")
}
