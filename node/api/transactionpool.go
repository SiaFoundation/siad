package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/julienschmidt/httprouter"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

type (
	// TpoolFeeGET contains the current estimated fee
	TpoolFeeGET struct {
		Minimum types.Currency `json:"minimum"`
		Maximum types.Currency `json:"maximum"`
	}

	// TpoolRawGET contains the requested transaction encoded to the raw
	// format, along with the id of that transaction.
	TpoolRawGET struct {
		ID          types.TransactionID `json:"id"`
		Parents     []byte              `json:"parents"`
		Transaction []byte              `json:"transaction"`
	}

	// TpoolConfirmedGET contains information about whether or not
	// the transaction has been seen on the blockhain
	TpoolConfirmedGET struct {
		Confirmed bool `json:"confirmed"`
	}

	// TpoolTxnsGET contains the information about the tpool's transactions
	TpoolTxnsGET struct {
		Transactions []types.Transaction `json:"transactions"`
	}

	// TpoolRawPOST reply with tx data
	TpoolRawPOST struct {
		Transaction types.Transaction `json:"transaction"`
	}
)

// RegisterRoutesTransactionPool is a helper function to register all
// transaction pool routes.
func RegisterRoutesTransactionPool(router *httprouter.Router, tpool modules.TransactionPool) {
	router.GET("/tpool/fee", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		tpoolFeeHandlerGET(tpool, w, req, ps)
	})
	router.GET("/tpool/raw/:id", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		tpoolRawHandlerGET(tpool, w, req, ps)
	})
	router.POST("/tpool/raw", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		tpoolRawHandlerPOST(tpool, w, req, ps)
	})
	router.GET("/tpool/confirmed/:id", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		tpoolConfirmedGET(tpool, w, req, ps)
	})
	router.GET("/tpool/transactions", func(w http.ResponseWriter, req *http.Request, ps httprouter.Params) {
		tpoolTransactionsHandler(tpool, w, req, ps)
	})
}

// decodeTransactionID will decode a transaction id from a string.
func decodeTransactionID(txidStr string) (types.TransactionID, error) {
	txid := new(crypto.Hash)
	err := txid.LoadString(txidStr)
	if err != nil {
		return types.TransactionID{}, err
	}
	return types.TransactionID(*txid), nil
}

// tpoolFeeHandlerGET returns the current estimated fee. Transactions with
// fees are lower than the estimated fee may take longer to confirm.
func tpoolFeeHandlerGET(tpool modules.TransactionPool, w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
	min, max := tpool.FeeEstimation()
	WriteJSON(w, TpoolFeeGET{
		Minimum: min,
		Maximum: max,
	})
}

// tpoolRawHandlerGET will provide the raw byte representation of a
// transaction that matches the input id.
func tpoolRawHandlerGET(tpool modules.TransactionPool, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	txid, err := decodeTransactionID(ps.ByName("id"))
	if err != nil {
		WriteError(w, Error{"error decoding transaction id: " + err.Error()}, http.StatusBadRequest)
		return
	}
	txn, parents, exists := tpool.Transaction(txid)
	if !exists {
		WriteError(w, Error{"transaction not found in transaction pool"}, http.StatusBadRequest)
		return
	}

	WriteJSON(w, TpoolRawGET{
		ID:          txid,
		Parents:     encoding.Marshal(parents),
		Transaction: encoding.Marshal(txn),
	})
}

// tpoolRawHandlerPOST takes a raw encoded transaction set and posts
// it to the transaction pool, relaying it to the transaction pool's peers
// regardless of if the set is accepted.
func tpoolRawHandlerPOST(tpool modules.TransactionPool, w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	var parents []types.Transaction
	var txn types.Transaction

	// JSON, base64, and raw binary are accepted
	if err := json.Unmarshal([]byte(req.FormValue("parents")), &parents); err != nil {
		rawParents, err := base64.StdEncoding.DecodeString(req.FormValue("parents"))
		if err != nil {
			rawParents = []byte(req.FormValue("parents"))
		}
		if err := encoding.Unmarshal(rawParents, &parents); err != nil {
			WriteError(w, Error{"error decoding parents: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	if err := json.Unmarshal([]byte(req.FormValue("transaction")), &txn); err != nil {
		rawTransaction, err := base64.StdEncoding.DecodeString(req.FormValue("transaction"))
		if err != nil {
			rawTransaction = []byte(req.FormValue("transaction"))
		}
		if err := encoding.Unmarshal(rawTransaction, &txn); err != nil {
			WriteError(w, Error{"error decoding transaction: " + err.Error()}, http.StatusBadRequest)
			return
		}
	}
	// Broadcast the transaction set, so that they are passed to any peers that
	// may have rejected them earlier.
	txnSet := append(parents, txn)
	tpool.Broadcast(txnSet)
	err := tpool.AcceptTransactionSet(txnSet)
	if err != nil && !errors.Contains(err, modules.ErrDuplicateTransactionSet) {
		WriteError(w, Error{"error accepting transaction set: " + err.Error()}, http.StatusBadRequest)
		return
	}
	
	WriteJSON(w, TpoolRawPOST{
		Transaction: txn,
	})
}

// tpoolConfirmedGET returns whether the specified transaction has
// been seen on the blockchain.
func tpoolConfirmedGET(tpool modules.TransactionPool, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	txid, err := decodeTransactionID(ps.ByName("id"))
	if err != nil {
		WriteError(w, Error{"error decoding transaction id: " + err.Error()}, http.StatusBadRequest)
		return
	}
	confirmed, err := tpool.TransactionConfirmed(txid)
	if err != nil {
		WriteError(w, Error{"error fetching transaction status: " + err.Error()}, http.StatusBadRequest)
		return
	}
	WriteJSON(w, TpoolConfirmedGET{
		Confirmed: confirmed,
	})
}

// tpoolTransactionsHandler returns the current transactions of the transaction
// pool
func tpoolTransactionsHandler(tpool modules.TransactionPool, w http.ResponseWriter, _ *http.Request, ps httprouter.Params) {
	txns := tpool.Transactions()
	WriteJSON(w, TpoolTxnsGET{
		Transactions: txns,
	})
}
