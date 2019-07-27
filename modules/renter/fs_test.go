package renter

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/consensus"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/modules/host"
	"gitlab.com/NebulousLabs/Sia/modules/miner"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/Sia/modules/wallet"
	"gitlab.com/NebulousLabs/Sia/types"
)

// newTestingWallet is a helper function that creates a ready-to-use wallet
// and mines some coins into it.
func newTestingWallet(testdir string, cs modules.ConsensusSet, tp modules.TransactionPool) (modules.Wallet, error) {
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, err
	}
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	encrypted, err := w.Encrypted()
	if err != nil {
		return nil, err
	}
	if !encrypted {
		_, err = w.Encrypt(key)
		if err != nil {
			return nil, err
		}
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, err
	}
	// give it some money
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, err
	}
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		_, err := m.AddBlock()
		if err != nil {
			return nil, err
		}
	}
	return w, nil
}

// newTestingHost is a helper function that creates a ready-to-use host.
func newTestingHost(testdir string, cs modules.ConsensusSet, tp modules.TransactionPool) (modules.Host, error) {
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	w, err := newTestingWallet(testdir, cs, tp)
	if err != nil {
		return nil, err
	}
	h, err := host.New(cs, g, tp, w, "localhost:0", filepath.Join(testdir, modules.HostDir))
	if err != nil {
		return nil, err
	}

	// configure host to accept contracts
	settings := h.InternalSettings()
	settings.AcceptingContracts = true
	err = h.SetInternalSettings(settings)
	if err != nil {
		return nil, err
	}

	// add storage to host
	storageFolder := filepath.Join(testdir, "storage")
	err = os.MkdirAll(storageFolder, 0700)
	if err != nil {
		return nil, err
	}
	err = h.AddStorageFolder(storageFolder, modules.SectorSize*64)
	if err != nil {
		return nil, err
	}

	return h, nil
}

// newTestingRenter is a helper function that creates a ready-to-use
// renter.
func newTestingRenter(testdir string, g modules.Gateway, cs modules.ConsensusSet, tp modules.TransactionPool) (*Renter, error) {
	w, err := newTestingWallet(testdir, cs, tp)
	if err != nil {
		return nil, err
	}
	return New(g, cs, w, tp, filepath.Join(testdir, "renter"))
}

// newTestingTrio creates a Renter, Host, and TestMiner that can be used
// for testing renter/host interactions.
func newTestingTrio(t *testing.T, name string) (*Renter, modules.Host, modules.TestMiner) {
	t.Helper()
	testdir := build.TempDir("renter", name)

	// create miner
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		t.Fatal(err)
	}
	cs, err := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err != nil {
		t.Fatal(err)
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		t.Fatal(err)
	}
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		t.Fatal(err)
	}
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	encrypted, err := w.Encrypted()
	if err != nil {
		t.Fatal(err)
	}
	if !encrypted {
		_, err = w.Encrypt(key)
		if err != nil {
			t.Fatal(err)
		}
	}
	err = w.Unlock(key)
	if err != nil {
		t.Fatal(err)
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, "miner"))
	if err != nil {
		t.Fatal("error creating testing miner", err)
	}

	// create renter and host, using same consensus set and gateway
	r, err := newTestingRenter(filepath.Join(testdir, "renter"), g, cs, tp)
	if err != nil {
		t.Fatal("error creating testing renter", err)
	}
	h, err := newTestingHost(filepath.Join(testdir, "host"), cs, tp)
	if err != nil {
		t.Fatal("error creating testing host", err)
	}

	// announce the host
	err = h.Announce()
	if err != nil {
		t.Fatal("error announcing host", err)
	}

	// make another host
	h2, err := newTestingHost(filepath.Join(testdir, "host2"), cs, tp)
	if err != nil {
		t.Fatal("error creating testing host", err)
	}
	err = h2.Announce()
	if err != nil {
		t.Fatal("error announcing host", err)
	}

	// mine a block, processing the announcement
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// wait for hostdb to scan host
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(r.hostDB.ActiveHosts()) == 0 {
			return errors.New("host did not make it into the contractor hostdb in time")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// set allowance
	a := modules.DefaultAllowance
	a.Hosts = 2
	err = r.SetSettings(modules.RenterSettings{
		Allowance: a,
	})
	if err != nil {
		t.Fatal(err)
	}

	return r, h, m
}

func TestFS(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	r, _, _ := newTestingTrio(t, t.Name())

	sourceData := []byte("Hello, FUSE!\n")
	ioutil.WriteFile("/tmp/foo", sourceData, 0666)
	sp, _ := modules.NewSiaPath("bar")
	rs, err := siafile.NewRSSubCode(1, 1, crypto.SegmentSize)
	if err != nil {
		t.Fatal(err)
	}
	err = r.Upload(modules.FileUploadParams{
		Source:      "/tmp/foo",
		SiaPath:     sp,
		ErasureCode: rs,
	})
	if err != nil {
		t.Fatal(err)
	}
	// wait for file to finish uploading
	if err := build.Retry(10, time.Second, func() error {
		fi, err := r.File(sp)
		if err != nil {
			return err
		} else if fi.UploadProgress != 100 {
			return errors.New("upload progress not 100")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	root, _ := modules.NewSiaPath(modules.SiapathRoot)
	fs, err := r.FileSystem(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	stat, err := fs.Stat("bar")
	if err != nil {
		t.Fatal(err)
	} else if stat.Size() != int64(len(sourceData)) {
		t.Fatal("incorrect stat.Size")
	}

	f, err := fs.OpenFile("bar", os.O_RDONLY, 0666)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	b := make([]byte, stat.Size())
	n, err := f.ReadAt(b, 0)
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(b[:n], sourceData) {
		t.Fatal("ReadAt data does not match source")
	}
}
