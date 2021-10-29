package api

import "go.sia.tech/core/types"

type WalletBalance struct {
	Siacoins types.Currency `json:"siacoins"`
}
