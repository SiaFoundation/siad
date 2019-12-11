package proto

import (
	"io"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// v132UpdateHeader was introduced due to backwards compatibility reasons after
// changing the format of the contractHeader. It contains the legacy
// v132ContractHeader.
type v132UpdateSetHeader struct {
	ID     types.FileContractID
	Header v132ContractHeader
}

// v132ContractHeader is a contractHeader without the Utility field. This field
// was added after v132 to be able to persist contract utilities.
type v132ContractHeader struct {
	// transaction is the signed transaction containing the most recent
	// revision of the file contract.
	Transaction types.Transaction

	// secretKey is the key used by the renter to sign the file contract
	// transaction.
	SecretKey crypto.SecretKey

	// Same as modules.RenterContract.
	StartHeight      types.BlockHeight
	DownloadSpending types.Currency
	StorageSpending  types.Currency
	UploadSpending   types.Currency
	TotalCost        types.Currency
	ContractFee      types.Currency
	TxnFee           types.Currency
	SiafundFee       types.Currency
}

// v1412ContractHeader is the contract header that was used up to and including
// v1.4.1.2
type v1412ContractHeader struct {
	// transaction is the signed transaction containing the most recent
	// revision of the file contract.
	Transaction types.Transaction

	// secretKey is the key used by the renter to sign the file contract
	// transaction.
	SecretKey crypto.SecretKey

	// Same as modules.RenterContract.
	StartHeight      types.BlockHeight
	DownloadSpending types.Currency
	StorageSpending  types.Currency
	UploadSpending   types.Currency
	TotalCost        types.Currency
	ContractFee      types.Currency
	TxnFee           types.Currency
	SiafundFee       types.Currency

	GoodForUpload bool
	GoodForRenew  bool
	LastOOSErr    types.BlockHeight
	Locked        bool
}

// contractHeaderDecodeV1412ToV142 attempts to decode a contract header using
// the persist struct as of v1.4.1.2, returning a header that has been converted
// to the v1.4.2 version of the header.
func contractHeaderDecodeV1412ToV142(f io.ReadSeeker, decodeMaxSize int) (contractHeader, error) {
	var v1412Header v1412ContractHeader
	err := encoding.NewDecoder(f, decodeMaxSize).Decode(&v1412Header)
	if err != nil {
		return contractHeader{}, errors.AddContext(err, "unable to decode header as a v1412 header")
	}

	return contractHeader{
		Transaction: v1412Header.Transaction,

		SecretKey: v1412Header.SecretKey,

		StartHeight:      v1412Header.StartHeight,
		DownloadSpending: v1412Header.DownloadSpending,
		StorageSpending:  v1412Header.StorageSpending,
		UploadSpending:   v1412Header.UploadSpending,
		TotalCost:        v1412Header.TotalCost,
		ContractFee:      v1412Header.ContractFee,
		TxnFee:           v1412Header.TxnFee,
		SiafundFee:       v1412Header.SiafundFee,

		Utility: modules.ContractUtility{
			GoodForUpload: v1412Header.GoodForUpload,
			GoodForRenew:  v1412Header.GoodForRenew,
			BadContract:   false,
			LastOOSErr:    v1412Header.LastOOSErr,
			Locked:        v1412Header.Locked,
		},
	}, nil
}

// updateSetHeaderUnmarshalV142ToV142 attempts to unmarshal an update set
// header using the v.1.3.2 encoding scheme, returning a v1.4.2 version of the
// update set header.
func updateSetHeaderUnmarshalV132ToV142(b []byte, u *updateSetHeader) error {
	var oldHeader v132UpdateSetHeader
	if err := encoding.Unmarshal(b, &oldHeader); err != nil {
		// If unmarshaling the header the old way also doesn't work we
		// return the original error.
		return errors.AddContext(err, "could not unmarshal update into v.1.3.2 format")
	}
	// If unmarshaling it the old way was successful we convert it to a new
	// header.
	u.Header = contractHeader{
		Transaction:      oldHeader.Header.Transaction,
		SecretKey:        oldHeader.Header.SecretKey,
		StartHeight:      oldHeader.Header.StartHeight,
		DownloadSpending: oldHeader.Header.DownloadSpending,
		StorageSpending:  oldHeader.Header.StorageSpending,
		UploadSpending:   oldHeader.Header.UploadSpending,
		TotalCost:        oldHeader.Header.TotalCost,
		ContractFee:      oldHeader.Header.ContractFee,
		TxnFee:           oldHeader.Header.TxnFee,
		SiafundFee:       oldHeader.Header.SiafundFee,
	}
	return nil
}
