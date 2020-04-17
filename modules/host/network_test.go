package host

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// blockingPortForward is a dependency set that causes the host port forward
// call at startup to block for 10 seconds, simulating the amount of blocking
// that can occur in production.
//
// blockingPortForward will also cause managedClearPort to always return an
// error.
type blockingPortForward struct {
	modules.ProductionDependencies
}

// disrupt will cause the port forward call to block for 10 seconds, but still
// complete normally. disrupt will also cause managedClearPort to return an
// error.
func (*blockingPortForward) Disrupt(s string) bool {
	// Return an error when clearing the port.
	if s == "managedClearPort return error" {
		return true
	}

	// Block during port forwarding.
	if s == "managedForwardPort" {
		time.Sleep(time.Second * 3)
	}
	return false
}

// TestPortFowardBlocking checks that the host does not accidentally call a
// write on a closed logger due to a long-running port forward call.
func TestPortForwardBlocking(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newMockHostTester(&blockingPortForward{}, "TestPortForwardBlocking")
	if err != nil {
		t.Fatal(err)
	}

	// The close operation would previously fail here because of improper
	// thread control regarding upnp and shutdown.
	err = ht.Close()
	if err != nil {
		t.Fatal(err)
	}

	// The trailing sleep is needed to catch the previously existing error
	// where the host was not shutting down correctly. Currently, the extra
	// sleep does nothing, but in the regression a logging panic would occur.
	time.Sleep(time.Second * 4)
}

// TestHostWorkingStatus checks that the host properly updates its working
// state
func TestHostWorkingStatus(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// TODO: this causes an ndf, because it relies on the host tester starting up
	// and fully returning faster than the first check, which isnt always the
	// case.  This check is disabled for now, but can be fixed by using the
	// Disrupt() pattern.
	// if ht.host.WorkingStatus() != modules.HostWorkingStatusChecking {
	// 	t.Fatal("expected working state to initially be modules.HostWorkingStatusChecking")
	// }

	for i := 0; i < 5; i++ {
		// Simulate some setting calls, and see if the host picks up on it.
		atomic.AddUint64(&ht.host.atomicSettingsCalls, workingStatusThreshold+1)

		success := false
		for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(time.Millisecond * 10) {
			if ht.host.WorkingStatus() == modules.HostWorkingStatusWorking {
				success = true
				break
			}
		}
		if !success {
			t.Fatal("expected working state to flip to HostWorkingStatusWorking after incrementing settings calls")
		}

		// make no settings calls, host should flip back to NotWorking
		success = false
		for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(time.Millisecond * 10) {
			if ht.host.WorkingStatus() == modules.HostWorkingStatusNotWorking {
				success = true
				break
			}
		}
		if !success {
			t.Fatal("expected working state to flip to HostStatusNotWorking if no settings calls occur")
		}
	}
}

// TestHostConnectabilityStatus checks that the host properly updates its
// connectable state
func TestHostConnectabilityStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// TODO: this causes an ndf, because it relies on the host tester starting up
	// and fully returning faster than the first check, which isnt always the
	// case.  This check is disabled for now, but can be fixed by using the
	// Disrupt() pattern.
	// if ht.host.ConnectabilityStatus() != modules.HostConnectabilityStatusChecking {
	// 		t.Fatal("expected connectability state to initially be ConnectablityStateChecking")
	// }

	success := false
	for start := time.Now(); time.Since(start) < 30*time.Second; time.Sleep(time.Millisecond * 10) {
		if ht.host.ConnectabilityStatus() == modules.HostConnectabilityStatusConnectable {
			success = true
			break
		}
	}
	if !success {
		t.Fatal("expected connectability state to flip to HostConnectabilityStatusConnectable")
	}
}

// TestHostStreamHandler verifies the host's stream handler. It ensures that the
// individual RPC handlers are called when the appropriate objects are sent over
// the stream.
func TestHostStreamHandler(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	pair, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	ht := pair.ht
	defer ht.Close()

	// create a refund account id
	var refundAccount modules.AccountID
	err = refundAccount.LoadString("prefix:deadbeef")
	if err != nil {
		t.Fatal(err)
	}

	// we recreate this on every error seeing as the host will have closed it
	stream := pair.newStream()

	// write a random rpc id to it and expect it to fail
	var randomRPCID types.Specifier
	fastrand.Read(randomRPCID[:])
	err = modules.RPCWrite(stream, randomRPCID)
	if err != nil {
		t.Fatal(err)
	}
	err = modules.RPCRead(stream, struct{}{})
	if err == nil || !strings.Contains(err.Error(), randomRPCID.String()) {
		t.Fatalf("Expected Unrecognized RPC ID error, but received '%v'", err)
	}

	// write a known rpc id to it, one that expects a price table but send a
	// random price table uid and expect it to fail
	stream = pair.newStream()
	err = modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		t.Fatal(err)
	}
	var randomUID modules.UniqueID
	fastrand.Read(randomUID[:])
	err = modules.RPCWrite(stream, randomUID)
	if err != nil {
		t.Fatal(err)
	}
	err = modules.RPCRead(stream, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "Price table not found") {
		t.Fatalf("Expected 'Price table not found' error, but received '%v'", err)
	}

	// call the update price table RPC to obtain an actual price table, however
	// try not paying for it, we expect a balance indicating this and we expect
	// for the price table *not* to be known by the host
	stream = pair.newStream()
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		t.Fatal(err)
	}
	var pt modules.RPCPriceTable
	var uptResp modules.RPCUpdatePriceTableResponse
	err = modules.RPCRead(stream, &uptResp)
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(uptResp.PriceTableJSON, &pt)
	if err != nil {
		t.Fatal(err)
	}

	// send a payment request but specify an invalid payment method
	pr := modules.PaymentRequest{Type: types.NewSpecifier("Invalid")}
	err = modules.RPCWrite(stream, pr)
	if err != nil {
		return
	}
	err = modules.RPCRead(stream, struct{}{})
	if err == nil || !strings.Contains(err.Error(), "unknown payment method") {
		t.Fatalf("Expected 'unknown payment method' error, but received '%v'", err)
	}
	_, exists := ht.host.staticPriceTables.managedGet(pt.UID)
	if exists {
		t.Fatal("Price table was tracked while it was not paid for")
	}

	// do that again but now effectively pay for the price table
	stream = pair.newStream()
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		t.Fatal(err)
	}
	err = modules.RPCRead(stream, &uptResp)
	if err != nil {
		t.Fatal(err)
	}
	err = json.Unmarshal(uptResp.PriceTableJSON, &pt)
	if err != nil {
		t.Fatal(err)
	}
	rev, sig, err := pair.paymentRevision(pt.UpdatePriceTableCost)
	if err != nil {
		t.Fatal(err)
	}
	pRequest := modules.PaymentRequest{Type: modules.PayByContract}
	pbcRequest := newPayByContractRequest(rev, sig, refundAccount)
	err = modules.RPCWriteAll(stream, pRequest, pbcRequest)
	if err != nil {
		t.Fatal(err)
	}
	var payByResponse modules.PayByContractResponse
	err = modules.RPCRead(stream, &payByResponse)
	if err != nil {
		t.Fatal(err)
	}
	_, exists = ht.host.staticPriceTables.managedGet(pt.UID)
	if !exists {
		t.Fatal("Price table was not tracked while it was paid for")
	}

	// now that we have a price table we can test the fund account rpc
	// send fund account request
	stream = pair.newStream()
	err = modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		t.Fatal(err)
	}
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		t.Fatal(err)
	}
	req := modules.FundAccountRequest{Account: pair.accountID}
	err = modules.RPCWrite(stream, req)
	if err != nil {
		t.Fatal(err)
	}
	deposit := types.NewCurrency64(100)
	rev, sig, err = pair.paymentRevision(deposit.Add(pt.UpdatePriceTableCost))
	if err != nil {
		t.Fatal(err)
	}
	pRequest = modules.PaymentRequest{Type: modules.PayByContract}
	pbcRequest = newPayByContractRequest(rev, sig, modules.ZeroAccountID)
	err = modules.RPCWriteAll(stream, pRequest, pbcRequest)
	if err != nil {
		t.Fatal(err)
	}
	err = modules.RPCRead(stream, &payByResponse)
	if err != nil {
		t.Fatal(err)
	}
	var resp modules.FundAccountResponse
	err = modules.RPCRead(stream, &resp)
	if err != nil {
		t.Fatal(err)
	}
	balance := getAccountBalance(ht.host.staticAccountManager, pair.accountID)
	if !balance.Equals(deposit) {
		t.Fatalf("Unexpected account balance after fund EA RPC, expected %v actual %v", deposit.HumanString(), balance.HumanString())
	}
}
