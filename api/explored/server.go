package explored

import (
	"encoding/json"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
)

type (
	// A Syncer can connect to other peers and synchronize the blockchain.
	Syncer interface {
		Addr() string
		Peers() []string
		Connect(addr string) error
		BroadcastTransaction(txn types.Transaction, dependsOn []types.Transaction)
	}

	// A TransactionPool can validate and relay unconfirmed transactions.
	TransactionPool interface {
		Transactions() []types.Transaction
		AddTransaction(txn types.Transaction) error
	}

	// A ChainManager manages blockchain state.
	ChainManager interface {
		TipState() consensus.State
	}
)

type server struct {
	s  Syncer
	cm ChainManager
	tp TransactionPool
}

func (s *server) txpoolBroadcastHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var tbr TxpoolBroadcastRequest
	if err := json.NewDecoder(req.Body).Decode(&tbr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, txn := range tbr.DependsOn {
		if err := s.tp.AddTransaction(txn); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if err := s.tp.AddTransaction(tbr.Transaction); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.s.BroadcastTransaction(tbr.Transaction, tbr.DependsOn)
}

func (s *server) txpoolTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.WriteJSON(w, s.tp.Transactions())
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

func (s *server) consensusTipHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	cs := s.cm.TipState()
	api.WriteJSON(w, ConsensusTipResponse{
		Index:             cs.Index,
		TotalWork:         cs.TotalWork,
		Difficulty:        cs.Difficulty,
		OakWork:           cs.OakWork,
		OakTime:           cs.OakTime,
		SiafundPool:       cs.SiafundPool,
		FoundationAddress: cs.FoundationAddress,
	})
}

// NewServer returns an HTTP handler that serves the explored API.
func NewServer(cm ChainManager, s Syncer, tp TransactionPool) http.Handler {
	srv := server{
		cm: cm,
		s:  s,
		tp: tp,
	}
	mux := httprouter.New()

	mux.GET("/api/txpool/transactions", srv.txpoolTransactionsHandler)
	mux.POST("/api/txpool/broadcast", srv.txpoolBroadcastHandler)

	mux.GET("/api/syncer/peers", srv.syncerPeersHandler)
	mux.POST("/api/syncer/connect", srv.syncerConnectHandler)

	mux.GET("/api/consensus/tip", srv.consensusTipHandler)

	return mux
}
