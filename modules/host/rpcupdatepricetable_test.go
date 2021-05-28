package host

import (
	"container/heap"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// TestPriceTableMarshaling tests a PriceTable can be marshaled and unmarshaled
func TestPriceTableMarshaling(t *testing.T) {
	pt := modules.RPCPriceTable{
		Validity:             rpcPriceGuaranteePeriod,
		HostBlockHeight:      types.BlockHeight(fastrand.Intn(1e3)),
		UpdatePriceTableCost: types.SiacoinPrecision,
		InitBaseCost:         types.SiacoinPrecision.Mul64(1e2),
		MemoryTimeCost:       types.SiacoinPrecision.Mul64(1e3),
		ReadBaseCost:         types.SiacoinPrecision.Mul64(1e4),
		ReadLengthCost:       types.SiacoinPrecision.Mul64(1e5),
		HasSectorBaseCost:    types.SiacoinPrecision.Mul64(1e6),
		TxnFeeMinRecommended: types.SiacoinPrecision.Mul64(1e7),
		TxnFeeMaxRecommended: types.SiacoinPrecision.Mul64(1e8),
		DropSectorsBaseCost:  types.SiacoinPrecision.Mul64(1e9),
		DropSectorsUnitCost:  types.SiacoinPrecision.Mul64(1e10),
		WriteBaseCost:        types.SiacoinPrecision.Mul64(1e11),
		WriteLengthCost:      types.SiacoinPrecision.Mul64(1e12),
		WriteStoreCost:       types.SiacoinPrecision.Mul64(1e13),
		LatestRevisionCost:   types.SiacoinPrecision.Mul64(1e14),
		FundAccountCost:      types.SiacoinPrecision.Mul64(1e15),
		AccountBalanceCost:   types.SiacoinPrecision.Mul64(1e16),
		SwapSectorCost:       types.SiacoinPrecision.Mul64(1e17),
	}
	fastrand.Read(pt.UID[:])

	ptBytes, err := json.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	var ptCopy modules.RPCPriceTable
	err = json.Unmarshal(ptBytes, &ptCopy)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(pt, ptCopy) {
		t.Log(pt.UID[:])
		t.Log(ptCopy.UID[:])
		t.Fatal(errors.New("PriceTable not equal after unmarshaling"))
	}
}

// TestPriceTableMinHeap verifies the working of the min heap
func TestPriceTableMinHeap(t *testing.T) {
	t.Parallel()

	now := time.Now()
	pth := priceTableHeap{heap: make([]*hostRPCPriceTable, 0)}

	pt1 := hostRPCPriceTable{
		modules.RPCPriceTable{Validity: rpcPriceGuaranteePeriod},
		now.Add(-rpcPriceGuaranteePeriod),
	}
	pth.Push(&pt1)

	pt2 := hostRPCPriceTable{
		modules.RPCPriceTable{Validity: rpcPriceGuaranteePeriod},
		now,
	}
	pth.Push(&pt2)

	pt3 := hostRPCPriceTable{
		modules.RPCPriceTable{Validity: rpcPriceGuaranteePeriod},
		now.Add(-3 * rpcPriceGuaranteePeriod),
	}
	pth.Push(&pt3)

	pt4 := hostRPCPriceTable{
		modules.RPCPriceTable{Validity: rpcPriceGuaranteePeriod},
		now.Add(-2 * rpcPriceGuaranteePeriod),
	}
	pth.Push(&pt4)

	// verify it expires 3 of them
	expired := pth.PopExpired()
	if len(expired) != 3 {
		t.Fatalf("Unexpected amount of price tables expired, expected %v, received %d", 3, len(expired))
	}

	// verify 'pop' returns the last remaining price table
	pth.mu.Lock()
	expectedPt2 := heap.Pop(&pth.heap)
	pth.mu.Unlock()
	if expectedPt2 != &pt2 {
		t.Fatal("Expected the last price table to be equal to pt2, which is the price table with the highest expiry")
	}
}

// TestPruneExpiredPriceTables verifies the rpc price tables get pruned from the
// host's price table map if they have expired.
func TestPruneExpiredPriceTables(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := rhp.staticHT

	// verify the price table is being tracked
	pt, err := rhp.managedFetchPriceTable()
	if err != nil {
		t.Fatal(err)
	}

	_, tracked := ht.host.staticPriceTables.managedGet(pt.UID)
	if !tracked {
		t.Fatal("Expected the testing price table to be tracked but isn't")
	}

	// retry until the price table expired and got pruned
	err = build.Retry(10, pruneExpiredRPCPriceTableFrequency, func() error {
		_, exists := ht.host.staticPriceTables.managedGet(pt.UID)
		if exists {
			return errors.New("Expected RPC price table to be pruned because it should have expired")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestUpdatePriceTableRPC tests the UpdatePriceTableRPC by manually calling the
// RPC handler.
func TestUpdatePriceTableRPC(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// setup a host and renter pair with an emulated file contract between them
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	t.Run("Basic", func(t *testing.T) {
		testUpdatePriceTableBasic(t, rhp)
	})
	t.Run("AfterSettingsUpdate", func(t *testing.T) {
		testUpdatePriceTableAfterSettingsUpdate(t, rhp)
	})
	t.Run("InsufficientPayment", func(t *testing.T) {
		testUpdatePriceTableInsufficientPayment(t, rhp)
	})
	t.Run("HostNoStreamClose", func(t *testing.T) {
		testUpdatePriceTableHostNoStreamClose(t, rhp)
	})
}

// testUpdatePriceTableBasic verifies the basic functionality of the update
// price table RPC
func testUpdatePriceTableBasic(t *testing.T, rhp *renterHostPair) {
	// set the registry size to a known value.
	host := rhp.staticHT.host
	is := host.InternalSettings()
	is.RegistrySize = 128 * modules.RegistryEntrySize
	err := rhp.staticHT.host.SetInternalSettings(is)
	if err != nil {
		t.Fatal(err)
	}

	// Add 64 entries.
	for i := 0; i < 64; i++ {
		sk, pk := crypto.GenerateKeyPair()
		var spk types.SiaPublicKey
		spk.Algorithm = types.SignatureEd25519
		spk.Key = pk[:]
		var tweak crypto.Hash
		fastrand.Read(tweak[:])
		rv := modules.NewRegistryValue(tweak, fastrand.Bytes(modules.RegistryDataSize), 0, modules.RegistryTypeWithoutPubkey).Sign(sk)
		_, err := host.RegistryUpdate(rv, spk, 1337)
		if err != nil {
			t.Fatal(err)
		}
	}
	left := host.staticRegistry.Cap() - host.staticRegistry.Len()

	// create a payment revision
	current := rhp.staticHT.host.staticPriceTables.managedCurrent()
	rev, sig, err := rhp.managedEAFundRevision(current.UpdatePriceTableCost)
	if err != nil {
		t.Fatal(err)
	}

	// execute the RPC request
	request := newPayByContractRequest(rev, sig, rhp.staticAccountID)
	pt, err := runUpdatePriceTableRPCWithRequest(rhp, request)
	if err != nil {
		t.Fatal(err)
	}

	// ensure the price table is tracked by the host
	_, tracked := rhp.staticHT.host.staticPriceTables.managedGet(pt.UID)
	if !tracked {
		t.Fatalf("Expected price table with.UID %v to be tracked after successful update", pt.UID)
	}
	// ensure its validity is positive and different from zero
	if pt.Validity.Seconds() <= 0 {
		t.Fatal("Expected price table validity to be positive and non zero")
	}

	// ensure it contains the host's block height
	if pt.HostBlockHeight != rhp.staticHT.host.BlockHeight() {
		t.Fatal("Expected host blockheight to be set on the price table")
	}
	// ensure it's not zero to be certain the blockheight is set and it's not
	// just the initial value
	if pt.HostBlockHeight == 0 {
		t.Fatal("Expected host blockheight to be not 0")
	}

	// ensure it has the txn fee estimates
	if pt.TxnFeeMinRecommended.IsZero() {
		t.Fatal("Expected TxnFeeMinRecommended to be set on the price table")
	}
	if pt.TxnFeeMaxRecommended.IsZero() {
		t.Fatal("Expected TxnFeeMaxRecommended to be set on the price table")
	}

	// ensure the ContractPrice is set
	if pt.ContractPrice.IsZero() {
		t.Fatal("Expected contract price to be set on the price table")
	}
	// ensure the CollateralCost is set
	if pt.CollateralCost.IsZero() {
		t.Fatal("Expected collateral to be set on the price table")
	}
	// ensure the MaxCollateral is set
	if pt.MaxCollateral.IsZero() {
		t.Fatal("Expected MaxCollateral to be set on the price table")
	}
	// ensure the MaxDuration is set
	if pt.MaxDuration == 0 {
		t.Fatal("Expected MaxDuration to be set on the price table")
	}
	// ensure the WindowSize is set
	if pt.WindowSize == 0 {
		t.Fatal("Expected WindowSize to be set on the price table")
	}
	if pt.RegistryEntriesLeft != left {
		t.Fatal("Wrong number of registry entries", pt.RegistryEntriesLeft, left)
	}
	if pt.RegistryEntriesTotal != 128 {
		t.Fatal("Wrong number of entries", pt.RegistryEntriesTotal, 128)
	}
	if !pt.RenewContractCost.Equals(modules.DefaultBaseRPCPrice) {
		t.Fatal("Wrong renew cost", pt.RenewContractCost, modules.DefaultBaseRPCPrice)
	}
}

// testUpdatePriceTableAfterSettingsUpdate verifies the price table is updated
// after the host updates its internal settings
func testUpdatePriceTableAfterSettingsUpdate(t *testing.T, rhp *renterHostPair) {
	// ensure the price table has valid upload and download bandwidth costs
	pt := rhp.staticHT.host.staticPriceTables.managedCurrent()
	if pt.DownloadBandwidthCost.IsZero() {
		t.Fatal("Expected DownloadBandwidthCost to be non zero")
	}
	if pt.UploadBandwidthCost.IsZero() {
		t.Fatal("Expected DownloadBandwidthCost to be non zero")
	}

	// update the host's internal settings
	his := rhp.staticHT.host.InternalSettings()
	his.MinDownloadBandwidthPrice = types.ZeroCurrency
	his.MinUploadBandwidthPrice = types.ZeroCurrency
	err := rhp.staticHT.host.SetInternalSettings(his)
	if err != nil {
		t.Fatal(err)
	}

	// trigger a price table update
	err = rhp.managedUpdatePriceTable(true)
	if err != nil {
		t.Fatal(err)
	}

	// ensure the pricetable reflects our changes
	pt = rhp.staticHT.host.staticPriceTables.managedCurrent()
	if !pt.DownloadBandwidthCost.IsZero() {
		t.Error("Expected DownloadBandwidthCost to be zero")
	}
	if !pt.UploadBandwidthCost.IsZero() {
		t.Error("Expected UploadBandwidthCost to be zero")
	}
}

// testUpdatePriceTableInsufficientPayment verifies the RPC fails if payment
// supplied through the payment revision did not cover the RPC cost
func testUpdatePriceTableInsufficientPayment(t *testing.T, rhp *renterHostPair) {
	// create a payment revision
	rev, sig, err := rhp.managedEAFundRevision(types.ZeroCurrency)
	if err != nil {
		t.Fatal(err)
	}

	// execute the RPC request
	request := newPayByContractRequest(rev, sig, rhp.staticAccountID)
	_, err = runUpdatePriceTableRPCWithRequest(rhp, request)
	if err == nil || !strings.Contains(err.Error(), modules.ErrInsufficientPaymentForRPC.Error()) {
		t.Fatalf("Expected error '%v', instead error was '%v'", modules.ErrInsufficientPaymentForRPC, err)
	}
}

// testUpdatePriceTableHostNoStreamClose verifies the RPC does not block if the
// host does not close the stream
func testUpdatePriceTableHostNoStreamClose(t *testing.T, rhp *renterHostPair) {
	err := rhp.staticHT.host.Close()
	if err != nil {
		t.Fatal(err)
	}

	deps := &dependencies.DependencyDisableStreamClose{}
	err = reopenCustomHost(rhp.staticHT, deps)
	if err != nil {
		t.Fatal(err)
	}
	// cleanup and reload with prod deps so this test can be easily extended
	defer func() {
		err = reloadHost(rhp.staticHT)
		if err != nil {
			t.Fatal(err)
		}
	}()

	// run the happy flow to verify it works
	testUpdatePriceTableBasic(t, rhp)
}

// runUpdatePriceTableRPCWithRequest is a helper function that performs the
// renter-side of the update price table RPC using a custom PayByContractRequest
// to similate various edge cases or error flows.
func runUpdatePriceTableRPCWithRequest(rhp *renterHostPair, pbcRequest modules.PayByContractRequest) (_ *modules.RPCPriceTable, err error) {
	stream := rhp.managedNewStream()
	defer func() {
		err = errors.Compose(err, stream.Close())
	}()

	// initiate the RPC
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		return nil, err
	}

	// receive the price table response
	var pt modules.RPCPriceTable
	var update modules.RPCUpdatePriceTableResponse
	err = modules.RPCRead(stream, &update)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(update.PriceTableJSON, &pt); err != nil {
		return nil, err
	}

	// send PaymentRequest & PayByContractRequest
	pRequest := modules.PaymentRequest{Type: modules.PayByContract}
	err = modules.RPCWriteAll(stream, pRequest, pbcRequest)
	if err != nil {
		return nil, err
	}

	// receive PayByContractResponse
	var payByResponse modules.PayByContractResponse
	err = modules.RPCRead(stream, &payByResponse)
	if err != nil {
		return nil, err
	}

	// await tracked response
	var tracked modules.RPCTrackedPriceTableResponse
	err = modules.RPCRead(stream, &tracked)
	if err != nil {
		return nil, err
	}
	return &pt, nil
}
