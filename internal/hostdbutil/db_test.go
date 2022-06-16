package hostdbutil

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/hostdb"
	"go.sia.tech/siad/v2/internal/chainutil"
)

type hostDB interface {
	chain.Subscriber
	RecordInteraction(hostKey types.PublicKey, hi hostdb.Interaction) error
	SetScore(hostKey types.PublicKey, score float64) error
	SelectHosts(n int, filter func(hostdb.Host) bool) []hostdb.Host
	Host(hostKey types.PublicKey) hostdb.Host
}

func TestDBs(t *testing.T) {
	sim := chainutil.NewChainSim()

	ephemeralDB := NewEphemeralDB()
	dir, err := os.MkdirTemp(os.TempDir(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	jsonDB, _, err := NewJSONDB(dir, types.ChainIndex{})
	if err != nil {
		t.Fatal(err)
	}

	for _, db := range []hostDB{ephemeralDB, jsonDB} {
		cm := chain.NewManager(chainutil.NewEphemeralStore(sim.Genesis), sim.State)
		cm.AddSubscriber(db, cm.Tip())

		const netAddress = "127.0.0.1:9999"
		txn := types.Transaction{
			Attestations: []types.Attestation{{
				Key:   "Host Announcement",
				Value: []byte(netAddress),
			}},
		}

		b := sim.MineBlockWithTxns(txn)
		if err := cm.AddTipBlock(b); err != nil {
			t.Fatal(err)
		}

		hosts := db.SelectHosts(math.MaxInt64, func(hostdb.Host) bool { return true })

		if len(hosts) != 1 {
			t.Fatalf("expected only 1 host, got %d", len(hosts))
		}

		pk := hosts[0].PublicKey

		expected := hostdb.Host{
			PublicKey: pk,
			Score:     0,
			Announcements: []hostdb.Announcement{{
				Index:      b.Index(),
				Timestamp:  b.Header.Timestamp,
				NetAddress: netAddress,
			}},
			Interactions: nil,
		}
		if !reflect.DeepEqual(expected, db.Host(pk)) {
			t.Fatalf("expected host %+v, got %+v", expected, db.Host(pk))
		}

		if err := db.SetScore(pk, 100); err != nil {
			t.Fatal(err)
		}
		expected.Score = 100
		if !reflect.DeepEqual(expected, db.Host(pk)) {
			t.Fatalf("expected host %+v, got %+v", expected, db.Host(pk))
		}

		settings := rhp.HostSettings{AcceptingContracts: true}
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		interaction := hostdb.Interaction{
			Timestamp: time.Now(),
			Type:      "scan",
			Success:   true,
			Result:    data,
		}
		if err := db.RecordInteraction(pk, interaction); err != nil {
			t.Fatal(err)
		}
		expected.Interactions = append(expected.Interactions, interaction)

		if !reflect.DeepEqual(expected, db.Host(pk)) {
			t.Fatalf("expected host %+v, got %+v", expected, db.Host(pk))
		}

		host := db.Host(pk)
		if host.NetAddress() != netAddress {
			t.Fatalf("expected net address %s, got %s", netAddress, host.NetAddress())
		}

		hostSettings, ok := host.LastKnownSettings()
		if !ok {
			t.Fatal("host has no settings")
		}
		if hostSettings.AcceptingContracts != true {
			t.Fatalf("expected host to be accepting contracts")
		}
	}
}
