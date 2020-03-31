package modules

import (
	"bytes"
	"io"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
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

		// Cancelled indicates whether or not this fee was cancelled
		Cancelled bool `json:"cancelled"`

		// Offset is the fee's offset in the persist file on disk
		Offset int64 `json:"offset"`

		// Recurring indicates whether or not this fee is a recurring fee and
		// will be charged in the next period as well
		Recurring bool `json:"recurring"`

		// UID is a unique identifier for the Fee
		UID FeeUID `json:"uid"`
	}

	// FeeManagerSettings are the set of FeeManager fields that are important
	// externally
	FeeManagerSettings struct {
		// CurrentPayout is how much currently will be paid out at the
		// PayoutHeight
		CurrentPayout types.Currency `json:"currentpayout"`

		// MaxPayout is the maximum that will be paid out per payout period
		MaxPayout types.Currency `json:"maxpayout"`

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

	// Fees returns all the fees that are being tracked by the FeeManager
	Fees() (pending []AppFee, paid []AppFee, err error)

	// SetFee sets a fee for the FeeManager to manage
	SetFee(address types.UnlockHash, amount types.Currency, appUID AppUID, recurring bool) error

	// Settings returns the settings of the FeeManager
	Settings() (FeeManagerSettings, error)
}

// marshalSia implements the encoding.SiaMarshaler interface.
func (fee *AppFee) marshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Encode(fee.Address)
	e.Encode(fee.Amount)
	e.Encode(fee.AppUID)
	e.WriteBool(fee.Cancelled)
	e.Encode(fee.Offset)
	e.WriteBool(fee.Recurring)
	e.Encode(fee.UID)
	return e.Err()
}

// unmarshalSia implements the encoding.SiaUnmarshaler interface.
func (fee *AppFee) unmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&fee.Address)
	d.Decode(&fee.Amount)
	d.Decode(&fee.AppUID)
	fee.Cancelled = d.NextBool()
	d.Decode(&fee.Offset)
	fee.Recurring = d.NextBool()
	d.Decode(&fee.UID)
	return d.Err()
}

// MarshalFee marshals the AppFee using Sia encoding.
func MarshalFee(fee AppFee) ([]byte, error) {
	// Create a buffer.
	var buf bytes.Buffer
	// Marshal all the data into the buffer
	err := fee.marshalSia(&buf)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalFees unmarshals the sia encoded fees.
func UnmarshalFees(raw []byte) (fees []AppFee, err error) {
	// Create the buffer.
	r := bytes.NewBuffer(raw)
	// Unmarshal the fees one by one until EOF or a different error occur.
	for {
		var fee AppFee
		if err = fee.unmarshalSia(r); err == io.EOF {
			break
		} else if err != nil {
			return nil, errors.AddContext(err, "unable to unmarshal fee")
		}
		fees = append(fees, fee)
	}
	return fees, nil
}
