package hostdbutil

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/renter/hostdb"
)

// EphemeralStore implements hostdb.Store in memory.
type EphemeralStore struct {
	mu    sync.Mutex
	hosts map[types.PublicKey]hostdb.Host
}

// NewEphemeralStore returns a new EphemeralStore.
func NewEphemeralStore() *EphemeralStore {
	return &EphemeralStore{
		hosts: make(map[types.PublicKey]hostdb.Host),
	}
}

// AddHost implements hostdb.Store.
func (s *EphemeralStore) AddHost(h hostdb.Host) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hosts[h.PublicKey] = h
	return nil
}

// RemoveHost implements hostdb.Store.
func (s *EphemeralStore) RemoveHost(pk types.PublicKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.hosts, pk)
	return nil
}

// Host implements hostdb.Store.
func (s *EphemeralStore) Host(pk types.PublicKey) (hostdb.Host, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.hosts[pk]
	if !ok {
		return hostdb.Host{}, errors.New("no such host")
	}
	return h, nil
}

// Close implements hostdb.Store.
func (s *EphemeralStore) Close() error {
	return nil
}

// JSONStore implements hostdb.Store in memory, backed by a JSON file.
type JSONStore struct {
	*EphemeralStore
	dir string
}

// AddHost implements hostdb.Store.
func (s *JSONStore) AddHost(h hostdb.Host) error {
	s.EphemeralStore.AddHost(h)
	return s.save()
}

// RemoveHost implements hostdb.Store.
func (s *JSONStore) RemoveHost(pk types.PublicKey) error {
	s.EphemeralStore.RemoveHost(pk)
	return s.save()
}

// Close implements hostdb.Store.
func (s *JSONStore) Close() error {
	return s.save()
}

func (s *JSONStore) load() error {
	dst := filepath.Join(s.dir, "hostdb.json")
	f, err := os.Open(dst)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(&s.hosts)
}

func (s *JSONStore) save() error {
	js, err := json.MarshalIndent(s.hosts, "", "  ")
	if err != nil {
		return err
	}

	dst := filepath.Join(s.dir, "hostdb.json")
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
