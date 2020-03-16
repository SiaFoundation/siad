package host

import (
	"fmt"
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

type testStream struct {
	conn net.Conn
}

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

	client = testStream{conn: clientConn}
	server = testStream{conn: serverConn}
	return
}
func (s testStream) Read(b []byte) (n int, err error) {
	return s.conn.Read(b)
}
func (s testStream) Write(b []byte) (n int, err error) {
	return s.conn.Write(b)
}
func (s testStream) Close() error {
	return s.conn.Close()
}
func (s testStream) LocalAddr() net.Addr {
	panic("not implemented yet")
}
func (s testStream) RemoteAddr() net.Addr {
	panic("not implemented yet")
}
func (s testStream) SetDeadline(t time.Time) error {
	panic("not implemented yet")
}
func (s testStream) SetPriority(priority int) error {
	panic("not implemented yet")
}
func (s testStream) SetReadDeadline(t time.Time) error {
	panic("not implemented yet")
}
func (s testStream) SetWriteDeadline(t time.Time) error {
	panic("not implemented yet")
}

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

// TestProcessPayment verifies the payment processing methods on the host.
func TestProcessPayment(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// create an SO
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	so, err = ht.addNoOpRevision(so)
	if err != nil {
		t.Fatal(err)
	}

	// add a SO to emulate a renter creating a contract
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so, false)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())

	// prepare an updated revision that pays the host
	payment := types.NewCurrency64(1)
	rev := newPaymentRevision(so, payment)

	// random renter sk
	var sk crypto.SecretKey
	copy(sk[:], fastrand.Bytes(64))

	// create transaction containing the revision
	signedTxn := types.NewTransaction(rev, 0)
	hash := signedTxn.SigHash(0, ht.host.blockHeight)
	sig := crypto.SignHash(hash, sk)

	// create two streams
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var wg sync.WaitGroup
	var payByResponse modules.PayByContractResponse

	renterFunc := func() {
		// send PaymentRequest & PayByContractRequest
		pRequest := modules.PaymentRequest{Type: modules.PayByContract}
		pbcRequest := newPayByContractRequest(rev, sig)
		err = modules.RPCWriteAll(rStream, pRequest, pbcRequest)
		if err != nil {
			t.Fatal(err)
		}

		// receive PayByContractResponse
		err := modules.RPCRead(rStream, &payByResponse)
		if err != nil {
			t.Fatal(err)
		}
	}
	hostFunc := func() {
		// process payment request
		_, err := ht.host.ProcessPayment(hStream)
		if err != nil {
			t.Fatal(err)
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		renterFunc()
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		hostFunc()
	}()
	wg.Wait()

	// verify the host's signature
	hash = crypto.HashAll(rev)
	var hpk crypto.PublicKey
	copy(hpk[:], ht.host.PublicKey().Key)
	err = crypto.VerifyHash(hash, hpk, payByResponse.Signature)
	if err != nil {
		t.Fatal("could not verify host's signature")
	}
}

// newTestStream is a helper method to create a stream
func newTestStream(h *Host) (siamux.Stream, error) {
	hes := h.ExternalSettings()
	muxAddress := fmt.Sprintf("%s:%s", hes.NetAddress.Host(), hes.SiaMuxPort)
	mux := h.staticMux

	// fetch a stream from the mux
	stream, err := mux.NewStream(modules.HostSiaMuxSubscriberName, muxAddress, modules.SiaPKToMuxPK(h.publicKey))
	if err != nil {
		return nil, err
	}
	return stream, nil
}

// newPaymentRevision is a helper function that transfers funds to the host.
func newPaymentRevision(so storageObligation, payment types.Currency) types.FileContractRevision {
	rev := so.recentRevision()
	validPayouts, missedPayouts := so.payouts()
	validPayouts[0].Value = validPayouts[0].Value.Sub(payment)
	validPayouts[1].Value = validPayouts[1].Value.Add(payment)
	missedPayouts[0].Value = missedPayouts[0].Value.Sub(payment)
	missedPayouts[1].Value = missedPayouts[1].Value.Add(payment)
	rev.NewValidProofOutputs = validPayouts
	rev.NewMissedProofOutputs = missedPayouts
	return rev
	// return types.FileContractRevision{
	// 	ParentID:          so.id(),
	// 	UnlockConditions:  types.UnlockConditions{},
	// 	NewRevisionNumber: 1,

	// 	NewFileSize:           so.fileSize(),
	// 	NewFileMerkleRoot:     so.merkleRoot(),
	// 	NewWindowStart:        so.expiration(),
	// 	NewWindowEnd:          so.proofDeadline(),
	// 	NewValidProofOutputs:  validPayouts,
	// 	NewMissedProofOutputs: missedPayouts,
	// 	NewUnlockHash:         types.UnlockConditions{}.UnlockHash(),
	// }
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
