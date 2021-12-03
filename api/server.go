package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/gateway"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/wallet"

	"github.com/julienschmidt/httprouter"
)

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	// encode nil slices as [] and nil maps as {} (instead of null)
	if val := reflect.ValueOf(v); val.Kind() == reflect.Slice && val.Len() == 0 {
		w.Write([]byte("[]\n"))
		return
	} else if val.Kind() == reflect.Map && val.Len() == 0 {
		w.Write([]byte("{}\n"))
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "\t")
	enc.Encode(v)
}

// A Wallet can spend and receive siacoins.
type Wallet interface {
	Balance() types.Currency
	NextAddress() types.Address
	Addresses() []types.Address
	Transactions(since time.Time, max int) []wallet.Transaction
	FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error)
	SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error
}

// A Syncer can connect to other peers and synchronize the blockchain.
type Syncer interface {
	Addr() string
	Peers() []gateway.Header
	Connect(addr string) error
	BroadcastTransaction(txn types.Transaction, dependsOn []types.Transaction)
}

// A TransactionPool can validate and relay unconfirmed transactions.
type TransactionPool interface {
	Transactions() []types.Transaction
	AddTransaction(txn types.Transaction) error
}

// A ChainManager manages blockchain state.
type ChainManager interface {
	TipContext() (consensus.ValidationContext, error)
}

// A Server serves the siad API.
type Server struct {
	w  Wallet
	s  Syncer
	cm ChainManager
	tp TransactionPool
}

func (s *Server) walletBalanceHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, WalletBalance{
		Siacoins: s.w.Balance(),
	})
}

func (s *Server) walletAddressHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, WalletAddress{s.w.NextAddress()})
}

func (s *Server) walletAddressesHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	addresses := s.w.Addresses()
	start, end := 0, len(addresses)
	if v := req.FormValue("start"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i > len(addresses) {
			http.Error(w, "invalid start value", http.StatusBadRequest)
			return
		}
		start = i
	}
	if v := req.FormValue("end"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i < start {
			http.Error(w, "invalid end value", http.StatusBadRequest)
			return
		} else if i > len(addresses) {
			i = len(addresses)
		}
		end = i
	}
	writeJSON(w, WalletAddresses(addresses[start:end]))
}

func (s *Server) walletTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
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
	writeJSON(w, s.w.Transactions(since, max))
}

func (s *Server) walletSignHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var wsr WalletSignRequest
	if err := json.NewDecoder(req.Body).Decode(&wsr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	txn, toSign := wsr.Transaction, wsr.ToSign
	if len(toSign) == 0 {
		// lazy mode: add standard sigs for every input we own
		for _, input := range txn.SiacoinInputs {
			toSign = append(toSign, input.Parent.ID)
		}
	}

	vc, err := s.cm.TipContext()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := s.w.SignTransaction(vc, &txn, nil); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, WalletTransaction{txn})
}

func (s *Server) txpoolBroadcastHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
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

func (s *Server) txpoolTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, s.tp.Transactions())
}

func (s *Server) syncerPeersHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	ps := s.s.Peers()
	sps := make([]SyncerPeer, len(ps))
	for i, peer := range ps {
		sps[i] = SyncerPeer{
			NetAddress: peer.NetAddress,
		}
	}
	writeJSON(w, sps)
}

func (s *Server) syncerConnectHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
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

// NewServer returns an HTTP handler that serves the siad API.
func NewServer(w Wallet, s Syncer, cm ChainManager, tp TransactionPool) http.Handler {
	srv := Server{
		w:  w,
		s:  s,
		cm: cm,
		tp: tp,
	}
	mux := httprouter.New()

	mux.GET("/wallet/balance", srv.walletBalanceHandler)
	mux.GET("/wallet/address", srv.walletAddressHandler)
	mux.GET("/wallet/addresses", srv.walletAddressesHandler)
	mux.GET("/wallet/transactions", srv.walletTransactionsHandler)
	mux.POST("/wallet/sign", srv.walletSignHandler)

	mux.GET("/txpool/transactions", srv.txpoolTransactionsHandler)
	mux.POST("/txpool/broadcast", srv.txpoolBroadcastHandler)

	mux.GET("/syncer/peers", srv.syncerPeersHandler)
	mux.POST("/syncer/connect", srv.syncerConnectHandler)

	return mux
}
