package main

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

// TODO: Remove nodes that haven't been connected to in over 30 days.

const MetadataHeader = "SiaNodeScanner Persisted Node Set"
const MetadataVersion = "0.0.1"

type PersistData struct {
	// Keep track of last time we successfully connected to each node.
	// Maps IP address to Unix timestamp.
	NodeSet map[modules.NetAddress]int64
}

// Persist maintains a persist file that contains the json-encoded
// PersistData.
type Persist struct {
	metadata    persist.Metadata
	persistFile string

	data PersistData
}

func NewPersist(persistFile string) (p *Persist, err error) {
	p = new(Persist)

	p.persistFile = persistFile
	p.metadata = persist.Metadata{
		Header:  MetadataHeader,
		Version: MetadataVersion,
	}

	p.data = PersistData{make(map[modules.NetAddress]int64)}

	// Try loading the persist file.
	err = persist.LoadJSON(p.metadata, &p.data, p.persistFile)
	if errors.IsOSNotExist(err) {
		// Ignore the error if the file doesn't exist yet.
		// It will be created when saved for the first time.
		return p, nil
	}

	return p, err
}

func (p *Persist) PersistData() error {
	return persist.SaveJSON(p.metadata, p.data, p.persistFile)
}
