package hostd

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	corehost "go.sia.tech/core/host"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/host"
	"go.sia.tech/siad/v2/internal/cpuminer"
	"go.sia.tech/siad/v2/internal/walletutil"
	"go.sia.tech/siad/v2/wallet"
	"nhooyr.io/websocket"
)

type (
	// A Wallet can spend and receive siacoins.
	Wallet interface {
		Balance() types.Currency
		Address() types.Address
		NewAddress() types.Address
		Addresses() ([]types.Address, error)
		Transactions(since time.Time, max int) ([]wallet.Transaction, error)
		FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error)
		SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error
	}

	// A Syncer can connect to other peers and synchronize the blockchain.
	Syncer interface {
		Addr() string
		Peers() []string
		Connect(addr string) error
		BroadcastBlock(b types.Block)
		BroadcastTransaction(txn types.Transaction, dependsOn []types.Transaction)
	}

	// A TransactionPool can validate and relay unconfirmed transactions.
	TransactionPool interface {
		RecommendedFee() types.Currency
		Transactions() []types.Transaction
		AddTransaction(txn types.Transaction) error
		AddTransactionSet(txns []types.Transaction) error
	}

	// A ChainManager manages blockchain state.
	ChainManager interface {
		AddTipBlock(block types.Block) error
		TipContext() consensus.ValidationContext
	}

	// A Host manages renter connections and contracts
	Host interface {
	}

	// A ContractStore stores contracts and their state
	ContractStore interface {
		// ActiveContracts returns unresolved contracts from the store.
		ActiveContracts(limit, skip uint64) ([]corehost.Contract, error)
		// ResolvedContracts returns resolved contracts from the store.
		ResolvedContracts(limit, skip uint64) ([]corehost.Contract, error)
	}
)

type server struct {
	w  Wallet
	s  Syncer
	cm ChainManager
	tp TransactionPool

	// miner synchronization
	mu     sync.Mutex
	m      *cpuminer.CPUMiner
	mining bool
	cancel chan struct{}
}

func (s *server) walletBalanceHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.WriteJSON(w, WalletBalanceResponse{
		Siacoins: s.w.Balance(),
	})
}

func (s *server) walletAddressHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.WriteJSON(w, s.w.NewAddress())
}

func (s *server) walletTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var since time.Time
	if v := req.FormValue("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			http.Error(w, "invalid since value: "+err.Error(), http.StatusBadRequest)
			return
		}
		since = t
	}
	max := -1
	if v := req.FormValue("max"); v != "" {
		t, err := strconv.Atoi(v)
		if err != nil {
			http.Error(w, "invalid max value: "+err.Error(), http.StatusBadRequest)
			return
		}
		max = t
	}
	txns, err := s.w.Transactions(since, max)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, txns)
}

func (s *server) syncerPeersHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	ps := s.s.Peers()
	sps := make([]SyncerPeerResponse, len(ps))
	for i, peer := range ps {
		sps[i] = SyncerPeerResponse{
			NetAddress: peer,
		}
	}
	api.WriteJSON(w, sps)
}

func (s *server) syncerConnectHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var scr SyncerConnectRequest
	if err := json.NewDecoder(req.Body).Decode(&scr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.s.Connect(scr.NetAddress); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

func (s *server) minerStartHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.mining {
		http.Error(w, "already mining", http.StatusBadRequest)
		return
	}

	s.mining = true
	s.cancel = make(chan struct{})
	go func(cancel chan struct{}) {
		for {
			select {
			case <-cancel:
				s.mu.Lock()
				defer s.mu.Unlock()
				s.mining = false
				return
			default:
				b := s.m.MineBlock()
				// give it to ourselves
				if err := s.cm.AddTipBlock(b); err != nil {
					if !errors.Is(err, chain.ErrUnknownIndex) {
						log.Println("Couldn't add block:", err)
					}
					continue
				}
				// broadcast it
				s.s.BroadcastBlock(b)
			}
		}
	}(s.cancel)
	w.WriteHeader(http.StatusOK)
}

func (s *server) minerStopHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.mining {
		http.Error(w, "not mining", http.StatusBadRequest)
		return
	}
	close(s.cancel)
	w.WriteHeader(http.StatusOK)
}

func (s *server) consensusTipHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	index := s.cm.TipContext().Index
	api.WriteJSON(w, ConsensusTipResponse{
		ID:     index.ID,
		Height: index.Height,
	})
}

// NewServer returns an HTTP handler that serves the hostd API.
func NewServer(cm ChainManager, s Syncer, w *walletutil.TestingWallet, m *cpuminer.CPUMiner, tp TransactionPool, h Host, cs ContractStore) http.Handler {
	srv := server{
		cm: cm,
		s:  s,
		w:  w,
		tp: tp,

		m: m,
	}
	mux := httprouter.New()

	mux.GET("/api/consensus/tip", srv.consensusTipHandler)

	mux.GET("/api/wallet/balance", srv.walletBalanceHandler)
	mux.GET("/api/wallet/address", srv.walletAddressHandler)
	mux.GET("/api/wallet/transactions", srv.walletTransactionsHandler)

	mux.GET("/api/syncer/peers", srv.syncerPeersHandler)
	mux.POST("/api/syncer/connect", srv.syncerConnectHandler)

	mux.POST("/api/miner/start", srv.minerStartHandler)
	mux.POST("/api/miner/stop", srv.minerStopHandler)

	return mux
}

type (
	event struct {
		Type  string     `json:"type"`
		Event host.Event `json:"event"`
	}

	wsSubscriber struct {
		log  host.Logger
		req  *http.Request
		conn *websocket.Conn
	}
)

// OnEvent writes an event to all websocket subscribers.
func (ws *wsSubscriber) OnEvent(ev host.Event) {
	var eventType string
	switch ev.(type) {
	case host.EventSessionStart:
		eventType = "sessionStart"
	case host.EventSessionEnd:
		eventType = "sessionEnd"
	case host.EventRPCStart:
		eventType = "rpcStart"
	case host.EventRPCEnd:
		eventType = "rpcEnd"
	case host.EventInstructionExecuted:
		eventType = "instructionExecuted"
	case host.EventContractFormed:
		eventType = "contractFormed"
	case host.EventContractRenewed:
		eventType = "contractRenewed"
	}
	buf, err := json.Marshal(event{
		Type:  eventType,
		Event: ev,
	})
	if err != nil {
		ws.log.Errorf("unable to marshal event: %v", err)
		return
	} else if err := ws.conn.Write(ws.req.Context(), websocket.MessageText, buf); err != nil {
		ws.log.Errorf("unable to write event: %v", err)
	}
}

// NewWebsocketServer returns an HTTP handler that provides real-time hostd events.
func NewWebsocketServer(cm ChainManager, mr host.MetricRecorder, log host.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{"echo"},
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			log.Warnln("websocket accept failed:", err)
			return
		}
		defer conn.Close(websocket.StatusInternalError, "")
		// handles keepalives, closes and ignores data sent from the client.
		ctx := conn.CloseRead(r.Context())
		wss := &wsSubscriber{
			log:  log.Scope("subscriber"),
			req:  r,
			conn: conn,
		}
		mr.Subscribe(wss)
		log.Infoln("new websocket client")
		// wait for the connection to be closed.
		<-ctx.Done()
		mr.Unsubscribe(wss)
		log.Infoln("websocket client disconnect")
	})
}
