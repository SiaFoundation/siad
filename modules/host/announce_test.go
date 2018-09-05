package host

import (
	"bytes"
	"net"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// announcementFinder is a quick module that parses the blockchain for host
// announcements, keeping a record of all the announcements that get found.
type announcementFinder struct {
	cs modules.ConsensusSet

	// Announcements that have been seen. The two slices are wedded.
	netAddresses []modules.NetAddress
	publicKeys   []types.SiaPublicKey
}

// ProcessConsensusChange receives consensus changes from the consensus set and
// parses them for valid host announcements.
func (af *announcementFinder) ProcessConsensusChange(cc modules.ConsensusChange) {
	for _, block := range cc.AppliedBlocks {
		for _, txn := range block.Transactions {
			for _, arb := range txn.ArbitraryData {
				addr, pubKey, err := modules.DecodeAnnouncement(arb)
				if err == nil {
					af.netAddresses = append(af.netAddresses, addr)
					af.publicKeys = append(af.publicKeys, pubKey)
				}
			}
		}
	}
}

// Close will shut down the announcement finder.
func (af *announcementFinder) Close() error {
	af.cs.Unsubscribe(af)
	return nil
}

// newAnnouncementFinder will create and return an announcement finder.
func newAnnouncementFinder(cs modules.ConsensusSet) (*announcementFinder, error) {
	af := &announcementFinder{
		cs: cs,
	}
	err := cs.ConsensusSetSubscribe(af, modules.ConsensusChangeBeginning, nil)
	if err != nil {
		return nil, err
	}
	return af, nil
}

// TestHostAnnounce checks that the host announce function is operating
// correctly.
func TestHostAnnounce(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester("TestHostAnnounce")
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// Create an announcement finder to scan the blockchain for host
	// announcements.
	af, err := newAnnouncementFinder(ht.cs)
	if err != nil {
		t.Fatal(err)
	}
	defer af.Close()

	// Create an announcement, then use the address finding module to scan the
	// blockchain for the host's address.
	err = ht.host.Announce()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ht.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(af.publicKeys) != 1 {
		t.Fatal("could not find host announcement in blockchain")
	}
	if af.netAddresses[0] != ht.host.autoAddress {
		t.Error("announcement has wrong address")
	}
	if !bytes.Equal(af.publicKeys[0].Key, ht.host.publicKey.Key) {
		t.Error("announcement has wrong host key")
	}
}

// TestHostAnnounceAddress checks that the host announce address function is
// operating correctly.
func TestHostAnnounceAddress(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester("TestHostAnnounceAddress")
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// Create an announcement finder to scan the blockchain for host
	// announcements.
	af, err := newAnnouncementFinder(ht.cs)
	if err != nil {
		t.Fatal(err)
	}
	defer af.Close()

	// Create an announcement, then use the address finding module to scan the
	// blockchain for the host's address.
	addr := modules.NetAddress("foo.com:1234")
	err = ht.host.AnnounceAddress(addr)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ht.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(af.netAddresses) != 1 {
		t.Fatal("could not find host announcement in blockchain")
	}
	if af.netAddresses[0] != addr {
		t.Error("announcement has wrong address")
	}
	if !bytes.Equal(af.publicKeys[0].Key, ht.host.publicKey.Key) {
		t.Error("announcement has wrong host key")
	}
}

// TestHostAnnounceCheckUnlockHash verifies that the host's unlock hash is
// checked when an announcement is performed.
func TestHostAnnounceCheckUnlockHash(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	ht.host.mu.RLock()
	oldUnlockHash := ht.host.unlockHash
	ht.host.mu.RUnlock()

	err = ht.wallet.Reset()
	if err != nil {
		t.Fatal(err)
	}
	err = ht.initWallet()
	if err != nil {
		t.Fatal(err)
	}
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		_, err = ht.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	err = ht.host.Announce()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ht.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}
	ht.host.mu.RLock()
	newUnlockHash := ht.host.unlockHash
	ht.host.mu.RUnlock()
	if newUnlockHash == oldUnlockHash {
		t.Fatal("host did not set a new unlock hash after announce with reset wallet")
	}
	hasAddr := false
	addrs, err := ht.wallet.AllAddresses()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		if addr == newUnlockHash {
			hasAddr = true
			break
		}
	}
	if !hasAddr {
		t.Fatal("host unlock has did not exist in wallet")
	}
}

// TestHostVerifyAnnouncementAddress tests various edge cases of the
// staticVerifyAnnouncementAddress function.
func TestHostVerifyAnnouncementAddress(t *testing.T) {
	ipv4loopback := net.IPv4(127, 0, 0, 1)
	if testing.Short() {
		t.SkipNow()
	}
	// Create custom dependency to test edge cases for various hostnames.
	deps := NewDependencyCustomLookupIP(func(host string) ([]net.IP, error) {
		switch host {
		case "TwoIPv4Loopback.com":
			return []net.IP{ipv4loopback, ipv4loopback}, nil
		case "TwoIPv6Loopback.com":
			return []net.IP{net.IPv6loopback, net.IPv6loopback}, nil
		case "DifferentLoopback.com":
			return []net.IP{ipv4loopback, net.IPv6loopback}, nil
		case "MoreThanTwo.com":
			return []net.IP{{}, {}, {}}, nil
		case "LessThanOne.com":
			return []net.IP{}, nil
		case "OneValidIP.com":
			return []net.IP{{1, 2, 3, 4}}, nil
		case "TwoValidIP.com":
			return []net.IP{{1, 2, 3, 4}, {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}}, nil
		case "TwoIPSameType.com":
			return []net.IP{{1, 2, 3, 4}, {4, 3, 2, 1}}, nil
		default:
			t.Fatal("shouldn't happen")
		}
		return nil, nil
	})
	// Create host from dependency.
	ht, err := newMockHostTester(deps, t.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Create host addresses.
	host1 := modules.NetAddress("TwoIPv4Loopback.com:1234")
	host2 := modules.NetAddress("TwoIPv6Loopback.com:1234")
	host3 := modules.NetAddress("DifferentLoopback.com:1234")
	host4 := modules.NetAddress("MoreThanTwo.com:1234")
	host5 := modules.NetAddress("LessThanOne.com:1234")
	host6 := modules.NetAddress("OneValidIP.com:1234")
	host7 := modules.NetAddress("TwoValidIP.com:1234")
	host8 := modules.NetAddress("TwoIPSameType.com:1234")

	// Test individual hosts.
	if err := ht.host.staticVerifyAnnouncementAddress(host1); err == nil {
		t.Error("Announcing host1 should have failed but didn't")
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host2); err == nil {
		t.Error("Announcing host2 should have failed but didn't")
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host3); err != nil {
		t.Error("Announcing host3 shouldn't have failed but did", err)
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host4); err == nil {
		t.Error("Announcing host4 should have failed but didn't")
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host5); err == nil {
		t.Error("Announcing host5 should have failed but didn't")
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host6); err != nil {
		t.Error("Announcing host6 shouldn't have failed but did", err)
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host7); err != nil {
		t.Error("Announcing host7 shouldn't have failed but did", err)
	}
	if err := ht.host.staticVerifyAnnouncementAddress(host8); err == nil {
		t.Error("Announcing host8 should have failed but didn't")
	}
}
