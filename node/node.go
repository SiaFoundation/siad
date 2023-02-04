// Package node provides tooling for creating a Sia node. Sia nodes consist of a
// collection of modules. The node package gives you tools to easily assemble
// various combinations of modules with varying dependencies and settings,
// including templates for assembling sane no-hassle Sia nodes.
package node

// TODO: Add support for the explorer.

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	mnemonics "gitlab.com/NebulousLabs/entropy-mnemonics"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/siamux"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/accounting"
	"go.sia.tech/siad/modules/consensus"
	"go.sia.tech/siad/modules/explorer"
	"go.sia.tech/siad/modules/gateway"
	"go.sia.tech/siad/modules/host"
	"go.sia.tech/siad/modules/miner"
	"go.sia.tech/siad/modules/renter"
	"go.sia.tech/siad/modules/renter/contractor"
	"go.sia.tech/siad/modules/renter/hostdb"
	"go.sia.tech/siad/modules/renter/proto"
	"go.sia.tech/siad/modules/transactionpool"
	"go.sia.tech/siad/modules/wallet"
	"go.sia.tech/siad/persist"
)

// NodeParams contains a bunch of parameters for creating a new test node. As
// there are many options, templates are provided that you can modify which
// cover the most common use cases.
//
// Each module is created separately. There are several ways to create a module,
// though not all methods are currently available for each module. You should
// only use one method for creating a module, using multiple methods will cause
// an error.
//   - Indicate with the 'CreateModule' bool that a module should be created
//     automatically. To create the module with custom dependencies, pass the
//     custom dependencies in using the 'ModuleDependencies' field.
//   - Pass an existing module in directly.
//   - Set 'CreateModule' to false and do not pass in an existing module.
//     This will result in a 'nil' module, meaning the node will not have
//     that module.
type NodeParams struct {
	// Flags to indicate which modules should be created automatically by the
	// server. If you are providing a pre-existing module, do not set the flag
	// for that module.
	//
	// NOTE / TODO: The code does not currently enforce this, but you should not
	// provide a custom module unless all of its dependencies are also custom.
	// Example: if the ConsensusSet is custom, the Gateway should also be
	// custom. The TransactionPool however does not need to be custom in this
	// example.
	CreateAccounting      bool
	CreateConsensusSet    bool
	CreateExplorer        bool
	CreateGateway         bool
	CreateHost            bool
	CreateMiner           bool
	CreateRenter          bool
	CreateTransactionPool bool
	CreateWallet          bool

	// Custom modules - if the modules is provided directly, the provided
	// module will be used instead of creating a new one. If a custom module is
	// provided, the 'omit' flag for that module must be set to false (which is
	// the default setting).
	Accounting      modules.Accounting
	ConsensusSet    modules.ConsensusSet
	Explorer        modules.Explorer
	Gateway         modules.Gateway
	Host            modules.Host
	Miner           modules.TestMiner
	Renter          modules.Renter
	TransactionPool modules.TransactionPool
	Wallet          modules.Wallet

	// Dependencies for each module supporting dependency injection.
	AccountingDeps   modules.Dependencies
	ConsensusSetDeps modules.Dependencies
	ContractorDeps   modules.Dependencies
	ContractSetDeps  modules.Dependencies
	GatewayDeps      modules.Dependencies
	HostDeps         modules.Dependencies
	HostDBDeps       modules.Dependencies
	RenterDeps       modules.Dependencies
	TPoolDeps        modules.Dependencies
	WalletDeps       modules.Dependencies

	// Dependencies for storage monitor supporting dependency injection.
	StorageManagerDeps modules.Dependencies

	// Custom settings for siamux
	SiaMuxTCPAddress string
	SiaMuxWSAddress  string

	// Custom settings for modules
	Allowance      modules.Allowance
	Bootstrap      bool
	UseUPNP        bool
	HostAddress    string
	HostStorage    uint64
	RPCAddress     string
	WalletPassword string

	// Initialize node from existing seed.
	PrimarySeed string

	// The following fields are used to skip parts of the node set up
	SkipSetAllowance     bool
	SkipHostDiscovery    bool
	SkipHostAnnouncement bool
	SkipWalletInit       bool

	// The high level directory where all the persistence gets stored for the
	// modules.
	Dir string
}

// Node is a collection of Sia modules operating together as a Sia node.
type Node struct {
	// The mux of the node.
	Mux    *siamux.SiaMux
	muxLog *os.File

	// The modules of the node. Modules that are not initialized will be nil.
	Accounting      modules.Accounting
	ConsensusSet    modules.ConsensusSet
	Explorer        modules.Explorer
	Gateway         modules.Gateway
	Host            modules.Host
	Miner           modules.TestMiner
	Renter          modules.Renter
	TransactionPool modules.TransactionPool
	Wallet          modules.Wallet

	// The high level directory where all the persistence gets stored for the
	// modules.
	Dir string
}

// NumModules returns how many of the major modules the given NodeParams would
// create.
func (np NodeParams) NumModules() (n int) {
	if np.CreateGateway || np.Gateway != nil {
		n++
	}
	if np.CreateConsensusSet || np.ConsensusSet != nil {
		n++
	}
	if np.CreateTransactionPool || np.TransactionPool != nil {
		n++
	}
	if np.CreateWallet || np.Wallet != nil {
		n++
	}
	if np.CreateHost || np.Host != nil {
		n++
	}
	if np.CreateRenter || np.Renter != nil {
		n++
	}
	if np.CreateMiner || np.Miner != nil {
		n++
	}
	if !np.CreateExplorer || np.Explorer != nil {
		n++
	}
	if np.CreateAccounting || np.Accounting != nil {
		n++
	}
	return
}

// printlnRelease is a wrapper that only prints to stdout in release builds.
func printlnRelease(a ...interface{}) {
	if build.Release == "standard" || build.Release == "testnet" {
		fmt.Println(a...)
	}
}

// printfRelease is a wrapper that only prints to stdout in release builds.
func printfRelease(format string, a ...interface{}) {
	if build.Release == "standard" || build.Release == "testnet" {
		fmt.Printf(format, a...)
	}
}

// Close will call close on every module within the node, combining and
// returning the errors.
func (n *Node) Close() (err error) {
	if n.Accounting != nil {
		printlnRelease("Closing accounting...")
		err = errors.Compose(err, n.Accounting.Close())
	}
	if n.Renter != nil {
		printlnRelease("Closing renter...")
		err = errors.Compose(err, n.Renter.Close())
	}
	if n.Host != nil {
		printlnRelease("Closing host...")
		err = errors.Compose(err, n.Host.Close())
	}
	if n.Miner != nil {
		printlnRelease("Closing miner...")
		err = errors.Compose(err, n.Miner.Close())
	}
	if n.Wallet != nil {
		printlnRelease("Closing wallet...")
		err = errors.Compose(err, n.Wallet.Close())
	}
	if n.TransactionPool != nil {
		printlnRelease("Closing transactionpool...")
		err = errors.Compose(err, n.TransactionPool.Close())
	}
	if n.Explorer != nil {
		printlnRelease("Closing explorer...")
		err = errors.Compose(err, n.Explorer.Close())
	}
	if n.ConsensusSet != nil {
		printlnRelease("Closing consensusset...")
		err = errors.Compose(err, n.ConsensusSet.Close())
	}
	if n.Gateway != nil {
		printlnRelease("Closing gateway...")
		err = errors.Compose(err, n.Gateway.Close())
	}
	if n.Mux != nil {
		printlnRelease("Closing siamux...")
		err = errors.Compose(err, n.Mux.Close(), n.muxLog.Close())
	}
	return err
}

// Unlock unlocks the server's wallet using the provided password.
func tryUnlockWallet(w modules.Wallet, password string) error {
	if w == nil {
		return errors.New("server doesn't have a wallet")
	}
	var validKeys []crypto.CipherKey
	dicts := []mnemonics.DictionaryID{"english", "german", "japanese"}
	for _, dict := range dicts {
		seed, err := modules.StringToSeed(password, dict)
		if err != nil {
			continue
		}
		validKeys = append(validKeys, crypto.NewWalletKey(crypto.HashObject(seed)))
	}
	validKeys = append(validKeys, crypto.NewWalletKey(crypto.HashObject(password)))
	for _, key := range validKeys {
		if err := w.Unlock(key); err == nil {
			return nil
		}
	}
	return modules.ErrBadEncryptionKey
}

// New will create a new node. The inputs to the function are the respective
// 'New' calls for each module. We need to use this awkward method of
// initialization because the siatest package cannot import any of the modules
// directly (so that the modules may use the siatest package to test
// themselves).
func New(params NodeParams, loadStartTime time.Time) (*Node, <-chan error) {
	// Make sure the path is an absolute one.
	dir, err := filepath.Abs(params.Dir)
	errChan := make(chan error, 1)
	if err != nil {
		errChan <- err
		return nil, errChan
	}

	// Create the siamux.
	mux, muxLog, err := modules.NewSiaMux(filepath.Join(dir, modules.SiaMuxDir), dir, params.SiaMuxTCPAddress, params.SiaMuxWSAddress)
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create siamux"))
		return nil, errChan
	}

	// Load all modules
	numModules := params.NumModules()
	i := 1
	printfRelease("(%d/%d) Loading siad...\n", i, numModules)
	// Gateway.
	g, err := func() (modules.Gateway, error) {
		if params.CreateGateway && params.Gateway != nil {
			return nil, errors.New("cannot both create a gateway and use a passed in gateway")
		}
		if params.Gateway != nil {
			return params.Gateway, nil
		}
		if !params.CreateGateway {
			return nil, nil
		}
		if params.RPCAddress == "" {
			params.RPCAddress = "localhost:0"
		}
		gatewayDeps := params.GatewayDeps
		if gatewayDeps == nil {
			gatewayDeps = modules.ProdDependencies
		}
		i++
		printfRelease("(%d/%d) Loading gateway...\n", i, numModules)
		return gateway.NewCustomGateway(params.RPCAddress, params.Bootstrap, params.UseUPNP, filepath.Join(dir, modules.GatewayDir), gatewayDeps)
	}()
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create gateway"))
		return nil, errChan
	}

	// Consensus.
	cs, errChanCS := func() (modules.ConsensusSet, <-chan error) {
		c := make(chan error, 1)
		defer close(c)
		if params.CreateConsensusSet && params.ConsensusSet != nil {
			c <- errors.New("cannot both create consensus and use passed in consensus")
			return nil, c
		}
		if params.ConsensusSet != nil {
			return params.ConsensusSet, c
		}
		if !params.CreateConsensusSet {
			return nil, c
		}
		i++
		printfRelease("(%d/%d) Loading consensus...\n", i, numModules)
		consensusSetDeps := params.ConsensusSetDeps
		if consensusSetDeps == nil {
			consensusSetDeps = modules.ProdDependencies
		}
		return consensus.NewCustomConsensusSet(g, params.Bootstrap, filepath.Join(dir, modules.ConsensusDir), consensusSetDeps)
	}()
	if err := modules.PeekErr(errChanCS); err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create consensus set"))
		return nil, errChan
	}

	// Explorer.
	e, err := func() (modules.Explorer, error) {
		if !params.CreateExplorer && params.Explorer != nil {
			return nil, errors.New("cannot create explorer and also use custom explorer")
		}
		if params.Explorer != nil {
			return params.Explorer, nil
		}
		if !params.CreateExplorer {
			return nil, nil
		}
		e, err := explorer.New(cs, filepath.Join(dir, modules.ExplorerDir))
		if err != nil {
			return nil, err
		}
		i++
		printfRelease("(%d/%d) Loading explorer...\n", i, numModules)
		return e, nil
	}()
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create explorer"))
		return nil, errChan
	}

	// Transaction Pool.
	tp, err := func() (modules.TransactionPool, error) {
		if params.CreateTransactionPool && params.TransactionPool != nil {
			return nil, errors.New("cannot create transaction pool and also use custom transaction pool")
		}
		if params.TransactionPool != nil {
			return params.TransactionPool, nil
		}
		if !params.CreateTransactionPool {
			return nil, nil
		}
		tpoolDeps := params.TPoolDeps
		if tpoolDeps == nil {
			tpoolDeps = modules.ProdDependencies
		}
		i++
		printfRelease("(%d/%d) Loading transaction pool...\n", i, numModules)
		return transactionpool.NewCustomTPool(cs, g, filepath.Join(dir, modules.TransactionPoolDir), tpoolDeps)
	}()
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create transaction pool"))
		return nil, errChan
	}

	// Wallet.
	w, err := func() (modules.Wallet, error) {
		if params.CreateWallet && params.Wallet != nil {
			return nil, errors.New("cannot create wallet and use custom wallet")
		}
		if params.Wallet != nil {
			return params.Wallet, nil
		}
		if !params.CreateWallet {
			return nil, nil
		}
		walletDeps := params.WalletDeps
		if walletDeps == nil {
			walletDeps = modules.ProdDependencies
		}
		i++
		printfRelease("(%d/%d) Loading wallet...\n", i, numModules)
		wallet, err := wallet.NewCustomWallet(cs, tp, filepath.Join(dir, modules.WalletDir), walletDeps)
		if err != nil {
			return nil, err
		}
		// automatically unlock the wallet if the password is provided
		if len(params.WalletPassword) != 0 {
			printfRelease("  Wallet password found, attempting to unlock wallet...\n")
			unlockErr := tryUnlockWallet(wallet, params.WalletPassword)
			if unlockErr != nil {
				fmt.Println("  Auto-unlock failed:", unlockErr)
			} else {
				fmt.Println("  Auto-unlock successful.")
			}
		}
		return wallet, nil
	}()
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create wallet"))
		return nil, errChan
	}

	// Miner.
	m, err := func() (modules.TestMiner, error) {
		if params.CreateMiner && params.Miner != nil {
			return nil, errors.New("cannot create miner and also use custom miner")
		}
		if params.Miner != nil {
			return params.Miner, nil
		}
		if !params.CreateMiner {
			return nil, nil
		}
		i++
		printfRelease("(%d/%d) Loading miner...\n", i, numModules)
		m, err := miner.New(cs, tp, w, filepath.Join(dir, modules.MinerDir))
		if err != nil {
			return nil, err
		}
		return m, nil
	}()
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create miner"))
		return nil, errChan
	}

	// Host.
	h, err := func() (modules.Host, error) {
		if params.CreateHost && params.Host != nil {
			return nil, errors.New("cannot create host and use custom host")
		}
		if params.Host != nil {
			return params.Host, nil
		}
		if !params.CreateHost {
			return nil, nil
		}
		if params.HostAddress == "" {
			params.HostAddress = "localhost:0"
		}
		hostDeps := params.HostDeps
		if hostDeps == nil {
			hostDeps = modules.ProdDependencies
		}
		smDeps := params.StorageManagerDeps
		if smDeps == nil {
			smDeps = new(modules.ProductionDependencies)
		}
		i++
		printfRelease("(%d/%d) Loading host...\n", i, numModules)
		host, err := host.NewCustomTestHost(hostDeps, smDeps, cs, g, tp, w, mux, params.HostAddress, filepath.Join(dir, modules.HostDir))
		return host, err
	}()
	if err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create host"))
		return nil, errChan
	}

	// Renter.
	r, errChanRenter := func() (modules.Renter, <-chan error) {
		c := make(chan error, 1)
		if params.CreateRenter && params.Renter != nil {
			c <- errors.New("cannot create renter and also use custom renter")
			close(c)
			return nil, c
		}
		if params.Renter != nil {
			close(c)
			return params.Renter, c
		}
		if !params.CreateRenter {
			close(c)
			return nil, c
		}
		contractorDeps := params.ContractorDeps
		if contractorDeps == nil {
			contractorDeps = modules.ProdDependencies
		}
		contractSetDeps := params.ContractSetDeps
		if contractSetDeps == nil {
			contractSetDeps = modules.ProdDependencies
		}
		hostDBDeps := params.HostDBDeps
		if hostDBDeps == nil {
			hostDBDeps = modules.ProdDependencies
		}
		renterDeps := params.RenterDeps
		if renterDeps == nil {
			renterDeps = modules.ProdDependencies
		}
		persistDir := filepath.Join(dir, modules.RenterDir)

		i++
		printfRelease("(%d/%d) Loading renter...\n", i, numModules)

		// HostDB
		hdb, errChanHDB := hostdb.NewCustomHostDB(g, cs, tp, mux, persistDir, hostDBDeps)
		if err := modules.PeekErr(errChanHDB); err != nil {
			c <- err
			close(c)
			return nil, c
		}
		// ContractSet
		renterRateLimit := ratelimit.NewRateLimit(0, 0, 0)
		contractSet, err := proto.NewContractSet(filepath.Join(persistDir, "contracts"), renterRateLimit, contractSetDeps)
		if err != nil {
			c <- err
			close(c)
			return nil, c
		}
		// Contractor
		logger, err := persist.NewFileLogger(filepath.Join(persistDir, "contractor.log"))
		if err != nil {
			c <- err
			close(c)
			return nil, c
		}
		hc, errChanContractor := contractor.NewCustomContractor(cs, w, tp, hdb, persistDir, contractSet, logger, contractorDeps)
		if err := modules.PeekErr(errChanContractor); err != nil {
			c <- err
			close(c)
			return nil, c
		}
		renter, errChanRenter := renter.NewCustomRenter(g, cs, tp, hdb, w, hc, mux, persistDir, renterRateLimit, renterDeps)
		if err := modules.PeekErr(errChanRenter); err != nil {
			c <- err
			close(c)
			return nil, c
		}
		go func() {
			c <- errors.Compose(<-errChanHDB, <-errChanContractor, <-errChanRenter)
			close(c)
		}()
		return renter, c
	}()
	if err := modules.PeekErr(errChanRenter); err != nil {
		errChan <- errors.Extend(err, errors.New("unable to create renter"))
		return nil, errChan
	}

	// Accounting.
	acc, err := func() (modules.Accounting, error) {
		if params.CreateAccounting && params.Accounting != nil {
			return nil, errors.New("cannot create accounting and also use custom accounting")
		}
		if params.Accounting != nil {
			return params.Accounting, nil
		}
		if !params.CreateAccounting {
			return nil, nil
		}
		accoutingDeps := params.AccountingDeps
		if accoutingDeps == nil {
			accoutingDeps = modules.ProdDependencies
		}
		persistDir := filepath.Join(dir, modules.AccountingDir)

		i++
		printfRelease("(%d/%d) Loading accounting...\n", i, numModules)

		// Load Accounting module
		acc, err := accounting.NewCustomAccounting(h, m, r, w, persistDir, accoutingDeps)
		if err != nil {
			return nil, err
		}
		return acc, nil
	}()
	if err != nil {
		errChan <- errors.AddContext(err, "unable to create accounting module")
		return nil, errChan
	}

	// Setup complete
	printfRelease("API is now available, synchronous startup completed in %.3f seconds\n", time.Since(loadStartTime).Seconds())
	go func() {
		errChan <- errors.Compose(<-errChanCS, <-errChanRenter)
		close(errChan)
	}()

	return &Node{
		Mux:    mux,
		muxLog: muxLog,

		Accounting:      acc,
		ConsensusSet:    cs,
		Explorer:        e,
		Gateway:         g,
		Host:            h,
		Miner:           m,
		Renter:          r,
		TransactionPool: tp,
		Wallet:          w,

		Dir: dir,
	}, errChan
}
