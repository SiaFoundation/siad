package hostdb

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/siatest"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// TestSiamuxRequired checks that the hostdb will count a host as offline if the
// host is not running siamux.
func TestSiamuxRequired(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Get a directory for testing.
	testDir := hostdbTestDir(t.Name())

	// Create a group. The renter should block the scanning thread using a
	// dependency.
	deps := &dependencies.DependencyDisableHostSiamux{}
	renterTemplate := node.Renter(filepath.Join(testDir, "renter"))
	renterTemplate.SkipSetAllowance = true
	renterTemplate.SkipHostDiscovery = true
	hostTemplate := node.Host(filepath.Join(testDir, "host"))
	hostTemplate.HostDeps = deps

	tg, err := siatest.NewGroup(testDir, renterTemplate, hostTemplate, node.Miner(filepath.Join(testDir, "miner")))
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Error(err)
		}
	}()

	// The renter should have 1 offline host in its database and
	// initialScanComplete should be false.
	renter := tg.Renters()[0]
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
		if hdag.Hosts[0].ScanHistory[0].Success {
			// t.Fatal here instead of returning an error because retrying isn't
			// going to change the results.
			t.Fatal("there should not be a successful scan")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestInitialScanComplete tests if the initialScanComplete field is set
// correctly.
func TestInitialScanComplete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	t.Parallel()

	// Get a directory for testing.
	testDir := hostdbTestDir(t.Name())

	// Create a group. The renter should block the scanning thread using a
	// dependency.
	deps := &dependencies.DependencyBlockScan{}
	renterTemplate := node.Renter(filepath.Join(testDir, "renter"))
	renterTemplate.SkipSetAllowance = true
	renterTemplate.SkipHostDiscovery = true
	renterTemplate.HostDBDeps = deps

	tg, err := siatest.NewGroup(testDir, renterTemplate, node.Host(filepath.Join(testDir, "host")),
		node.Miner(filepath.Join(testDir, "miner")))
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
	testDir := hostdbTestDir(t.Name())

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
	renterTemplate.HostDBDeps = dependencies.NewDependencyCustomResolver(func(host string) ([]net.IP, error) {
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

	// Disable the IPViolationCheck to avoid race conditions during testing.
	if err := renter.RenterSetCheckIPViolationPost(false); err != nil {
		t.Fatal(err)
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
			return err
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

	// Enable the IPViolationCheck again to cancel the "youngest" host.
	if err := renter.RenterSetCheckIPViolationPost(true); err != nil {
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
	testDir := hostdbTestDir(t.Name())

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
	renterTemplate.HostDBDeps = dependencies.NewDependencyCustomResolver(func(host string) ([]net.IP, error) {
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
	renterTemplate.ContractSetDeps = renterTemplate.HostDBDeps

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
	testDir := hostdbTestDir(t.Name())

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
	renterTemplate.HostDBDeps = dependencies.NewDependencyCustomResolver(func(host string) ([]net.IP, error) {
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

// TestFilterMode tests enabling and disabling the blacklist and whitelist modes
func TestFilterMode(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:  6,
		Miners: 1,
	}
	testDir := hostdbTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(errors.AddContext(err, "failed to create group"))
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create renter. Set allowance of 2 with 6 total hosts, this will allow a
	// blacklist or whitelist of 2, and a number of extra hosts to potentially
	// cancel contracts with
	renterParams := node.Renter(testDir + "/renter")
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Hosts = 2
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	if err := testFilterModePublicKeys(tg, renter, modules.HostDBActivateBlacklist); err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
	if err := testFilterModePublicKeys(tg, renter, modules.HostDBActiveWhitelist); err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
	if err := testFilterModeNetAddresses(tg, renter, modules.HostDBActivateBlacklist); err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
	if err := testFilterModeNetAddresses(tg, renter, modules.HostDBActiveWhitelist); err != nil {
		renter.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}
}

func testFilterModePublicKeys(tg *siatest.TestGroup, renter *siatest.TestNode, fm modules.FilterMode) error {
	// Grab all host pks
	var hosts []types.SiaPublicKey
	for _, h := range tg.Hosts() {
		pk, err := h.HostPublicKey()
		if err != nil {
			return err
		}
		hosts = append(hosts, pk)
	}

	// Get Renter Settings
	rg, err := renter.RenterGet()
	if err != nil {
		return err
	}
	allowHosts := int(rg.Settings.Allowance.Hosts)

	// Confirm we are starting with expected number of contracts and active
	// hosts.
	loop := 0
	m := tg.Miners()[0]
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) < allowHosts {
			return fmt.Errorf("Contracts did not form as expected, have %v expected at least %v", len(rc.ActiveContracts), allowHosts)
		}
		hdbActive, err := renter.HostDbActiveGet()
		if err != nil {
			return err
		}

		if len(hdbActive.Hosts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v active hosts but got %v", len(tg.Hosts()), len(hdbActive.Hosts))
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Get listedHosts. If testing blacklist mode we want to grab hosts we
	// currently have contracts with. if we are in whitelist mode we want to
	// grab hosts that we don't have contracts with to ensure that contract
	// formation and replacement is properly tested
	rc, err := renter.RenterInactiveContractsGet()
	if err != nil {
		return err
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
	var filteredHosts []types.SiaPublicKey
	numHosts := 0
	isWhitelist := fm == modules.HostDBActiveWhitelist
	for _, pk := range hosts {
		_, ok := contractHosts[pk.String()]
		if isWhitelist != ok {
			filteredHosts = append(filteredHosts, pk)
			numHosts++
			if numHosts == allowHosts {
				break
			}
		}
	}

	// enable list mode
	if err = renter.HostDbFilterModePost(fm, filteredHosts, nil); err != nil {
		return err
	}

	// Confirm filter mode is set as expected
	hdfmg, err := renter.HostDbFilterModeGet()
	if err != nil {
		return err
	}
	if hdfmg.FilterMode != fm.String() {
		return fmt.Errorf("filter mode not set as expected, got %v expected %v", hdfmg.FilterMode, fm.String())
	}
	if len(hdfmg.Hosts) != len(filteredHosts) {
		return fmt.Errorf("Number of filtered hosts incorrect, got %v expected %v", len(hdfmg.Hosts), len(filteredHosts))
	}
	// Create map for comparison
	filteredHostsMap := make(map[string]struct{})
	for _, pk := range filteredHosts {
		filteredHostsMap[pk.String()] = struct{}{}
	}
	for _, host := range hdfmg.Hosts {
		if _, ok := filteredHostsMap[host]; !ok {
			return errors.New("host returned not found in filtered hosts")
		}
	}

	// Confirm hosts are marked as filtered in original hosttree by querying AllHost
	hbag, err := renter.HostDbAllGet()
	if err != nil {
		return err
	}
	for _, host := range hbag.Hosts {
		if _, ok := filteredHostsMap[host.PublicKeyString]; !ok {
			continue
		}
		if !host.Filtered {
			return errors.New("Host not marked as filtered")
		}
	}

	// confirm contracts are dropped and replaced appropriately for the FilterMode
	loop = 0
	err = build.Retry(50, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Check for correct number of contracts
		if len(rc.ActiveContracts) != allowHosts {
			return fmt.Errorf("Contracts did not form as expected, have %v expected %v", len(rc.ActiveContracts), allowHosts)
		}
		// Check that ActiveContracts are with correct hosts
		for _, c := range rc.ActiveContracts {
			_, exists := filteredHostsMap[c.HostPublicKey.String()]
			if isWhitelist != exists {
				return errors.New("non listed host found in active contracts")
			}
		}
		// Check that Inactive contracts are with hosts that weren't listed
		check := 0
		for _, c := range rc.InactiveContracts {
			if _, ok := contractHosts[c.HostPublicKey.String()]; ok {
				check++
			}
		}
		if check != len(contractHosts) {
			return fmt.Errorf("did not find all previous hosts in inactive contracts, found %v expected %v", check, len(contractHosts))
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Confirm HostDbActiveGet is filtered appropriately
	hdbActive, err := renter.HostDbActiveGet()
	if err != nil {
		return err
	}
	numExpectedHosts := len(tg.Hosts()) - len(filteredHosts)
	if isWhitelist {
		numExpectedHosts = len(filteredHosts)
	}
	if len(hdbActive.Hosts) != numExpectedHosts {
		return fmt.Errorf("Number of active hosts doesn't equal number of non list hosts: got %v expected %v", len(hdbActive.Hosts), numExpectedHosts)
	}
	for _, h := range hdbActive.Hosts {
		_, ok := filteredHostsMap[h.PublicKeyString]
		if isWhitelist != ok {
			return errors.New("Blacklisted host returned as active host")
		}
	}

	// Confirm that HostDbAllGet is not filtered
	hdbAll, err := renter.HostDbAllGet()
	if err != nil {
		return err
	}
	if len(hdbAll.Hosts) != len(tg.Hosts()) {
		return fmt.Errorf("Number of all hosts doesn't equal number of test group hosts: got %v expected %v", len(hdbAll.Hosts), len(tg.Hosts()))
	}

	// Disable FilterMode and confirm all hosts are active again
	var nullList []types.SiaPublicKey
	if err = renter.HostDbFilterModePost(modules.HostDBDisableFilter, nullList, nil); err != nil {
		return err
	}
	hdbActive, err = renter.HostDbActiveGet()
	if err != nil {
		return err
	}
	hdbag, err := renter.HostDbAllGet()
	if err != nil {
		return err
	}
	if len(hdbActive.Hosts) != len(tg.Hosts()) {
		return fmt.Errorf("Unexpected number of active hosts after disabling FilterMode: got %v expected %v (%v)", len(hdbActive.Hosts), len(tg.Hosts()), len(hdbag.Hosts))
	}

	// Confirm that contracts will form with non listed hosts again by
	// canceling contracts until a contract is formed with a nonlisted host.
	loop = 0
	err = build.Retry(50, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) == 0 {
			return errors.New("no contracts")
		}
		for _, c := range rc.ActiveContracts {
			_, exists := filteredHostsMap[c.HostPublicKey.String()]
			if isWhitelist != exists {
				return nil
			}
			if err = renter.RenterContractCancelPost(c.ID); err != nil {
				return err
			}
		}
		return errors.New("no contracts reformed with blacklist hosts")
	})
	if err != nil {
		return err
	}

	return nil
}

func testFilterModeNetAddresses(tg *siatest.TestGroup, renter *siatest.TestNode, fm modules.FilterMode) error {
	// Get Renter Settings
	rg, err := renter.RenterGet()
	if err != nil {
		return err
	}
	allowHosts := int(rg.Settings.Allowance.Hosts)

	// Confirm we are starting with expected number of contracts and active
	// hosts.
	loop := 0
	m := tg.Miners()[0]
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) < allowHosts {
			return fmt.Errorf("Contracts did not form as expected, have %v expected at least %v", len(rc.ActiveContracts), allowHosts)
		}
		hdbActive, err := renter.HostDbActiveGet()
		if err != nil {
			return err
		}

		if len(hdbActive.Hosts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v active hosts but got %v", len(tg.Hosts()), len(hdbActive.Hosts))
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Get listedHosts. If testing blacklist mode we want to grab hosts we
	// currently have contracts with. if we are in whitelist mode we want to
	// grab hosts that we don't have contracts with to ensure that contract
	// formation and replacement is properly tested
	rc, err := renter.RenterInactiveContractsGet()
	if err != nil {
		return err
	}
	contractHosts := make(map[string]struct{})
	for _, c := range rc.ActiveContracts {
		if _, ok := contractHosts[string(c.NetAddress)]; ok {
			continue
		}
		contractHosts[string(c.NetAddress)] = struct{}{}
	}
	for _, c := range rc.InactiveContracts {
		if _, ok := contractHosts[string(c.NetAddress)]; ok {
			continue
		}
		contractHosts[string(c.NetAddress)] = struct{}{}
	}
	var filteredHosts []string
	numHosts := 0
	isWhitelist := fm == modules.HostDBActiveWhitelist
	for _, host := range tg.Hosts() {
		hg, err := host.HostGet()
		if err != nil {
			return err
		}
		addr := hg.ExternalSettings.NetAddress

		_, ok := contractHosts[string(addr)]
		if isWhitelist != ok {
			filteredHosts = append(filteredHosts, string(addr))
			numHosts++
			if numHosts == allowHosts {
				break
			}
		}
	}

	// enable list mode
	if err = renter.HostDbFilterModePost(fm, nil, filteredHosts); err != nil {
		return err
	}

	// Confirm filter mode is set as expected
	hdfmg, err := renter.HostDbFilterModeGet()
	if err != nil {
		return err
	}
	if hdfmg.FilterMode != fm.String() {
		return fmt.Errorf("filter mode not set as expected, got %v expected %v", hdfmg.FilterMode, fm.String())
	}
	if len(hdfmg.Hosts) != len(filteredHosts) {
		return fmt.Errorf("Number of filtered hosts incorrect, got %v expected %v", len(hdfmg.Hosts), len(filteredHosts))
	}
	// Create map for comparison
	filteredHostsMap := make(map[string]struct{})
	for _, host := range filteredHosts {
		filteredHostsMap[host] = struct{}{}
	}
	// hdfmg.Hosts has pubkeys not NetAddresses
	// for _, host := range hdfmg.Hosts {
	// 	if _, ok := filteredHostsMap[host]; !ok {
	// 		return errors.New("host returned not found in filtered hosts")
	// 	}
	// }

	// Confirm hosts are marked as filtered in original hosttree by querying AllHost
	hbag, err := renter.HostDbAllGet()
	if err != nil {
		return err
	}
	for _, host := range hbag.Hosts {
		if _, ok := filteredHostsMap[string(host.NetAddress)]; !ok {
			continue
		}
		if !host.Filtered {
			return errors.New("Host not marked as filtered")
		}
	}

	// confirm contracts are dropped and replaced appropriately for the FilterMode
	loop = 0
	err = build.Retry(50, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err := renter.RenterInactiveContractsGet()
		if err != nil {
			return err
		}
		// Check for correct number of contracts
		if len(rc.ActiveContracts) != allowHosts {
			return fmt.Errorf("Contracts did not form as expected, have %v expected %v", len(rc.ActiveContracts), allowHosts)
		}
		// Check that ActiveContracts are with correct hosts
		for _, c := range rc.ActiveContracts {
			_, exists := filteredHostsMap[string(c.NetAddress)]
			if isWhitelist != exists {
				return errors.New("non listed host found in active contracts")
			}
		}
		// Check that Inactive contracts are with hosts that weren't listed
		check := 0
		for _, c := range rc.InactiveContracts {
			if _, ok := contractHosts[string(c.NetAddress)]; ok {
				check++
			}
		}
		if check != len(contractHosts) {
			return fmt.Errorf("did not find all previous hosts in inactive contracts, found %v expected %v", check, len(contractHosts))
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Confirm HostDbActiveGet is filtered appropriately
	hdbActive, err := renter.HostDbActiveGet()
	if err != nil {
		return err
	}
	numExpectedHosts := len(tg.Hosts()) - len(filteredHosts)
	if isWhitelist {
		numExpectedHosts = len(filteredHosts)
	}
	if len(hdbActive.Hosts) != numExpectedHosts {
		return fmt.Errorf("Number of active hosts doesn't equal number of non list hosts: got %v expected %v", len(hdbActive.Hosts), numExpectedHosts)
	}
	for _, h := range hdbActive.Hosts {
		_, ok := filteredHostsMap[string(h.NetAddress)]
		if isWhitelist != ok {
			return errors.New("Blacklisted host returned as active host")
		}
	}

	// Confirm that HostDbAllGet is not filtered
	hdbAll, err := renter.HostDbAllGet()
	if err != nil {
		return err
	}
	if len(hdbAll.Hosts) != len(tg.Hosts()) {
		return fmt.Errorf("Number of all hosts doesn't equal number of test group hosts: got %v expected %v", len(hdbAll.Hosts), len(tg.Hosts()))
	}

	// Disable FilterMode and confirm all hosts are active again
	var nullList []string
	if err = renter.HostDbFilterModePost(modules.HostDBDisableFilter, nil, nullList); err != nil {
		return err
	}
	hdbActive, err = renter.HostDbActiveGet()
	if err != nil {
		return err
	}
	hdbag, err := renter.HostDbAllGet()
	if err != nil {
		return err
	}
	if len(hdbActive.Hosts) != len(tg.Hosts()) {
		return fmt.Errorf("Unexpected number of active hosts after disabling FilterMode: got %v expected %v (%v)", len(hdbActive.Hosts), len(tg.Hosts()), len(hdbag.Hosts))
	}

	// Confirm that contracts will form with non listed hosts again by
	// canceling contracts until a contract is formed with a nonlisted host.
	loop = 0
	err = build.Retry(50, 100*time.Millisecond, func() error {
		// Mine a block every 10 iterations to make sure
		// threadedContractMaintenance is being triggered
		if loop%10 == 0 {
			if err = m.MineBlock(); err != nil {
				return err
			}
		}
		loop++
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) == 0 {
			return errors.New("no contracts")
		}
		for _, c := range rc.ActiveContracts {
			_, exists := filteredHostsMap[string(c.NetAddress)]
			if isWhitelist != exists {
				return nil
			}
			if err = renter.RenterContractCancelPost(c.ID); err != nil {
				return err
			}
		}
		return errors.New("no contracts reformed with blacklist hosts")
	})
	if err != nil {
		return err
	}

	return nil
}
