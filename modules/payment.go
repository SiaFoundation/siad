package modules

import (
	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrInsufficientPaymentForRPC is returned when the provided payment was
	// lower than the cost of the RPC.
	ErrInsufficientPaymentForRPC = errors.New("Insufficient payment, the provided payment did not cover the cost of the RPC.")
)
