package renter

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestInitialScanComplete tests if the initialScanComplete field is set
// correctly.
func TestInitialScanComplete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Get a directory for testing.
	testDir := renterTestDir(t.Name())

	// Create a group. The renter should block the scanning thread using a
	// dependency.
	deps := &dependencyBlockScan{}
	renterTemplate := node.Renter(filepath.Join(testDir, "renter"))
	renterTemplate.SkipSetAllowance = true
	renterTemplate.SkipHostDiscovery = true
	renterTemplate.HostDBDeps = deps

	tg, err := siatest.NewGroup(testDir, renterTemplate, node.Host(filepath.Join(testDir, "host")),
		siatest.Miner(filepath.Join(testDir, "miner")))
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		deps.Scan()
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// The renter should have 1 offline host in its database and
	// initialScanComplete should be false.
	renter := tg.Renters()[0]
	hdag, err := renter.HostDbAllGet()
	if err != nil {
		t.Fatal(err)
	}
	hdg, err := renter.HostDbGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(hdag.Hosts) != 1 {
		t.Fatalf("HostDB should have 1 host but had %v", len(hdag.Hosts))
	}
	if hdag.Hosts[0].ScanHistory.Len() > 0 {
		t.Fatalf("Host should have 0 scans but had %v", hdag.Hosts[0].ScanHistory.Len())
	}
	if hdg.InitialScanComplete {
		t.Fatal("Initial scan is complete even though it shouldn't")
	}

	deps.Scan()
	err = build.Retry(600, 100*time.Millisecond, func() error {
		hdag, err := renter.HostDbAllGet()
		if err != nil {
			t.Fatal(err)
		}
		hdg, err := renter.HostDbGet()
		if err != nil {
			t.Fatal(err)
		}
		if !hdg.InitialScanComplete {
			return fmt.Errorf("Initial scan is not complete even though it should be")
		}
		if len(hdag.Hosts) != 1 {
			return fmt.Errorf("HostDB should have 1 host but had %v", len(hdag.Hosts))
		}
		if hdag.Hosts[0].ScanHistory.Len() == 0 {
			return fmt.Errorf("Host should have >0 scans but had %v", hdag.Hosts[0].ScanHistory.Len())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPruneRedundantAddressRange checks if the contractor correctly cancels
// contracts with redundant IP ranges.
func TestPruneRedundantAddressRange(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Get the testDir for this test.
	testDir := renterTestDir(t.Name())

	// Create a group with a few hosts.
	groupParams := siatest.GroupParams{
		Hosts:  3,
		Miners: 1,
	}
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Get the ports of the hosts.
	allHosts := tg.Hosts()
	hg1, err1 := allHosts[0].HostGet()
	hg2, err2 := allHosts[1].HostGet()
	hg3, err3 := allHosts[2].HostGet()
	err = errors.Compose(err1, err2, err3)
	if err != nil {
		t.Fatal("Failed to get ports from at least one host", err)
	}
	host1Port := hg1.ExternalSettings.NetAddress.Port()
	host2Port := hg2.ExternalSettings.NetAddress.Port()
	host3Port := hg3.ExternalSettings.NetAddress.Port()

	// Reannounce the hosts with custom hostnames which match the hostnames
	// from the custom resolver method. We announce host1 first and host3 last
	// to make sure host1 is the 'oldest' and host3 the 'youngest'.
	err1 = allHosts[0].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host1.com:%s", host1Port)))
	err2 = tg.Miners()[0].MineBlock()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal("failed to announce host1")
	}
	err1 = allHosts[1].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host2.com:%s", host2Port)))
	err2 = tg.Miners()[0].MineBlock()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal("failed to announce host2")
	}
	err1 = allHosts[2].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host3.com:%s", host3Port)))
	err2 = tg.Miners()[0].MineBlock()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal("failed to announce host3")
	}

	// Mine announcements.
	if tg.Miners()[0].MineBlock() != nil {
		t.Fatal(err)
	}

	// Add a renter with a custom resolver to the group.
	renterTemplate := node.Renter(testDir + "/renter")
	renterTemplate.HostDBDeps = siatest.NewDependencyCustomResolver(func(host string) ([]net.IP, error) {
		switch host {
		case "host1.com":
			return []net.IP{{128, 0, 0, 1}}, nil
		case "host2.com":
			return []net.IP{{129, 0, 0, 1}}, nil
		case "host3.com":
			return []net.IP{{130, 0, 0, 1}}, nil
		case "host4.com":
			return []net.IP{{130, 0, 0, 2}}, nil
		case "localhost":
			return []net.IP{{127, 0, 0, 1}}, nil
		default:
			panic("shouldn't happen")
		}
	})
	renterTemplate.ContractorDeps = renterTemplate.HostDBDeps

	// Adding a custom RenewWindow will make a contract renewal during the test
	// unlikely.
	renterTemplate.Allowance = siatest.DefaultAllowance
	renterTemplate.Allowance.Period *= 2
	renterTemplate.Allowance.RenewWindow = 1
	renterTemplate.Allowance.Hosts = uint64(len(allHosts))
	_, err = tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// We expect the renter to have 3 active contracts.
	renter := tg.Renters()[0]
	contracts, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts.ActiveContracts) != len(allHosts) {
		t.Fatalf("Expected %v active contracts but got %v", len(allHosts), len(contracts.ActiveContracts))
	}

	// Check that all the hosts have been scanned.
	hdag, err := renter.HostDbAllGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range hdag.Hosts {
		if host.LastIPNetChange.IsZero() {
			t.Fatal("host's LastIPNetChange is still zero", host.NetAddress.Host())
		}
		if len(host.IPNets) == 0 {
			t.Fatal("host doesn't have any IPNets associated with it")
		}
	}

	// Reannounce host1 as host4 which creates a violation with host3 and
	// causes host4 to be the 'youngest'.
	err = allHosts[0].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host4.com:%s", host1Port)))
	if err != nil {
		t.Fatal("Failed to reannonce host 1")
	}

	// Mine the announcement.
	if err := tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal("Failed to mine block", err)
	}

	// The 'youngest' host should be host4.com.
	retry := 0
	err = build.Retry(600, 100*time.Millisecond, func() error {
		// Mine new blocks periodically.
		if retry%60 == 0 {
			if tg.Miners()[0].MineBlock() != nil {
				return err
			}
		}
		retry++
		hdag, err := renter.HostDbAllGet()
		if err != nil {
			return (err)
		}
		sort.Slice(hdag.Hosts, func(i, j int) bool {
			return hdag.Hosts[i].LastIPNetChange.Before(hdag.Hosts[j].LastIPNetChange)
		})
		if hdag.Hosts[len(hdag.Hosts)-1].NetAddress.Host() != "host4.com" {
			return fmt.Errorf("Youngest host should be host4.com but was %v", hdag.Hosts[len(hdag.Hosts)-1].NetAddress.Host())
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, false)
		t.Fatal(err)
	}

	// host4.com has the most recent change time. It should be canceled and
	// show up as inactive.
	retry = 0
	err = build.Retry(600, 100*time.Millisecond, func() error {
		// Mine new blocks periodically.
		if retry%60 == 0 {
			if tg.Miners()[0].MineBlock() != nil {
				return err
			}
		}
		retry++
		// The renter should now have 2 active contracts and 1 inactive one.
		// The inactive one should be host4 since it's the 'youngest'.
		contracts, err = renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(contracts.InactiveContracts) != 1 {
			return fmt.Errorf("Expected 1 inactive contract but got %v", len(contracts.InactiveContracts))
		}
		if len(contracts.ActiveContracts) != len(allHosts)-1 {
			return fmt.Errorf("Expected %v active contracts but got %v", len(allHosts)-1, len(contracts.ActiveContracts))
		}
		canceledHost := contracts.InactiveContracts[0].NetAddress.Host()
		if canceledHost != "host4.com" {
			return fmt.Errorf("Expected canceled contract to be host4.com but was %v", canceledHost)
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, false)
		t.Fatal(err)
	}
}

// TestSelectRandomCanceledHost makes sure that we can form a contract with a
// hostB even if it has a conflict with a hostA iff hostA is canceled.
func TestSelectRandomCanceledHost(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Get the testDir for this test.
	testDir := renterTestDir(t.Name())

	// Create a group with a single host.
	groupParams := siatest.GroupParams{
		Hosts:  1,
		Miners: 1,
	}
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Get the host's port.
	hg, err := tg.Hosts()[0].HostGet()
	if err != nil {
		t.Fatal("Failed to get port from host", err)
	}
	hostPort := hg.ExternalSettings.NetAddress.Port()

	// Reannounce the hosts with custom hostnames which match the hostnames from the custom resolver method.
	err = tg.Hosts()[0].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host1.com:%s", hostPort)))
	if err != nil {
		t.Fatal("Failed to reannounce at least one of the hosts", err)
	}

	// Mine the announcements.
	if err := tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal("Failed to mine block", err)
	}

	// Add a renter with a custom resolver to the group.
	renterTemplate := node.Renter(testDir + "/renter")
	renterTemplate.HostDBDeps = siatest.NewDependencyCustomResolver(func(host string) ([]net.IP, error) {
		switch host {
		case "host1.com":
			return []net.IP{{128, 0, 0, 1}}, nil
		case "host2.com":
			return []net.IP{{128, 1, 0, 1}}, nil
		case "localhost":
			return []net.IP{{127, 0, 0, 1}}, nil
		default:
			panic("shouldn't happen")
		}
	})
	renterTemplate.ContractorDeps = renterTemplate.HostDBDeps

	// Create renter.
	_, err = tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// We expect the renter to have 1 active contract.
	renter := tg.Renters()[0]
	contracts, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts.ActiveContracts) != 1 {
		t.Fatalf("Expected 1 active contract but got %v", len(contracts.Contracts))
	}

	// Cancel the active contract.
	err = renter.RenterContractCancelPost(contracts.ActiveContracts[0].ID)
	if err != nil {
		t.Fatal("Failed to cancel contract", err)
	}

	// We expect the renter to have 1 inactive contract.
	contracts, err = renter.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts.InactiveContracts) != 1 {
		t.Fatalf("Expected 1 inactive contract but got %v", len(contracts.InactiveContracts))
	}

	// Create a new host which doesn't announce itself right away.
	newHostTemplate := node.Host(testDir + "/host")
	newHostTemplate.SkipHostAnnouncement = true
	newHost, err := tg.AddNodes(newHostTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// Announce the new host as host2.com. That should cause a conflict between
	// the hosts. That shouldn't be an issue though since one of the hosts has
	// a canceled contract.
	hg, err = newHost[0].HostGet()
	if err != nil {
		t.Fatal("Failed to get port from host", err)
	}
	hostPort = hg.ExternalSettings.NetAddress.Port()
	err1 := newHost[0].HostModifySettingPost(client.HostParamAcceptingContracts, true)
	err2 := newHost[0].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host2.com:%s", hostPort)))
	err = errors.Compose(err1, err2)
	if err != nil {
		t.Fatal("Failed to announce the new host", err)
	}

	// Mine the announcement.
	if err := tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal("Failed to mine block", err)
	}

	// The renter should have an active contract with the new host and an
	// inactive contract with the old host now.
	numRetries := 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			err := tg.Miners()[0].MineBlock()
			if err != nil {
				return err
			}
		}
		numRetries++
		// Get the active and inactive contracts.
		contracts, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Should have 1 active contract and 1 inactive contract.
		if len(contracts.ActiveContracts) != 1 || len(contracts.InactiveContracts) != 1 {
			return fmt.Errorf("Expected 1 active contract and 1 inactive contract. (%v/%v)",
				len(contracts.ActiveContracts), len(contracts.InactiveContracts))
		}
		// The active contract should be with host2.
		if contracts.ActiveContracts[0].NetAddress.Host() != "host2.com" {
			return fmt.Errorf("active contract should be with host2.com")
		}
		// The inactive contract should be with host1.
		if contracts.InactiveContracts[0].NetAddress.Host() != "host1.com" {
			return fmt.Errorf("active contract should be with host1.com")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDisableIPViolationCheck checks if disabling the ip violation check
// allows for forming multiple contracts with the same ip.
func TestDisableIPViolationCheck(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Get the testDir for this test.
	testDir := renterTestDir(t.Name())

	// Create a group with a few hosts.
	groupParams := siatest.GroupParams{
		Hosts:  3,
		Miners: 1,
	}
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Get the ports of the hosts.
	allHosts := tg.Hosts()
	hg1, err1 := allHosts[0].HostGet()
	hg2, err2 := allHosts[1].HostGet()
	hg3, err3 := allHosts[2].HostGet()
	err = errors.Compose(err1, err2, err3)
	if err != nil {
		t.Fatal("Failed to get ports from at least one host", err)
	}
	host1Port := hg1.ExternalSettings.NetAddress.Port()
	host2Port := hg2.ExternalSettings.NetAddress.Port()
	host3Port := hg3.ExternalSettings.NetAddress.Port()

	// Reannounce the hosts with custom hostnames which match the hostnames
	// from the custom resolver method. We announce host1 first and host3 last
	// to make sure host1 is the 'oldest' and host3 the 'youngest'.
	err1 = allHosts[0].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host1.com:%s", host1Port)))
	err2 = tg.Miners()[0].MineBlock()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal("failed to announce host1")
	}
	err1 = allHosts[1].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host2.com:%s", host2Port)))
	err2 = tg.Miners()[0].MineBlock()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal("failed to announce host2")
	}
	err1 = allHosts[2].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host3.com:%s", host3Port)))
	err2 = tg.Miners()[0].MineBlock()
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal("failed to announce host3")
	}

	// Add a renter with a custom resolver to the group.
	renterTemplate := node.Renter(testDir + "/renter")
	renterTemplate.HostDBDeps = siatest.NewDependencyCustomResolver(func(host string) ([]net.IP, error) {
		switch host {
		case "host1.com":
			return []net.IP{{128, 0, 0, 1}}, nil
		case "host2.com":
			return []net.IP{{129, 0, 0, 1}}, nil
		case "host3.com":
			return []net.IP{{130, 0, 0, 1}}, nil
		case "host4.com":
			return []net.IP{{130, 0, 0, 2}}, nil
		case "localhost":
			return []net.IP{{127, 0, 0, 1}}, nil
		default:
			panic("shouldn't happen")
		}
	})
	renterTemplate.ContractorDeps = renterTemplate.HostDBDeps

	// Adding a custom RenewWindow will make a contract renewal during the test
	// unlikely.
	renterTemplate.Allowance = siatest.DefaultAllowance
	renterTemplate.Allowance.Period *= 2
	renterTemplate.Allowance.RenewWindow = 1
	renterTemplate.Allowance.Hosts = uint64(len(allHosts))
	_, err = tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}

	// We expect the renter to have 3 active contracts.
	renter := tg.Renters()[0]
	contracts, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(contracts.ActiveContracts) != len(allHosts) {
		t.Fatalf("Expected %v active contracts but got %v", len(allHosts), len(contracts.ActiveContracts))
	}

	// Disable the ip violation check.
	if err := renter.RenterSetCheckIPViolationPost(false); err != nil {
		t.Fatal("Failed to disable IP violation check", err)
	}

	// Reannounce host1 as host4 which creates a violation with host3 and
	// causes host1 to be the 'youngest'.
	err = allHosts[0].HostAnnounceAddrPost(modules.NetAddress(fmt.Sprintf("host4.com:%s", host1Port)))
	if err != nil {
		t.Fatal("Failed to reannonce host 1")
	}

	// Mine the announcement.
	if err := tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal("Failed to mine block", err)
	}

	// Check that all the hosts have been scanned.
	hdag, err := renter.HostDbAllGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range hdag.Hosts {
		if host.LastIPNetChange.IsZero() {
			t.Fatal("host's LastIPNetChange is still zero", host.NetAddress.Host())
		}
		if len(host.IPNets) == 0 {
			t.Fatal("host doesn't have any IPNets associated with it")
		}
	}

	retry := 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Mine new blocks periodically.
		if retry%25 == 0 {
			if tg.Miners()[0].MineBlock() != nil {
				return err
			}
		}
		retry++
		// The renter should now have one contract with every host since we
		// disabled the ip violation check.
		contracts, err = renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		if len(contracts.ActiveContracts) != len(allHosts) {
			return fmt.Errorf("Expected %v active contracts but got %v", len(allHosts), len(contracts.ActiveContracts))
		}
		if len(contracts.InactiveContracts) != 0 {
			return fmt.Errorf("Expected 0 inactive contracts but got %v", len(contracts.InactiveContracts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestListMode tests enabling and disabling the blacklist and whitelist modes
func TestListMode(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:  10,
		Miners: 1,
	}

	tg, err := siatest.NewGroupFromTemplate(renterTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal(errors.AddContext(err, "failed to create group"))
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Set allowance of 2 with 10 total hosts, this will allow a blacklist of 2,
	// a whitelist of 2, and a number of extra hosts to potentially cancel
	// contracts with
	renterParams := node.Renter(filepath.Join(siatest.TestDir(t.Name()), "renter"))
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Hosts = uint64(2)
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// Wait for contracts to form
	allowHosts := int(renterParams.Allowance.Hosts)
	err = build.Retry(50, 100*time.Millisecond, func() error {
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != allowHosts {
			return fmt.Errorf("Contracts did not form as expected, have %v expected %v", len(rc.ActiveContracts), allowHosts)
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}

	// Get hosts that currently have contracts to create blacklist, this will
	// ensure that the functionality of cancelling contracts with blacklisted
	// hosts is tested
	rc, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	var blacklistHosts []types.SiaPublicKey
	for _, c := range rc.ActiveContracts {
		blacklistHosts = append(blacklistHosts, c.HostPublicKey)
	}

	// enable blacklist mode
	if err = renter.HostDbListmodePost(blacklistHosts, "blacklist"); err != nil {
		t.Fatal(err)
	}

	// confirm blacklisted host contracts are dropped and replaced with new contracts
	blacklistHostsMap := make(map[string]struct{})
	for _, pk := range blacklistHosts {
		blacklistHostsMap[pk.String()] = struct{}{}
	}
	loop := 0
	m := tg.Miners()[0]
	err = build.Retry(50, 100*time.Millisecond, func() error {
		loop++
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != allowHosts {
			return fmt.Errorf("Contracts did not form as expected, have %v expected %v", len(rc.ActiveContracts), allowHosts)
		}
		for _, c := range rc.ActiveContracts {
			if _, exists := blacklistHostsMap[c.HostPublicKey.String()]; exists {
				return errors.New("blacklisted host found in active contracts")
			}
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}

	// Confirm that only non blacklisted hosts are returned from HostDbActiveGet
	hdbActive, err := renter.HostDbActiveGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(hdbActive.Hosts) != len(tg.Hosts())-len(blacklistHosts) {
		t.Fatalf("Number of active hosts doesn't equal number of non list hosts: got %v expected %v", len(hdbActive.Hosts), len(tg.Hosts())-len(blacklistHosts))
	}
	for _, h := range hdbActive.Hosts {
		if _, ok := blacklistHostsMap[h.PublicKeyString]; ok {
			t.Fatal("Blacklisted host returned as active host")
		}
	}

	// Confirm that HostDbAllGet is not filtered
	hdbAll, err := renter.HostDbAllGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(hdbAll.Hosts) != len(tg.Hosts()) {
		t.Fatalf("Number of all hosts doesn't equal number of test group hosts: got %v expected %v", len(hdbAll.Hosts), len(tg.Hosts()))
	}

	// Disable blacklist
	var nullList []types.SiaPublicKey
	if err = renter.HostDbListmodePost(nullList, "disable"); err != nil {
		t.Fatal(err)
	}

	// Confirm that contracts will form with blacklisted hosts again but
	// canceling contracts until a contract is formed with a blacklisted host.
	err = build.Retry(50, 100*time.Millisecond, func() error {
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) == 0 {
			return errors.New("no contracts")
		}
		for _, c := range rc.ActiveContracts {
			if _, exists := blacklistHostsMap[c.HostPublicKey.String()]; exists {
				return nil
			}
			if err = renter.RenterContractCancelPost(c.ID); err != nil {
				return err
			}
		}
		return errors.New("no contracts reformed with blacklist hosts")
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}

	// create whitelist and map of whitelisted hosts, confirm whitelisted hosts
	// are hosts that have not been used yet to avoid canceled contracts
	rc, err = renter.RenterInactiveContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	contractHosts := make(map[string]struct{})
	for _, c := range rc.ActiveContracts {
		if _, ok := contractHosts[c.HostPublicKey.String()]; ok {
			continue
		}
		contractHosts[c.HostPublicKey.String()] = struct{}{}
	}
	for _, c := range rc.InactiveContracts {
		if _, ok := contractHosts[c.HostPublicKey.String()]; ok {
			continue
		}
		contractHosts[c.HostPublicKey.String()] = struct{}{}
	}
	var whitelistHosts []types.SiaPublicKey
	whitelistHostsMap := make(map[string]struct{})
	for _, h := range tg.Hosts() {
		pk, err := h.HostPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		// Make sure host wasn't previously a blacklisted host
		if _, ok := blacklistHostsMap[pk.String()]; ok {
			continue
		}
		// Make sure the host hasn't already had a contract
		if _, ok := contractHosts[pk.String()]; ok {
			continue
		}
		// Check to see if there are enough white listed hosts
		if len(whitelistHosts) >= len(blacklistHosts) {
			break
		}
		whitelistHosts = append(whitelistHosts, pk)
		whitelistHostsMap[pk.String()] = struct{}{}
	}

	// Enable white list
	if err = renter.HostDbListmodePost(whitelistHosts, "whitelist"); err != nil {
		t.Fatal(err)
	}

	// confirm contracts are only formed with hosts on the white list, since it
	// was just confirmed that there are contracts with at least one blacklisted
	// host it will ensure that the canceling of non whitelisted hosts is tested
	loop = 0
	err = build.Retry(50, 100*time.Millisecond, func() error {
		loop++
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is forming contracts
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(whitelistHosts) {
			return fmt.Errorf("not the correct number of contracts: have %v expected %v", len(rc.ActiveContracts), len(whitelistHosts))
		}
		for _, c := range rc.ActiveContracts {
			if _, exists := whitelistHostsMap[c.HostPublicKey.String()]; !exists {
				return errors.New("non whitelisted host found in active contracts")
			}
		}
		return nil
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}

	// Confirm that only whitelisted hosts are returned from HostDbActiveGet
	hdbActive, err = renter.HostDbActiveGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(hdbActive.Hosts) != len(whitelistHostsMap) {
		t.Fatalf("Number of active hosts doesn't equal number of whitelisted hosts: got %v expected %v", len(hdbActive.Hosts), len(whitelistHostsMap))
	}
	for _, h := range hdbActive.Hosts {
		if _, ok := whitelistHostsMap[h.PublicKeyString]; !ok {
			t.Fatal("Whitelisted host not returned as active host")
		}
	}

	// Confirm that HostDbAllGet is not filtered
	hdbAll, err = renter.HostDbAllGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(hdbAll.Hosts) != len(tg.Hosts()) {
		t.Fatalf("Number of all hosts doesn't equal number of test group hosts: got %v expected %v", len(hdbAll.Hosts), len(tg.Hosts()))
	}

	// Disable whitelist
	if err = renter.HostDbListmodePost(nullList, "disable"); err != nil {
		t.Fatal(err)
	}

	// Confirm that contracts will form with non whitelisted hosts again
	loop = 0
	err = build.Retry(50, 100*time.Millisecond, func() error {
		loop++
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is forming contracts
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) == 0 {
			return errors.New("no contracts")
		}
		for _, c := range rc.ActiveContracts {
			if _, exists := whitelistHostsMap[c.HostPublicKey.String()]; !exists {
				return nil
			}
			if err = renter.RenterContractCancelPost(c.ID); err != nil {
				return err
			}
		}
		return errors.New("no contracts reformed with non whitelisted hosts")
	})
	if err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
}
