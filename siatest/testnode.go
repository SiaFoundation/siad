package siatest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/node/api/server"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestNode is a helper struct for testing that contains a server and a client
// as embedded fields.
type TestNode struct {
	*server.Server
	client.Client
	params      node.NodeParams
	primarySeed string

	downloadDir *LocalDir
	filesDir    *LocalDir
}

// PrintDebugInfo prints out helpful debug information when debug tests and ndfs, the
// boolean arguments dictate what is printed
func (tn *TestNode) PrintDebugInfo(t *testing.T, contractInfo, hostInfo, renterInfo bool) {
	if contractInfo {
		rc, err := tn.RenterAllContractsGet()
		if err != nil {
			t.Log(err)
		}
		t.Log("Active Contracts")
		for _, c := range rc.ActiveContracts {
			t.Log("    ID", c.ID)
			t.Log("    HostPublicKey", c.HostPublicKey)
			t.Log("    GoodForUpload", c.GoodForUpload)
			t.Log("    GoodForRenew", c.GoodForRenew)
			t.Log("    EndHeight", c.EndHeight)
		}
		t.Log()
		t.Log("Passive Contracts")
		for _, c := range rc.PassiveContracts {
			t.Log("    ID", c.ID)
			t.Log("    HostPublicKey", c.HostPublicKey)
			t.Log("    GoodForUpload", c.GoodForUpload)
			t.Log("    GoodForRenew", c.GoodForRenew)
			t.Log("    EndHeight", c.EndHeight)
		}
		t.Log()
		t.Log("Refreshed Contracts")
		for _, c := range rc.RefreshedContracts {
			t.Log("    ID", c.ID)
			t.Log("    HostPublicKey", c.HostPublicKey)
			t.Log("    GoodForUpload", c.GoodForUpload)
			t.Log("    GoodForRenew", c.GoodForRenew)
			t.Log("    EndHeight", c.EndHeight)
		}
		t.Log()
		t.Log("Disabled Contracts")
		for _, c := range rc.DisabledContracts {
			t.Log("    ID", c.ID)
			t.Log("    HostPublicKey", c.HostPublicKey)
			t.Log("    GoodForUpload", c.GoodForUpload)
			t.Log("    GoodForRenew", c.GoodForRenew)
			t.Log("    EndHeight", c.EndHeight)
		}
		t.Log()
		t.Log("Expired Contracts")
		for _, c := range rc.ExpiredContracts {
			t.Log("    ID", c.ID)
			t.Log("    HostPublicKey", c.HostPublicKey)
			t.Log("    GoodForUpload", c.GoodForUpload)
			t.Log("    GoodForRenew", c.GoodForRenew)
			t.Log("    EndHeight", c.EndHeight)
		}
		t.Log()
		t.Log("Expired Refreshed Contracts")
		for _, c := range rc.ExpiredRefreshedContracts {
			t.Log("    ID", c.ID)
			t.Log("    HostPublicKey", c.HostPublicKey)
			t.Log("    GoodForUpload", c.GoodForUpload)
			t.Log("    GoodForRenew", c.GoodForRenew)
			t.Log("    EndHeight", c.EndHeight)
		}
		t.Log()
	}

	if hostInfo {
		hdbag, err := tn.HostDbAllGet()
		if err != nil {
			t.Log(err)
		}
		t.Log("Active Hosts from HostDB")
		for _, host := range hdbag.Hosts {
			hostInfo, err := tn.HostDbHostsGet(host.PublicKey)
			if err != nil {
				t.Log(err)
			}
			t.Log("    Host:", host.NetAddress)
			t.Log("        score", hostInfo.ScoreBreakdown.Score)
			t.Log("        breakdown", hostInfo.ScoreBreakdown)
			t.Log("        pk", host.PublicKey)
			t.Log("        Accepting Contracts", host.HostExternalSettings.AcceptingContracts)
			t.Log("        Filtered", host.Filtered)
			t.Log("        LastIPNetChange", host.LastIPNetChange.String())
			t.Log("        Subnets")
			for _, subnet := range host.IPNets {
				t.Log("            ", subnet)
			}
			t.Log()
		}
		t.Log()
	}

	if renterInfo {
		t.Log("Renter Info")
		rg, err := tn.RenterGet()
		if err != nil {
			t.Log(err)
		}
		t.Log("CP:", rg.CurrentPeriod)
		cg, err := tn.ConsensusGet()
		if err != nil {
			t.Log(err)
		}
		t.Log("BH:", cg.Height)
		settings := rg.Settings
		t.Log("Allowance Funds:", settings.Allowance.Funds.HumanString())
		fm := rg.FinancialMetrics
		t.Log("Unspent Funds:", fm.Unspent.HumanString())
		t.Log()
	}
}

// RestartNode restarts a TestNode
func (tn *TestNode) RestartNode() error {
	err := tn.StopNode()
	if err != nil {
		return errors.AddContext(err, "Could not stop node")
	}
	err = tn.StartNode()
	if err != nil {
		return errors.AddContext(err, "Could not start node")
	}
	return nil
}

// StartNode starts a TestNode from an active group
func (tn *TestNode) StartNode() error {
	// Create server
	s, err := server.New(":0", tn.UserAgent, tn.Password, tn.params)
	if err != nil {
		return err
	}
	tn.Server = s
	tn.Client.Address = s.APIAddress()
	if !tn.params.CreateWallet && tn.params.Wallet == nil {
		return nil
	}
	return tn.WalletUnlockPost(tn.primarySeed)
}

// StartNodeCleanDeps restarts a node from an active group without its
// previously assigned dependencies.
func (tn *TestNode) StartNodeCleanDeps() error {
	tn.params.ContractSetDeps = nil
	tn.params.ContractorDeps = nil
	tn.params.RenterDeps = nil
	return tn.StartNode()
}

// StopNode stops a TestNode
func (tn *TestNode) StopNode() error {
	return errors.AddContext(tn.Close(), "failed to stop node")
}

// NewNode creates a new funded TestNode
func NewNode(nodeParams node.NodeParams) (*TestNode, error) {
	// We can't create a funded node without a miner
	if !nodeParams.CreateMiner && nodeParams.Miner == nil {
		return nil, errors.New("Can't create funded node without miner")
	}
	// Create clean node
	tn, err := NewCleanNode(nodeParams)
	if err != nil {
		return nil, err
	}
	// Fund the node
	for i := types.BlockHeight(0); i <= types.MaturityDelay+types.TaxHardforkHeight; i++ {
		if err := tn.MineBlock(); err != nil {
			return nil, err
		}
	}
	// Return TestNode
	return tn, nil
}

// NewCleanNode creates a new TestNode that's not yet funded
func NewCleanNode(nodeParams node.NodeParams) (*TestNode, error) {
	userAgent := "Sia-Agent"
	password := "password"

	// Create server
	s, err := server.New(":0", userAgent, password, nodeParams)
	if err != nil {
		return nil, err
	}

	// Create client
	c := client.New(s.APIAddress())
	c.UserAgent = userAgent
	c.Password = password

	// Create TestNode
	tn := &TestNode{
		Server:      s,
		Client:      *c,
		params:      nodeParams,
		primarySeed: "",
	}
	if err = tn.initRootDirs(); err != nil {
		return nil, errors.AddContext(err, "failed to create root directories")
	}

	// If there is no wallet we are done.
	if !nodeParams.CreateWallet && nodeParams.Wallet == nil {
		return tn, nil
	}

	// Init wallet
	if nodeParams.PrimarySeed != "" {
		err := tn.WalletInitSeedPost(nodeParams.PrimarySeed, "", false)
		if err != nil {
			return nil, err
		}
		tn.primarySeed = nodeParams.PrimarySeed
	} else {
		wip, err := tn.WalletInitPost("", false)
		if err != nil {
			return nil, err
		}
		tn.primarySeed = wip.PrimarySeed
	}

	// Unlock wallet
	if err := tn.WalletUnlockPost(tn.primarySeed); err != nil {
		return nil, err
	}

	// Return TestNode
	return tn, nil
}

// initRootDirs creates the download and upload directories for the TestNode
func (tn *TestNode) initRootDirs() error {
	tn.downloadDir = &LocalDir{
		path: filepath.Join(tn.RenterDir(), "downloads"),
	}
	if err := os.MkdirAll(tn.downloadDir.path, 0777); err != nil {
		return err
	}
	tn.filesDir = &LocalDir{
		path: filepath.Join(tn.RenterDir(), modules.SiapathRoot),
	}
	if err := os.MkdirAll(tn.filesDir.path, 0777); err != nil {
		return err
	}
	return nil
}

// SiaPath returns the siapath of a local file or directory to be used for
// uploading
func (tn *TestNode) SiaPath(path string) modules.SiaPath {
	s := strings.TrimPrefix(path, tn.filesDir.path+string(filepath.Separator))
	sp, err := modules.NewSiaPath(s)
	if err != nil {
		build.Critical("This shouldn't happen", err)
	}
	return sp
}
