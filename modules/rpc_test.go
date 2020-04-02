package modules

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/siamux"
)

// TODO (PJ): switch interface to io.Writer and get rid of the teststream in
// this file

// TestRPCReadWriteError verifies the functionality of RPCRead, RPCWrite and
// RPCWriteError
func TestRPCReadWriteError(t *testing.T) {
	t.Parallel()

	client, server := NewTestStreams()

	expectedErr := errors.New("some error")
	expectedData := []byte("some data")

	var errResp error
	var dataResp []byte

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		err := RPCWriteError(server, expectedErr)
		if err != nil {
			t.Error(err)
		}
		err = RPCWrite(server, expectedData)
		if err != nil {
			t.Error(err)
		}
		wg.Done()
	}()
	wg.Add(1)
	go func() {
		errResp = RPCRead(client, &struct{}{})
		err := RPCRead(client, &dataResp)
		if err != nil {
			t.Error(err)
		}
		wg.Done()
	}()
	wg.Wait()

	if errResp.Error() != expectedErr.Error() {
		t.Fatalf("Expected '%v' but received '%v'", expectedErr, errResp)
	}
	if !bytes.Equal(dataResp, expectedData) {
		t.Fatalf("Expected data to be '%v' but received '%v'", expectedData, dataResp)
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
