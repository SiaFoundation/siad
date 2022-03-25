package host

import (
	"encoding/hex"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
	"lukechampine.com/frand"
)

// A UniqueID is a unique identifier for an RPC or Session.
type UniqueID [8]byte

// String returns a string representation of the UniqueID.
func (u UniqueID) String() string {
	return hex.EncodeToString(u[:])
}

// MarshalJSON marshals the UniqueID to JSON.
func (u UniqueID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + u.String() + `"`), nil
}

// generateUniqueID returns a random UniqueID.
func generateUniqueID() (id UniqueID) {
	frand.Read(id[:])
	return
}

type (
	// An Event represents a metric that can be recorded.
	Event interface {
		isEvent()
	}

	// An EventSubscriber can subscribe to the host's event stream.
	EventSubscriber interface {
		OnEvent(Event)
	}

	// MetricRecorder is an interface for recording RHP session events
	MetricRecorder interface {
		RecordEvent(m Event)
		Subscribe(EventSubscriber)
		Unsubscribe(EventSubscriber)
	}

	// EventSessionStart records the start of a new renter session.
	EventSessionStart struct {
		UID       UniqueID  `json:"uid"`
		RenterIP  string    `json:"renterIP"`
		Timestamp time.Time `json:"timestamp"`
	}

	// EventSessionEnd records the end of a renter session.
	EventSessionEnd struct {
		UID       UniqueID  `json:"uid"`
		Timestamp time.Time `json:"timestamp"`
		Elapsed   int64     `json:"elapsed"`
	}

	// EventRPCStart records the start of an RPC.
	EventRPCStart struct {
		UID        UniqueID      `json:"uid"`
		SessionUID UniqueID      `json:"sessionUID"`
		RPC        rpc.Specifier `json:"rpc"`
		Timestamp  time.Time     `json:"timestamp"`
	}

	// EventRPCEnd records the end of an RPC.
	EventRPCEnd struct {
		UID        UniqueID       `json:"uid"`
		SessionUID UniqueID       `json:"sessionUID"`
		RPC        rpc.Specifier  `json:"rpc"`
		Error      error          `json:"error"`
		Timestamp  time.Time      `json:"timestamp"`
		Elapsed    int64          `json:"elapsed"`
		Spending   types.Currency `json:"spending"`
		ReadBytes  uint64         `json:"readBytes"`
		WriteBytes uint64         `json:"writeBytes"`
	}

	// EventInstructionExecuted records the execution of an instruction.
	EventInstructionExecuted struct {
		ProgramUID     UniqueID       `json:"programUID"`
		RPCUID         UniqueID       `json:"rpcUID"`
		SessionUID     UniqueID       `json:"sessionUID"`
		Instruction    rpc.Specifier  `json:"instruction"`
		StartTimestamp time.Time      `json:"startTimestamp"`
		Elapsed        int64          `json:"elapsed"`
		Spending       types.Currency `json:"spending"`
		ReadBytes      uint64         `json:"readBytes"`
		WriteBytes     uint64         `json:"writeBytes"`
	}

	// EventContractFormed records the formation of a new contract.
	EventContractFormed struct {
		RPCUID     UniqueID     `json:"rpcUID"`
		SessionUID UniqueID     `json:"sessionUID"`
		Contract   rhp.Contract `json:"contract"`
	}

	// EventContractRenewed records the renewal of a contract.
	EventContractRenewed struct {
		RPCUID            UniqueID     `json:"rpcUID"`
		SessionUID        UniqueID     `json:"sessionUID"`
		FinalizedContract rhp.Contract `json:"finalizedContract"`
		RenewedContract   rhp.Contract `json:"renewedContract"`
	}
)

func (e EventSessionStart) isEvent()        {}
func (e EventSessionEnd) isEvent()          {}
func (e EventRPCStart) isEvent()            {}
func (e EventRPCEnd) isEvent()              {}
func (e EventInstructionExecuted) isEvent() {}
func (e EventContractFormed) isEvent()      {}
func (e EventContractRenewed) isEvent()     {}

func (s *Session) record(renterIP string) func() {
	start := EventSessionStart{
		UID:       s.uid,
		RenterIP:  renterIP,
		Timestamp: time.Now(),
	}
	s.recorder.RecordEvent(start)
	return func() {
		timestamp := time.Now()
		s.recorder.RecordEvent(EventSessionEnd{
			UID:       start.UID,
			Timestamp: timestamp,
			Elapsed:   timestamp.Sub(start.Timestamp).Milliseconds(),
		})
	}
}

func (r *rpcSession) recordExecuteInstruction(programUID UniqueID, instruction rpc.Specifier) func() {
	timestamp := time.Now()
	readStart, writeStart := r.stream.Usage()
	spendStart := r.budget.Spent()
	return func() {
		read, write := r.stream.Usage()
		r.recorder.RecordEvent(EventInstructionExecuted{
			ProgramUID:     programUID,
			RPCUID:         r.uid,
			SessionUID:     r.session.uid,
			Instruction:    instruction,
			ReadBytes:      read - readStart,
			WriteBytes:     write - writeStart,
			Spending:       r.budget.Spent().Sub(spendStart),
			StartTimestamp: timestamp,
			Elapsed:        time.Since(timestamp).Milliseconds(),
		})
	}
}

func (r *rpcSession) record(rpc rpc.Specifier) func(err error) {
	start := EventRPCStart{
		UID:        r.uid,
		SessionUID: r.session.uid,
		RPC:        rpc,
		Timestamp:  time.Now(),
	}
	r.recorder.RecordEvent(start)
	return func(err error) {
		read, write := r.stream.Usage()
		timestamp := time.Now()
		r.recorder.RecordEvent(EventRPCEnd{
			UID:        start.UID,
			SessionUID: start.SessionUID,
			RPC:        rpc,
			Timestamp:  timestamp,
			Elapsed:    timestamp.Sub(start.Timestamp).Milliseconds(),
			Spending:   r.budget.Spent(),
			Error:      err,
			ReadBytes:  read,
			WriteBytes: write,
		})
	}
}
