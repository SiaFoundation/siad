package main

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	siaPersist "gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

const metadataHeader = "SiaNodeScanner Persisted Node Set"
const metadataVersion = "0.0.1"

var metadata = siaPersist.Metadata{
	Header:  metadataHeader,
	Version: metadataVersion,
}

// persist maintains a persist file that contains the json-encoded
// persistData.
type persist struct {
	metadata    siaPersist.Metadata
	persistFile string

	data persistData
}

type persistData struct {
	// StartTime is the Unix timestamp of the first scan's start.
	StartTime int64

	// Keep connection time and uptime stats for each node.
	NodeStats map[modules.NetAddress]nodeStats
}

type nodeStats struct {
	// Unix timestamp of first succesful connection to this node.
	// Used for total uptime and uptime percentage calculations.
	FirstConnectionTime int64

	// Keep track of last time we successfully connected to each node.
	LastSuccessfulConnectionTime int64

	// RecentUptime counts the number of seconds since the node was
	// last down (or since first time scanned if it hasn't failed yet).
	RecentUptime int64

	// TotalUptime counts the total number of seconds this node has been up since
	// the time of its first scan.
	TotalUptime int64

	// UptimePercentage is TotalUptime divided by time since
	// FirstConnectionTime.
	UptimePercentage float64
}

func (p *persist) updateNodeStats(res nodeScanResult) {
	stats, ok := p.data.NodeStats[res.Addr]

	// If this node isn't in the persisted set, initalize it.
	if !ok {
		stats = nodeStats{
			FirstConnectionTime:          res.Timestamp,
			LastSuccessfulConnectionTime: res.Timestamp,
			RecentUptime:                 1,
			TotalUptime:                  1,
			UptimePercentage:             100.0,
		}
		p.data.NodeStats[res.Addr] = stats
		return
	}

	// Update stats and uptime percentage.
	if res.Err != nil {
		stats.RecentUptime = 0
	} else {
		timeElapsed := res.Timestamp - stats.LastSuccessfulConnectionTime
		stats.LastSuccessfulConnectionTime = res.Timestamp
		stats.RecentUptime += timeElapsed
		stats.TotalUptime += timeElapsed
	}
	// Subtract 1 from TotalUptime because we give everyone an extra second to
	// start. This makes sure the uptime rate isn't higher than 1.
	stats.UptimePercentage = 100.0 * float64(stats.TotalUptime-1) / float64(res.Timestamp-stats.FirstConnectionTime)

	p.data.NodeStats[res.Addr] = stats
}

func newPersist(persistFile string) (p *persist, err error) {
	p = new(persist)

	p.persistFile = persistFile
	p.metadata = metadata
	p.data = persistData{
		StartTime: time.Now().Unix(),
		NodeStats: make(map[modules.NetAddress]nodeStats),
	}

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
