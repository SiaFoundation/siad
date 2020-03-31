package host

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// TestPaymentDetails verifies the payment details that are returned by both its
// constructors in case of payment by file contract or by account
func TestPaymentDetails(t *testing.T) {
	// prepare some variables
	account := "c436215b2c9c"
	amount := types.NewCurrency64(1)

	// verify payment details that are generated from a withdrawal message
	msg := modules.WithdrawalMessage{
		Account: account,
		Amount:  amount,
	}
	details, err := accountPaymentDetails(msg)
	if err != nil {
		t.Fatal("Could not generate account payment details", err)
	}
	if details.Account() != account {
		t.Fatalf("Unexpected account, expected %v actual %v", account, details.Account())
	}
	if !details.Amount().Equals(amount) {
		t.Fatalf("Unexpected amount, expected %v actual %v", amount.HumanString(), details.Amount().HumanString())
	}
	if !details.AddedCollateral().IsZero() {
		t.Fatalf("Unexpected added collateral, expected 0 actual %v", details.AddedCollateral().HumanString())
	}

	// verify payment details that are generated from a revision
	curr := types.FileContractRevision{
		NewValidProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.ZeroCurrency},
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(10)},
			{Value: types.NewCurrency64(10)},
			{Value: types.ZeroCurrency},
		},
	}
	rev, err := curr.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}
	details, err = contractPaymentDetails(curr, rev)
	if err != nil {
		t.Fatal("Could not generate contract payment details", err)
	}
	if details.Account() != "" {
		t.Fatal("Expected account to be an empty string")
	}
	if !details.Amount().Equals(amount) {
		t.Fatalf("Unexpected amount, expected %v actual %v", amount.HumanString(), details.Amount().HumanString())
	}
	if !details.AddedCollateral().IsZero() {
		t.Fatalf("Unexpected added collateral, expected 0 actual %v", details.AddedCollateral().HumanString())
	}

	// update the current revision, update amount as well to keep things spicy
	curr = rev
	amount = types.NewCurrency64(2)
	collateral := types.NewCurrency64(1)

	// verify a revision that moves collateral
	rev, err = curr.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}

	// move the collateral
	rev.SetMissedHostPayout(rev.MissedHostOutput().Value.Sub(collateral))
	voidOutput, err := rev.MissedVoidOutput()
	if err != nil {
		t.Fatal(err)
	}
	err = rev.SetMissedVoidPayout(voidOutput.Value.Add(collateral))
	if err != nil {
		t.Fatal(err)
	}

	details, err = contractPaymentDetails(curr, rev)
	if err != nil {
		t.Fatal("Could not generate contract payment details", err)
	}
	if details.Account() != "" {
		t.Fatal("Expected account to be an empty string")
	}
	if !details.Amount().Equals(amount) {
		t.Fatalf("Unexpected amount, expected %v actual %v", amount.HumanString(), details.Amount().HumanString())
	}
	if !details.AddedCollateral().Equals(collateral) {
		t.Fatalf("Unexpected added collateral, expected %v actual %v", collateral.HumanString(), details.AddedCollateral().HumanString())
	}

	// update the current revision
	oldHostPayout := curr.ValidHostPayout()
	curr = rev

	// verify a revision that would cause an underflow
	rev, err = curr.PaymentRevision(amount)
	rev.SetValidHostPayout(oldHostPayout)
	assertRecover := func() {
		if r := recover(); r == nil {
			t.Fatalf("Expected a panic when a revision is passed that results in an underflow")
		}
	}
	func() {
		defer assertRecover()
		contractPaymentDetails(curr, rev)
	}()
}

// TestProcessPayment verifies the host's ProcessPayment method. It covers both
// the PayByContract and PayByEphemeralAccount payment methods.
func TestProcessPayment(t *testing.T) {
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

	// create a renter key pair
	sk, pk := crypto.GenerateKeyPair()
	renterPK := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
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

	// test both payment methods
	testPayByContract(t, ht.host, so, sk)
	testPayByEphemeralAccount(t, ht.host, so)
}

// testPayByContract verifies payment is processed correctly in the case of the
// PayByContract payment method.
func testPayByContract(t *testing.T, host *Host, so storageObligation, renterSK crypto.SecretKey) {
	amount := types.SiacoinPrecision
	amountStr := amount.HumanString()

	// prepare an updated revision that pays the host
	recent, err := so.recentRevision()
	if err != nil {
		t.Fatal(err)
	}
	rev, err := recent.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}
	sig := revisionSignature(rev, host.blockHeight, renterSK)

	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var payment modules.PaymentDetails
	var payByResponse modules.PayByContractResponse

	renterFunc := func() error {
		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig)
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
		payment, err = host.ProcessPayment(hStream)
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
	updated, err := host.managedGetStorageObligation(so.id())
	if err != nil {
		t.Fatal(err)
	}
	recent, err = updated.recentRevision()
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
	if !payment.AddedCollateral().IsZero() {
		t.Fatalf("Unexpected collateral added, expected 0H actual %v", payment.AddedCollateral())
	}

	// prepare a set of payouts that do not deduct payment from the renter
	validPayouts, missedPayouts := updated.payouts()
	validPayouts[1].Value = validPayouts[1].Value.Add(amount)
	missedPayouts[0].Value = missedPayouts[0].Value.Sub(amount)
	missedPayouts[1].Value = missedPayouts[1].Value.Add(amount)

	// overwrite the correct payouts with the faulty payouts
	rev, err = recent.PaymentRevision(amount)
	if err != nil {
		t.Fatal(err)
	}
	rev.NewValidProofOutputs = validPayouts
	rev.NewMissedProofOutputs = missedPayouts
	sig = revisionSignature(rev, host.blockHeight, renterSK)

	// verify err is not nil
	err = run(renterFunc, hostFunc)
	if err == nil || !strings.Contains(err.Error(), "Invalid payment revision") {
		t.Fatalf("Expected error indicating the invalid revision, instead error was: '%v'", err)
	}
}

// testPayByEphemeralAccount verifies payment is processed correctly in the case
// of the PayByEphemeralAccount payment method.
func testPayByEphemeralAccount(t *testing.T, host *Host, so storageObligation) {
	amount := types.NewCurrency64(5)
	deposit := types.NewCurrency64(8) // enough to perform 1 payment, but not 2

	// prepare an ephmeral account and fund it
	sk, spk := prepareAccount()
	account := spk.String()
	err := callDeposit(host.staticAccountManager, account, deposit)
	if err != nil {
		t.Fatal(err)
	}
	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var payment modules.PaymentDetails
	var payByResponse modules.PayByEphemeralAccountResponse

	renterFunc := func() error {
		// send PaymentRequest & PayByEphemeralAccountRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByEphemeralAccount}
		pbcRequest := newPayByEphemeralAccountRequest(account, host.blockHeight+6, amount, sk)
		err := modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			return err
		}

		// receive PayByEphemeralAccountResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			return err
		}
		return nil
	}
	hostFunc := func() error {
		// process payment request
		payment, err = host.ProcessPayment(hStream)
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
	if payment.Account() != account {
		t.Fatalf("Unexpected account id, expected %s but received %s", account, payment.Account())
	}

	// verify the response contains the amount that got withdrawn
	if !payByResponse.Amount.Equals(amount) {
		t.Fatalf("Unexpected payment amount, expected %s, but received %s", amount.HumanString(), payByResponse.Amount.HumanString())
	}

	// verify the payment got withdrawn from the ephemeral account
	balance := getAccountBalance(host.staticAccountManager, account)
	if !balance.Equals(deposit.Sub(amount)) {
		t.Fatalf("Unexpected account balance, expected %v but received %s", deposit.Sub(amount), balance.HumanString())
	}

	// try and perform the same request again, which should fail because the
	// account balance is insufficient verify err is not nil and contains a
	// mention of insufficient balance
	err = run(renterFunc, hostFunc)
	if err == nil || !strings.Contains(err.Error(), "balance was insufficient") {
		t.Fatalf("Expected error to mention account balance was insuficient, instead error was: '%v'", err)
	}
}

// newPayByContractRequest uses a revision and signature to build the
// PayBycontractRequest
func newPayByContractRequest(rev types.FileContractRevision, sig crypto.Signature) modules.PayByContractRequest {
	var req modules.PayByContractRequest

	req.ContractID = rev.ID()
	req.NewRevisionNumber = rev.NewRevisionNumber
	req.NewValidProofValues = make([]types.Currency, len(rev.NewValidProofOutputs))
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

// newPayByEphemeralAccountRequest uses the given parameters to create a
// PayByEphemeralAccountRequest
func newPayByEphemeralAccountRequest(account string, expiry types.BlockHeight, amount types.Currency, sk crypto.SecretKey) modules.PayByEphemeralAccountRequest {
	// generate a nonce
	var nonce [modules.WithdrawalNonceSize]byte
	copy(nonce[:], fastrand.Bytes(len(nonce)))

	// create a new WithdrawalMessage
	wm := modules.WithdrawalMessage{
		Account: account,
		Expiry:  expiry,
		Amount:  amount,
		Nonce:   nonce,
	}

	// sign it
	sig := crypto.SignHash(crypto.HashObject(wm), sk)
	return modules.PayByEphemeralAccountRequest{
		Message:   wm,
		Signature: sig,
	}
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

// revisionSignature is a helper function that signs the given revision with the
// given key
func revisionSignature(rev types.FileContractRevision, blockHeight types.BlockHeight, secretKey crypto.SecretKey) crypto.Signature {
	signedTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
		TransactionSignatures: []types.TransactionSignature{{
			ParentID:       crypto.Hash(rev.ParentID),
			CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
			PublicKeyIndex: 0,
		}},
	}
	hash := signedTxn.SigHash(0, blockHeight)
	return crypto.SignHash(hash, secretKey)
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
func NewTestStreams() (client siamux.Stream, server siamux.Stream) {
	var clientConn net.Conn
	var serverConn net.Conn
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverConn, _ = ln.Accept()
		wg.Done()
	}()
	clientConn, _ = net.Dial("tcp", ln.Addr().String())
	wg.Wait()

	client = testStream{c: clientConn}
	server = testStream{c: serverConn}
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

// TestStreams is a small test that verifies the working of the test stream. It
// will test that an object can be written to and read from the stream over the
// underlying connection.
func TestStreams(t *testing.T) {
	renter, host := NewTestStreams()

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
