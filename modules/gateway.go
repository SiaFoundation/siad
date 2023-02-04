package modules

import (
	"net"
	"time"

	"go.sia.tech/siad/build"
)

const (
	// GatewayDir is the name of the directory used to store the gateway's
	// persistent data.
	GatewayDir = "gateway"
)

var (
	// BootstrapPeers is a list of peers that can be used to find other peers -
	// when a client first connects to the network, the only options for
	// finding peers are either manual entry of peers or to use a hardcoded
	// bootstrap point. While the bootstrap point could be a central service,
	// it can also be a list of peers that are known to be stable. We have
	// chosen to hardcode known-stable peers.
	//
	// These peers have been verified to be v1.5.4 or higher
	BootstrapPeers = build.Select(build.Var{
		Standard: []NetAddress{
			"82.65.206.23:9981",
			"135.181.208.120:9981",
			"176.106.59.120:9981",
			"144.91.127.102:9981",
			"109.195.83.186:9981",
			"96.60.27.112:5151",
			"144.217.7.188:9981",
			"5.9.19.246:9981",
			"94.110.4.134:9981",
			"208.88.8.166:5151",
			"139.162.81.190:9981",
			"5.19.177.22:9981",
			"13.113.190.118:9981",
			"134.209.89.131:9981",
			"176.104.8.173:9981",
			"81.25.225.178:9981",
			"147.135.23.200:9981",
			"76.216.70.162:9981",
			"72.8.228.138:9981",
			"164.138.31.105:9981",
			"78.69.28.182:9861",
			"176.215.255.127:9981",
			"72.69.188.134:9981",
			"71.237.62.44:9981",
			"95.211.140.149:9981",
			"5.196.66.75:9981",
			"2.26.62.61:9981",
			"88.98.208.124:9981",
			"50.116.14.37:9981",
			"173.235.144.230:9981",
			"73.74.104.175:10031",
			"199.195.252.152:9981",
			"135.181.142.113:9981",
			"148.251.152.35:9981",
			"148.251.125.117:9981",
			"136.52.87.46:9981",
			"82.217.213.145:9981",
			"81.7.16.159:9681",
			"146.52.104.39:9981",
			"78.197.237.216:9981",
			"162.196.91.121:9981",
			"148.251.82.174:9981",
			"65.21.79.100:9981",
			"103.76.41.143:9981",
			"81.98.132.81:9981",
			"109.236.92.161:9981",
			"167.86.109.162:9981",
			"139.162.187.240:9991",
			"92.83.254.237:9791",
			"80.101.32.17:9981",
			"81.196.138.172:11152",
			"50.54.136.187:9981",
			"5.39.76.82:9981",
			"92.90.91.29:9981",
			"116.202.87.160:9981",
			"13.212.22.103:9981",
			"5.141.81.96:9981",
			"66.176.169.2:9981",
			"92.58.23.92:9981",
			"149.248.110.111:9981",
			"142.182.59.54:9981",
			"190.111.196.118:9981",
			"54.38.120.222:9981",
			"109.195.166.133:9981",
			"172.104.15.208:9981",
			"65.30.133.139:9981",
			"95.217.180.130:9981",
			"82.223.202.234:9981",
			"82.64.236.171:9981",
			"188.122.0.242:9991",
			"121.41.105.53:9981",
			"161.97.176.97:9981",
			"71.178.248.177:9981",
			"89.69.17.157:9981",
			"63.141.234.114:9981",
		},
		Testnet: []NetAddress{
			"147.135.16.182:9881",
			"147.135.39.109:9881",
			"73.229.132.74:9881",
			"51.81.208.10:9881",
		},
		Dev:     []NetAddress(nil),
		Testing: []NetAddress(nil),
	}).([]NetAddress)
)

type (
	// Peer contains all the info necessary to Broadcast to a peer.
	Peer struct {
		Inbound    bool       `json:"inbound"`
		Local      bool       `json:"local"`
		NetAddress NetAddress `json:"netaddress"`
		Version    string     `json:"version"`
	}

	// A PeerConn is the connection type used when communicating with peers during
	// an RPC. It is identical to a net.Conn with the additional RPCAddr method.
	// This method acts as an identifier for peers and is the address that the
	// peer can be dialed on. It is also the address that should be used when
	// calling an RPC on the peer.
	PeerConn interface {
		net.Conn
		RPCAddr() NetAddress
	}

	// RPCFunc is the type signature of functions that handle RPCs. It is used for
	// both the caller and the callee. RPCFuncs may perform locking. RPCFuncs may
	// close the connection early, and it is recommended that they do so to avoid
	// keeping the connection open after all necessary I/O has been performed.
	RPCFunc func(PeerConn) error

	// A Gateway facilitates the interactions between the local node and remote
	// nodes (peers). It relays incoming blocks and transactions to local modules,
	// and broadcasts outgoing blocks and transactions to peers. In a broad sense,
	// it is responsible for ensuring that the local consensus set is consistent
	// with the "network" consensus set.
	Gateway interface {
		Alerter

		// BandwidthCounters returns the Gateway's upload and download bandwidth
		BandwidthCounters() (uint64, uint64, time.Time, error)

		// Connect establishes a persistent connection to a peer.
		Connect(NetAddress) error

		// ConnectManual is a Connect wrapper for a user-initiated Connect
		ConnectManual(NetAddress) error

		// Disconnect terminates a connection to a peer.
		Disconnect(NetAddress) error

		// DiscoverAddress discovers and returns the current public IP address
		// of the gateway. Contrary to Address, DiscoverAddress is blocking and
		// might take multiple minutes to return. A channel to cancel the
		// discovery can be supplied optionally.
		DiscoverAddress(cancel <-chan struct{}) (net.IP, error)

		// ForwardPort adds a port mapping to the router. It will block until
		// the mapping is established or until it is interrupted by a shutdown.
		ForwardPort(port string) error

		// DisconnectManual is a Disconnect wrapper for a user-initiated
		// disconnect
		DisconnectManual(NetAddress) error

		// AddToBlocklist adds addresses to the blocklist of the gateway
		AddToBlocklist(addresses []string) error

		// Blocklist returns the current blocklist of the Gateway
		Blocklist() ([]string, error)

		// RemoveFromBlocklist removes addresses from the blocklist of the
		// gateway
		RemoveFromBlocklist(addresses []string) error

		// SetBlocklist sets the blocklist of the gateway
		SetBlocklist(addresses []string) error

		// Address returns the Gateway's address.
		Address() NetAddress

		// Peers returns the addresses that the Gateway is currently connected
		// to.
		Peers() []Peer

		// RegisterRPC registers a function to handle incoming connections that
		// supply the given RPC ID.
		RegisterRPC(string, RPCFunc)

		// RateLimits returns the currently set bandwidth limits of the gateway.
		RateLimits() (int64, int64)

		// SetRateLimits changes the rate limits for the peer-connections of the
		// gateway.
		SetRateLimits(downloadSpeed, uploadSpeed int64) error

		// UnregisterRPC unregisters an RPC and removes all references to the
		// RPCFunc supplied in the corresponding RegisterRPC call. References to
		// RPCFuncs registered with RegisterConnectCall are not removed and
		// should be removed with UnregisterConnectCall. If the RPC does not
		// exist no action is taken.
		UnregisterRPC(string)

		// RegisterConnectCall registers an RPC name and function to be called
		// upon connecting to a peer.
		RegisterConnectCall(string, RPCFunc)

		// UnregisterConnectCall unregisters an RPC and removes all references to the
		// RPCFunc supplied in the corresponding RegisterConnectCall call. References
		// to RPCFuncs registered with RegisterRPC are not removed and should be
		// removed with UnregisterRPC. If the RPC does not exist no action is taken.
		UnregisterConnectCall(string)

		// RPC calls an RPC on the given address. RPC cannot be called on an
		// address that the Gateway is not connected to.
		RPC(NetAddress, string, RPCFunc) error

		// Broadcast transmits obj, prefaced by the RPC name, to all of the
		// given peers in parallel.
		Broadcast(name string, obj interface{}, peers []Peer)

		// Online returns true if the gateway is connected to remote hosts
		Online() bool

		// Close safely stops the Gateway's listener process.
		Close() error
	}
)
