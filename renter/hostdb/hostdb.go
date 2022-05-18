package hostdb

import (
	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"
)

// Host represents a host in the host database.
type Host struct {
	PublicKey  types.PublicKey
	NetAddress string
	Score      float64
}

// Store stores host information.
type Store interface {
	AddHost(h Host) error
	RemoveHost(pk types.PublicKey) error
	Host(pk types.PublicKey) (Host, error)
	Close() error
}

// DB stores host information in a store.
type DB struct {
	store Store
}

// New returns a new DB.
func New(store Store) *DB {
	return &DB{store}
}

func findAnnouncements(b types.Block) (hosts []Host) {
	const announcementAttestationKey = "Host Announcement"
	for _, txn := range b.Transactions {
		for _, a := range txn.Attestations {
			if a.Key == announcementAttestationKey {
				hosts = append(hosts, Host{a.PublicKey, string(a.Value), 0})
			}
		}
	}
	return
}

// ProcessChainApplyUpdate implements chain.Subscriber.
func (db *DB) ProcessChainApplyUpdate(cau *chain.ApplyUpdate, mayCommit bool) error {
	for _, h := range findAnnouncements(cau.Block) {
		if err := db.store.AddHost(h); err != nil {
			return err
		}
	}
	return nil
}

// ProcessChainRevertUpdate implements chain.Subscriber.
func (db *DB) ProcessChainRevertUpdate(cru *chain.RevertUpdate) error {
	for _, h := range findAnnouncements(cru.Block) {
		if err := db.store.RemoveHost(h.PublicKey); err != nil {
			return err
		}
	}
	return nil
}

// Host returns the host with the given public key.
func (db *DB) Host(pk types.PublicKey) (Host, error) {
	return db.store.Host(pk)
}

// Close closes the database.
func (db *DB) Close() error {
	return db.store.Close()
}
