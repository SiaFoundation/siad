package host

import (
	"net"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

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
	// prepare an updated revision that pays the host
	rev := so.recentRevision()
	payment := types.NewCurrency64(1)
	validPayouts, missedPayouts := so.payouts()
	validPayouts[0].Value = validPayouts[0].Value.Sub(payment)
	validPayouts[1].Value = validPayouts[1].Value.Add(payment)
	missedPayouts[0].Value = missedPayouts[0].Value.Sub(payment)
	missedPayouts[1].Value = missedPayouts[1].Value.Add(payment)
	rev.NewValidProofOutputs = validPayouts
	rev.NewMissedProofOutputs = missedPayouts
	rev.NewRevisionNumber = rev.NewRevisionNumber + 1

	// create transaction containing the revision
	signedTxn := types.NewTransaction(rev, 0)
	hash := signedTxn.SigHash(0, host.blockHeight)
	sig := crypto.SignHash(hash, renterSK)

	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var wg sync.WaitGroup
	var payByResponse modules.PayByContractResponse

	wg.Add(1)
	go func() {
		defer wg.Done()

		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig)
		err := modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			t.Log(err)
			return
		}

		// receive PayByContractResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			t.Log(err)
			return
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// process payment request
		_, err := host.ProcessPayment(hStream)
		if err != nil {
			modules.RPCWriteError(hStream, err)
			return
		}
	}()
	wg.Wait()

	// verify the host's signature
	hash = crypto.HashAll(rev)
	var hpk crypto.PublicKey
	copy(hpk[:], host.PublicKey().Key)
	err := crypto.VerifyHash(hash, hpk, payByResponse.Signature)
	if err != nil {
		t.Fatal("could not verify host's signature")
	}

	// verify the host updated the storage obligation
	host.managedLockStorageObligation(so.id())
	updated, err := host.managedGetStorageObligation(so.id())
	if err != nil {
		t.Fatal(err)
	}
	rr := updated.recentRevision()
	if rev.NewRevisionNumber != rr.NewRevisionNumber {
		t.Log("expected", rev.NewRevisionNumber)
		t.Log("actual", rr.NewRevisionNumber)
		t.Fatal("Unexpected revision number")
	}
}

// testPayByEphemeralAccount verifies payment is processed correctly in the case
// of the PayByEphemeralAccount payment method.
func testPayByEphemeralAccount(t *testing.T, host *Host, so storageObligation) {
	payment := types.NewCurrency64(1)

	// prepare an ephmeral account and fund it
	sk, spk := prepareAccount()
	account := spk.String()
	err := callDeposit(host.staticAccountManager, account, payment)
	if err != nil {
		t.Fatal(err)
	}
	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var wg sync.WaitGroup
	var payByResponse modules.PayByEphemeralAccountResponse

	wg.Add(1)
	go func() {
		defer wg.Done()

		// send PaymentRequest & PayByEphemeralAccountRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByEphemeralAccount}
		pbcRequest := newPayByEphemeralAccountRequest(account, host.blockHeight+6, payment, sk)
		err := modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			t.Log(err)
			return
		}

		// receive PayByEphemeralAccountResponse
		err = modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			t.Log(err)
			return
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// process payment request
		_, err := host.ProcessPayment(hStream)
		if err != nil {
			t.Log(err)
			return
		}
	}()
	wg.Wait()

	// verify the response contains the amount that got withdrawn
	if !payByResponse.Amount.Equals(payment) {
		t.Log("Expected: ", payment.HumanString())
		t.Log("Actual: ", payByResponse.Amount.HumanString())
		t.Fatal("Unexpected payment amount")
	}

	// verify the payment got withdrawn from the ephemeral account
	balance := getAccountBalance(host.staticAccountManager, account)
	if !balance.IsZero() {
		t.Log("Expected: 0")
		t.Log("Actual: ", balance.HumanString())
		t.Fatal("Unexpected account balance")
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
