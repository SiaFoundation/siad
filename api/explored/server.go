package explored

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/julienschmidt/httprouter"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api"
	"go.sia.tech/siad/v2/explorer"
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

	// An Explorer contains a database storing information about blocks, outputs,
	// contracts.
	Explorer interface {
		SiacoinElement(id types.ElementID) (types.SiacoinElement, error)
		SiafundElement(id types.ElementID) (types.SiafundElement, error)
		FileContractElement(id types.ElementID) (types.FileContractElement, error)
		ChainStats(index types.ChainIndex) (explorer.ChainStats, error)
		ChainStatsLatest() (explorer.ChainStats, error)
		SiacoinBalance(address types.Address) (types.Currency, error)
		SiafundBalance(address types.Address) (uint64, error)
		Transaction(id types.TransactionID) (types.Transaction, error)
		UnspentSiacoinElements(address types.Address) ([]types.ElementID, error)
		UnspentSiafundElements(address types.Address) ([]types.ElementID, error)
		Transactions(address types.Address, amount, offset int) ([]types.TransactionID, error)
		State(index types.ChainIndex) (context consensus.State, err error)
	}
)

type server struct {
	s  Syncer
	e  Explorer
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

func (s *server) explorerElementSiacoinHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var id types.ElementID
	if err := id.UnmarshalText([]byte(p.ByName("id"))); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	elem, err := s.e.SiacoinElement(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, elem)
}

func (s *server) explorerElementSiafundHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var id types.ElementID
	if err := id.UnmarshalText([]byte(p.ByName("id"))); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	elem, err := s.e.SiafundElement(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, elem)
}

func (s *server) explorerElementContractHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var id types.ElementID
	if err := id.UnmarshalText([]byte(p.ByName("id"))); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	elem, err := s.e.FileContractElement(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, elem)
}

func (s *server) explorerChainStatsHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	if p.ByName("index") == "latest" {
		facts, err := s.e.ChainStatsLatest()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		api.WriteJSON(w, facts)
		return
	}

	index, err := types.ParseChainIndex(p.ByName("index"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	facts, err := s.e.ChainStats(index)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, facts)
}

func (s *server) explorerChainStateHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	index, err := types.ParseChainIndex(p.ByName("index"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	vc, err := s.e.State(index)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, vc)
}

func (s *server) explorerElementSearchHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var id types.ElementID
	if err := id.UnmarshalText([]byte(p.ByName("id"))); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var response ExplorerSearchResponse
	if elem, err := s.e.SiacoinElement(id); err == nil {
		response.Type = "siacoin"
		response.SiacoinElement = elem
	} else if elem, err := s.e.SiafundElement(id); err == nil {
		response.Type = "siafund"
		response.SiafundElement = elem
	} else if elem, err := s.e.FileContractElement(id); err == nil {
		response.Type = "contract"
		response.FileContractElement = elem
	}
	api.WriteJSON(w, response)
}

func (s *server) explorerAddressBalanceHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var address types.Address
	if err := json.Unmarshal([]byte(p.ByName("address")), &address); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scBalance, err := s.e.SiacoinBalance(address)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sfBalance, err := s.e.SiafundBalance(address)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, ExplorerWalletBalanceResponse{scBalance, sfBalance})
}

func (s *server) explorerAddressSiacoinsHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var address types.Address
	if err := json.Unmarshal([]byte(p.ByName("address")), &address); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	outputs, err := s.e.UnspentSiacoinElements(address)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, outputs)
}

func (s *server) explorerAddressSiafundsHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var address types.Address
	if err := json.Unmarshal([]byte(p.ByName("address")), &address); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	outputs, err := s.e.UnspentSiafundElements(address)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, outputs)
}

func (s *server) explorerAddressTransactionsHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var address types.Address
	if err := json.Unmarshal([]byte(p.ByName("address")), &address); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	amount, err := strconv.Atoi(req.FormValue("amount"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	offset, err := strconv.Atoi(req.FormValue("offset"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ids, err := s.e.Transactions(address, amount, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, ids)
}

func (s *server) explorerTransactionHandler(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
	var id types.TransactionID
	if err := json.Unmarshal([]byte(p.ByName("id")), &id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	txn, err := s.e.Transaction(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	api.WriteJSON(w, txn)
}

func (s *server) explorerBatchAddressesBalanceHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var addresses []types.Address
	if err := json.NewDecoder(req.Body).Decode(&addresses); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var balances []ExplorerWalletBalanceResponse
	for _, address := range addresses {
		scBalance, err := s.e.SiacoinBalance(address)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sfBalance, err := s.e.SiafundBalance(address)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		balances = append(balances, ExplorerWalletBalanceResponse{scBalance, sfBalance})
	}
	api.WriteJSON(w, balances)
}

func (s *server) explorerBatchAddressesSiacoinsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var addresses []types.Address
	if err := json.NewDecoder(req.Body).Decode(&addresses); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var elems [][]types.SiacoinElement
	for _, address := range addresses {
		ids, err := s.e.UnspentSiacoinElements(address)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var elemsList []types.SiacoinElement
		for _, id := range ids {
			elem, err := s.e.SiacoinElement(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			elemsList = append(elemsList, elem)
		}
		elems = append(elems, elemsList)
	}
	api.WriteJSON(w, elems)
}

func (s *server) explorerBatchAddressesSiafundsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var addresses []types.Address
	if err := json.NewDecoder(req.Body).Decode(&addresses); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var elems [][]types.SiafundElement
	for _, address := range addresses {
		ids, err := s.e.UnspentSiafundElements(address)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var elemsList []types.SiafundElement
		for _, id := range ids {
			elem, err := s.e.SiafundElement(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			elemsList = append(elemsList, elem)
		}
		elems = append(elems, elemsList)
	}
	api.WriteJSON(w, elems)
}

func (s *server) explorerBatchAddressesTransactionsHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var etrs []ExplorerTransactionsRequest
	if err := json.NewDecoder(req.Body).Decode(&etrs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var txns [][]types.Transaction
	for _, etr := range etrs {
		ids, err := s.e.Transactions(etr.Address, etr.Amount, etr.Offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var txnsList []types.Transaction
		for _, id := range ids {
			txn, err := s.e.Transaction(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			txnsList = append(txnsList, txn)
		}
		txns = append(txns, txnsList)
	}
	api.WriteJSON(w, txns)
}

// NewServer returns an HTTP handler that serves the explored API.
func NewServer(cm ChainManager, s Syncer, tp TransactionPool, e Explorer) http.Handler {
	srv := server{
		cm: cm,
		s:  s,
		tp: tp,
		e:  e,
	}
	mux := httprouter.New()

	mux.GET("/api/txpool/transactions", srv.txpoolTransactionsHandler)
	mux.POST("/api/txpool/broadcast", srv.txpoolBroadcastHandler)

	mux.GET("/api/syncer/peers", srv.syncerPeersHandler)
	mux.POST("/api/syncer/connect", srv.syncerConnectHandler)

	mux.GET("/api/consensus/tip", srv.consensusTipHandler)

	mux.GET("/api/explorer/element/search/:id", srv.explorerElementSearchHandler)
	mux.GET("/api/explorer/element/siacoin/:id", srv.explorerElementSiacoinHandler)
	mux.GET("/api/explorer/element/siafund/:id", srv.explorerElementSiafundHandler)
	mux.GET("/api/explorer/element/contract/:id", srv.explorerElementContractHandler)
	mux.GET("/api/explorer/chain/stats/:index", srv.explorerChainStatsHandler)
	mux.GET("/api/explorer/chain/state/:index", srv.explorerChainStateHandler)
	mux.GET("/api/explorer/transaction/:id", srv.explorerTransactionHandler)

	mux.GET("/api/explorer/address/balance/:address", srv.explorerAddressBalanceHandler)
	mux.GET("/api/explorer/address/siacoins/:address", srv.explorerAddressSiacoinsHandler)
	mux.GET("/api/explorer/address/siafunds/:address", srv.explorerAddressSiacoinsHandler)
	mux.GET("/api/explorer/address/transactions/:address", srv.explorerAddressTransactionsHandler)

	mux.POST("/api/explorer/batch/addresses/balance", srv.explorerBatchAddressesBalanceHandler)
	mux.POST("/api/explorer/batch/addresses/siacoins", srv.explorerBatchAddressesSiacoinsHandler)
	mux.POST("/api/explorer/batch/addresses/siafunds", srv.explorerBatchAddressesSiafundsHandler)
	mux.POST("/api/explorer/batch/addresses/transactions", srv.explorerBatchAddressesTransactionsHandler)

	return mux
}
