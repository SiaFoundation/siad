package modules

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
)

// RPCPriceTable contains the cost of executing a RPC on a host. Each host can
// set its own prices for the individual MDM instructions and RPC costs.
type RPCPriceTable struct {
	// UID is a specifier that uniquely identifies this price table
	UID UniqueID `json:"uid"`

	// Expiry is a unix timestamp that specifies the time until which the
	// MDMCostTable is valid.
	Expiry int64 `json:"expiry"`

	// UpdatePriceTableCost refers to the cost of fetching a new price table
	// from the host.
	UpdatePriceTableCost types.Currency `json:"updatepricetablecost"`

	// MDM related costs
	//
	// InitBaseCost is the amount of cost that is incurred when an MDM program
	// starts to run. This doesn't include the memory used by the program data.
	// The total cost to initialize a program is calculated as
	// InitCost = InitBaseCost + MemoryTimeCost * Time
	InitBaseCost types.Currency `json:"initbasecost"`

	// MemoryTimeCost is the amount of cost per byte per time that is incurred
	// by the memory consumption of the program.
	MemoryTimeCost types.Currency `json:"memorytimecost"`

	// Cost values specific to the DropSectors instruction.
	DropSectorsBaseCost   types.Currency `json:"dropsectorsbasecost"`
	DropSectorsLengthCost types.Currency `json:"dropsectorslengthcost"`

	// Cost values specific to the Read instruction.
	ReadBaseCost   types.Currency `json:"readbasecost"`
	ReadLengthCost types.Currency `json:"readlengthcost"`

	// Cost values specific to the Write instruction.
	WriteBaseCost   types.Currency `json:"writebasecost"`
	WriteLengthCost types.Currency `json:"writelengthcost"`
	WriteStoreCost  types.Currency `json:"writestorecost"`
}

var (
	// RPCUpdatePriceTable specifier
	RPCUpdatePriceTable = types.NewSpecifier("UpdatePriceTable")
)

type (
	// RPCUpdatePriceTableResponse contains a JSON encoded RPC price table
	RPCUpdatePriceTableResponse struct {
		PriceTableJSON []byte
	}

	// rpcResponse is a helper type for encoding and decoding RPC response
	// messages.
	rpcResponse struct {
		err  *RPCError
		data interface{}
	}
)

// RPCRead tries to read the given object from the stream.
func RPCRead(r io.Reader, obj interface{}) error {
	resp := rpcResponse{nil, obj}
	err := encoding.ReadObject(r, &resp, uint64(RPCMinLen))
	if err != nil {
		return err
	}
	if resp.err != nil {
		// must wrap the error here, for more info see: https://www.pixelstech.net/article/1554553347-Be-careful-about-nil-check-on-interface-in-GoLang
		return errors.New(resp.err.Error())
	}
	return nil
}

// RPCWrite writes the given object to the stream.
func RPCWrite(w io.Writer, obj interface{}) error {
	return encoding.WriteObject(w, &rpcResponse{nil, obj})
}

// RPCWriteAll writes the given objects to the stream.
func RPCWriteAll(w io.Writer, objs ...interface{}) error {
	for _, obj := range objs {
		err := encoding.WriteObject(w, &rpcResponse{nil, obj})
		if err != nil {
			return err
		}
	}
	return nil
}

// RPCWriteError writes the given error to the stream.
func RPCWriteError(w io.Writer, err error) error {
	re, ok := err.(*RPCError)
	if err != nil && !ok {
		re = &RPCError{Description: err.Error()}
	}
	return encoding.WriteObject(w, &rpcResponse{re, nil})
}

// MarshalSia implements the encoding.SiaMarshaler interface.
func (resp *rpcResponse) MarshalSia(w io.Writer) error {
	if resp.data == nil {
		resp.data = struct{}{}
	}
	return encoding.NewEncoder(w).EncodeAll(resp.err, resp.data)
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (resp *rpcResponse) UnmarshalSia(r io.Reader) error {
	// NOTE: no allocation limit is required because this method is always
	// called via encoding.Unmarshal, which already imposes an allocation limit.
	d := encoding.NewDecoder(r, 0)
	if err := d.Decode(&resp.err); err != nil {
		return err
	}
	if resp.err != nil {
		// rpc response data is not decoded in the event of an error, we return
		// nil here because unmarshaling was successful and is unrelated from
		// the error in the rpc response
		return nil
	}
	return d.Decode(resp.data)
}

// UniqueID is a unique identifier
type UniqueID types.Specifier

// MarshalJSON marshals an id as a hex string.
func (uid UniqueID) MarshalJSON() ([]byte, error) {
	return json.Marshal(uid.String())
}

// String prints the uid in hex.
func (uid UniqueID) String() string {
	return hex.EncodeToString(uid[:])
}

// LoadString loads the unique id from the given string. It is the inverse of
// the `String` method.
func (uid *UniqueID) LoadString(input string) error {
	// *2 because there are 2 hex characters per byte.
	if len(input) != types.SpecifierLen*2 {
		return errors.New("incorrect length")
	}
	uidBytes, err := hex.DecodeString(input)
	if err != nil {
		return errors.New("could not unmarshal hash: " + err.Error())
	}
	copy(uid[:], uidBytes)
	return nil
}

// UnmarshalJSON decodes the json hex string of the id.
func (uid *UniqueID) UnmarshalJSON(b []byte) error {
	// *2 because there are 2 hex characters per byte.
	// +2 because the encoded JSON string is wrapped in `"`.
	if len(b) != types.SpecifierLen*2+2 {
		return errors.New("incorrect length")
	}

	// b[1 : len(b)-1] cuts off the leading and trailing `"` in the JSON string.
	return uid.LoadString(string(b[1 : len(b)-1]))
}
