package p2p

import (
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/gateway"
	"go.sia.tech/core/net/mux"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/txpool"
)

var (
	// sentinel error for shutdown
	errClosing = errors.New("closing")

	// generic RPC response error, for when e.g. our disk fails
	errInternalError = errors.New("could not complete request due to internal error")

	errBlacklistedPeer = errors.New("refusing to connect to blacklisted peer")
)

func rpcDeadline(id rpc.Specifier) time.Duration {
	// TODO: pick reasonable values for these
	switch id {
	case gateway.RPCHeadersID:
		return time.Minute
	case gateway.RPCPeersID:
		return time.Minute
	case gateway.RPCBlocksID:
		return 10 * time.Minute
	case gateway.RPCCheckpointID:
		return time.Minute
	case gateway.RPCRelayBlockID:
		return time.Minute
	case gateway.RPCRelayTxnID:
		return time.Minute
	default:
		panic(fmt.Sprintf("unhandled ID %v", id))
	}
}

// A Syncer manages peers and relays new blocks and transactions.
type Syncer struct {
	l  net.Listener
	cm *chain.Manager
	tp *txpool.Pool
	pm *PeerManager

	// for gateway handshake
	genesisID types.BlockID
	uniqueID  gateway.UniqueID

	cond  sync.Cond
	mu    sync.Mutex
	peers map[gateway.UniqueID]*gateway.Session
	err   error
}

func (s *Syncer) setErr(err error) error {
	if s.err == nil {
		s.err = err
		for _, peer := range s.peers {
			peer.Close()
		}
		s.cond.Broadcast()
	}
	return s.err
}

func (s *Syncer) rpc(peer *gateway.Session, id rpc.Specifier, req rpc.Object, resp rpc.Object) error {
	// TODO: consider rate-limiting RPCs, i.e. allowing only n inflight

	stream := peer.DialStream()
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(rpcDeadline(id)))
	if err := rpc.WriteRequest(stream, id, req); err != nil {
		return err
	}
	if resp != nil {
		if err := rpc.ReadResponse(stream, resp); err != nil {
			return err
		}
	}
	return nil
}

func (s *Syncer) relay(id rpc.Specifier, msg rpc.Object, sourcePeer gateway.UniqueID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, peer := range s.peers {
		if peer.RemoteID != sourcePeer {
			go s.rpc(peer, id, msg, nil)
		}
	}
}

func (s *Syncer) broadcast(id rpc.Specifier, msg rpc.Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, peer := range s.peers {
		go s.rpc(peer, id, msg, nil)
	}
}

func (s *Syncer) relayBlock(block types.Block, sourcePeer gateway.UniqueID) {
	s.relay(gateway.RPCRelayBlockID, &gateway.RPCRelayBlockRequest{
		Block: block,
	}, sourcePeer)
}

func (s *Syncer) relayTransaction(txn types.Transaction, dependsOn []types.Transaction, sourcePeer gateway.UniqueID) {
	s.relay(gateway.RPCRelayTxnID, &gateway.RPCRelayTxnRequest{
		Transaction: txn,
		DependsOn:   dependsOn,
	}, sourcePeer)
}

// BroadcastBlock broadcasts a block to all connected peers.
func (s *Syncer) BroadcastBlock(block types.Block) {
	s.broadcast(gateway.RPCRelayBlockID, &gateway.RPCRelayBlockRequest{
		Block: block,
	})
}

// BroadcastTransaction broadcasts a transaction to all connected peers.
func (s *Syncer) BroadcastTransaction(txn types.Transaction, dependsOn []types.Transaction) {
	s.broadcast(gateway.RPCRelayTxnID, &gateway.RPCRelayTxnRequest{
		Transaction: txn,
		DependsOn:   dependsOn,
	})
}

func (s *Syncer) getHeaders(peer *gateway.Session, req *gateway.RPCHeadersRequest) (resp gateway.RPCHeadersResponse, err error) {
	err = s.rpc(peer, gateway.RPCHeadersID, req, &resp)
	return
}

func (s *Syncer) getPeers(peer *gateway.Session) (resp gateway.RPCPeersResponse, err error) {
	err = s.rpc(peer, gateway.RPCPeersID, &gateway.RPCPeersRequest{}, &resp)
	return
}

func (s *Syncer) getBlocks(peer *gateway.Session, req *gateway.RPCBlocksRequest) (resp gateway.RPCBlocksResponse, err error) {
	err = s.rpc(peer, gateway.RPCBlocksID, req, &resp)
	return
}

func (s *Syncer) handleStream(peer *gateway.Session, stream *mux.Stream) {
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(5 * time.Minute))
	err := func() error {
		id, err := rpc.ReadID(stream)
		if err != nil {
			return err
		}
		msg := map[rpc.Specifier]rpc.Object{
			gateway.RPCPeersID:      new(gateway.RPCPeersRequest),
			gateway.RPCHeadersID:    new(gateway.RPCHeadersRequest),
			gateway.RPCBlocksID:     new(gateway.RPCBlocksRequest),
			gateway.RPCCheckpointID: new(gateway.RPCCheckpointRequest),
			gateway.RPCRelayBlockID: new(gateway.RPCRelayBlockRequest),
			gateway.RPCRelayTxnID:   new(gateway.RPCRelayTxnRequest),
		}[id]
		if msg == nil {
			return fmt.Errorf("unrecognized RPC %q", id)
		} else if err := rpc.ReadObject(stream, msg); err != nil {
			return err
		}
		if gateway.IsRelayRPC(msg) {
			s.handleRelay(peer, msg)
		} else {
			resp, err := s.handleRPC(peer, msg)
			if err == nil {
				err = rpc.WriteResponse(stream, resp)
			} else {
				err = rpc.WriteResponseErr(stream, err)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}()
	if err != nil {
		s.pm.IncreaseBanscore(peer.RemoteAddr, 20)
	}
}

func (s *Syncer) handleStreams(peer *gateway.Session) error {
	for {
		s.pm.NoteSeen(peer.RemoteAddr)
		stream, err := peer.AcceptStream()
		if err != nil {
			return err
		}
		// TODO: limit to n concurrent streams
		go s.handleStream(peer, stream)
	}
}

func (s *Syncer) handleConn(conn net.Conn) error {
	addr := conn.RemoteAddr().String()
	if p := s.pm.Info(addr); p.Blacklisted {
		conn.Close()
		return errBlacklistedPeer
	} else if err := s.pm.AddPeer(addr); err != nil {
		return err
	}

	conn.SetDeadline(time.Now().Add(time.Minute))
	sess, err := gateway.AcceptSession(conn, s.genesisID, s.uniqueID)
	if err != nil {
		conn.Close()
		return err
	}
	conn.SetDeadline(time.Time{})

	s.mu.Lock()
	if _, ok := s.peers[sess.RemoteID]; ok {
		s.mu.Unlock()
		conn.Close()
		return errors.New("already connected to that peer")
	}
	s.peers[sess.RemoteID] = sess
	s.mu.Unlock()
	s.handleNewPeer(sess)
	return s.handleStreams(sess)
}

func (s *Syncer) handleNewPeer(peer *gateway.Session) {
	// request potentially-connectable node addresses
	go func() {
		// TODO: logging
		peers, _ := s.getPeers(peer)
		for _, addr := range peers {
			s.pm.AddPeer(addr)
		}
	}()

	go s.syncToPeer(peer)
}

func (s *Syncer) handleRelay(peer *gateway.Session, msg rpc.Object) {
	switch msg := msg.(type) {
	case *gateway.RPCRelayBlockRequest:
		err := s.cm.AddTipBlock(msg.Block)
		if err == nil {
			go s.relayBlock(msg.Block, peer.RemoteID)
		} else if errors.Is(err, chain.ErrUnknownIndex) {
			go s.syncToPeer(peer)
		}
	case *gateway.RPCRelayTxnRequest:
		err := s.tp.AddTransaction(msg.Transaction)
		if err == nil {
			go s.relayTransaction(msg.Transaction, msg.DependsOn, peer.RemoteID)
		}
	default:
		panic(fmt.Sprintf("unhandled type %T", msg))
	}
}

func (s *Syncer) handleRPC(peer *gateway.Session, msg rpc.Object) (resp rpc.Object, err error) {
	switch msg := msg.(type) {
	case *gateway.RPCHeadersRequest:
		sort.Slice(msg.History, func(i, j int) bool {
			return msg.History[i].Height > msg.History[j].Height
		})
		headers, err := s.cm.HeadersForHistory(make([]types.BlockHeader, 2000), msg.History)
		if err != nil {
			s.setErr(fmt.Errorf("%T: couldn't load headers: %w", msg, err))
			return nil, errInternalError
		}
		return &gateway.RPCHeadersResponse{Headers: headers}, nil

	case *gateway.RPCPeersRequest:
		peers := append(s.pm.RandomGoodPeers(gateway.MaxRPCPeersLen-1), s.Addr()) // include our own address
		return (*gateway.RPCPeersResponse)(&peers), nil

	case *gateway.RPCBlocksRequest:

		// if len(msg.Blocks) == 0 {
		// 	p.ban(fmt.Errorf("empty %T", msg))
		// 	return nil
		// }

		var blocks []types.Block
		for _, index := range msg.Blocks {
			b, err := s.cm.Block(index)
			if errors.Is(err, chain.ErrPruned) {
				break // nothing we can do
			} else if errors.Is(err, chain.ErrUnknownIndex) {
				//p.warn(fmt.Errorf("peer requested blocks we don't have"))
				break
			} else if err != nil {
				s.setErr(fmt.Errorf("%T: couldn't load transactions: %w", msg, err))
				return nil, errInternalError
			}
			blocks = append(blocks, b)
		}
		return &gateway.RPCBlocksResponse{Blocks: blocks}, nil

	case *gateway.RPCCheckpointRequest:
		b, err := s.cm.Block(msg.Index)
		if errors.Is(err, chain.ErrPruned) {
			return nil, err // nothing we can do
		} else if errors.Is(err, chain.ErrUnknownIndex) {
			//p.warn(err)
			return nil, err
		} else if err != nil {
			s.setErr(fmt.Errorf("%T: couldn't load block: %w", msg, err))
			return nil, errInternalError
		}
		cs, err := s.cm.State(b.Header.ParentIndex())
		if errors.Is(err, chain.ErrPruned) {
			return nil, err
		} else if err != nil {
			s.setErr(fmt.Errorf("%T: couldn't load consensus state: %w", msg, err))
			return nil, errInternalError
		}
		return &gateway.RPCCheckpointResponse{Block: b, ParentState: cs}, nil

	default:
		panic(fmt.Sprintf("unhandled type %T", msg))
	}
}

func (s *Syncer) getCheckpointsForSync(peer *gateway.Session) ([]consensus.Checkpoint, error) {
	return nil, nil
}

func (s *Syncer) downloadHeaders(checkpoints []consensus.Checkpoint, syncPeer *gateway.Session) ([]types.BlockHeader, error) {
	history, err := s.cm.History()
	if err != nil {
		return nil, err
	}
	resp, err := s.getHeaders(syncPeer, &gateway.RPCHeadersRequest{History: history})
	if err != nil {
		return nil, err
	}
	headers := resp.Headers
	// continue downloading until the peer has no more to give us
	//
	// TODO: this is a DoS vector (host can send us infinite headers)
	for len(resp.Headers) == 2000 {
		resp, err = s.getHeaders(syncPeer, &gateway.RPCHeadersRequest{History: []types.ChainIndex{headers[len(headers)-1].Index()}})
		if err != nil {
			return nil, err
		}
		headers = append(headers, resp.Headers...)
	}
	return headers, nil
}

func (s *Syncer) downloadBlocks(blocks []types.ChainIndex, syncPeer *gateway.Session) ([]types.Block, error) {
	resp, err := s.getBlocks(syncPeer, &gateway.RPCBlocksRequest{Blocks: blocks})
	return resp.Blocks, err
}

func (s *Syncer) syncToPeer(peer *gateway.Session) {
	// request checkpoints from the sync peer
	checkpoints, err := s.getCheckpointsForSync(peer)
	if err != nil {
		return
	}

	// download header chains for each checkpoint; try to do this in parallel
	// from multiple peers, but fall back to the sync peer if necessary
	headers, err := s.downloadHeaders(checkpoints, peer)
	if err != nil {
		return
	} else if len(headers) == 0 {
		return
	}

	// TODO: this awkward locking is necessary because multiple syncToPeer
	// goroutines can race on sc.
	var unvalidated []types.ChainIndex
	s.mu.Lock()
	sc, err := s.cm.AddHeaders(headers)
	if sc != nil {
		unvalidated = append(unvalidated, sc.Unvalidated()...)
	}
	s.mu.Unlock()
	if err != nil {
		s.pm.IncreaseBanscore(peer.RemoteAddr, 20)
		return
	} else if sc == nil {
		// chain is not the best; keep the headers around (since this chain
		// might become the best later), but don't bother downloading blocks
		return
	}

	blocks, err := s.downloadBlocks(unvalidated, peer)
	if err != nil {
		return
	}

	s.mu.Lock()
	sc, err = s.cm.AddHeaders(headers)
	if sc != nil {
		_, err = s.cm.AddBlocks(blocks)
	}
	s.mu.Unlock()
	if err != nil {
		s.pm.IncreaseBanscore(peer.RemoteAddr, 20)
		return
	}
}

// maintainHealthyPeerSet tries to add peers to the syncer until it has 8.
func (s *Syncer) maintainHealthyPeerSet() {
	const healthyPeerCount = 8
	seen := make(map[string]bool)
	for {
		s.cond.L.Lock()
		for s.err == nil && len(s.peers) >= healthyPeerCount {
			s.cond.Wait()
		}
		if s.err != nil {
			s.cond.L.Unlock()
			return
		}
		s.cond.L.Unlock()
		peer, err := s.pm.RandomGoodPeer()
		if err == nil && !seen[peer] {
			s.Connect(peer)
			seen[peer] = true
		} else {
			// sleep on failure to avoid spinning unproductively
			time.Sleep(time.Second)
		}
	}
}

func (s *Syncer) listen() {
	for {
		conn, err := s.l.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

// Run starts the syncer and blocks until it is stopped.
func (s *Syncer) Run() error {
	go s.listen()
	go s.maintainHealthyPeerSet()

	s.cond.L.Lock()
	defer s.cond.L.Unlock()
	for s.err == nil {
		s.cond.Wait()
	}
	if s.err != errClosing {
		return s.err
	}
	return nil
}

// Addr returns the listening address of the Syncer.
func (s *Syncer) Addr() string {
	return s.l.Addr().String()
}

// Connect attempts to connect to a peer.
func (s *Syncer) Connect(addr string) error {
	s.mu.Lock()
	alreadyConnected := false
	for _, peer := range s.peers {
		if peer.RemoteAddr == addr {
			alreadyConnected = true
			break
		}
	}
	s.mu.Unlock()
	if alreadyConnected {
		return errors.New("already connected to that peer")
	} else if addr == s.Addr() {
		return errors.New("refusing to connect to our own address")
	}

	if p := s.pm.Info(addr); p.Blacklisted {
		return errBlacklistedPeer
	} else if err := s.pm.AddPeer(addr); err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		s.pm.NoteFailedConnection(addr)
		return err
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	sess, err := gateway.DialSession(conn, s.genesisID, s.uniqueID)
	if err != nil {
		conn.Close()
		s.pm.NoteFailedConnection(addr)
		return err
	}
	conn.SetDeadline(time.Time{})

	s.mu.Lock()
	if _, ok := s.peers[sess.RemoteID]; ok {
		s.mu.Unlock()
		conn.Close()
		return errors.New("already connected to that peer")
	}
	s.peers[sess.RemoteID] = sess
	s.mu.Unlock()
	s.handleNewPeer(sess)
	go s.handleStreams(sess)
	return nil
}

// Disconnect disconnects from a peer.
func (s *Syncer) Disconnect(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.peers {
		if sess.RemoteAddr == addr {
			delete(s.peers, id)
			s.cond.Broadcast() // wake maintainHealthyPeerSet
			return sess.Close()
		}
	}
	return errors.New("unknown peer")
}

// Peers returns the addresses of all currently-connected peers.
func (s *Syncer) Peers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers := make([]string, 0, len(s.peers))
	for _, peer := range s.peers {
		peers = append(peers, peer.RemoteAddr)
	}
	return peers
}

// Close closes the sync and stops the listener.
func (s *Syncer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setErr(errClosing)
	return s.l.Close()
}

// NewSyncer creates a new syncer listening on the given address.
func NewSyncer(addr string, genesisID types.BlockID, cm *chain.Manager, tp *txpool.Pool, store PeerStore) (*Syncer, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Syncer{
		l:         l,
		cm:        cm,
		tp:        tp,
		pm:        NewPeerManager(store),
		genesisID: genesisID,
		uniqueID:  gateway.GenerateUniqueID(),
		peers:     make(map[gateway.UniqueID]*gateway.Session),
	}
	s.cond.L = &s.mu
	return s, nil
}
