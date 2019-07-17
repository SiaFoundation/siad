package main

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	siaPersist "gitlab.com/NebulousLabs/Sia/persist"
)

const testPersistFile = "testdata/persisted-node-set.json"

func TestLoad(t *testing.T) {
	data := persistData{
		StartTime: time.Now().Unix(),
		NodeStats: make(map[modules.NetAddress]nodeStats),
	}

	err := siaPersist.LoadJSON(metadata, &data, testPersistFile)
	if err != nil {
		t.Fatal("Error loading persisted node set: ", err)
	}

	// Make sure StartTime has a reasonable (i.e. nonzero) value.
	if data.StartTime == 0 {
		t.Fatal("Expected nonzero StartTime value")
	}

	// Make sure the data set is non-empty.
	if len(data.NodeStats) == 0 {
		t.Fatal("Expected nonzero NodeStats")
	}

	// Check that all structs have nonzero values.
	// This makes sure that we exported all the fields for NodeStats.
	ok := true
	for addr, nodeStats := range data.NodeStats {
		if addr == "" {
			ok = false
		}
		if nodeStats.FirstConnectionTime == 0 {
			ok = false
		}
		if nodeStats.LastSuccessfulConnectionTime == 0 {
			ok = false
		}
		if nodeStats.RecentUptime == 0 {
			ok = false
		}
		if nodeStats.TotalUptime == 0 {
			ok = false
		}
		if nodeStats.UptimePercentage == 0.0 {
			ok = false
		}
		if !ok {
			t.Fatal("Expected nonzero fields in NodeStats: ", addr, nodeStats)
		}
	}
}
