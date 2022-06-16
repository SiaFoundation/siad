package hostdbutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/hostdb"
)

func forEachAnnouncement(b types.Block, fn func(types.PublicKey, hostdb.Announcement)) {
	for _, txn := range b.Transactions {
		for _, a := range txn.Attestations {
			if a.Key == "Host Announcement" {
				fn(a.PublicKey, hostdb.Announcement{
					Index:      b.Index(),
					Timestamp:  b.Header.Timestamp,
					NetAddress: string(a.Value),
				})
			}
		}
	}
}

// EphemeralDB implements a HostDB in memory.
type EphemeralDB struct {
	hosts map[types.PublicKey]hostdb.Host
	mu    sync.Mutex
}

func (db *EphemeralDB) modifyHost(hostKey types.PublicKey, fn func(*hostdb.Host)) {
	h, ok := db.hosts[hostKey]
	if !ok {
		h = hostdb.Host{PublicKey: hostKey}
	}
	fn(&h)
	db.hosts[hostKey] = h
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (db *EphemeralDB) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	forEachAnnouncement(cau.Block, func(hostKey types.PublicKey, ann hostdb.Announcement) {
		db.modifyHost(hostKey, func(h *hostdb.Host) {
			// skip duplicate announcements
			for i := range h.Announcements {
				if ann.NetAddress == h.Announcements[i].NetAddress {
					return
				}
			}
			h.Announcements = append(h.Announcements, ann)
		})
	})
	return nil
}

// ProcessChainRevertUpdate implements chain.Subscriber.
func (db *EphemeralDB) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	// Perhaps surprisingly, we do not have to do anything if a block is
	// reverted. Although any announcements in the block will no longer be in
	// the chain, that doesn't really matter; there's no rule that hosts in the
	// DB *have* to come from the blockchain.
	return nil
}

// Host returns information about a host.
func (db *EphemeralDB) Host(hostKey types.PublicKey) hostdb.Host {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.hosts[hostKey]
}

// RecordInteraction records an interaction with a host. If the host is not in
// the store, a new entry is created for it.
func (db *EphemeralDB) RecordInteraction(hostKey types.PublicKey, hi hostdb.Interaction) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.modifyHost(hostKey, func(h *hostdb.Host) {
		h.Interactions = append(h.Interactions, hi)
	})
	return nil
}

// SetScore sets the score associated with the specified host. If the host is
// not in the store, a new entry is created for it.
func (db *EphemeralDB) SetScore(hostKey types.PublicKey, score float64) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.modifyHost(hostKey, func(h *hostdb.Host) {
		h.Score = score
	})
	return nil
}

// SelectHosts returns up to n hosts for which the supplied filter returns true.
func (db *EphemeralDB) SelectHosts(n int, filter func(hostdb.Host) bool) []hostdb.Host {
	db.mu.Lock()
	defer db.mu.Unlock()
	var hosts []hostdb.Host
	for _, host := range db.hosts {
		if len(hosts) == n {
			break
		} else if filter(host) {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// NewEphemeralDB returns a new EphemeralDB.
func NewEphemeralDB() *EphemeralDB {
	return &EphemeralDB{
		hosts: make(map[types.PublicKey]hostdb.Host),
	}
}

// JSONDB implements a HostDB in memory, backed by a JSON file.
type JSONDB struct {
	*EphemeralDB
	tip types.ChainIndex
	dir string
}

type jsonPersistData struct {
	Tip   types.ChainIndex
	Hosts map[types.PublicKey]hostdb.Host
}

func (db *JSONDB) save() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	js, _ := json.MarshalIndent(jsonPersistData{
		Tip:   db.tip,
		Hosts: db.hosts,
	}, "", "  ")

	dst := filepath.Join(db.dir, "hostdb.json")
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

func (db *JSONDB) load(tip types.ChainIndex) (types.ChainIndex, error) {
	var p jsonPersistData
	if js, err := os.ReadFile(filepath.Join(db.dir, "hostdb.json")); os.IsNotExist(err) {
		db.tip = tip
		return tip, nil
	} else if err != nil {
		return types.ChainIndex{}, err
	} else if err := json.Unmarshal(js, &p); err != nil {
		return types.ChainIndex{}, err
	}
	db.tip = tip
	return p.Tip, nil
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (db *JSONDB) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	db.EphemeralDB.ProcessChainApplyUpdate(cau, mayCommit)
	if mayCommit {
		return db.save()
	}
	return nil
}

// RecordInteraction records an interaction with a host. If the host is not in
// the store, a new entry is created for it.
func (db *JSONDB) RecordInteraction(hostKey types.PublicKey, hi hostdb.Interaction) error {
	db.EphemeralDB.RecordInteraction(hostKey, hi)
	return db.save()
}

// SetScore sets the score associated with the specified host. If the host is
// not in the store, a new entry is created for it.
func (db *JSONDB) SetScore(hostKey types.PublicKey, score float64) error {
	db.EphemeralDB.SetScore(hostKey, score)
	return db.save()
}

// NewJSONDB returns a new JSONDB.
func NewJSONDB(dir string, initialIndex types.ChainIndex) (*JSONDB, types.ChainIndex, error) {
	db := &JSONDB{
		EphemeralDB: NewEphemeralDB(),
		dir:         dir,
	}
	tip, err := db.load(initialIndex)
	if err != nil {
		return nil, types.ChainIndex{}, err
	}
	return db, tip, nil
}
