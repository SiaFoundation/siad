package host

import (
	"sync"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
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
	pt := ht.host.PriceTable()
	bh := ht.host.BlockHeight()
	hpk := ht.host.PublicKey()

	// create a renter key pair
	sk, rpk := crypto.GenerateKeyPair()
	renterPK := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       rpk[:],
	}

	// setup storage obligationn (emulating a renter creating a contract)
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

	// prepare an updated revision that pays the host
	recent, err := so.recentRevision()
	if err != nil {
		t.Fatal(err)
	}
	funding := types.NewCurrency64(100)
	payment := funding.Add(pt.FundAccountCost)
	rev, err := recent.PaymentRevision(payment)
	if err != nil {
		t.Fatal(err)
	}

	// create transaction containing the revision
	sig := revisionSignature(rev, bh, sk)

	// prepare an account
	_, accountID := prepareAccount()

	// create streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var resp modules.FundAccountResponse
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		// send fund account request
		req := modules.FundAccountRequest{Account: accountID}
		err := modules.RPCWrite(rStream, req)
		if err != nil {
			t.Log(err)
			return
		}

		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig)
		err = modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			t.Log(err)
			return
		}

		// receive PayByContractResponse
		var payByResponse modules.PayByContractResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			t.Log(err)
			return
		}

		// receive FundAccountResponse
		err = modules.RPCRead(rStream, &resp)
		if err != nil {
			t.Log(err)
			return
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		err := ht.host.managedRPCFundEphemeralAccount(hStream, pt)
		if err != nil {
			modules.RPCWriteError(hStream, err)
		}
	}()
	wg.Wait()

	// verify the signature
	var pk crypto.PublicKey
	copy(pk[:], hpk.Key)
	err = crypto.VerifyHash(crypto.HashAll(resp.Receipt), pk, resp.Signature)
	if err != nil {
		t.Fatal("could not verify host's signature")
	}

	// verify the receipt
	if !resp.Receipt.Amount.Equals(funding) {
		t.Fatalf("Unexpected funded amount in the receipt, expected %v but received %v", funding.HumanString(), resp.Receipt.Amount.HumanString())
	}
	if resp.Receipt.Account != accountID {
		t.Fatalf("Unexpected account id in the receipt, expected %v but received %v", accountID, resp.Receipt.Account)
	}
	if !resp.Receipt.Host.Equals(hpk) {
		t.Fatalf("Unexpected host pubkey in the receipt, expected %v but received %v", hpk, resp.Receipt.Host)
	}

	// verify the funding got deposited into the ephemeral account
	balance := getAccountBalance(ht.host.staticAccountManager, accountID)
	if !balance.Equals(funding) {
		t.Fatalf("Unexpected account balance, expected %v but received %v", funding.HumanString(), balance.HumanString())
	}
}
