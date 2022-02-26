package renterd

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/renter"
	"go.sia.tech/siad/v2/wallet"
)

type (
	// A Wallet can spend and receive siacoins.
	Wallet interface {
		Balance() types.Currency
		Address() types.Address
		NextAddress() types.Address
		Addresses() []types.Address
		SpendPolicy(types.Address) (types.SpendPolicy, bool)
		Transactions(since time.Time, max int) []wallet.Transaction
		FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error)
		SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error
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
		RecommendedFee() types.Currency
		Transactions() []types.Transaction
		AddTransaction(txn types.Transaction) error
		AddTransactionSet(txns []types.Transaction) error
	}

	// A ChainManager manages blockchain state.
	ChainManager interface {
		TipContext() consensus.ValidationContext
	}
)

type server struct {
	w  Wallet
	s  Syncer
	cm ChainManager
	tp TransactionPool
}

func (s *server) walletBalanceHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.WriteJSON(w, WalletBalanceResponse{
		Siacoins: s.w.Balance(),
	})
}

func (s *server) walletAddressHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	api.WriteJSON(w, WalletAddressResponse{s.w.NextAddress()})
}

func (s *server) walletAddressesHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
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
	api.WriteJSON(w, s.w.Transactions(since, max))
}

func (s *server) walletSignHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var wsr WalletSignRequest
	if err := json.NewDecoder(req.Body).Decode(&wsr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	txn, toSign := wsr.Transaction, wsr.ToSign
	if err := s.w.SignTransaction(s.cm.TipContext(), &txn, toSign); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, WalletTransactionResponse{txn})
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

func (s *server) contractsScanHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var cbr ContractsScanRequest
	if err := json.NewDecoder(req.Body).Decode(&cbr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session, err := renter.NewSession(cbr.NetAddress, cbr.HostKey, s.w, s.tp, s.cm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer session.Close()

	settings, err := session.ScanSettings()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, settings)
}

func (s *server) contractsFormHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var cfr ContractsFormRequest
	if err := json.NewDecoder(req.Body).Decode(&cfr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session, err := renter.NewSession(cfr.NetAddress, cfr.HostKey, s.w, s.tp, s.cm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer session.Close()

	contract, parent, err := session.FormContract(cfr.RenterKey, cfr.HostFunds, cfr.RenterFunds, cfr.EndHeight, cfr.HostSettings)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, ContractsFormResponse{contract, parent})
}

func (s *server) contractsRenewHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var crr ContractsRenewRequest
	if err := json.NewDecoder(req.Body).Decode(&crr); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	session, err := renter.NewSession(crr.NetAddress, crr.HostKey, s.w, s.tp, s.cm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer session.Close()

	// TODO: compare request settings to current host settings

	contract, parent, err := session.RenewContract(crr.RenterKey, crr.Contract, crr.RenterFunds, crr.HostCollateral, crr.Extension)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	api.WriteJSON(w, ContractsRenewResponse{contract, parent})
}

// NewServer returns an HTTP handler that serves the renterd API.
func NewServer(cm ChainManager, s Syncer, w Wallet, tp TransactionPool) http.Handler {
	srv := server{
		cm: cm,
		s:  s,
		w:  w,
		tp: tp,
	}
	mux := httprouter.New()

	mux.GET("/api/wallet/balance", srv.walletBalanceHandler)
	mux.GET("/api/wallet/address", srv.walletAddressHandler)
	mux.GET("/api/wallet/addresses", srv.walletAddressesHandler)
	mux.GET("/api/wallet/transactions", srv.walletTransactionsHandler)
	mux.POST("/api/wallet/sign", srv.walletSignHandler)

	mux.GET("/api/txpool/transactions", srv.txpoolTransactionsHandler)
	mux.POST("/api/txpool/broadcast", srv.txpoolBroadcastHandler)

	mux.GET("/api/syncer/peers", srv.syncerPeersHandler)
	mux.POST("/api/syncer/connect", srv.syncerConnectHandler)

	mux.POST("/api/contracts/scan", srv.contractsScanHandler)
	mux.POST("/api/contracts/form", srv.contractsFormHandler)
	mux.POST("/api/contracts/renew", srv.contractsRenewHandler)

	return mux
}
