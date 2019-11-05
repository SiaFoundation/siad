package modules

import (
	"net"

	"gitlab.com/NebulousLabs/Sia/build"
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
	// These peers have been verified to be v1.3.7 or higher
	BootstrapPeers = build.Select(build.Var{
		Standard: []NetAddress{
			"95.78.166.67:9981",
			"68.199.121.249:9981",
			"24.194.148.158:9981",
			"82.231.193.206:9981",
			"185.216.208.214:9981",
			"165.73.59.75:9981",
			"81.5.154.29:9981",
			"68.133.15.97:9981",
			"223.19.102.54:9981",
			"136.52.23.122:9981",
			"45.56.21.129:9981",
			"109.172.42.157:9981",
			"188.244.40.69:9985",
			"176.37.126.147:9981",
			"68.96.80.134:9981",
			"92.255.195.111:9981",
			"88.202.201.30:9981",
			"76.103.83.241:9981",
			"77.132.24.85:9981",
			"81.167.50.168:9981",
			"91.206.15.126:9981",
			"91.231.94.22:9981",
			"212.105.168.207:9981",
			"94.113.86.207:9981",
			"188.242.52.10:9981",
			"94.137.140.40:9981",
			"137.74.1.200:9981",
			"85.27.163.135:9981",
			"46.246.68.66:9981",
			"92.70.88.30:9981",
			"188.68.37.232:9981",
			"153.210.37.241:9981",
			"24.20.240.181:9981",
			"92.154.126.211:9981",
			"45.50.26.222:9981",
			"41.160.218.190:9981",
			"23.175.0.151:9981",
			"109.248.206.13:9981",
			"222.161.26.222:9981",
			"68.97.208.223:9981",
			"71.190.208.128:9981",
			"69.120.2.164:9981",
			"37.204.141.163:9981",
			"188.243.111.129:9981",
			"78.46.64.86:9981",
			"188.244.40.69:9981",
			"87.237.42.180:9981",
			"212.42.213.179:9981",
			"62.216.59.236:9981",
			"80.56.227.209:9981",
			"202.181.196.157:9981",
			"188.242.52.10:9986",
			"188.242.52.10:9988",
			"81.24.30.12:9981",
			"109.233.59.68:9981",
			"77.162.159.137:9981",
			"176.240.111.223:9981",
			"126.28.73.206:9981",
			"178.63.11.62:9981",
			"174.84.49.170:9981",
			"185.6.124.16:9981",
			"81.24.30.13:9981",
			"31.208.123.118:9981",
			"85.69.198.249:9981",
			"5.9.147.103:9981",
			"77.168.231.70:9981",
			"81.24.30.14:9981",
			"82.253.237.216:9981",
			"161.53.40.130:9981",
			"34.209.55.245:9981",
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
		BandwidthCounters() (uint64, uint64, error)

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

		// AddToBlacklist adds addresses to the blacklist of the gateway
		AddToBlacklist(addresses []NetAddress) error

		// Blacklist returns the current blacklist of the Gateway
		Blacklist() ([]string, error)

		// RemoveFromBlacklist removes addresses from the blacklist of the
		// gateway
		RemoveFromBlacklist(addresses []NetAddress) error

		// SetBlacklist sets the blacklist of the gateway
		SetBlacklist(addresses []NetAddress) error

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
