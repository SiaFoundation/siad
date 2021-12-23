package p2putil

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"go.sia.tech/siad/v2/p2p"
)

// EphemeralStore implements p2p.PeerStore in memory.
type EphemeralStore struct {
	peers map[string]p2p.PeerInfo
	mu    sync.Mutex
}

// AddPeer implements p2p.PeerStore.
func (s *EphemeralStore) AddPeer(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := p2p.PeerInfo{
		NetAddress: addr,
	}
	if _, ok := s.peers[addr]; !ok {
		s.peers[addr] = p
	}
	return nil
}

// Info implements p2p.PeerStore.
func (s *EphemeralStore) Info(addr string) p2p.PeerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[addr]
	if !ok {
		p = p2p.PeerInfo{
			NetAddress: addr,
		}
	}
	return p
}

// UpdatePeer implements p2p.PeerStore.
func (s *EphemeralStore) UpdatePeer(addr string, fn func(*p2p.PeerInfo)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[addr]
	if !ok {
		return errors.New("unknown peer")
	}
	fn(&p)
	s.peers[addr] = p
	return nil
}

// RandomPeers implements p2p.PeerStore.
func (s *EphemeralStore) RandomPeers(n int, filter func(p2p.PeerInfo) bool) ([]p2p.PeerInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var peers []p2p.PeerInfo
	for _, peer := range s.peers {
		if n <= 0 {
			break
		} else if filter(peer) {
			peers = append(peers, peer)
			n--
		}
	}
	return peers, nil
}

// NewEphemeralStore returns a new EphemeralStore.
func NewEphemeralStore() *EphemeralStore {
	return &EphemeralStore{
		peers: make(map[string]p2p.PeerInfo),
	}
}

// JSONStore implements p2p.PeerStore in memory, backed by a JSON file.
type JSONStore struct {
	*EphemeralStore
	dir string
}

// AddPeer implements p2p.PeerStore.
func (s *JSONStore) AddPeer(addr string) error {
	s.EphemeralStore.AddPeer(addr)
	return s.save()
}

// UpdatePeer implements p2p.PeerStore.
func (s *JSONStore) UpdatePeer(addr string, fn func(*p2p.PeerInfo)) error {
	if err := s.EphemeralStore.UpdatePeer(addr, fn); err != nil {
		return err
	}
	return s.save()
}

func (s *JSONStore) load() error {
	dst := filepath.Join(s.dir, "peers.json")
	f, err := os.Open(dst)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(&s.peers)
}

func (s *JSONStore) save() error {
	js, err := json.MarshalIndent(s.peers, "", "  ")
	if err != nil {
		return err
	}

	dst := filepath.Join(s.dir, "peers.json")
	f, err := os.OpenFile(dst+"_tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0660)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(js); err != nil {
		return err
	} else if f.Sync(); err != nil {
		return err
	} else if f.Close(); err != nil {
		return err
	} else if err := os.Rename(dst+"_tmp", dst); err != nil {
		return err
	}
	return nil
}

// NewJSONStore returns a new JSONStore.
func NewJSONStore(dir string) (*JSONStore, error) {
	s := &JSONStore{
		EphemeralStore: NewEphemeralStore(),
		dir:            dir,
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}
