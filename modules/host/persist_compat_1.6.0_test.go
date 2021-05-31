package host

import (
	"fmt"
	"path/filepath"
	"testing"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
)

const (
	// v143Host is the name of the file that contains the legacy host
	// persistence directory testdata.
	v151Host = "v151Host.tar.gz"
)

// TestV151HostUpgrade tests upgrading a v151 host to v160.
func TestV151HostUpgrade(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a tmp dir
	dst := build.TempDir(t.Name())
	persistDir := filepath.Join(dst, modules.HostDir)
	hostDir := filepath.Join(persistDir, modules.HostDir)

	// extract the legacy host data
	source := filepath.Join("testdata", v151Host)
	err := build.ExtractTarGz(source, dst)
	if err != nil {
		t.Fatal(err)
	}
	println("dst", dst)

	// load a new host
	siaMuxDir := filepath.Join(persistDir, modules.SiaMuxDir)
	closefn, host, err := loadExistingHostWithNewDeps(persistDir, siaMuxDir, hostDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closefn(); err != nil {
			t.Fatal(err)
		}
	}()

	pt := host.PriceTable()
	fmt.Println("pt", pt.RegistryEntriesTotal, pt.RegistryEntriesLeft)
}
