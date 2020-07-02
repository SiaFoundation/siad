package feemanager

import (
	"bytes"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// marshalledFeeUIDSize is the size of a marshalled FeeUID
	marshalledFeeUIDSize = 48

	// persistEntrySize is the size of a persist entry in the persist file.
	persistEntrySize = 256

	// persistEntryPayloadSize is the size of a persist entry minus the size of
	// a types.Specifier.
	persistEntryPayloadSize = 240

	// persistFeeUIDsSize is the size designated for feeUIDs in a transaction
	// created persist entry.
	persistFeeUIDsSize = 192

	// persistTransactionSize is the size that the transaction needs to be split
	// into for its persist entrys
	persistTransactionSize = 192
)

var (
	// Persist entry types
	entryTypeAddFee               = types.NewSpecifier("add fee")
	entryTypeCancelFee            = types.NewSpecifier("cancel fee")
	entryTypeTransaction          = types.NewSpecifier("transaction")
	entryTypeTransactionConfirmed = types.NewSpecifier("txn confirmed")
	entryTypeTransactionCreated   = types.NewSpecifier("txn created")
	entryTypeTransactionDropped   = types.NewSpecifier("txn dropped")
	entryTypeUpdateFee            = types.NewSpecifier("update fee")

	// errUnrecognizedEntryType is returned if the FeeManager tries to apply an
	// unrecognized entry type
	errUnrecognizedEntryType = errors.New("unrecognized entry type")
)

type (
	// entryAddFee is the persist entry that adds a new fee.
	entryAddFee struct {
		Fee modules.AppFee
	}

	// entryCancelFee is the persist entry that is used for recording a cancel
	// fee request
	entryCancelFee struct {
		FeeUID    modules.FeeUID
		Timestamp int64
	}

	// entryTransaction is the persist entry for a transaction that the watchdog
	// will be monitoring
	entryTransaction struct {
		FinalIndex int
		Index      int
		TxnBytes   [persistTransactionSize]byte
		TxnID      types.TransactionID
	}

	// entryTransactionAndFeeUIDs is the persist entry that links a transaction
	// ID with a set of Fee UIDs in a persist event such as a transaction being
	// created, confirmed, or dropped.
	entryTransactionAndFeeUIDs struct {
		FeeUIDsBytes [persistFeeUIDsSize]byte
		NumFeeUIDs   int
		Timestamp    int64
		TxnID        types.TransactionID
	}

	// entryUpdateFee is the persist entry that updates a pending fee's payout
	// height.
	entryUpdateFee struct {
		FeeUID       modules.FeeUID
		PayoutHeight types.BlockHeight
	}

	// persistEntry is a generic entry in the persist database.
	persistEntry struct {
		EntryType types.Specifier
		Payload   [persistEntryPayloadSize]byte
	}
)

// createAddFeeEntry will create a persist entry for an add fee request.
func createAddFeeEntry(fee modules.AppFee) (ret [persistEntrySize]byte) {
	// Create the add fee entry and marshal it.
	eaf := entryAddFee{
		Fee: fee,
	}
	payload := encoding.Marshal(eaf)
	if len(payload) > persistEntryPayloadSize {
		build.Critical("an encoded payload is too big", len(payload))
	}

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

// createCancelFeeEntry will take a feeUID and create a persist entry for a
// cancel fee request.
func createCancelFeeEntry(feeUID modules.FeeUID) (ret [persistEntrySize]byte) {
	// Create the timestamp entry and marshal it.
	et := entryCancelFee{
		FeeUID:    feeUID,
		Timestamp: time.Now().Unix(),
	}
	payload := encoding.Marshal(et)
	if len(payload) > persistEntryPayloadSize {
		build.Critical("an encoded payload is too big", len(payload))
	}

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

// createTransactionEntrys will take a transaction and create a slice of persist
// entrys for the transaction.
func createTransactionEntrys(txn types.Transaction) (rets [][persistEntrySize]byte, err error) {
	// First marshal the transaction to see how many entrys we need to create
	b := new(bytes.Buffer)
	err = txn.MarshalSia(b)
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal Transaction")
	}
	txnBytes := b.Bytes()
	txnSize := len(txnBytes)
	finalIndex := txnSize / persistTransactionSize
	txnID := txn.ID()
	index := 0
	for i := 0; i < txnSize; i += persistTransactionSize {
		// Create the transaction entry
		et := entryTransaction{
			FinalIndex: finalIndex,
			Index:      index,
			TxnID:      txnID,
		}
		index++
		end := i + persistTransactionSize
		if end > len(txnBytes) {
			end = len(txnBytes)
		}
		copy(et.TxnBytes[:], txnBytes[i:end])

		// Marshal the transaction entry
		payload := encoding.Marshal(et)
		if len(payload) > persistEntryPayloadSize {
			build.Critical("an encoded payload is too big", len(payload))
		}

		// Load the marshalled entry into the generic entry.
		entry := persistEntry{
			EntryType: entryTypeTransaction,
		}
		copy(entry.Payload[:], payload)

		// Encode the generic entry and check a size invariant.
		encodedEntry := encoding.Marshal(entry)
		if len(encodedEntry) != persistEntrySize {
			build.Critical("an encoded entry has the wrong size")
		}

		// Set the return value and return.
		var ret [persistEntrySize]byte
		copy(ret[:], encodedEntry)
		rets = append(rets, ret)
	}
	return
}

// createTxnConfirmedEntrys will take a list of feeUIDs and a transaction ID and
// create persist entrys for when a transaction was confirmed.
func createTxnConfirmedEntrys(feeUIDs []modules.FeeUID, txnID types.TransactionID) ([][persistEntrySize]byte, error) {
	return createTransactionFeeUIDEntrys(feeUIDs, txnID, entryTypeTransactionConfirmed)
}

// createTxnCreatedEntrys will take a list of feeUIDs and a transaction ID and
// create persist entrys for when a transaction was created.
func createTxnCreatedEntrys(feeUIDs []modules.FeeUID, txnID types.TransactionID) ([][persistEntrySize]byte, error) {
	return createTransactionFeeUIDEntrys(feeUIDs, txnID, entryTypeTransactionCreated)
}

// createTxnDroppedEntrys will take a list of feeUIDs and a transaction ID and
// create persist entrys for when a transaction was dropped.
func createTxnDroppedEntrys(feeUIDs []modules.FeeUID, txnID types.TransactionID) ([][persistEntrySize]byte, error) {
	return createTransactionFeeUIDEntrys(feeUIDs, txnID, entryTypeTransactionDropped)
}

// createTransactionsFeeUIDEntrys will create entryTransactionAndFeeUIDs persist
// entrys for the provided entryType specifier
func createTransactionFeeUIDEntrys(feeUIDs []modules.FeeUID, txnID types.TransactionID, entryType types.Specifier) (rets [][persistEntrySize]byte, err error) {
	// Validate the entryType
	switch entryType {
	case entryTypeTransactionConfirmed:
	case entryTypeTransactionCreated:
	case entryTypeTransactionDropped:
	default:
		build.Critical("createTxnIDTimestampEntry called with the wrong entry type")
	}

	var feeUIDsBytes []byte
	numFeeUIDs := 0
	timeStamp := time.Now().Unix()
	for i, feeUID := range feeUIDs {
		numFeeUIDs++
		feeUIDsBytes = append(feeUIDsBytes, encoding.Marshal(feeUID)...)
		if len(feeUIDsBytes) < persistFeeUIDsSize && i+1 < len(feeUIDs) {
			continue
		}

		// Create the transaction and FeeUIDs entry.
		etc := entryTransactionAndFeeUIDs{
			NumFeeUIDs: numFeeUIDs,
			Timestamp:  timeStamp,
			TxnID:      txnID,
		}
		copy(etc.FeeUIDsBytes[:], feeUIDsBytes)

		// Marshal the transaction and FeeUIDs entry
		payload := encoding.Marshal(etc)
		if len(payload) > persistEntryPayloadSize {
			build.Critical("an encoded payload is too big", len(payload))
		}

		// Load the marshalled entry into the generic entry.
		entry := persistEntry{
			EntryType: entryType,
		}
		copy(entry.Payload[:], payload)

		// Encode the generic entry and check a size invariant.
		encodedEntry := encoding.Marshal(entry)
		if len(encodedEntry) != persistEntrySize {
			build.Critical("an encoded entry has the wrong size")
		}

		// Update the return value
		var ret [persistEntrySize]byte
		copy(ret[:], encodedEntry)
		rets = append(rets, ret)

		// Zero out feeUIDsBytes and numFeeUIDs
		feeUIDsBytes = []byte{}
		numFeeUIDs = 0
	}
	return
}

// createUpdateFeeEntry will create a persist entry for an update fee request.
func createUpdateFeeEntry(feeUID modules.FeeUID, payoutHeight types.BlockHeight) (ret [persistEntrySize]byte) {
	// Create the cancel fee entry and marshal it.
	euf := entryUpdateFee{
		FeeUID:       feeUID,
		PayoutHeight: payoutHeight,
	}
	payload := encoding.Marshal(euf)
	if len(payload) > persistEntryPayloadSize {
		build.Critical("an encoded payload is too big", len(payload))
	}

	// Load the marshalled entry into the generic entry.
	entry := persistEntry{
		EntryType: entryTypeUpdateFee,
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

// applyEntry will apply the provided entry to the fee manager.
func (fm *FeeManager) applyEntry(entry []byte) error {
	var pe persistEntry
	err := encoding.Unmarshal(entry, &pe)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal generic entry")
	}
	switch pe.EntryType {
	case entryTypeAddFee:
		return fm.applyEntryAddFee(pe.Payload)
	case entryTypeCancelFee:
		return fm.applyEntryCancelFee(pe.Payload)
	case entryTypeTransaction:
		return fm.applyEntryTransaction(pe.Payload)
	case entryTypeTransactionConfirmed:
		return fm.applyEntryTxnConfirmed(pe.Payload)
	case entryTypeTransactionCreated:
		return fm.applyEntryTxnCreated(pe.Payload)
	case entryTypeTransactionDropped:
		return fm.applyEntryTxnDropped(pe.Payload)
	case entryTypeUpdateFee:
		return fm.applyEntryUpdateFee(pe.Payload)
	}
	return errUnrecognizedEntryType
}

// applyEntryAddFee will apply an add fee entry to the fee manager.
func (fm *FeeManager) applyEntryAddFee(payload [persistEntryPayloadSize]byte) error {
	var eaf entryAddFee
	err := encoding.Unmarshal(payload[:], &eaf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal add fee entry payload")
	}
	fm.fees[eaf.Fee.FeeUID] = &eaf.Fee
	return nil
}

// applyEntryCancelFee will apply a cancel fee entry to the fee manager.
func (fm *FeeManager) applyEntryCancelFee(payload [persistEntryPayloadSize]byte) error {
	var ecf entryCancelFee
	err := encoding.Unmarshal(payload[:], &ecf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal cancel fee entry payload")
	}
	delete(fm.fees, ecf.FeeUID)
	return nil
}

// applyEntryTransaction will apply a transaction entry to the fee manager.
func (fm *FeeManager) applyEntryTransaction(payload [persistEntryPayloadSize]byte) error {
	var et entryTransaction
	err := encoding.Unmarshal(payload[:], &et)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal transaction entry payload")
	}
	return fm.staticCommon.staticWatchdog.callApplyTransaction(et)
}

// applyEntryTxnConfirmed will apply a transaction confirmed entry to the fee
// manager.
func (fm *FeeManager) applyEntryTxnConfirmed(payload [persistEntryPayloadSize]byte) error {
	var etf entryTransactionAndFeeUIDs
	err := encoding.Unmarshal(payload[:], &etf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal transaction confirmed entry payload")
	}

	// Apply the Transaction Confirmed event to the FeeManager
	index := 0
	feeUIDs := make([]modules.FeeUID, etf.NumFeeUIDs)
	for i := 0; i < etf.NumFeeUIDs; i++ {
		var feeUID modules.FeeUID
		err = encoding.Unmarshal(etf.FeeUIDsBytes[index:index+marshalledFeeUIDSize], &feeUID)
		if err != nil {
			return errors.AddContext(err, "could not unmarshal feeUID")
		}
		index += marshalledFeeUIDSize
		fee, ok := fm.fees[feeUID]
		if !ok {
			err = errors.New("fee not found in map but has transaction confirmed persist entry")
			build.Critical(err)
			return err
		}
		fee.PaymentCompleted = true
		feeUIDs[i] = feeUID
	}

	// Apply the Transaction Confirmed event to the Watchdog
	fm.staticCommon.staticWatchdog.callApplyTxnConfirmed(etf.TxnID)
	return nil
}

// applyEntryTxnCreated will apply a transaction created entry to the fee
// manager.
func (fm *FeeManager) applyEntryTxnCreated(payload [persistEntryPayloadSize]byte) error {
	var etf entryTransactionAndFeeUIDs
	err := encoding.Unmarshal(payload[:], &etf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal transaction created entry payload")
	}

	// Apply the Transaction Created event to the FeeManager
	index := 0
	feeUIDs := make([]modules.FeeUID, etf.NumFeeUIDs)
	for i := 0; i < etf.NumFeeUIDs; i++ {
		var feeUID modules.FeeUID
		err = encoding.Unmarshal(etf.FeeUIDsBytes[index:index+marshalledFeeUIDSize], &feeUID)
		if err != nil {
			return errors.AddContext(err, "could not unmarshal feeUID")
		}
		index += marshalledFeeUIDSize
		fee, ok := fm.fees[feeUID]
		if !ok {
			err = errors.New("fee not found in map but has transaction created persist entry")
			build.Critical(err)
			return err
		}
		fee.TransactionCreated = true
		feeUIDs[i] = feeUID
	}

	// Apply the Transaction Created event to the Watchdog
	fm.staticCommon.staticWatchdog.callApplyTxnCreated(feeUIDs, etf.TxnID)
	return nil
}

// applyEntryTxnDropped will apply a transaction dropped entry to the fee
// manager.
func (fm *FeeManager) applyEntryTxnDropped(payload [persistEntryPayloadSize]byte) error {
	var etf entryTransactionAndFeeUIDs
	err := encoding.Unmarshal(payload[:], &etf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal transaction dropped entry payload")
	}

	// Apply the Transaction Dropped event to the FeeManager
	index := 0
	feeUIDs := make([]modules.FeeUID, etf.NumFeeUIDs)
	for i := 0; i < etf.NumFeeUIDs; i++ {
		var feeUID modules.FeeUID
		err = encoding.Unmarshal(etf.FeeUIDsBytes[index:index+marshalledFeeUIDSize], &feeUID)
		if err != nil {
			return errors.AddContext(err, "could not unmarshal feeUID")
		}
		index += marshalledFeeUIDSize
		fee, ok := fm.fees[feeUID]
		if !ok {
			err = errors.New("fee not found in map but has transaction dropped persist entry")
			build.Critical(err)
			return err
		}
		// Make sure the fee reflects that the transaction was dropped by setting
		// TransactionCreated to false. This will ensure that a new transaction is
		// created.
		fee.TransactionCreated = false
		feeUIDs[i] = feeUID
	}

	// Apply the Transaction Created event to the Watchdog
	fm.staticCommon.staticWatchdog.callApplyTxnDropped(etf.TxnID)
	return nil
}

// applyEntryUpdateFee will apply an update fee entry to the fee manager.
func (fm *FeeManager) applyEntryUpdateFee(payload [persistEntryPayloadSize]byte) error {
	var euf entryUpdateFee
	err := encoding.Unmarshal(payload[:], &euf)
	if err != nil {
		return errors.AddContext(err, "could not unmarshal update fee entry payload")
	}
	fee, ok := fm.fees[euf.FeeUID]
	if !ok {
		return errors.New("Fee Update found for non existent or cancelled fee")
	}
	fee.PayoutHeight = euf.PayoutHeight
	return nil
}
