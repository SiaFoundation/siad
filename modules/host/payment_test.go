package host

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	invalidSpecifier = types.NewSpecifier("Invalid")
)

// TestVerifyEAFundRevision is a unit test covering verifyEAFundRevision
func TestVerifyEAFundRevision(t *testing.T) {
	t.Parallel()

	// create a current revision and a payment revision
	height := types.BlockHeight(0)
	amount := types.NewCurrency64(1)
	curr := types.FileContractRevision{
		NewValidProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(1)},
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(1)},
			{Value: types.ZeroCurrency},
		},
		NewWindowStart: types.BlockHeight(revisionSubmissionBuffer) + 1,
	}
	payment, err := curr.EAFundRevision(amount)
	if err != nil {
		t.Fatal(err)
	}

	// verify a properly created payment revision is accepted
	err = verifyEAFundRevision(curr, payment, height, amount)
	if err != nil {
		t.Fatal("Unexpected error when verifying revision, ", err)
	}

	// deepCopy is a helper function that makes a deep copy of a revision
	deepCopy := func(rev types.FileContractRevision) (revCopy types.FileContractRevision) {
		rBytes := encoding.Marshal(rev)
		err := encoding.Unmarshal(rBytes, &revCopy)
		if err != nil {
			panic(err)
		}
		return
	}

	// expect ErrBadContractOutputCounts
	badOutputs := []types.SiacoinOutput{payment.NewMissedProofOutputs[0]}
	badPayment := deepCopy(payment)
	badPayment.NewMissedProofOutputs = badOutputs
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatalf("Expected ErrBadContractOutputCounts but received '%v'", err)
	}

	// expect ErrLateRevision
	badCurr := deepCopy(curr)
	badCurr.NewWindowStart = curr.NewWindowStart - 1
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, ErrLateRevision) {
		t.Fatalf("Expected ErrLateRevision but received '%v'", err)
	}

	// expect host payout address changed
	hash := crypto.HashBytes([]byte("random"))
	badCurr = deepCopy(curr)
	badCurr.NewValidProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "host payout address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect host payout address changed
	badCurr = deepCopy(curr)
	badCurr.NewMissedProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "host payout address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect missed void output
	badCurr = deepCopy(curr)
	badCurr.NewMissedProofOutputs = append([]types.SiacoinOutput{}, curr.NewMissedProofOutputs[:2]...)
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, types.ErrMissingVoidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", types.ErrMissingVoidOutput, err)
	}

	// expect lost collateral address changed
	badPayment = deepCopy(payment)
	badPayment.NewMissedProofOutputs[2].UnlockHash = types.UnlockHash(hash)
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), "lost collateral address was changed") {
		t.Fatalf("Expected lost collaterall error but received '%v'", err)
	}

	// expect renter increased its proof output
	badPayment = deepCopy(payment)
	badPayment.SetValidRenterPayout(curr.ValidRenterPayout().Add64(1))
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrHighRenterValidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterValidOutput), err)
	}

	// expect an error saying not enough money was transferred
	err = verifyEAFundRevision(curr, payment, height, amount.Add64(1))
	if !errors.Contains(err, ErrHighRenterValidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterValidOutput), err)
	}
	expectedErrorMsg := fmt.Sprintf("expected at least %v to be exchanged, but %v was exchanged: ", amount.Add64(1), curr.ValidRenterPayout().Sub(payment.ValidRenterPayout()))
	if err == nil || !strings.Contains(err.Error(), expectedErrorMsg) {
		t.Fatalf("Expected '%v' but received '%v'", expectedErrorMsg, err)
	}

	// expect ErrLowHostValidOutput
	badPayment = deepCopy(payment)
	badPayment.SetValidHostPayout(curr.ValidHostPayout().Sub64(1))
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrLowHostValidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostValidOutput), err)
	}

	// expect ErrLowHostValidOutput
	badCurr = deepCopy(curr)
	badCurr.SetValidHostPayout(curr.ValidHostPayout().Sub64(1))
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, ErrLowHostValidOutput) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostValidOutput), err)
	}

	// expect ErrHighRenterMissedOutput
	badPayment = deepCopy(payment)
	badPayment.SetMissedRenterPayout(payment.MissedRenterOutput().Value.Sub64(1))
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if err == nil || !strings.Contains(err.Error(), string(ErrHighRenterMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterMissedOutput), err)
	}

	// expect ErrLowHostMissedOutput
	badCurr = deepCopy(curr)
	currOut := curr.MissedHostOutput()
	currOut.Value = currOut.Value.Add64(1)
	badCurr.NewMissedProofOutputs[1] = currOut
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, ErrLowHostMissedOutput) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostMissedOutput), err)
	}

	// expect ErrBadRevisionNumber
	badOutputs = []types.SiacoinOutput{payment.NewMissedProofOutputs[0]}
	badPayment = deepCopy(payment)
	badPayment.NewMissedProofOutputs = badOutputs
	badPayment.NewRevisionNumber--
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadRevisionNumber) {
		t.Fatalf("Expected ErrBadRevisionNumber but received '%v'", err)
	}

	// expect ErrBadParentID
	badPayment = deepCopy(payment)
	badPayment.ParentID = types.FileContractID(hash)
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadParentID) {
		t.Fatalf("Expected ErrBadParentID but received '%v'", err)
	}

	// expect ErrBadUnlockConditions
	badPayment = deepCopy(payment)
	badPayment.UnlockConditions.Timelock = payment.UnlockConditions.Timelock + 1
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadUnlockConditions) {
		t.Fatalf("Expected ErrBadUnlockConditions but received '%v'", err)
	}

	// expect ErrBadFileSize
	badPayment = deepCopy(payment)
	badPayment.NewFileSize = payment.NewFileSize + 1
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadFileSize) {
		t.Fatalf("Expected ErrBadFileSize but received '%v'", err)
	}

	// expect ErrBadFileMerkleRoot
	badPayment = deepCopy(payment)
	badPayment.NewFileMerkleRoot = hash
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadFileMerkleRoot) {
		t.Fatalf("Expected ErrBadFileMerkleRoot but received '%v'", err)
	}

	// expect ErrBadWindowStart
	badPayment = deepCopy(payment)
	badPayment.NewWindowStart = curr.NewWindowStart + 1
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadWindowStart) {
		t.Fatalf("Expected ErrBadWindowStart but received '%v'", err)
	}

	// expect ErrBadWindowEnd
	badPayment = deepCopy(payment)
	badPayment.NewWindowEnd = curr.NewWindowEnd - 1
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadWindowEnd) {
		t.Fatalf("Expected ErrBadWindowEnd but received '%v'", err)
	}

	// expect ErrBadUnlockHash
	badPayment = deepCopy(payment)
	badPayment.NewUnlockHash = types.UnlockHash(hash)
	err = verifyEAFundRevision(curr, badPayment, height, amount)
	if !errors.Contains(err, ErrBadUnlockHash) {
		t.Fatalf("Expected ErrBadUnlockHash but received '%v'", err)
	}

	// expect ErrLowHostMissedOutput
	badCurr = deepCopy(curr)
	badCurr.SetMissedHostPayout(payment.MissedHostPayout().Add64(1))
	err = verifyEAFundRevision(badCurr, payment, height, amount)
	if !errors.Contains(err, ErrLowHostMissedOutput) {
		t.Fatalf("Expected ErrLowHostMissedOutput but received '%v'", err)
	}

	// NOTE: we don't trigger the last check in verifyEAFundRevision which makes
	// sure that the payouts between the revisions match. This is due to the
	// fact that the existing checks around the outputs are so tight, that they
	// will trigger before the payout check does. This essentially makes the
	// payout check redundant, but it's skill kept to be 100% sure.
}

// TestProcessPayment verifies the host's ProcessPayment method. It covers both
// the PayByContract and PayByEphemeralAccount payment methods.
func TestProcessPayment(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// setup a host and renter pair with an emulated file contract between them
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

	// test both payment methods
	testPayByContract(t, pair)
	testPayByEphemeralAccount(t, pair)

	// test unknown payment method
	testUnknownPaymentMethodError(t, pair)
}

// testPayByContract verifies payment is processed correctly in the case of the
// PayByContract payment method.
func testPayByContract(t *testing.T, pair *renterHostPair) {
	host := pair.staticHT.host
	amount := types.SiacoinPrecision.Div64(2)
	amountStr := amount.HumanString()

	// prepare an updated revision that pays the host
	rev, sig, err := pair.managedEAFundRevision(amount)
	if err != nil {
		t.Fatal(err)
	}

	// create two streams
	rStream, hStream, err := NewTestStreams()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		if err := rStream.Close(); err != nil {
			t.Fatal(err)
		}
		if err := hStream.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// create a refund account.
	_, refundAccount := prepareAccount()

	var payment modules.PaymentDetails
	var payByResponse modules.PayByContractResponse

	renterFunc := func() error {
		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig, refundAccount)
		err := modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			return err
		}

		// receive PayByContractResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			return err
		}
		return nil
	}
	hostFunc := func() error {
		// process payment request
		payment, err = host.ProcessPayment(hStream, host.BlockHeight())
		if err != nil {
			modules.RPCWriteError(hStream, err)
		}
		return nil
	}

	// run the payment code
	err = run(renterFunc, hostFunc)
	if err != nil {
		t.Fatal("Unexpected error occurred", err.Error())
	}

	// verify the host's signature
	hash := crypto.HashAll(rev)
	var hpk crypto.PublicKey
	copy(hpk[:], host.PublicKey().Key)
	err = crypto.VerifyHash(hash, hpk, payByResponse.Signature)
	if err != nil {
		t.Fatal("could not verify host's signature")
	}

	// verify the host updated the storage obligation
	updated, err := host.managedGetStorageObligation(pair.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	recent, err := updated.recentRevision()
	if err != nil {
		t.Fatal(err)
	}
	if rev.NewRevisionNumber != recent.NewRevisionNumber {
		t.Log("expected", rev.NewRevisionNumber)
		t.Log("actual", recent.NewRevisionNumber)
		t.Fatal("Unexpected revision number")
	}

	// verify the payment details
	if !payment.Amount().Equals(amount) {
		t.Fatalf("Unexpected amount paid, expected %v actual %v", amountStr, payment.Amount().HumanString())
	}

	// prepare a set of payouts that do not deduct payment from the renter
	validPayouts, missedPayouts := updated.payouts()
	validPayouts[1].Value = validPayouts[1].Value.Add(amount)
	missedPayouts[0].Value = missedPayouts[0].Value.Sub(amount)
	missedPayouts[1].Value = missedPayouts[1].Value.Add(amount)

	// overwrite the correct payouts with the faulty payouts
	rev, err = recent.EAFundRevision(amount)
	if err != nil {
		t.Fatal(err)
	}
	rev.NewValidProofOutputs = validPayouts
	rev.NewMissedProofOutputs = missedPayouts
	sig = pair.managedSign(rev)

	// verify err is not nil
	err = run(renterFunc, hostFunc)
	if err == nil || !strings.Contains(err.Error(), "Invalid payment revision") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}

	// Manually add money to the refund account.
	refund := types.NewCurrency64(fastrand.Uint64n(100) + 1)
	err = pair.staticHT.host.staticAccountManager.callRefund(refundAccount, refund)
	if err != nil {
		t.Fatal(err)
	}

	// Run the code again. This time since we funded the account, the
	// payByResponse would report the funded amount instead of 0.
	rev, sig, err = pair.managedEAFundRevision(amount)
	if err != nil {
		t.Fatal(err)
	}
	err = run(renterFunc, hostFunc)
	if err != nil {
		t.Fatal(err)
	}

	//  Run the code again. This time it should fail due to no refund account
	//  being provided.
	refundAccount = modules.ZeroAccountID
	err = run(renterFunc, hostFunc)
	if err == nil || !strings.Contains(err.Error(), "no account id provided for refunds") {
		t.Fatal("Unexpected error occurred", err.Error())
	}
}

// testPayByEphemeralAccount verifies payment is processed correctly in the case
// of the PayByEphemeralAccount payment method.
func testPayByEphemeralAccount(t *testing.T, pair *renterHostPair) {
	host := pair.staticHT.host
	amount := types.NewCurrency64(5)
	deposit := types.NewCurrency64(8) // enough to perform 1 payment, but not 2

	// prepare an ephemeral account and fund it
	sk, accountID := prepareAccount()
	err := callDeposit(host.staticAccountManager, accountID, deposit)
	if err != nil {
		t.Fatal(err)
	}
	// create two streams
	rStream, hStream, err := NewTestStreams()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		if err := rStream.Close(); err != nil {
			t.Fatal(err)
		}
		if err := hStream.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	var payment modules.PaymentDetails

	renterFunc := func() error {
		// send PaymentRequest & PayByEphemeralAccountRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByEphemeralAccount}
		pbcRequest := modules.NewPayByEphemeralAccountRequest(accountID, host.blockHeight+6, amount, sk)
		return modules.RPCWriteAll(rStream, pRequest, pbcRequest)
	}
	hostFunc := func() error {
		// process payment request
		payment, err = host.ProcessPayment(hStream, host.BlockHeight())
		if err != nil {
			modules.RPCWriteError(hStream, err)
		}
		return nil
	}

	// verify err is nil
	err = run(renterFunc, hostFunc)
	if err != nil {
		t.Fatal("Unexpected error occurred", err.Error())
	}

	// verify the account id that's returned equals the account
	if payment.AccountID() != accountID {
		t.Fatalf("Unexpected account id, expected %s but received %s", accountID, payment.AccountID())
	}

	// verify the payment got withdrawn from the ephemeral account
	balance := getAccountBalance(host.staticAccountManager, accountID)
	if !balance.Equals(deposit.Sub(amount)) {
		t.Fatalf("Unexpected account balance, expected %v but received %s", deposit.Sub(amount), balance.HumanString())
	}
}

// testUnknownPaymentMethodError verifies the host returns an error if we
// specify an unknown payment method
func testUnknownPaymentMethodError(t *testing.T, pair *renterHostPair) {
	// create two streams
	rStream, hStream, err := NewTestStreams()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		if err := rStream.Close(); err != nil {
			t.Fatal(err)
		}
		if err := hStream.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	err = run(func() error {
		// send PaymentRequest
		pr := modules.PaymentRequest{Type: invalidSpecifier}
		err := modules.RPCWriteAll(rStream, modules.RPCUpdatePriceTable, pr)
		if err != nil {
			return err
		}
		return modules.RPCRead(rStream, struct{}{})
	}, func() error {
		// process payment request
		_, err := pair.staticHT.host.ProcessPayment(hStream, pair.pt.HostBlockHeight)
		if err != nil {
			modules.RPCWriteError(hStream, err)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "unknown payment method") {
		t.Fatalf("Expected 'unknown payment method' error, but received '%v'", err)
	}
}

// newPayByContractRequest uses a revision and signature to build the
// PayBycontractRequest
func newPayByContractRequest(rev types.FileContractRevision, sig crypto.Signature, refundAccount modules.AccountID) modules.PayByContractRequest {
	var req modules.PayByContractRequest

	req.ContractID = rev.ID()
	req.NewRevisionNumber = rev.NewRevisionNumber
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
	req.RefundAccount = refundAccount
	for i, o := range rev.NewValidProofOutputs {
		req.NewValidProofValues[i] = o.Value
	}
	req.NewMissedProofValues = make([]types.Currency, len(rev.NewMissedProofOutputs))
	for i, o := range rev.NewMissedProofOutputs {
		req.NewMissedProofValues[i] = o.Value
	}
	req.Signature = sig[:]

	return req
}

// addNoOpRevision is a helper method that adds a revision to the given
// obligation. In production this 'noOpRevision' is always added, however the
// obligation returned by `newTesterStorageObligation` does not add it.
func (ht *hostTester) addNoOpRevision(so storageObligation, renterPK types.SiaPublicKey) (storageObligation, error) {
	builder, err := ht.wallet.StartTransaction()
	if err != nil {
		return storageObligation{}, err
	}

	txnSet := so.OriginTransactionSet
	contractTxn := txnSet[len(txnSet)-1]
	fc := contractTxn.FileContracts[0]

	noOpRevision := types.FileContractRevision{
		ParentID: contractTxn.FileContractID(0),
		UnlockConditions: types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{
				renterPK,
				ht.host.publicKey,
			},
			SignaturesRequired: 2,
		},
		NewRevisionNumber:     fc.RevisionNumber + 1,
		NewFileSize:           fc.FileSize,
		NewFileMerkleRoot:     fc.FileMerkleRoot,
		NewWindowStart:        fc.WindowStart,
		NewWindowEnd:          fc.WindowEnd,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
		NewUnlockHash:         fc.UnlockHash,
	}

	builder.AddFileContractRevision(noOpRevision)
	tSet, err := builder.Sign(true)
	if err != nil {
		return so, err
	}
	so.RevisionTransactionSet = tSet
	return so, nil
}

// addNewRevision is a helper method that adds a new revision to the given
// obligation with given newfilesize and newfilemerkleroot.
func (ht *hostTester) addNewRevision(so storageObligation, renterPK types.SiaPublicKey, newFileSize uint64, newFileMerkleRoot crypto.Hash) (storageObligation, error) {
	builder, err := ht.wallet.StartTransaction()
	if err != nil {
		return storageObligation{}, err
	}

	txnSet := so.OriginTransactionSet
	contractTxn := txnSet[len(txnSet)-1]
	fc := contractTxn.FileContracts[0]

	noOpRevision := types.FileContractRevision{
		ParentID: contractTxn.FileContractID(0),
		UnlockConditions: types.UnlockConditions{
			PublicKeys: []types.SiaPublicKey{
				renterPK,
				ht.host.publicKey,
			},
			SignaturesRequired: 2,
		},
		NewRevisionNumber:     fc.RevisionNumber + 1,
		NewFileSize:           newFileSize,
		NewFileMerkleRoot:     newFileMerkleRoot,
		NewWindowStart:        fc.WindowStart,
		NewWindowEnd:          fc.WindowEnd,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
		NewUnlockHash:         fc.UnlockHash,
	}

	builder.AddFileContractRevision(noOpRevision)
	tSet, err := builder.Sign(true)
	if err != nil {
		return so, err
	}
	so.RevisionTransactionSet = tSet
	return so, nil
}

// run is a helper function that runs the given functions in separate goroutines
// and awaits them
func run(f1, f2 func() error) error {
	var errF1, errF2 error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		errF1 = f1()
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		errF2 = f2()
		wg.Done()
	}()
	wg.Wait()
	return errors.Compose(errF1, errF2)
}

// testStream is a helper struct that wraps a net.Conn and implements the
// siamux.Stream interface.
type testStream struct {
	c net.Conn
}

// NewTestStreams returns two siamux.Stream mock objects.
func NewTestStreams() (client siamux.Stream, server siamux.Stream, err error) {
	var clientConn net.Conn
	var serverConn net.Conn
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, errors.AddContext(err, "net.Listen failed")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverConn, err = ln.Accept()
		wg.Done()
	}()
	clientConn, clientErr := net.Dial("tcp", ln.Addr().String())
	wg.Wait()
	if err != nil {
		return nil, nil, errors.AddContext(err, "listener.Accept failed")
	}
	if clientErr != nil {
		return nil, nil, errors.AddContext(clientErr, "net.Dial failed for clientConn")
	}

	client = testStream{c: clientConn}
	server = testStream{c: serverConn}
	return client, server, nil
}

func (s testStream) Read(b []byte) (n int, err error)  { return s.c.Read(b) }
func (s testStream) Write(b []byte) (n int, err error) { return s.c.Write(b) }
func (s testStream) Close() error                      { return s.c.Close() }

func (s testStream) LocalAddr() net.Addr            { panic("not implemented") }
func (s testStream) Mux() *mux.Mux                  { panic("not implemented") }
func (s testStream) RemoteAddr() net.Addr           { panic("not implemented") }
func (s testStream) SetDeadline(t time.Time) error  { panic("not implemented") }
func (s testStream) SetPriority(priority int) error { panic("not implemented") }

func (s testStream) Limit() mux.BandwidthLimit           { panic("not implemented") }
func (s testStream) SetLimit(_ mux.BandwidthLimit) error { panic("not implemented") }

func (s testStream) SetReadDeadline(t time.Time) error {
	panic("not implemented")
}
func (s testStream) SetWriteDeadline(t time.Time) error {
	panic("not implemented")
}

// TestStreams is a small test that verifies the working of the test stream. It
// will test that an object can be written to and read from the stream over the
// underlying connection.
func TestStreams(t *testing.T) {
	renter, host, err := NewTestStreams()
	if err != nil {
		t.Fatal(err)
	}

	var pr modules.PaymentRequest
	var wg sync.WaitGroup
	wg.Add(1)
	func() {
		defer wg.Done()
		req := modules.PaymentRequest{Type: modules.PayByContract}
		err := modules.RPCWrite(renter, req)
		if err != nil {
			t.Fatal(err)
		}
	}()

	wg.Add(1)
	func() {
		defer wg.Done()
		err := modules.RPCRead(host, &pr)
		if err != nil {
			t.Fatal(err)
		}
	}()
	wg.Wait()

	if pr.Type != modules.PayByContract {
		t.Fatal("Unexpected request received")
	}
}

// TestRevisionFromRequest tests revisionFromRequest valid flow and some edge
// cases.
func TestRevisionFromRequest(t *testing.T) {
	recent := types.FileContractRevision{
		NewValidProofOutputs: []types.SiacoinOutput{
			{Value: types.SiacoinPrecision},
			{Value: types.SiacoinPrecision},
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{Value: types.SiacoinPrecision},
			{Value: types.SiacoinPrecision},
			{Value: types.SiacoinPrecision},
		},
	}
	pbcr := modules.PayByContractRequest{
		NewValidProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10),
			types.SiacoinPrecision.Mul64(100),
		},
		NewMissedProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(1000),
			types.SiacoinPrecision.Mul64(10000),
			types.SiacoinPrecision.Mul64(100000),
		},
	}

	// valid case
	rev := revisionFromRequest(recent, pbcr)
	if !rev.NewValidProofOutputs[0].Value.Equals(types.SiacoinPrecision.Mul64(10)) {
		t.Fatal("valid output 0 doesn't match")
	}
	if !rev.NewValidProofOutputs[1].Value.Equals(types.SiacoinPrecision.Mul64(100)) {
		t.Fatal("valid output 1 doesn't match")
	}
	if !rev.NewMissedProofOutputs[0].Value.Equals(types.SiacoinPrecision.Mul64(1000)) {
		t.Fatal("missed output 0 doesn't match")
	}
	if !rev.NewMissedProofOutputs[1].Value.Equals(types.SiacoinPrecision.Mul64(10000)) {
		t.Fatal("missed output 1 doesn't match")
	}
	if !rev.NewMissedProofOutputs[2].Value.Equals(types.SiacoinPrecision.Mul64(100000)) {
		t.Fatal("missed output 2 doesn't match")
	}

	// too few valid outputs
	pbcr = modules.PayByContractRequest{
		NewValidProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10),
		},
		NewMissedProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(1000),
			types.SiacoinPrecision.Mul64(10000),
			types.SiacoinPrecision.Mul64(100000),
		},
	}
	_ = revisionFromRequest(recent, pbcr)

	// too many valid outputs
	pbcr = modules.PayByContractRequest{
		NewValidProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10),
			types.SiacoinPrecision.Mul64(10),
			types.SiacoinPrecision.Mul64(10),
		},
		NewMissedProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(1000),
			types.SiacoinPrecision.Mul64(10000),
			types.SiacoinPrecision.Mul64(100000),
		},
	}
	_ = revisionFromRequest(recent, pbcr)

	// too few missed outputs.
	pbcr = modules.PayByContractRequest{
		NewValidProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10),
			types.SiacoinPrecision.Mul64(100),
		},
		NewMissedProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10000),
			types.SiacoinPrecision.Mul64(100000),
		},
	}
	_ = revisionFromRequest(recent, pbcr)

	// too many missed outputs.
	pbcr = modules.PayByContractRequest{
		NewValidProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10),
			types.SiacoinPrecision.Mul64(100),
		},
		NewMissedProofValues: []types.Currency{
			types.SiacoinPrecision.Mul64(10000),
			types.SiacoinPrecision.Mul64(100000),
			types.SiacoinPrecision.Mul64(100000),
			types.SiacoinPrecision.Mul64(100000),
		},
	}
	_ = revisionFromRequest(recent, pbcr)
}
