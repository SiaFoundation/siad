package modules

import (
	"bytes"
	"errors"
	"testing"
)

// TestRPCReadWriteError verifies the functionality of RPCRead, RPCWrite and
// RPCWriteError
func TestRPCReadWriteError(t *testing.T) {
	t.Parallel()

	// use a buffer as stream to avoid unnecessary setup
	stream := new(bytes.Buffer)

	expectedData := []byte("some data")
	err := RPCWrite(stream, expectedData)
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	err = RPCRead(stream, &data)
	if err != nil {
		t.Error(err)
	}
	if !bytes.Equal(data, expectedData) {
		t.Fatalf("Expected data to be '%v' but received '%v'", expectedData, data)
	}

	expectedErr := errors.New("some error")
	err = RPCWriteError(stream, expectedErr)
	if err != nil {
		t.Fatal(err)
	}
	resp := RPCRead(stream, &struct{}{})
	if resp.Error() != expectedErr.Error() {
		t.Fatalf("Expected '%v' but received '%v'", expectedErr, resp)
	}
}
