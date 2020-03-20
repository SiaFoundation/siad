package modules

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
)

// TestSiaMuxCompat verifies the SiaMux is initialized in compatibility mode
// when the host's persitence metadata version is v1.4.2
func TestSiaMuxCompat(t *testing.T) {
	// ensure the host's persistence file does not exist
	siaDataDir := filepath.Join(os.TempDir(), t.Name())
	siaMuxDir := filepath.Join(siaDataDir, SiaMuxDir)
	persistPath := filepath.Join(siaDataDir, HostDir, HostSettingsFile)
	os.Remove(persistPath)

	// create a new siamux, seeing as there won't be a host persistence file, it
	// will act as if this is a fresh new node and create a new key pair
	mux, err := NewSiaMux(siaMuxDir, siaDataDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	expectedPK := mux.PublicKey()
	expectedSK := mux.PrivateKey()
	mux.Close()

	// re-open the mux and verify it uses the same keys
	mux, err = NewSiaMux(siaMuxDir, siaDataDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	actualPK := mux.PublicKey()
	actualSK := mux.PrivateKey()
	if !bytes.Equal(actualPK[:], expectedPK[:]) {
		t.Log(actualPK)
		t.Log(expectedPK)
		t.Fatal("SiaMux's public key was different after reloading the mux")
	}
	if !bytes.Equal(actualSK[:], expectedSK[:]) {
		t.Log(actualSK)
		t.Log(expectedSK)
		t.Fatal("SiaMux's private key was different after reloading the mux")
	}
	mux.Close()

	// prepare a host's persistence file with v1.4.2 and verify the mux is now
	// initialised using the host's key pair

	// create the host directory if it doesn't exist.
	err = os.MkdirAll(filepath.Join(siaDataDir, HostDir), 0700)
	if err != nil {
		t.Fatal(err)
	}

	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	persistence := struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		SecretKey crypto.SecretKey   `json:"secretkey"`
	}{
		PublicKey: spk,
		SecretKey: sk,
	}
	err = persist.SaveJSON(Hostv120PersistMetadata, persistence, persistPath)
	if err != nil {
		t.Fatal(err)
	}

	// create a new siamux
	mux, err = NewSiaMux(siaMuxDir, siaDataDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	actualPK = mux.PublicKey()
	actualSK = mux.PrivateKey()
	if !bytes.Equal(actualPK[:], spk.Key) {
		t.Log(actualPK)
		t.Log(spk.Key)
		t.Fatal("SiaMux's public key was not equal to the host's pubkey")
	}
	if !bytes.Equal(actualSK[:], sk[:]) {
		t.Log(mux.PrivateKey())
		t.Log(spk.Key)
		t.Fatal("SiaMux's public key was not equal to the host's pubkey")
	}
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

	var pr PaymentRequest
	var wg sync.WaitGroup
	wg.Add(1)
	func() {
		defer wg.Done()
		req := PaymentRequest{Type: PayByContract}
		err := RPCWrite(renter, req)
		if err != nil {
			t.Fatal(err)
		}
	}()

	wg.Add(1)
	func() {
		defer wg.Done()
		err := RPCRead(host, &pr)
		if err != nil {
			t.Fatal(err)
		}
	}()
	wg.Wait()

	if pr.Type != PayByContract {
		t.Fatal("Unexpected request received")
	}
}
