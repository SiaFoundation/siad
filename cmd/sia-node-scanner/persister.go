package main

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"log"
	"strings"
)

// TODO: Remove nodes that haven't been connected to in over 30 days.

const MetadataHeader = "SiaNodeScanner Persisted Node Set"
const MetadataVersion = "0.0.1"

type PersistData struct {
	// Keep track of last time we successfully connected to each node.
	// Maps IP address to Unix timestamp.
	NodeSet map[modules.NetAddress]int64
}

// TODO: comment
type Persister struct {
	metadata    persist.Metadata
	persistFile string

	data PersistData
}

func NewPersister(persistFile string) (p *Persister, err error) {
	p = new(Persister)

	p.persistFile = persistFile
	p.metadata = persist.Metadata{
		Header:  MetadataHeader,
		Version: MetadataVersion,
	}

	p.data = PersistData{make(map[modules.NetAddress]int64)}

	// TODO: Figure out why os.IsNotExist did not work here!
	err = persist.LoadJSON(p.metadata, &p.data, p.persistFile)
	if err != nil && strings.Contains(err.Error(), "no such file") {
		log.Println("IS NOT EXIT")
		return p, nil
	}

	return p, err
}

func (p *Persister) PersistData() error {
	return persist.SaveJSON(p.metadata, p.data, p.persistFile)
}
