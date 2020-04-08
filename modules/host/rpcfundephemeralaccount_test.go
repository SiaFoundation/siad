package host

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// TestFundEphemeralAccountRPC tests the FundEphemeralAccountRPC by manually
// calling the RPC handler.
func TestFundEphemeralAccountRPC(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// setup host
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// fetch some host variables
	pt := ht.host.staticPriceTables.managedCurrent()
	bh := ht.host.BlockHeight()
	hpk := ht.host.PublicKey()
	his := ht.host.InternalSettings()

	// create the host's crypto public key
	var hcpk crypto.PublicKey
	copy(hcpk[:], hpk.Key)

	// create a renter key pair
	sk, rpk := crypto.GenerateKeyPair()
	renterPK := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       rpk[:],
	}

	// setup storage obligation (emulating a renter creating a contract)
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	so, err = ht.addNoOpRevision(so, renterPK)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so, false)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())
	fcid := so.id()

	// prepare an ephemeral account
	_, accountID := prepareAccount()

	renterFunc := func(stream siamux.Stream, revision types.FileContractRevision, signature crypto.Signature) (*modules.PayByContractResponse, *modules.FundAccountResponse, error) {
		// send fund account request
		req := modules.FundAccountRequest{Account: accountID}
		err := modules.RPCWrite(stream, req)
		if err != nil {
			return nil, nil, err
		}

		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(revision, signature)
		err = modules.RPCWriteAll(stream, pRequest, pbcRequest)
		if err != nil {
			return nil, nil, err
		}

		// receive PayByContractResponse
		var payByResponse modules.PayByContractResponse
		err = modules.RPCRead(stream, &payByResponse)
		if err != nil {
			return nil, nil, err
		}

		// receive FundAccountResponse
		var resp modules.FundAccountResponse
		err = modules.RPCRead(stream, &resp)
		if err != nil {
			return nil, nil, err
		}
		return &payByResponse, &resp, nil
	}

	hostFunc := func(stream siamux.Stream) error {
		err := ht.host.managedRPCFundEphemeralAccount(stream, pt)
		if err != nil {
			return modules.RPCWriteError(stream, err)
		}
		return nil
	}

	var mu sync.Mutex
	addBlock := func() {
		mu.Lock()
		defer mu.Unlock()
		bh += 1
	}

	runWithRevision := func(rev types.FileContractRevision) (payByResponse *modules.PayByContractResponse, fundResponse *modules.FundAccountResponse, err error) {
		// create streams
		rStream, hStream := NewTestStreams()
		defer rStream.Close()
		defer hStream.Close()

		var rErr, hErr error
		sig := revisionSignature(rev, bh, sk)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			payByResponse, fundResponse, rErr = renterFunc(rStream, rev, sig)
			wg.Done()
		}()

		wg.Add(1)
		go func() {
			hErr = hostFunc(hStream)
			wg.Done()
		}()
		wg.Wait()
		addBlock() // increase the blockheight on every run
		err = errors.Compose(rErr, hErr)
		return
	}

	verifyResponse := func(rev types.FileContractRevision, payByResponse *modules.PayByContractResponse, fundResponse *modules.FundAccountResponse, prevBalance, prevAccountFunding, funding types.Currency) error {
		// verify the host signature
		if err := crypto.VerifyHash(crypto.HashAll(rev), hcpk, payByResponse.Signature); err != nil {
			return errors.New("could not verify host signature")
		}

		// verify the receipt
		receipt := fundResponse.Receipt
		if err := crypto.VerifyHash(crypto.HashAll(receipt), hcpk, fundResponse.Signature); err != nil {
			return errors.New("could not verify receipt signature")
		}
		if !receipt.Amount.Equals(funding) {
			return fmt.Errorf("Unexpected funded amount in the receipt, expected %v but received %v", funding.HumanString(), receipt.Amount.HumanString())
		}
		if receipt.Account != accountID {
			return fmt.Errorf("Unexpected account id in the receipt, expected %v but received %v", accountID, receipt.Account)
		}
		if !receipt.Host.Equals(hpk) {
			return fmt.Errorf("Unexpected host pubkey in the receipt, expected %v but received %v", hpk, receipt.Host)
		}

		// verify the funding got deposited into the ephemeral account
		currBalance := getAccountBalance(ht.host.staticAccountManager, accountID)
		if !currBalance.Equals(prevBalance.Add(funding)) {
			t.Fatalf("Unexpected account balance, expected %v but received %v", prevBalance.Add(funding).HumanString(), currBalance.HumanString())
		}

		// verify the funding get added to the host's financial metrics
		currAccountFunding := ht.host.FinancialMetrics().PotentialAccountFunding
		if !currAccountFunding.Equals(prevAccountFunding.Add(funding)) {
			t.Fatalf("Unexpected account funding, expected %v but received %v", prevAccountFunding.Add(funding).HumanString(), currAccountFunding.HumanString())
		}
		return nil
	}

	recentSO := func() types.FileContractRevision {
		so, err = ht.host.managedGetStorageObligation(fcid)
		if err != nil {
			t.Fatal(err)
		}
		recent, err := so.recentRevision()
		if err != nil {
			t.Fatal(err)
		}
		return recent
	}

	// verify happy flow
	recent := recentSO()
	funding := types.NewCurrency64(100)
	accountFunding := ht.host.FinancialMetrics().PotentialAccountFunding
	rev, err := recent.PaymentRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	balance := getAccountBalance(ht.host.staticAccountManager, accountID)
	pbcResp, fundAccResp, err := runWithRevision(rev)
	if err != nil {
		t.Fatal(err)
	}

	err = verifyResponse(rev, pbcResp, fundAccResp, balance, accountFunding, funding)
	if err != nil {
		t.Fatal(err)
	}

	// expect error when we move funds back to the renter
	recent = recentSO()
	rev, err = recent.PaymentRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	rev.SetValidRenterPayout(rev.ValidRenterPayout().Add64(1))
	_, _, err = runWithRevision(rev)
	if err == nil || !strings.Contains(err.Error(), "rejected for low paying host valid output") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}

	// expect error when we didn't move enough funds to the renter
	recent = recentSO()
	rev, err = recent.PaymentRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	rev.SetValidHostPayout(rev.ValidHostPayout().Sub64(1))
	_, _, err = runWithRevision(rev)
	if err == nil || !strings.Contains(err.Error(), "rejected for low paying host valid output") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}

	// expect error when the funds we move are not enough to cover the cost
	recent = recentSO()
	rev, err = recent.PaymentRevision(pt.FundAccountCost.Sub64(1))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runWithRevision(rev)
	if err == nil || !strings.Contains(err.Error(), "amount that was deposited did not cover the cost of the RPC") {
		t.Fatalf("Expected error indicating the lack of funds, instead error was: '%v'", err)
	}

	// expect error when the funds exceed the host's max ephemeral account
	// balance
	recent = recentSO()
	rev, err = recent.PaymentRevision(pt.FundAccountCost.Add(his.MaxEphemeralAccountBalance.Add64(1)))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runWithRevision(rev)
	if err == nil || !strings.Contains(err.Error(), ErrBalanceMaxExceeded.Error()) {
		t.Fatalf("Expected error '%v', instead error was '%v'", ErrBalanceMaxExceeded, err)
	}

	// expect error when we corrupt the renter's revision signature
	recent = recentSO()
	rStream, hStream := NewTestStreams()
	var rErr, hErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer rStream.Close()
		sig := revisionSignature(rev, bh, sk)
		fastrand.Read(sig[:4]) // corrupt the signature
		_, _, rErr = renterFunc(rStream, rev, sig)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer hStream.Close()
		hErr = hostFunc(hStream)
	}()
	wg.Wait()
	if rErr == nil || !strings.Contains(rErr.Error(), "invalid signature") {
		t.Fatalf("Unexpected renter err, expected 'invalid signature' but got '%v'", err)
	}
	if hErr != nil {
		t.Fatal(err)
	}

	// expect error when we run 2 revisions in parallel with the same revision
	// number
	recent = recentSO()
	rev1, err1 := recent.PaymentRevision(funding.Add(pt.FundAccountCost))
	rev2, err2 := recent.PaymentRevision(funding.Mul64(2).Add(pt.FundAccountCost))
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}

	wg.Add(1)
	go func() {
		_, _, err1 = runWithRevision(rev1)
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		_, _, err2 = runWithRevision(rev2)
		wg.Done()
	}()
	wg.Wait()
	err = errors.Compose(err1, err2)
	if err == nil {
		t.Fatal("Expected failure when running 2 in parallel because they are using the same revision number, instead err was nil")
	}

	// verify happy flow again to make sure the error'ed out calls don't mess
	// anything up
	recent = recentSO()
	accountFunding = ht.host.FinancialMetrics().PotentialAccountFunding
	rev, err = recent.PaymentRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	balance = getAccountBalance(ht.host.staticAccountManager, accountID)
	pbcResp, fundAccResp, err = runWithRevision(rev)
	if err != nil {
		t.Fatal(err)
	}
	err = verifyResponse(rev, pbcResp, fundAccResp, balance, accountFunding, funding)
	if err != nil {
		t.Fatal(err)
	}
}
