package proto

import (
	"os"
	"path/filepath"
	"testing"
)

var (
	v1412ContractLocation = filepath.Join("testdata", "v1412.contract")
)

// TestLoadV1412Contract will test that loading a v1412 legacy contract is
// successful.
func TestLoadV1412Contract(t *testing.T) {
	f, err := os.Open(v1412ContractLocation)
	if err != nil {
		t.Fatal(err)
	}
	stat, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	decodeMaxSize := int(stat.Size() * 3)

	_, err = loadSafeContractHeader(f, decodeMaxSize)
	if err != nil {
		t.Fatal(err)
	}
}
