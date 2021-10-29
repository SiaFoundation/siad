package api

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/julienschmidt/httprouter"
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
}

type Server struct {
	w Wallet
}

func (s *Server) walletBalanceHandler(w http.ResponseWriter, req *http.Request, _ httprouter.Params) {
	writeJSON(w, WalletBalance{
		Siacoins: s.w.Balance(),
	})
}

// NewServer returns an HTTP handler that serves the siad API.
func NewServer(w Wallet) http.Handler {
	s := Server{
		w: w,
	}
	mux := httprouter.New()
	mux.GET("/wallet/balance", s.walletBalanceHandler)
	return mux
}
