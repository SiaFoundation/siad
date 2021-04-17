package host

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestFundEphemeralAccountRPC tests the FundEphemeralAccountRPC by manually
// calling the RPC handler.
func TestFundEphemeralAccountRPC(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// setup renter host pair
	pair, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := pair.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := pair.staticHT

	// fetch the price table
	pt := pair.managedPriceTable()

	// fetch some host variables
	hpk := ht.host.PublicKey()
	his := ht.host.InternalSettings()

	// create the host's crypto public key
	var hcpk crypto.PublicKey
	copy(hcpk[:], hpk.Key)

	// specify a refund account. Needs to be zero account string for funding.
	refundAccount := modules.ZeroAccountID

	// runWithRequest is a helper function that runs the fundEphemeralAccountRPC
	// with the given pay by contract request
	runWithRequest := func(req modules.PayByContractRequest) (*modules.PayByContractResponse, *modules.FundAccountResponse, error) {
		stream := pair.managedNewStream()

		// write rpc ID
		err := modules.RPCWrite(stream, modules.RPCFundAccount)
		if err != nil {
			return nil, nil, err
		}

		// send price table uid
		err = modules.RPCWrite(stream, pt.UID)
		if err != nil {
			return nil, nil, err
		}

		// send fund account request
		far := modules.FundAccountRequest{Account: pair.staticAccountID}
		err = modules.RPCWrite(stream, far)
		if err != nil {
			return nil, nil, err
		}

		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		err = modules.RPCWriteAll(stream, pRequest, req)
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

		// expect clean stream close
		err = modules.RPCRead(stream, struct{}{})
		if !errors.Contains(err, io.ErrClosedPipe) {
			return nil, nil, err
		}

		return &payByResponse, &resp, nil
	}

	verifyResponse := func(rev types.FileContractRevision, payByResponse *modules.PayByContractResponse, fundResponse *modules.FundAccountResponse, prevBalance, prevPotAccFunding, funding types.Currency) error {
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
		if receipt.Account != pair.staticAccountID {
			return fmt.Errorf("Unexpected account id in the receipt, expected %v but received %v", pair.staticAccountID, receipt.Account)
		}
		if !receipt.Host.Equals(hpk) {
			return fmt.Errorf("Unexpected host pubkey in the receipt, expected %v but received %v", hpk, receipt.Host)
		}

		// verify the funding got deposited into the ephemeral account
		currBalance := getAccountBalance(ht.host.staticAccountManager, pair.staticAccountID)
		if !currBalance.Equals(prevBalance.Add(funding)) {
			t.Fatalf("Unexpected account balance, expected %v but received %v", prevBalance.Add(funding).HumanString(), currBalance.HumanString())
		}

		// verify the host responded with the correct new balance.
		if !currBalance.Equals(fundResponse.Balance) {
			t.Fatalf("Unexpected returned account balance, expected %v but received %v", fundResponse.Balance, currBalance.HumanString())
		}

		// verify the funding get added to the host's financial metrics
		currPotAccFunding := ht.host.FinancialMetrics().PotentialAccountFunding
		if !currPotAccFunding.Equals(prevPotAccFunding.Add(funding)) {
			t.Fatalf("Unexpected account funding, expected %v but received %v", prevPotAccFunding.Add(funding).HumanString(), currPotAccFunding.HumanString())
		}
		return nil
	}

	// verify happy flow
	funding := types.NewCurrency64(100)
	fmPAF := ht.host.FinancialMetrics().PotentialAccountFunding
	rev, sig, err := pair.managedEAFundRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	balance := getAccountBalance(ht.host.staticAccountManager, pair.staticAccountID)
	pbcResp, fundAccResp, err := runWithRequest(newPayByContractRequest(rev, sig, refundAccount))
	if err != nil {
		t.Fatal(err)
	}

	err = verifyResponse(rev, pbcResp, fundAccResp, balance, fmPAF, funding)
	if err != nil {
		t.Fatal(err)
	}

	// expect error when we move funds back to the renter
	rev, _, err = pair.managedEAFundRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	rev.SetValidRenterPayout(rev.ValidRenterPayout().Add64(1))
	_, _, err = runWithRequest(newPayByContractRequest(rev, pair.managedSign(rev), refundAccount))

	if err == nil || !strings.Contains(err.Error(), "rejected for low paying host valid output") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}

	// expect error when we didn't move enough funds to the renter
	rev, _, err = pair.managedEAFundRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	rev.SetValidHostPayout(rev.ValidHostPayout().Sub64(1))
	_, _, err = runWithRequest(newPayByContractRequest(rev, pair.managedSign(rev), refundAccount))

	if err == nil || !strings.Contains(err.Error(), "rejected for low paying host valid output") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}

	// expect error when the funds we move are not enough to cover the cost
	rev, sig, err = pair.managedEAFundRevision(pt.FundAccountCost.Sub64(1))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runWithRequest(newPayByContractRequest(rev, sig, refundAccount))

	if err == nil || !strings.Contains(err.Error(), "amount that was deposited did not cover the cost of the RPC") {
		t.Fatalf("Expected error indicating the lack of funds, instead error was: '%v'", err)
	}

	// expect error when the funds exceed the host's max ephemeral account
	// balance
	rev, sig, err = pair.managedEAFundRevision(pt.FundAccountCost.Add(his.MaxEphemeralAccountBalance.Add64(1)))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runWithRequest(newPayByContractRequest(rev, sig, refundAccount))
	if err == nil || !strings.Contains(err.Error(), ErrBalanceMaxExceeded.Error()) {
		t.Fatalf("Expected error '%v', instead error was '%v'", ErrBalanceMaxExceeded, err)
	}

	// expect error when we corrupt the renter's revision signature
	rev, sig, err = pair.managedEAFundRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}
	fastrand.Read(sig[:4]) // corrupt the signature
	_, _, err = runWithRequest(newPayByContractRequest(rev, sig, refundAccount))
	if err == nil || !strings.Contains(err.Error(), "invalid signature") {
		t.Fatalf("Unexpected renter err, expected 'invalid signature' but got '%v'", err)
	}

	// expect error when refund account id is provided for funding account.
	var aid modules.AccountID
	err = aid.LoadString("prefix:deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runWithRequest(newPayByContractRequest(rev, sig, aid))
	if err == nil {
		t.Fatal("expected error when refund account is provided for funding account")
	}

	// expect error when revision moves collateral
	// update the host collateral
	collateral := types.NewCurrency64(5)
	so, err := ht.host.managedGetStorageObligation(pair.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	numRevisions := len(so.RevisionTransactionSet)
	so.RevisionTransactionSet[numRevisions-1].FileContractRevisions[0].SetMissedVoidPayout(collateral)
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, make(map[crypto.Hash][]byte))
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())

	// create a revision and move some collateral
	rev, _, err = pair.managedEAFundRevision(funding.Add(pt.FundAccountCost))
	voidOutput, err := rev.MissedVoidOutput()
	if err != nil {
		t.Fatal(err)
	}
	rev.SetMissedHostPayout(rev.MissedHostOutput().Value.Sub(collateral))
	err = rev.SetMissedVoidPayout(voidOutput.Value.Add(collateral))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = runWithRequest(newPayByContractRequest(rev, pair.managedSign(rev), refundAccount))

	if err == nil || !strings.Contains(err.Error(), ErrLowHostMissedOutput.Error()) {
		t.Fatalf("Expected error ErrLowHostMissedOutput, instead error was '%v'", err)
	}

	// undo host collateral update
	so, err = ht.host.managedGetStorageObligation(pair.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	so.RevisionTransactionSet[numRevisions-1].FileContractRevisions[0].SetMissedVoidPayout(types.ZeroCurrency)
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, make(map[crypto.Hash][]byte))
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())

	// verify happy flow again to make sure the error'ed out calls don't mess
	// anything up
	fmPAF = ht.host.FinancialMetrics().PotentialAccountFunding
	rev, sig, err = pair.managedEAFundRevision(funding.Add(pt.FundAccountCost))
	if err != nil {
		t.Fatal(err)
	}

	balance = getAccountBalance(ht.host.staticAccountManager, pair.staticAccountID)
	pbcResp, fundAccResp, err = runWithRequest(newPayByContractRequest(rev, sig, refundAccount))
	if err != nil {
		t.Fatal(err)
	}
	err = verifyResponse(rev, pbcResp, fundAccResp, balance, fmPAF, funding)
	if err != nil {
		t.Fatal(err)
	}
}
