package renter

import (
	"net"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
)

// TestAccount verifies the PaymentProvider interface on the account.
func TestAccount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// generate a hostkey
	_, pk := crypto.GenerateKeyPair()
	hpk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// verify we can open an account
	acc := openAccount(hpk)
	var apk types.SiaPublicKey
	apk.LoadString(acc.staticID)

	// create two streams and verify the payment process
	rStream, hStream := NewTestStreams()
	defer rStream.Close()
	defer hStream.Close()

	var pErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		pErr = acc.ProvidePayment(rStream, hpk, types.Specifier{}, types.ZeroCurrency, 0)
	}()

	// verify the PaymentRequest
	var pr modules.PaymentRequest
	if err := modules.RPCRead(hStream, &pr); err != nil {
		t.Fatal("Could not read the PaymentRequest")
	}
	if pr.Type != modules.PayByEphemeralAccount {
		t.Fatal("Unexpected payment method")
	}

	// verify the PayByEphemeralAccountRequest
	var req modules.PayByEphemeralAccountRequest
	if err := modules.RPCRead(hStream, &req); err != nil {
		t.Fatal("Could not read the PayByEphemeralAccountRequest", err)
	}
	var cpk crypto.PublicKey
	copy(cpk[:], apk.Key[:])
	err := crypto.VerifyHash(crypto.HashObject(req.Message), cpk, req.Signature)
	if err != nil {
		t.Fatal("Could not verify the signature", err)
	}

	// send the response
	if err := modules.RPCWrite(hStream, modules.PayByEphemeralAccountResponse{Amount: req.Message.Amount}); err != nil {
		t.Fatal("Could not send the response")
	}

	wg.Wait()
	if pErr != nil {
		t.Fatal("Unexpected payment error", pErr)
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
