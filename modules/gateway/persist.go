package gateway

import (
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// logFile is the name of the log file.
	logFile = modules.GatewayDir + ".log"

	// nodesFile is the name of the file that contains all seen nodes.
	nodesFile = "nodes.json"

	// persistFilename is the filename to be used when persisting gateway information to a JSON file
	persistFilename = "gateway.json"
)

// nodePersistMetadata contains the header and version strings that identify the
// node persist file.
var nodePersistMetadata = persist.Metadata{
	Header:  "Sia Node List",
	Version: "1.3.0",
}

// persistMetadata contains the header and version strings that identify the
// gateway persist file.
var persistMetadata = persist.Metadata{
	Header:  "Gateway Persistence",
	Version: "1.3.5",
}

// persistMetadataBlacklist contains the header and version strings that
// identify the gateway persist file.
var persistMetadataBlacklist = persist.Metadata{
	Header:  "Sia Node Blacklist",
	Version: "1.4.1",
}

type (
	// persist contains all of the persistent gateway data.
	persistence struct {
		RouterURL string

		// rate limit settings
		MaxDownloadSpeed int64
		MaxUploadSpeed   int64

		// blacklisted IPs
		Blacklist []string
	}
)

// nodePersistData returns the node data in the Gateway that will be saved to disk.
func (g *Gateway) nodePersistData() (nodes []*node) {
	for _, node := range g.nodes {
		nodes = append(nodes, node)
	}
	return
}

// load loads the Gateway's persistent data from disk.
func (g *Gateway) load() error {
	// load nodes
	var nodes []*node
	var v130 bool
	err := persist.LoadJSON(nodePersistMetadata, &nodes, filepath.Join(g.persistDir, nodesFile))
	if err != nil {
		// COMPATv1.3.0
		compatErr := g.loadv033persist()
		if compatErr != nil {
			return err
		}
		v130 = true
	}
	for i := range nodes {
		g.nodes[nodes[i].NetAddress] = nodes[i]
	}

	// If we were loading a 1.3.0 gateway we are done. It doesn't have a
	// gateway.json.
	if v130 {
		return nil
	}

	// load g.persist
	err = persist.LoadJSON(persistMetadata, &g.persist, filepath.Join(g.persistDir, persistFilename))
	if err != nil {
		return errors.AddContext(err, "failed to load gateway persistence")
	}
	// create map from blacklist
	for _, ip := range g.persist.Blacklist {
		g.blacklist[ip] = struct{}{}
	}
	return nil
}

// saveSync stores the Gateway's persistent data on disk, and then syncs to
// disk to minimize the possibility of data loss.
func (g *Gateway) saveSync() error {
	return persist.SaveJSON(persistMetadata, g.persist, filepath.Join(g.persistDir, persistFilename))
}

// saveSyncNodes stores the Gateway's persistent node data on disk, and then
// syncs to disk to minimize the possibility of data loss.
func (g *Gateway) saveSyncNodes() error {
	return persist.SaveJSON(nodePersistMetadata, g.nodePersistData(), filepath.Join(g.persistDir, nodesFile))
}

// threadedSaveLoop periodically saves the gateway nodes.
func (g *Gateway) threadedSaveLoop() {
	for {
		select {
		case <-g.threads.StopChan():
			return
		case <-time.After(saveFrequency):
		}

		func() {
			err := g.threads.Add()
			if err != nil {
				return
			}
			defer g.threads.Done()

			g.mu.Lock()
			err = g.saveSyncNodes()
			g.mu.Unlock()
			if err != nil {
				g.log.Println("ERROR: Unable to save gateway nodes:", err)
			}
		}()
	}
}

// updateBlacklistPersistData updates g.persist.Blacklist to match the blacklist
// map.
func (g *Gateway) updateBlacklistPersistData() {
	g.persist.Blacklist = make([]string, 0, len(g.blacklist))
	for ip := range g.blacklist {
		g.persist.Blacklist = append(g.persist.Blacklist, ip)
	}
}

// loadv033persist loads the v0.3.3 Gateway's persistent node data from disk.
func (g *Gateway) loadv033persist() error {
	var nodes []modules.NetAddress
	err := persist.LoadJSON(persist.Metadata{
		Header:  "Sia Node List",
		Version: "0.3.3",
	}, &nodes, filepath.Join(g.persistDir, nodesFile))
	if err != nil {
		return err
	}
	for _, addr := range nodes {
		err := g.addNode(addr)
		if err != nil {
			g.log.Printf("WARN: error loading node '%v' from persist: %v", addr, err)
		}
	}
	return nil
}
