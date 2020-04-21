package feemanager

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// persistEntrySize is the size of a persist entry in the persist file.
	persistEntrySize = 256

	// persistEntryPayloadSize is the size of a persist entry minus the size of
	// a types.Specifier.
	//
	// TODO: Write a test to ensure this invariant is correct.
	persistEntryPayloadSize = 240
)

var (
	entryTypeAddFee    = types.Specifier{'a', 'd', 'd', ' ', 'f', 'e', 'e'}
	entryTypeCancelFee = types.Specifier{'c', 'a', 'n', 'c', 'e', 'l', ' ', 'f', 'e', 'e'}
)

type (
	// persistEntry is a generic entry in the persist database.
	persistEntry struct {
		EntryType types.Specifier
		Payload   [persistEntryPayloadSize]byte
	}
)

type (
	// entryAddFee is the persist entry that adds a new fee.
	entryAddFee struct {
		Fee modules.AppFee
	}

	// entryCancelFee is the persist entry that cancels a pending fee.
	entryCancelFee struct {
		FeeUID    modules.FeeUID
		Timestamp int64
	}
)

// createAddFeeEntry will create a persist entry for an add fee request.
func createAddFeeEntry(fee modules.AppFee) (ret [persistEntrySize]byte) {
	// Create the add fee entry and marshal it.
	eaf := entryAddFee{
		Fee: fee,
	}
	payload := encoding.Marshal(eaf)

	// Load the marshalled entry into the generic entry.
	entry := persistEntry{
		EntryType: entryTypeAddFee,
	}
	copy(entry.Payload[:], payload)

	// Encode the generic entry and check a size invariant.
	encodedEntry := encoding.Marshal(entry)
	if len(encodedEntry) != persistEntrySize {
		build.Critical("an encoded entry has the wrong size")
	}

	// Set the return value and return.
	copy(ret[:], encodedEntry)
	return
}

// createCancelFeeEntry will take a feeUID and create a persist entry.
func createCancelFeeEntry(feeUID modules.FeeUID) (ret [persistEntrySize]byte) {
	// Create the cancel fee entry and marshal it.
	ecf := entryCancelFee{
		FeeUID:    feeUID,
		Timestamp: time.Now().Unix(),
	}
	payload := encoding.Marshal(ecf)

	// Load the marshalled entry into the generic entry.
	entry := persistEntry{
		EntryType: entryTypeCancelFee,
	}
	copy(entry.Payload[:], payload)

	// Encode the generic entry and check a size invariant.
	encodedEntry := encoding.Marshal(entry)
	if len(encodedEntry) != persistEntrySize {
		build.Critical("an encoded entry has the wrong size")
	}

	// Set the return value and return.
	copy(ret[:], encodedEntry)
	return
}

// integrateEntry will integrate a provided entry and integrate it into the fee
// manager.
func (fm *FeeManager) integrateEntry(entry []byte) error {
	var pe persistEntry
	err := encoding.Unmarshal(entry, &pe)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal generic entry")
	}
	if pe.EntryType == entryTypeAddFee {
		return fm.integrateEntryAddFee(pe.Payload)
	} else if pe.EntryType == entryTypeCancelFee {
		return fm.integrateEntryCancelFee(pe.Payload)
	}
	return errors.New("unrecoginzed entry type")
}

// integrateEntryAddFee will integrate an add fee entry and integrate it into
// the fee manager.
func (fm *FeeManager) integrateEntryAddFee(payload [persistEntryPayloadSize]byte) error {
	var eaf entryAddFee
	err := encoding.Unmarshal(payload[:], &eaf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal add fee entry payload")
	}
	fm.fees[eaf.Fee.UID] = &eaf.Fee
	return nil
}

// integrateEntryCancelFee will integrate a cancel fee entry and integrate it
// into the fee manager.
func (fm *FeeManager) integrateEntryCancelFee(payload [persistEntryPayloadSize]byte) error {
	var ecf entryCancelFee
	err := encoding.Unmarshal(payload[:], &ecf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal cancel fee entry payload")
	}
	delete(fm.fees, ecf.FeeUID)
	return nil
}
