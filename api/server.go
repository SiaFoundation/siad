package api

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/gateway"
	"go.sia.tech/siad/v2/p2p"
	"go.sia.tech/siad/v2/txpool"
	"go.sia.tech/siad/v2/wallet"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/types"
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

type Wallet interface {
	Balance() types.Currency
	NextAddress() types.Address
	Addresses() []types.Address
	Transactions() []wallet.Transaction
	FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error)
	SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error
}

type Syncer interface {
	Addr() string
	Peers() []gateway.Header
	Connect(addr string) error
	BroadcastTransaction(txn types.Transaction, dependsOn []types.Transaction)
}

type TransactionPool interface {
	Transactions() []types.Transaction
	AddTransaction(txn types.Transaction) error
}

type ChainManager interface {
	Tip() types.ChainIndex
	ValidationContext(index types.ChainIndex) (consensus.ValidationContext, error)
}

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
	writeJSON(w, WalletAddresses(s.w.Addresses()))
}

func (s *Server) walletTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, WalletTransactions(s.w.Transactions()))
}

func (s *Server) walletSignHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var sign WalletSignData
	if err := json.NewDecoder(req.Body).Decode(&sign); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	txn, toSign := sign.Transaction, sign.ToSign
	if len(toSign) == 0 {
		// lazy mode: add standard sigs for every input we own
		for _, input := range txn.SiacoinInputs {
			toSign = append(toSign, input.Parent.ID)
		}
	}

	vc, err := s.cm.ValidationContext(s.cm.Tip())
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

func (s *Server) walletBroadcastTransactionHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var txn types.Transaction
	if err := json.NewDecoder(req.Body).Decode(&txn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.tp.AddTransaction(txn); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.s.BroadcastTransaction(txn, nil)
}

func (s *Server) txpoolTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, TxpoolTransactions(s.tp.Transactions()))
}

func (s *Server) syncerPeersHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, SyncerPeers(s.s.Peers()))
}

func (s *Server) syncerAddressHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, SyncerAddress{s.s.Addr()})
}

func (s *Server) syncerConnectHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var addr string
	if err := json.NewDecoder(req.Body).Decode(&addr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.s.Connect(addr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// NewServer returns an HTTP handler that serves the siad API.
func NewServer(w Wallet, s *p2p.Syncer, cm *chain.Manager, tp *txpool.Pool) http.Handler {
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
	mux.POST("/wallet/broadcast", srv.walletBroadcastTransactionHandler)

	mux.GET("/txpool/transactions", srv.txpoolTransactionsHandler)

	mux.GET("/syncer/peers", srv.syncerPeersHandler)
	mux.GET("/syncer/address", srv.syncerAddressHandler)
	mux.POST("/syncer/connect", srv.syncerConnectHandler)

	return mux
}
