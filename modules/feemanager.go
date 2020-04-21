package modules

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// FeeManagerDir is the name of the directory that is used to store the
	// FeeManager's persistent data
	FeeManagerDir = "feemanager"
)

// AppUID is a unique identifier for an application that had submitted a fee to
// the FeeManager
type AppUID string

// FeeUID is a unique identifier for a fee that is being managed by the
// FeeManager
type FeeUID string

type (
	// AppFee is the struct that contains information about a fee submitted by
	// an application to the FeeManager
	AppFee struct {
		// Address of the developer wallet
		Address types.UnlockHash `json:"address"`

		// Amount of SC that the Fee is for
		Amount types.Currency `json:"amount"`

		// AppUID is a unique Application ID that the fee is for
		AppUID AppUID `json:"appuid"`

		// PaymentCompleted indicates whether the payment for this fee has
		// appeared on-chain.
		PaymentCompleted bool `json:"paymentcompleted"`

		// PayoutHeight is the height at which the fee will be paid out.
		PayoutHeight types.BlockHeight `json:"payoutheight"`

		// Recurring indicates whether or not this fee is a recurring fee and
		// will be charged in the next period as well
		Recurring bool `json:"recurring"`

		// Timestamp is the moment that the fee was requested.
		Timestamp int64

		// TransactionCreated indicates whether the transaction for this fee has
		// been created and sent to the Sia network for processing.
		TransactionCreated bool `json:"transactioncreated"`

		// UID is a unique identifier for the Fee
		UID FeeUID `json:"uid"`
	}

	// FeeManagerSettings are the set of FeeManager fields that are important
	// externally
	FeeManagerSettings struct {
		// PayoutHeight is the blockheight at which the next payout will occur
		PayoutHeight types.BlockHeight `json:"payoutheight"`
	}
)

// FeeManager manages fees for applications
type FeeManager interface {
	// Close closes the FeeManager
	Close() error

	// CancelFee cancels the fee associated with the FeeUID
	CancelFee(feeUID FeeUID) error

	// PaidFees returns all the paid fees that are being tracked by the
	// FeeManager
	PaidFees() ([]AppFee, error)

	// PendingFees returns all the pending fees that are being tracked by the
	// FeeManager
	PendingFees() ([]AppFee, error)

	// AddFee adds a fee for the FeeManager to manage
	AddFee(address types.UnlockHash, amount types.Currency, appUID AppUID, recurring bool) error

	// Settings returns the settings of the FeeManager
	Settings() (FeeManagerSettings, error)
}
