package renterhost

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
)

type stubHostDB struct{}

func (stubHostDB) IncrementSuccessfulInteractions(types.SiaPublicKey) {}
func (stubHostDB) IncrementFailedInteractions(types.SiaPublicKey)     {}

// TestSession tests the new RPC loop by creating a host and requesting new
// RPCs via the proto.Session type.
func TestSession(t *testing.T) {
	gp := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(renterHostTestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	// manually grab a renter contract
	renter := tg.Renters()[0]
	cs, err := proto.NewContractSet(filepath.Join(renter.Dir, "renter", "contracts"), new(modules.ProductionDependencies))
	if err != nil {
		t.Fatal(err)
	}
	contract := cs.ViewAll()[0]

	hhg, err := renter.HostDbHostsGet(contract.HostPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := renter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}

	// begin the RPC session
	s, err := cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// upload a sector
	sector := fastrand.Bytes(int(modules.SectorSize))
	_, root, err := s.Append(sector)
	if err != nil {
		t.Fatal(err)
	}
	// upload another sector, to test Merkle proofs
	_, _, err = s.Append(sector)
	if err != nil {
		t.Fatal(err)
	}

	// download the sector
	_, dsector, err := s.ReadSection(root, 0, uint32(len(sector)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dsector, sector) {
		t.Fatal("downloaded sector does not match")
	}

	// download less than a full sector
	_, partialSector, err := s.ReadSection(root, crypto.SegmentSize*5, crypto.SegmentSize*12)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(partialSector, sector[crypto.SegmentSize*5:crypto.SegmentSize*17]) {
		t.Fatal("downloaded sector does not match")
	}

	// download the sector root
	_, droots, err := s.SectorRoots(modules.LoopSectorRootsRequest{
		RootOffset: 0,
		NumRoots:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if droots[0] != root {
		t.Fatal("downloaded sector root does not match")
	}

	// perform a more complex modification: append+swap+trim
	sector2 := fastrand.Bytes(int(modules.SectorSize))
	_, err = s.Write([]modules.LoopWriteAction{
		{Type: modules.WriteActionAppend, Data: sector2},
		{Type: modules.WriteActionSwap, A: 0, B: 2},
		{Type: modules.WriteActionTrim, A: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	// check that the write was applied correctly
	_, droots, err = s.SectorRoots(modules.LoopSectorRootsRequest{
		RootOffset: 0,
		NumRoots:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if droots[0] != crypto.MerkleRoot(sector2) {
		t.Fatal("updated sector root does not match")
	}

	// shut down and restart the host to ensure the sectors are durable
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	host := tg.Hosts()[0]
	if err := host.RestartNode(); err != nil {
		t.Fatal(err)
	}
	// restarting changes the host's address
	hg, err := host.HostGet()
	if err != nil {
		t.Fatal(err)
	}
	hhg.Entry.HostDBEntry.NetAddress = hg.ExternalSettings.NetAddress
	// initiate session
	s, err = cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, s2data, err := s.ReadSection(droots[0], 0, uint32(len(sector2)))
	if err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(s2data, sector2) {
		t.Fatal("downloaded data does not match")
	}
}

// TestHostLockTimeout tests that the host respects the requested timeout in the
// Lock RPC.
func TestHostLockTimeout(t *testing.T) {
	gp := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(renterHostTestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	// manually grab a renter contract
	renter := tg.Renters()[0]
	cs, err := proto.NewContractSet(filepath.Join(renter.Dir, "renter", "contracts"), new(modules.ProductionDependencies))
	if err != nil {
		t.Fatal(err)
	}
	contract := cs.ViewAll()[0]

	hhg, err := renter.HostDbHostsGet(contract.HostPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := renter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}

	// Begin an RPC session. This will lock the contract.
	s1, err := cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to begin a separate RPC session. This will block while waiting
	// to acquire the contract lock, and eventually fail.
	_, err = cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err == nil || !strings.Contains(err.Error(), "contract is locked by another party") {
		t.Fatal("expected contract lock error, got", err)
	}

	// Try again, but this time, unlock the contract during the timeout period.
	// The new session should successfully acquire the lock.
	go func() {
		time.Sleep(3 * time.Second)
		if err := s1.Close(); err != nil {
			panic(err) // can't call t.Fatal from goroutine
		}
	}()
	s2, err := cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

// TestHostBaseRPCPrice tests that the host rejects RPCs when its base RPC price
// is not respected.
func TestHostBaseRPCPrice(t *testing.T) {
	gp := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(renterHostTestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	// manually grab a renter contract
	renter := tg.Renters()[0]
	cs, err := proto.NewContractSet(filepath.Join(renter.Dir, "renter", "contracts"), new(modules.ProductionDependencies))
	if err != nil {
		t.Fatal(err)
	}
	contract := cs.ViewAll()[0]

	hhg, err := renter.HostDbHostsGet(contract.HostPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := renter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}

	// Begin an RPC session.
	s, err := cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Upload a sector.
	sector := fastrand.Bytes(int(modules.SectorSize))
	_, _, err = s.Append(sector)
	if err != nil {
		t.Fatal(err)
	}

	// Increase the host's base price.
	host := tg.Hosts()[0]
	err = host.HostModifySettingPost(client.HostParamMinBaseRPCPrice, types.SiacoinPrecision.Mul64(1000))
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to upload another sector.
	_, _, err = s.Append(sector)
	if err == nil || !strings.Contains(err.Error(), "rejected for high paying renter valid output") {
		t.Fatal("expected underpayment error, got", err)
	}
}

// TestMultiRead tests the Read RPC.
func TestMultiRead(t *testing.T) {
	gp := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(renterHostTestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	// manually grab a renter contract
	renter := tg.Renters()[0]
	cs, err := proto.NewContractSet(filepath.Join(renter.Dir, "renter", "contracts"), new(modules.ProductionDependencies))
	if err != nil {
		t.Fatal(err)
	}
	contract := cs.ViewAll()[0]

	hhg, err := renter.HostDbHostsGet(contract.HostPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := renter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}

	// begin the RPC session
	s, err := cs.NewSession(hhg.Entry.HostDBEntry, contract.ID, cg.Height, stubHostDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// upload a sector
	sector := fastrand.Bytes(int(modules.SectorSize))
	_, root, err := s.Append(sector)
	if err != nil {
		t.Fatal(err)
	}

	// download a single section without interrupting.
	var buf bytes.Buffer
	req := modules.LoopReadRequest{
		Sections: []modules.LoopReadRequestSection{{
			MerkleRoot: root,
			Offset:     0,
			Length:     uint32(modules.SectorSize),
		}},
		MerkleProof: true,
	}
	_, err = s.Read(&buf, req, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), sector) {
		t.Fatal("downloaded sector does not match")
	}

	// download two sections, but interrupt immediately; we should receive the
	// first section
	buf.Reset()
	req.Sections = []modules.LoopReadRequestSection{
		{MerkleRoot: root, Offset: 0, Length: uint32(modules.SectorSize)},
		{MerkleRoot: root, Offset: 0, Length: uint32(modules.SectorSize)},
	}
	cancel := make(chan struct{}, 1)
	cancel <- struct{}{}
	_, err = s.Read(&buf, req, cancel)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), sector) {
		t.Fatal("downloaded sector does not match")
	}
}
