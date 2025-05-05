package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/gateway"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/syncer"
	"go.sia.tech/exchange-bridge/api"
	vaultSqlite "go.sia.tech/vaultd/persist/sqlite"
	"go.sia.tech/vaultd/vault"
	walletSqlite "go.sia.tech/walletd/v2/persist/sqlite"
	"go.sia.tech/walletd/v2/wallet"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	dir     string
	network string

	apiAddr     string
	apiPassword string = os.Getenv("SIAD_API_PASSWORD")

	syncerAddr string
)

func loadCustomNetwork(fp string) (*consensus.Network, types.Block, error) {
	f, err := os.Open(fp)
	if err != nil {
		return nil, types.Block{}, fmt.Errorf("failed to open network file: %w", err)
	}
	defer f.Close()

	var network struct {
		Network consensus.Network `json:"network" yaml:"network"`
		Genesis types.Block       `json:"genesis" yaml:"genesis"`
	}

	if err := json.NewDecoder(f).Decode(&network); err != nil {
		return nil, types.Block{}, fmt.Errorf("failed to decode JSON network file: %w", err)
	}
	return &network.Network, network.Genesis, nil
}

func runNode(ctx context.Context, log *zap.Logger) error {
	walletdLog := log.Named("walletd")
	vaultdLog := log.Named("vaultd")

	var n *consensus.Network
	var genesis types.Block
	var bootstrapPeers []string
	switch network {
	case "mainnet":
		n, genesis = chain.Mainnet()
		bootstrapPeers = syncer.MainnetBootstrapPeers
	case "zen":
		n, genesis = chain.TestnetZen()
		bootstrapPeers = syncer.ZenBootstrapPeers
	case "anagami":
		n, genesis = chain.TestnetAnagami()
		bootstrapPeers = syncer.AnagamiBootstrapPeers
	case "erravimus":
		n, genesis = chain.TestnetErravimus()
		bootstrapPeers = syncer.ErravimusBootstrapPeers
	default:
		var err error
		n, genesis, err = loadCustomNetwork(network)
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("invalid network: must be one of 'mainnet', 'zen', or 'anagami'")
		} else if err != nil {
			return fmt.Errorf("failed to load custom network: %w", err)
		}
	}

	bdb, err := coreutils.OpenBoltChainDB(filepath.Join(dir, "consensus.db"))
	if err != nil {
		return fmt.Errorf("failed to open consensus database: %w", err)
	}
	defer bdb.Close()

	dbstore, tipState, err := chain.NewDBStore(bdb, n, genesis, chain.NewZapMigrationLogger(log.Named("chaindb")))
	if err != nil {
		return fmt.Errorf("failed to create chain store: %w", err)
	}
	cm := chain.NewManager(dbstore, tipState, chain.WithLog(log.Named("chain")))

	wdb, err := walletSqlite.OpenDatabase("walletd.sqlite3", walletdLog.Named("sqlite"))
	if err != nil {
		return fmt.Errorf("failed to open wallet database: %w", err)
	}
	defer wdb.Close()

	vdb, err := vaultSqlite.OpenDatabase("vaultd.sqlite3", vaultSqlite.WithLogger(vaultdLog.Named("sqlite")))
	if err != nil {
		return fmt.Errorf("failed to open vault database: %w", err)
	}
	defer vdb.Close()

	syncerListener, err := net.Listen("tcp", syncerAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %w", syncerAddr, err)
	}
	defer syncerListener.Close()

	httpListener, err := net.Listen("tcp", apiAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %w", apiAddr, err)
	}
	defer httpListener.Close()

	syncerAddr := syncerListener.Addr().String()

	// peers will reject us if our hostname is empty or unspecified, so use loopback
	host, port, _ := net.SplitHostPort(syncerAddr)
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		syncerAddr = net.JoinHostPort("127.0.0.1", port)
	}

	ps, err := walletSqlite.NewPeerStore(wdb)
	if err != nil {
		return fmt.Errorf("failed to create peer store: %w", err)
	}
	for _, peer := range bootstrapPeers {
		if err := wdb.AddPeer(peer); err != nil {
			return fmt.Errorf("failed to add bootstrap peer %q: %w", peer, err)
		}
	}

	header := gateway.Header{
		GenesisID:  genesis.ID(),
		UniqueID:   gateway.GenerateUniqueID(),
		NetAddress: syncerAddr,
	}

	s := syncer.New(syncerListener, cm, ps, header, syncer.WithLogger(log.Named("syncer")))
	defer s.Close()
	go s.Run()

	wm, err := wallet.NewManager(cm, wdb, wallet.WithIndexMode(wallet.IndexModeFull), wallet.WithLogger(walletdLog.Named("wallet")))
	if err != nil {
		return fmt.Errorf("failed to create wallet manager: %w", err)
	}
	defer wm.Close()

	vm := vault.New(vdb)
	defer vm.Close()

	api := http.Server{
		Handler:      api.NewHandler(cm, s, vm, wm, log.Named("api")),
		ReadTimeout:  time.Minute,
		WriteTimeout: time.Minute,
	}
	defer api.Close()
	go func() {
		if err := api.Serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Panic("failed to serve API", zap.Error(err))
		}
	}()

	<-ctx.Done()
	time.AfterFunc(30*time.Second, func() {
		log.Panic("failed to shut down gracefully")
	})
	return nil
}

func main() {
	var logLevel zap.AtomicLevel
	flag.StringVar(&dir, "dir", ".", "directory to store data")
	flag.StringVar(&network, "network", "mainnet", "network to use (mainnet, zen, anagami, or path to custom network file)")
	flag.TextVar(&logLevel, "log.level", zap.NewAtomicLevelAt(zap.InfoLevel), "log level")
	flag.Parse()

	cfg := zap.NewProductionEncoderConfig()
	cfg.EncodeTime = zapcore.RFC3339TimeEncoder
	cfg.EncodeDuration = zapcore.StringDurationEncoder
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.StacktraceKey = ""
	enc := zapcore.NewConsoleEncoder(cfg)
	core := zapcore.NewCore(enc, zapcore.Lock(os.Stdout), logLevel)
	log := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	defer log.Sync()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := runNode(ctx, log); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
