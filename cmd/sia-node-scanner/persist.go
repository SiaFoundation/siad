package main

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	siaPersist "gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

const metadataHeader = "SiaNodeScanner Persisted Node Set"
const metadataVersion = "0.0.1"

type persistData struct {
	// Keep track of last time we successfully connected to each node.
	// Maps IP address to Unix timestamp.
	NodeSet map[modules.NetAddress]int64
}

// Persist maintains a persist file that contains the json-encoded
// PersistData.
type persist struct {
	metadata    siaPersist.Metadata
	persistFile string

	data persistData
}

func newPersist(persistFile string) (p *persist, err error) {
	p = new(persist)

	p.persistFile = persistFile
	p.metadata = siaPersist.Metadata{
		Header:  metadataHeader,
		Version: metadataVersion,
	}

	p.data = persistData{make(map[modules.NetAddress]int64)}

	// Try loading the persist file.
	err = siaPersist.LoadJSON(p.metadata, &p.data, p.persistFile)
	if errors.IsOSNotExist(err) {
		// Ignore the error if the file doesn't exist yet.
		// It will be created when saved for the first time.
		return p, nil
	}

	return p, err
}

func (p *persist) persistData() error {
	return siaPersist.SaveJSON(p.metadata, p.data, p.persistFile)
}
