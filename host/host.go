package host

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/host"
	"go.sia.tech/core/net/mux"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

type (
	// A Logger logs messages to a location.
	Logger interface {
		Scope(scope string) Logger

		Errorf(f string, v ...interface{})
		Errorln(v ...interface{})

		Infof(f string, v ...interface{})
		Infoln(v ...interface{})

		Warnf(f string, v ...interface{})
		Warnln(v ...interface{})
	}

	settingsManager struct {
		cm *chain.Manager

		mu             sync.Mutex
		settings       rhp.HostSettings
		activeSettings map[rhp.SettingsID]rhp.HostSettings
	}

	// A Host is an ephemeral host that can be used for testing.
	Host struct {
		privkey types.PrivateKey

		cm       *chain.Manager
		wallet   host.Wallet
		tpool    host.TransactionPool
		settings *settingsManager

		accounts  host.EphemeralAccountStore
		contracts host.ContractManager
		registry  *host.RegistryManager
		sectors   host.SectorStore
		recorder  MetricRecorder
		log       Logger

		listener net.Listener
	}
)

// valid returns the settings with the given UID, if they exist.
func (s *settingsManager) valid(id rhp.SettingsID) (rhp.HostSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings, exists := s.activeSettings[id]
	if !exists {
		return rhp.HostSettings{}, errors.New("not found")
	}
	return settings, nil
}

// register registers the setting's UID with the session handler for
// renters to reference in other RPC.
func (s *settingsManager) register(id rhp.SettingsID, settings rhp.HostSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeSettings[id] = settings
	time.AfterFunc(time.Until(settings.ValidUntil), func() {
		s.mu.Lock()
		delete(s.activeSettings, id)
		s.mu.Unlock()
	})
}

// UpdateSettings updates the host's settings. Implements host.SettingsReporter.
func (s *settingsManager) UpdateSettings(settings rhp.HostSettings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings = settings
}

// Settings returns the host's current settings. Implements host.SettingsReporter.
func (s *settingsManager) Settings() rhp.HostSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := s.settings
	settings.BlockHeight = s.cm.Tip().Height
	settings.ValidUntil = time.Now().Add(time.Minute * 10)
	return settings
}

// Serve starts a new renter-host session on the provided conn.
func (h *Host) serve(conn net.Conn) error {
	defer conn.Close()

	s, err := rhp.AcceptSession(conn, h.privkey)
	if err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}

	session := &Session{
		Session: s,

		uid:       generateUniqueID(),
		privkey:   h.privkey,
		cm:        h.cm,
		wallet:    h.wallet,
		tpool:     h.tpool,
		settings:  h.settings,
		accounts:  h.accounts,
		contracts: h.contracts,
		registry:  h.registry,
		sectors:   h.sectors,
		recorder:  h.recorder,
		log:       h.log,
	}

	// record the RHP session start and end
	recordEnd := session.record(conn.RemoteAddr().String())
	defer recordEnd()

	for {
		stream, err := session.AcceptStream()
		if errors.Is(err, mux.ErrClosedConn) || errors.Is(err, mux.ErrPeerClosedConn) {
			return nil
		} else if err != nil {
			return fmt.Errorf("failed to accept stream: %w", err)
		}
		go func() {
			defer stream.Close()

			log := h.log.Scope("host rpc")

			var specifier rpc.Specifier
			if err := rpc.ReadRequest(stream, &specifier); err != nil {
				log.Warnln("failed to read specifier:", err)
				return
			}

			session.handleRPC(stream, specifier)
		}()
	}
}

// Addr returns the host's listener address.
func (h *Host) Addr() string {
	return h.listener.Addr().String()
}

// PublicKey returns the host's public key.
func (h *Host) PublicKey() types.PublicKey {
	return h.privkey.PublicKey()
}

// Settings returns the host's current settings.
func (h *Host) Settings() (settings rhp.HostSettings) {
	return h.settings.Settings()
}

// Close closes the host.
func (h *Host) Close() error {
	return h.listener.Close()
}

// New initializes a new host.
func New(addr string, privkey types.PrivateKey, settings rhp.HostSettings, cm *chain.Manager, ss host.SectorStore, cs host.ContractStore, rs host.RegistryStore, es host.EphemeralAccountStore, w host.Wallet, tp host.TransactionPool, r MetricRecorder, log Logger) (*Host, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	settings.NetAddress = listener.Addr().String()
	settings.Address = w.Address()
	contracts := &ContractManager{
		store:   cs,
		sectors: ss,
		tpool:   tp,
		wallet:  w,
		cm:      cm,
		log:     log.Scope("contract manager"),
		locks:   make(map[types.ElementID]*locker),
	}
	cm.AddSubscriber(contracts, cm.Tip())
	h := &Host{
		privkey: privkey,

		cm:        cm,
		wallet:    w,
		tpool:     tp,
		accounts:  es,
		contracts: contracts,
		settings: &settingsManager{
			cm:             cm,
			settings:       settings,
			activeSettings: make(map[rhp.SettingsID]rhp.HostSettings),
		},
		registry: host.NewRegistryManager(privkey, rs),
		sectors:  ss,
		recorder: r,

		listener: listener,
		log:      log,
	}
	// start listening for incoming RHP connections
	go func() {
		for {
			conn, err := listener.Accept()
			if errors.Is(err, net.ErrClosed) {
				return
			} else if err != nil {
				panic(err)
			}
			go h.serve(conn)
		}
	}()
	return h, nil
}
