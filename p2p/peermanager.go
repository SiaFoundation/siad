package p2p

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// PeerInfo tracks information about a peer.
type PeerInfo struct {
	NetAddress     string
	LastSeen       time.Time
	FailedConnects int
	Banscore       int
	Blacklisted    bool
}

func (peer PeerInfo) isGood() bool {
	const (
		maxBanscore       = 100
		maxFailedConnects = 3
	)
	return !peer.Blacklisted && peer.Banscore <= maxBanscore && peer.FailedConnects <= maxFailedConnects
}

// A PeerStore stores peer information.
type PeerStore interface {
	AddPeer(addr string) error
	Info(addr string) PeerInfo
	UpdatePeer(addr string, fn func(*PeerInfo)) error
	RandomPeers(max int, filter func(PeerInfo) bool) ([]PeerInfo, error)
}

// A PeerManager tracks information about potential network peers.
type PeerManager struct {
	store PeerStore
	mu    sync.Mutex
}

// AddPeer adds a new peer to the manager.
func (pm *PeerManager) AddPeer(addr string) error {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("invalid peer address: %w", err)
	}
	return pm.store.AddPeer(addr)
}

// Info returns everything known about a given peer.
func (pm *PeerManager) Info(addr string) PeerInfo {
	return pm.store.Info(addr)
}

// RandomGoodPeers returns up to n peers that are likely to be connectable.
func (pm *PeerManager) RandomGoodPeers(n int) []string {
	peers, _ := pm.store.RandomPeers(n, (PeerInfo).isGood)
	var addrs []string
	for _, peer := range peers {
		addrs = append(addrs, peer.NetAddress)
	}
	return addrs
}

// RandomGoodPeer returns a random peer that is likely to be connectable.
func (pm *PeerManager) RandomGoodPeer() (string, error) {
	ps := pm.RandomGoodPeers(1)
	if len(ps) != 1 {
		return "", errors.New("no good peers")
	}
	return ps[0], nil
}

func (pm *PeerManager) update(addr string, fn func(peer *PeerInfo)) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	err := pm.store.UpdatePeer(addr, fn)
	_ = err // TODO: log?
}

// NoteSeen updates the peers last seen time to the current time.
func (pm *PeerManager) NoteSeen(addr string) {
	pm.update(addr, func(peer *PeerInfo) { peer.LastSeen = time.Now() })
}

// NoteFailedConnection increments the failed connects count of a peer.
func (pm *PeerManager) NoteFailedConnection(addr string) {
	pm.update(addr, func(peer *PeerInfo) { peer.FailedConnects++ })
}

// IncreaseBanscore increases the banscore of a peer by the specified amount.
func (pm *PeerManager) IncreaseBanscore(addr string, score int) {
	pm.update(addr, func(peer *PeerInfo) { peer.Banscore += score })
}

// Blacklist blacklists a peer.
func (pm *PeerManager) Blacklist(addr string) {
	pm.update(addr, func(peer *PeerInfo) { peer.Blacklisted = true })
}

// NewPeerManager initializes a PeerManager using the provided store.
func NewPeerManager(store PeerStore) *PeerManager {
	return &PeerManager{
		store: store,
	}
}
