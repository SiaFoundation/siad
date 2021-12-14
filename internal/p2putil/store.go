package p2putil

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// EphemeralStore implements p2p.SyncerStore in memory.
type EphemeralStore struct {
	mu    sync.Mutex
	peers map[string]struct{}
}

// AddPeer implements p2p.SyncerStore.
func (s *EphemeralStore) AddPeer(addr string) error {
	s.mu.Lock()
	s.peers[addr] = struct{}{}
	s.mu.Unlock()
	return nil
}

// RandomPeer implements p2p.SyncerStore.
func (s *EphemeralStore) RandomPeer() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for peer := range s.peers {
		return peer, nil
	}
	return "", errors.New("no peers in list")
}

// RandomPeers implements p2p.SyncerStore.
func (s *EphemeralStore) RandomPeers(n int) (peers []string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for peer := range s.peers {
		if n <= 0 {
			break
		}
		peers = append(peers, peer)
		n--
	}
	return
}

// NewEphemeralStore returns a new EphemeralStore.
func NewEphemeralStore() *EphemeralStore {
	return &EphemeralStore{
		peers: make(map[string]struct{}),
	}
}

// JSONStore implements p2p.SyncerStore in memory, backed by a JSON file.
type JSONStore struct {
	*EphemeralStore
	dir string
}

// AddPeer implements p2p.SyncerStore.
func (s *JSONStore) AddPeer(addr string) error {
	s.EphemeralStore.AddPeer(addr)
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
