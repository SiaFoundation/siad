package siad

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/wallet"
)

type (
	// A WalletStore tracks transactions and outputs associated with a set of
	// addresses.
	WalletStore interface {
		SeedIndex() uint64
		Balance() (types.Currency, uint64)
		AddAddress(addr types.Address, info wallet.AddressInfo) error
		AddressInfo(addr types.Address) (wallet.AddressInfo, error)
		Addresses() ([]types.Address, error)
		UnspentSiacoinElements() ([]types.SiacoinElement, error)
		UnspentSiafundElements() ([]types.SiafundElement, error)
		Transactions(since time.Time, max int) ([]wallet.Transaction, error)
	}

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
		TipContext() consensus.ValidationContext
	}
)

type server struct {
	w  WalletStore
	s  Syncer
	cm ChainManager
	tp TransactionPool
}

func (s *server) walletBalanceHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	sc, sf := s.w.Balance()
	api.WriteJSON(w, WalletBalanceResponse{
		Siacoins: sc,
		Siafunds: sf,
	})
}

func (s *server) walletSeedIndexHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.WriteJSON(w, s.w.SeedIndex())
}

func (s *server) walletAddressHandlerPOST(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	addr, err := types.ParseAddress(ps.ByName("addr"))
	if err != nil {
		http.Error(w, "invalid address: "+err.Error(), http.StatusBadRequest)
		return
	}
	var info wallet.AddressInfo
	if err := json.NewDecoder(req.Body).Decode(&info); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.w.AddAddress(addr, info); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (s *server) walletAddressHandlerGET(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
	addr, err := types.ParseAddress(ps.ByName("addr"))
	if err != nil {
		http.Error(w, "invalid address: "+err.Error(), http.StatusBadRequest)
		return
	}
	info, err := s.w.AddressInfo(addr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, info)
}

func (s *server) walletAddressesHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	addresses, err := s.w.Addresses()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	api.WriteJSON(w, WalletAddressesResponse(addresses[start:end]))
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

func (s *server) walletUTXOsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	scos, err := s.w.UnspentSiacoinElements()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sfos, err := s.w.UnspentSiafundElements()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, WalletUTXOsResponse{
		Siacoins: scos,
		Siafunds: sfos,
	})
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

// NewServer returns an HTTP handler that serves the siad API.
func NewServer(cm ChainManager, s Syncer, w WalletStore, tp TransactionPool) http.Handler {
	srv := server{
		cm: cm,
		s:  s,
		w:  w,
		tp: tp,
	}
	mux := httprouter.New()

	mux.GET("/api/wallet/balance", srv.walletBalanceHandler)
	mux.GET("/api/wallet/seedindex", srv.walletSeedIndexHandler)
	mux.POST("/api/wallet/address/:addr", srv.walletAddressHandlerPOST)
	mux.GET("/api/wallet/address/:addr", srv.walletAddressHandlerGET)
	mux.GET("/api/wallet/addresses", srv.walletAddressesHandler)
	mux.GET("/api/wallet/transactions", srv.walletTransactionsHandler)
	mux.GET("/api/wallet/utxos", srv.walletUTXOsHandler)

	mux.GET("/api/txpool/transactions", srv.txpoolTransactionsHandler)
	mux.POST("/api/txpool/broadcast", srv.txpoolBroadcastHandler)

	mux.GET("/api/syncer/peers", srv.syncerPeersHandler)
	mux.POST("/api/syncer/connect", srv.syncerConnectHandler)

	return mux
}
