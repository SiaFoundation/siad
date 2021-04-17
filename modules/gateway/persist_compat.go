package gateway

import (
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/persist"
)

const (
	// nodesFile is the name of the file that contains all seen nodes.
	nodesFile = "nodes.json"
)

// nodePersistMetadata contains the header and version strings that identify the
// node persist file.
var nodePersistMetadata = persist.Metadata{
	Header:  "Sia Node List",
	Version: "1.3.0",
}

// persistMetadatav135 contains the header and version strings that identify the
// gateway persist file for v135 nodes.
var persistMetadatav135 = persist.Metadata{
	Header:  "Gateway Persistence",
	Version: "1.3.5",
}

type (
	// persistencev135 contains all of the persistent gateway data for a v135
	// node.
	persistencev135 struct {
		RouterURL        string
		MaxDownloadSpeed int64
		MaxUploadSpeed   int64
		Blacklist        []string
	}
)

// convertPersistv135Tov150 converts the gateway persistence from v135 to v150
func (g *Gateway) convertPersistv135Tov150() error {
	// Try and load the v135 metadata
	var persistv135 persistencev135
	gatewayPersistFile := filepath.Join(g.persistDir, persistFilename)
	err := persist.LoadJSON(persistMetadatav135, &persistv135, gatewayPersistFile)
	if err != nil {
		return errors.AddContext(err, "failed to load v135 gateway persistence")
	}

	// Load persistence into gateway
	g.persist.RouterURL = persistv135.RouterURL
	g.persist.MaxDownloadSpeed = persistv135.MaxDownloadSpeed
	g.persist.MaxUploadSpeed = persistv135.MaxUploadSpeed
	g.persist.Blocklist = persistv135.Blacklist

	// Save the persist to update the persistence metadata version
	err = persist.SaveJSON(persistMetadata, g.persist, gatewayPersistFile)
	if err != nil {
		return errors.AddContext(err, "failed to save v150 gateway persistence")
	}
	return nil
}
