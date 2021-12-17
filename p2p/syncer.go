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
	"go.sia.tech/core/merkle"
	"go.sia.tech/core/net/gateway"
	"go.sia.tech/core/net/mux"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/txpool"
	"lukechampine.com/frand"
)

// sentinel error for shutdown
var errClosing = errors.New("closing")

// generic RPC response error, for when e.g. our disk fails
var errInternalError = errors.New("could not complete request due to internal error")

var (
	rpcGetHeaders    = rpc.NewSpecifier("GetHeaders")
	rpcGetPeers      = rpc.NewSpecifier("GetPeers")
	rpcGetBlocks     = rpc.NewSpecifier("GetBlocks")
	rpcGetCheckpoint = rpc.NewSpecifier("GetCheckpoint")
	rpcRelayBlock    = rpc.NewSpecifier("RelayBlock")
	rpcRelayTxn      = rpc.NewSpecifier("RelayTxn")
)

// Maximum number of peers a getPeers request can return.
const maxGetPeers = 100

type msgGetHeaders struct {
	History []types.ChainIndex
}

func (m *msgGetHeaders) EncodeTo(e *types.Encoder) {
	e.WritePrefix(len(m.History))
	for i := range m.History {
		m.History[i].EncodeTo(e)
	}
}

func (m *msgGetHeaders) DecodeFrom(d *types.Decoder) {
	m.History = make([]types.ChainIndex, d.ReadPrefix())
	for i := range m.History {
		m.History[i].DecodeFrom(d)
	}
}

func (m *msgGetHeaders) MaxLen() int {
	return 10e6 // arbitrary
}

type msgHeaders struct {
	Headers []types.BlockHeader
}

func (m *msgHeaders) EncodeTo(e *types.Encoder) {
	e.WritePrefix(len(m.Headers))
	for i := range m.Headers {
		m.Headers[i].EncodeTo(e)
	}
}

func (m *msgHeaders) DecodeFrom(d *types.Decoder) {
	m.Headers = make([]types.BlockHeader, d.ReadPrefix())
	for i := range m.Headers {
		m.Headers[i].DecodeFrom(d)
	}
}

func (m *msgHeaders) MaxLen() int {
	return 10e6 // arbitrary
}

type msgGetPeers struct{}

func (m *msgGetPeers) EncodeTo(e *types.Encoder)   {}
func (m *msgGetPeers) DecodeFrom(d *types.Decoder) {}
func (m *msgGetPeers) MaxLen() int                 { return 0 }

type msgPeers []string

func (m *msgPeers) EncodeTo(e *types.Encoder) {
	e.WritePrefix(len(*m))
	for i := range *m {
		e.WriteString((*m)[i])
	}
}

func (m *msgPeers) DecodeFrom(d *types.Decoder) {
	*m = make([]string, d.ReadPrefix())
	for i := range *m {
		(*m)[i] = d.ReadString()
	}
}

func (m *msgPeers) MaxLen() int {
	const maxDomainLen = 256 // See https://www.freesoft.org/CIE/RFC/1035/9.htm
	return 8 + maxGetPeers*maxDomainLen
}

type msgGetBlocks struct {
	Blocks []types.ChainIndex
}

func (m *msgGetBlocks) EncodeTo(e *types.Encoder) {
	e.WritePrefix(len(m.Blocks))
	for i := range m.Blocks {
		m.Blocks[i].EncodeTo(e)
	}
}

func (m *msgGetBlocks) DecodeFrom(d *types.Decoder) {
	m.Blocks = make([]types.ChainIndex, d.ReadPrefix())
	for i := range m.Blocks {
		m.Blocks[i].DecodeFrom(d)
	}
}

func (m *msgGetBlocks) MaxLen() int {
	return 10e6 // arbitrary
}

type msgBlocks struct {
	Blocks []types.Block
}

func (m *msgBlocks) EncodeTo(e *types.Encoder) {
	e.WritePrefix(len(m.Blocks))
	for i := range m.Blocks {
		merkle.CompressedBlock(m.Blocks[i]).EncodeTo(e)
	}
}

func (m *msgBlocks) DecodeFrom(d *types.Decoder) {
	m.Blocks = make([]types.Block, d.ReadPrefix())
	for i := range m.Blocks {
		(*merkle.CompressedBlock)(&m.Blocks[i]).DecodeFrom(d)
	}
}

func (m *msgBlocks) MaxLen() int {
	return 100e6 // arbitrary
}

type msgGetCheckpoint struct {
	Index types.ChainIndex
}

func (m *msgGetCheckpoint) EncodeTo(e *types.Encoder) {
	m.Index.EncodeTo(e)
}

func (m *msgGetCheckpoint) DecodeFrom(d *types.Decoder) {
	m.Index.DecodeFrom(d)
}

func (m *msgGetCheckpoint) MaxLen() int {
	return 40
}

type msgCheckpoint struct {
	// NOTE: we don't use a consensus.Checkpoint, because a Checkpoint.Context
	// is the *child* context for the block, not its parent context.
	Block         types.Block
	ParentContext consensus.ValidationContext
}

func (m *msgCheckpoint) EncodeTo(e *types.Encoder) {
	merkle.CompressedBlock(m.Block).EncodeTo(e)
	m.ParentContext.EncodeTo(e)
}

func (m *msgCheckpoint) DecodeFrom(d *types.Decoder) {
	(*merkle.CompressedBlock)(&m.Block).DecodeFrom(d)
	m.ParentContext.DecodeFrom(d)
}

func (m *msgCheckpoint) MaxLen() int {
	return 10e6 // arbitrary
}

type msgRelayBlock struct {
	Block types.Block
}

func (m *msgRelayBlock) EncodeTo(e *types.Encoder) {
	merkle.CompressedBlock(m.Block).EncodeTo(e)
}

func (m *msgRelayBlock) DecodeFrom(d *types.Decoder) {
	(*merkle.CompressedBlock)(&m.Block).DecodeFrom(d)
}

func (m *msgRelayBlock) MaxLen() int {
	return 10e6 // arbitrary
}

type msgRelayTxn struct {
	Transaction types.Transaction
	DependsOn   []types.Transaction
}

func (m *msgRelayTxn) EncodeTo(e *types.Encoder) {
	m.Transaction.EncodeTo(e)
	e.WritePrefix(len(m.DependsOn))
	for i := range m.DependsOn {
		m.DependsOn[i].EncodeTo(e)
	}
}

func (m *msgRelayTxn) DecodeFrom(d *types.Decoder) {
	m.Transaction.DecodeFrom(d)
	m.DependsOn = make([]types.Transaction, d.ReadPrefix())
	for i := range m.DependsOn {
		m.DependsOn[i].DecodeFrom(d)
	}
}

func (m *msgRelayTxn) MaxLen() int {
	return 10e6 // arbitrary
}

func isRelay(msg rpc.Object) bool {
	switch msg.(type) {
	case *msgGetHeaders,
		*msgGetPeers,
		*msgGetBlocks,
		*msgGetCheckpoint:
		return false
	case *msgRelayBlock,
		*msgRelayTxn:
		return true
	default:
		panic(fmt.Sprintf("unhandled type %T", msg))
	}
}

func rpcDeadline(id rpc.Specifier) time.Duration {
	// TODO: pick reasonable values for these
	switch id {
	case rpcGetHeaders:
		return time.Minute
	case rpcGetPeers:
		return time.Minute
	case rpcGetBlocks:
		return 10 * time.Minute
	case rpcGetCheckpoint:
		return time.Minute
	case rpcRelayBlock:
		return time.Minute
	case rpcRelayTxn:
		return time.Minute
	default:
		panic(fmt.Sprintf("unhandled ID %v", id))
	}
}

// A SyncerStore stores peer addresses. Implementations are assumed to be
// thread-safe.
type SyncerStore interface {
	AddPeer(addr string) error
	RandomPeer() (string, error)
	RandomPeers(n int) ([]string, error)
}

type Syncer struct {
	l      net.Listener
	cm     *chain.Manager
	tp     *txpool.Pool
	header gateway.Header
	store  SyncerStore

	cond  sync.Cond
	mu    sync.Mutex
	peers map[gateway.Header]*gateway.Session
	err   error
}

func (s *Syncer) setErr(err error) error {
	if s.err == nil {
		s.err = err
		s.cond.Broadcast()
	}
	return s.err
}

func (s *Syncer) rpc(peer gateway.Header, id rpc.Specifier, req rpc.Object, resp rpc.Object) error {
	// TODO: consider rate-limiting RPCs, i.e. allowing only n inflight

	s.mu.Lock()
	sess, ok := s.peers[peer]
	s.mu.Unlock()
	if !ok {
		return errors.New("unknown peer")
	}
	stream, err := sess.DialStream()
	if err != nil {
		return err
	}
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

func (s *Syncer) relay(id rpc.Specifier, msg rpc.Object, sourcePeer gateway.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for peer := range s.peers {
		if peer != sourcePeer {
			go s.rpc(peer, id, msg, nil)
		}
	}
}

func (s *Syncer) broadcast(id rpc.Specifier, msg rpc.Object) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for peer := range s.peers {
		go s.rpc(peer, id, msg, nil)
	}
}

func (s *Syncer) relayBlock(block types.Block, sourcePeer gateway.Header) {
	s.relay(rpcRelayBlock, &msgRelayBlock{block}, sourcePeer)
}

func (s *Syncer) relayTransaction(txn types.Transaction, dependsOn []types.Transaction, sourcePeer gateway.Header) {
	s.relay(rpcRelayTxn, &msgRelayTxn{txn, dependsOn}, sourcePeer)
}

func (s *Syncer) BroadcastBlock(block types.Block) {
	s.broadcast(rpcRelayBlock, &msgRelayBlock{block})
}

func (s *Syncer) BroadcastTransaction(txn types.Transaction, dependsOn []types.Transaction) {
	s.broadcast(rpcRelayTxn, &msgRelayTxn{txn, dependsOn})
}

func (s *Syncer) getHeaders(peer gateway.Header, req *msgGetHeaders) (resp msgHeaders, err error) {
	err = s.rpc(peer, rpcGetHeaders, req, &resp)
	return
}

func (s *Syncer) getPeers(peer gateway.Header) (resp msgPeers, err error) {
	err = s.rpc(peer, rpcGetPeers, &msgGetPeers{}, &resp)
	return
}

func (s *Syncer) getBlocks(peer gateway.Header, req *msgGetBlocks) (resp msgBlocks, err error) {
	err = s.rpc(peer, rpcGetBlocks, req, &resp)
	return
}

func (s *Syncer) getCheckpoint(peer gateway.Header, req *msgGetCheckpoint) (resp msgCheckpoint, err error) {
	err = s.rpc(peer, rpcGetCheckpoint, req, &resp)
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
			rpcGetHeaders:    new(msgGetHeaders),
			rpcGetBlocks:     new(msgGetBlocks),
			rpcGetCheckpoint: new(msgGetCheckpoint),
			rpcRelayBlock:    new(msgRelayBlock),
			rpcRelayTxn:      new(msgRelayTxn),
		}[id]
		if msg == nil {
			return fmt.Errorf("unrecognized RPC %q", id)
		} else if err := rpc.ReadObject(stream, msg); err != nil {
			return err
		}
		if isRelay(msg) {
			s.handleRelay(peer.Peer, msg)
		} else {
			if resp, rerr := s.handleRPC(peer.Peer, msg); rerr != nil {
				if err := rpc.WriteResponseErr(stream, rerr); err != nil {
					return err
				}
			} else {
				if err := rpc.WriteResponse(stream, resp); err != nil {
					return err
				}
			}
		}
		return nil
	}()
	// TODO: give peer a strike based on err?
	_ = err
	//peer.setErr(err)
}

func (s *Syncer) handleStreams(peer *gateway.Session) error {
	for {
		stream, err := peer.AcceptStream()
		if err != nil {
			return err
		}
		// TODO: limit to n concurrent streams
		go s.handleStream(peer, stream)
	}
}

func (s *Syncer) handleConn(conn net.Conn) error {
	conn.SetDeadline(time.Now().Add(time.Minute))
	peer, err := gateway.AcceptSession(conn, s.header)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Time{})
	s.mu.Lock()
	s.peers[peer.Peer] = peer
	s.mu.Unlock()
	s.handleNewPeer(peer.Peer)
	return s.handleStreams(peer)
}

func (s *Syncer) handleNewPeer(peer gateway.Header) {
	// request potentially-connectable node addresses
	go func() {
		// TODO: logging
		peers, _ := s.getPeers(peer)
		for _, peer := range peers {
			s.store.AddPeer(peer)
		}
	}()

	go s.syncToPeer(peer)
}

func (s *Syncer) handleRelay(peer gateway.Header, msg rpc.Object) {
	switch msg := msg.(type) {
	case *msgRelayBlock:
		err := s.cm.AddTipBlock(msg.Block)
		if err == nil {
			go s.relayBlock(msg.Block, peer)
		} else if errors.Is(err, chain.ErrUnknownIndex) {
			go s.syncToPeer(peer)
		}
	case *msgRelayTxn:
		err := s.tp.AddTransaction(msg.Transaction)
		if err == nil {
			go s.relayTransaction(msg.Transaction, msg.DependsOn, peer)
		}
	default:
		panic(fmt.Sprintf("unhandled type %T", msg))
	}
}

func (s *Syncer) handleRPC(peer gateway.Header, msg rpc.Object) (resp rpc.Object, err error) {
	switch msg := msg.(type) {
	case *msgGetHeaders:
		sort.Slice(msg.History, func(i, j int) bool {
			return msg.History[i].Height > msg.History[j].Height
		})
		headers, err := s.cm.HeadersForHistory(make([]types.BlockHeader, 2000), msg.History)
		if err != nil {
			s.setErr(fmt.Errorf("%T: couldn't load headers: %w", msg, err))
			return nil, errInternalError
		}
		return &msgHeaders{Headers: headers}, nil

	case *msgGetPeers:
		peers, err := s.store.RandomPeers(maxGetPeers)
		if err != nil {
			return nil, errInternalError
		}
		return (*msgPeers)(&peers), nil

	case *msgGetBlocks:

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
		return &msgBlocks{Blocks: blocks}, nil

	case *msgGetCheckpoint:
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
		vc, err := s.cm.ValidationContext(b.Header.ParentIndex())
		if errors.Is(err, chain.ErrPruned) {
			return nil, err
		} else if err != nil {
			s.setErr(fmt.Errorf("%T: couldn't load validation context: %w", msg, err))
			return nil, errInternalError
		}
		return &msgCheckpoint{Block: b, ParentContext: vc}, nil

	default:
		panic(fmt.Sprintf("unhandled type %T", msg))
	}
}

func (s *Syncer) getCheckpointsForSync(peer gateway.Header) ([]consensus.Checkpoint, error) {
	return nil, nil
}

func (s *Syncer) downloadHeaders(checkpoints []consensus.Checkpoint, syncPeer gateway.Header) ([]types.BlockHeader, error) {
	history, err := s.cm.History()
	if err != nil {
		return nil, err
	}
	resp, err := s.getHeaders(syncPeer, &msgGetHeaders{History: history})
	return resp.Headers, err
}

func (s *Syncer) downloadBlocks(blocks []types.ChainIndex, syncPeer gateway.Header) ([]types.Block, error) {
	resp, err := s.getBlocks(syncPeer, &msgGetBlocks{Blocks: blocks})
	return resp.Blocks, err
}

func (s *Syncer) syncToPeer(peer gateway.Header) {
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

	if _, err := s.cm.AddBlocks(blocks); err != nil {
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
		peer, err := s.store.RandomPeer()
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

func (s *Syncer) Addr() string {
	return s.header.NetAddress
}

func (s *Syncer) Connect(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	sess, err := gateway.DialSession(conn, s.header)
	if err != nil {
		conn.Close()
		return err
	}
	conn.SetDeadline(time.Time{})

	s.mu.Lock()
	s.peers[sess.Peer] = sess
	s.mu.Unlock()
	s.handleNewPeer(sess.Peer)
	go s.handleStreams(sess)
	if err := s.store.AddPeer(addr); err != nil {
		conn.Close()
		return err
	}
	return nil
}

func (s *Syncer) Disconnect(peer gateway.Header) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.peers[peer]
	if !ok {
		return errors.New("unknown peer")
	}
	s.cond.Broadcast() // wake maintainHealthyPeerSet
	return sess.Close()
}

func (s *Syncer) Peers() []gateway.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers := make([]gateway.Header, 0, len(s.peers))
	for peer := range s.peers {
		peers = append(peers, peer)
	}
	return peers
}

func (s *Syncer) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setErr(errClosing)
	return s.l.Close()
}

func NewSyncer(addr string, genesisID types.BlockID, cm *chain.Manager, tp *txpool.Pool, store SyncerStore) (*Syncer, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Syncer{
		l:     l,
		cm:    cm,
		tp:    tp,
		store: store,
		header: gateway.Header{
			GenesisID:  genesisID,
			NetAddress: l.Addr().String(),
		},
		peers: make(map[gateway.Header]*gateway.Session),
	}
	frand.Read(s.header.UniqueID[:])
	s.cond.L = &s.mu
	return s, nil
}
