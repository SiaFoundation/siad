package hostdb

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

// Announcement represents a host announcement in a given block.
type Announcement struct {
	Index      types.ChainIndex
	Timestamp  time.Time
	NetAddress string
}

// Interaction represents an interaction with a host at a given time.
type Interaction struct {
	Timestamp time.Time
	Type      string
	Success   bool
	Result    json.RawMessage
}

// A Host pairs a host's public key with a score and a set of interactions.
type Host struct {
	PublicKey     types.PublicKey
	Score         float64
	Announcements []Announcement
	Interactions  []Interaction
}

// NetAddress returns the host's last announced NetAddress, if available.
func (h *Host) NetAddress() string {
	if len(h.Announcements) == 0 {
		return ""
	}
	return h.Announcements[len(h.Announcements)-1].NetAddress
}

// LastKnownSettings returns the host's last known settings, if available. Note
// that the settings may no longer be valid.
func (h *Host) LastKnownSettings() (rhp.HostSettings, bool) {
	for i := len(h.Interactions) - 1; i >= 0; i-- {
		if hi := h.Interactions[i]; hi.Type == "scan" && hi.Success {
			var settings rhp.HostSettings
			if json.Unmarshal(hi.Result, &settings) == nil {
				return settings, true
			}
		}
	}
	return rhp.HostSettings{}, false
}

// SimpleContractFilter returns a filter (for use with SelectHosts) that rejects
// hosts without a net address, hosts with no known settings, hosts that
// are not accepting contracts, and hosts whose maximum contract duration is
// shorter than contractDuration.
func SimpleContractFilter(contractDuration uint64) func(Host) bool {
	return func(h Host) bool {
		settings, hasSettings := h.LastKnownSettings()
		return h.NetAddress() != "" &&
			hasSettings &&
			settings.AcceptingContracts &&
			settings.MaxDuration >= contractDuration
	}
}

// closeOnCancel spawns a goroutine that closes c if ctx is cancelled. It
// returns a function that should be called in a defer statement to terminate
// the goroutine and (if the ctx was cancelled) set the provided error to
// ctx.Err().
func closeOnCancel(ctx context.Context, c io.Closer, err *error) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-ctx.Done():
			c.Close()
		}
	}()
	return func() {
		if ctx.Err() != nil {
			*err = ctx.Err()
		}
		close(done)
	}
}

// ScanHost scans the specified host, returning its reported settings.
func ScanHost(ctx context.Context, hostIP string, hostKey types.PublicKey) (settings rhp.HostSettings, err error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", hostIP)
	if err != nil {
		return rhp.HostSettings{}, err
	}
	defer conn.Close()
	defer closeOnCancel(ctx, conn, &err)

	sess, err := rhp.DialSession(conn, hostKey)
	if err != nil {
		return rhp.HostSettings{}, err
	}
	defer sess.Close()
	stream := sess.DialStream()
	defer stream.Close()
	if err := rpc.WriteRequest(stream, rhp.RPCSettingsID, nil); err != nil {
		return rhp.HostSettings{}, err
	}
	var resp rhp.RPCSettingsResponse
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return rhp.HostSettings{}, err
	} else if err := json.Unmarshal(resp.Settings, &settings); err != nil {
		return rhp.HostSettings{}, err
	}
	return settings, nil
}
