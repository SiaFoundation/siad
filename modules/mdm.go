package modules

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux/mux"
)

type (
	// Instruction specifies a generic instruction used as an input to
	// `mdm.ExecuteProgram`.
	Instruction struct {
		Specifier InstructionSpecifier
		Args      []byte
	}
	// InstructionSpecifier specifies the type of the instruction.
	InstructionSpecifier types.Specifier
	// Program specifies a generic program used as input to `mdm.ExecuteProram`.
	Program []Instruction
	// ProgramData contains the raw byte data for the program.
	ProgramData []byte

	// MDMCancellationToken is a token that can be used to request cancellation
	// of a program
	MDMCancellationToken [MDMCancellationTokenLen]byte
)

const (
	// MDMCancellationTokenLen is the length of a program's cancellation token
	// in bytes.
	MDMCancellationTokenLen = 16
)

const (
	// MDMTimeAppend is the time for executing an 'Append' instruction.
	MDMTimeAppend = 10000

	// MDMTimeCommit is the time used for executing managedFinalize.
	// TODO: This should scale with the number of added + removed sectors.
	MDMTimeCommit = 50e3

	// MDMTimeDropSectorsBase is the base time for executing a 'DropSectors'
	// instruction.
	MDMTimeDropSectorsBase = 1

	// MDMTimeDropSingleSector is the time for dropping a single sector.
	MDMTimeDropSingleSector = 1

	// MDMTimeHasSector is the time for executing a 'HasSector' instruction.
	MDMTimeHasSector = 1

	// MDMTimeInitProgram is the base time for initializing a program. `1`
	// because no disk IO is involved.
	MDMTimeInitProgram = 1

	// MDMTimeInitSingleInstruction is the time it takes to initialize a single
	// instruction.
	MDMTimeInitSingleInstruction = 1

	// MDMTimeReadOffset is the time for executing a 'ReadOffset' instruction.
	MDMTimeReadOffset = 1000

	// MDMTimeReadSector is the time for executing a 'ReadSector' instruction.
	MDMTimeReadSector = 1000

	// MDMTimeRevision is the time for executing a 'Revision' instruction.
	MDMTimeRevision = 1

	// MDMTimeSwapSector is the time for executing an 'SwapSector' instruction.
	MDMTimeSwapSector = 1

	// MDMTimeWriteSector is the time for executing a 'WriteSector' instruction.
	MDMTimeWriteSector = 10000

	// MDMTimeUpdateRegistry is the time for executing an 'UpdateRegistry'
	// instruction.
	MDMTimeUpdateRegistry = 10000

	// RPCIAppendLen is the expected length of the 'Args' of an Append
	// instructon.
	RPCIAppendLen = 9

	// RPCIDropSectorsLen is the expected length of the 'Args' of a DropSectors
	// Instruction.
	RPCIDropSectorsLen = 9

	// RPCIHasSectorLen is the expected length of the 'Args' of a HasSector
	// instruction.
	RPCIHasSectorLen = 8

	// RPCIReadSectorLen is the expected length of the 'Args' of a ReadSector
	// instruction.
	RPCIReadSectorLen = 25

	// RPCIReadOffsetLen is the expected length of the 'Args' of a ReadOffset
	// instruction.
	RPCIReadOffsetLen = 17

	// RPCIRevisionLen is the expected length of the 'Args' of a Revision
	// instruction.
	RPCIRevisionLen = 0

	// RPCISwapSectorLen is the expected length of the 'Args' of an SwapSector
	// instructon.
	RPCISwapSectorLen = 17 // 2 uint64 offsets + merkle proof flag

	// RPCIUpdateRegistryLen is the expected length of the 'Args' of an
	// UpdateRegistry instruction.
	// tweakOffset + revisionOffset + signatureOffset + pubKeyOffset +
	// pubKeyLength + dataOffset + dataLength = 7 * 8 bytes = 56 byte
	RPCIUpdateRegistryLen = 56
)

var (
	// MDMProgramWriteResponseTime defines the amount of time that the host
	// allows to write the output of an instruction to the stream. The time is
	// set high enough that a renter behind Tor has a reasonable chance of
	// completing the read.
	MDMProgramWriteResponseTime = build.Select(build.Var{
		Standard: time.Minute,
		Dev:      30 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)

	// SpecifierAppend is the specifier for the Append instruction.
	SpecifierAppend = InstructionSpecifier{'A', 'p', 'p', 'e', 'n', 'd'}

	// SpecifierDropSectors is the specifier for the DropSectors instruction.
	SpecifierDropSectors = InstructionSpecifier{'D', 'r', 'o', 'p', 'S', 'e', 'c', 't', 'o', 'r', 's'}

	// SpecifierHasSector is the specifier for the HasSector instruction.
	SpecifierHasSector = InstructionSpecifier{'H', 'a', 's', 'S', 'e', 'c', 't', 'o', 'r'}

	// SpecifierReadOffset is the specifier for the ReadOffset instruction.
	SpecifierReadOffset = InstructionSpecifier{'R', 'e', 'a', 'd', 'O', 'f', 'f', 's', 'e', 't'}

	// SpecifierReadSector is the specifier for the ReadSector instruction.
	SpecifierReadSector = InstructionSpecifier{'R', 'e', 'a', 'd', 'S', 'e', 'c', 't', 'o', 'r'}

	// SpecifierRevision is the specifier for the Revision instruction.
	SpecifierRevision = InstructionSpecifier{'R', 'e', 'v', 'i', 's', 'i', 'o', 'n'}

	// SpecifierSwapSector is the specifier for the SwapSector instruction.
	SpecifierSwapSector = InstructionSpecifier{'S', 'w', 'a', 'p', 'S', 'e', 'c', 't', 'o', 'r'}

	// SpecifierUpdateRegistry is the specifier for the UpdateRegistry
	// instruction.
	SpecifierUpdateRegistry = InstructionSpecifier{'U', 'p', 'd', 'a', 't', 'e', 'R', 'e', 'g', 'i', 's', 't', 'r', 'y'}

	// ErrInsufficientBandwidthBudget is returned when bandwidth can no longer
	// be paid for with the provided budget.
	ErrInsufficientBandwidthBudget = errors.New("insufficient budget for bandwidth")

	// ErrMDMInsufficientBudget is the error returned if the remaining budget of
	// an MDM program is not sufficient to execute the next instruction.
	ErrMDMInsufficientBudget = errors.New("remaining budget is insufficient")

	// ErrMDMInsufficientCollateralBudget is the error returned if the remaining
	// collateral budget of an MDM program is not sufficient to execute the next
	// instruction.
	ErrMDMInsufficientCollateralBudget = errors.New("remaining collateral budget is insufficient")
)

type (
	// MDMInstructionRevisionResponse is the format of the MDM's revision
	// instruction's output.
	MDMInstructionRevisionResponse struct {
		RevisionTxn types.Transaction
	}
)

// RPCHasSectorInstruction creates an Instruction from arguments.
func RPCHasSectorInstruction(merkleRootOffset uint64) Instruction {
	i := Instruction{
		Specifier: SpecifierHasSector,
		Args:      make([]byte, RPCIHasSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	return i
}

// RPCIReadSector is a convenience method to create an Instruction of type 'ReadSector'.
func RPCIReadSector(rootOff, offsetOff, lengthOff uint64, merkleProof bool) Instruction {
	args := make([]byte, RPCIReadSectorLen)
	binary.LittleEndian.PutUint64(args[:8], rootOff)
	binary.LittleEndian.PutUint64(args[8:16], offsetOff)
	binary.LittleEndian.PutUint64(args[16:24], lengthOff)
	if merkleProof {
		args[24] = 1
	}
	return Instruction{
		Args:      args,
		Specifier: SpecifierReadSector,
	}
}

// MDMAppendCost is the cost of executing an 'Append' instruction.
func MDMAppendCost(pt *RPCPriceTable, duration types.BlockHeight) (types.Currency, types.Currency) {
	// Cost for writing the Data.
	writeCost := MDMWriteCost(pt, SectorSize)
	// Cost of storing for the duration.
	storeCost := pt.WriteStoreCost.Mul64(SectorSize).Mul64(uint64(duration))
	return writeCost.Add(storeCost), storeCost
}

// MDMCopyCost is the cost of executing a 'Copy' instruction.
func MDMCopyCost(pt RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// MDMDropSectorsCost is the cost of executing a 'DropSectors' instruction for a
// certain number of dropped sectors.
func MDMDropSectorsCost(pt *RPCPriceTable, numSectorsDropped uint64) types.Currency {
	cost := pt.DropSectorsUnitCost.Mul64(numSectorsDropped).Add(pt.DropSectorsBaseCost)
	return cost
}

// MDMInitCost is the cost of instantiating the MDM.
func MDMInitCost(pt *RPCPriceTable, programLen, numInstructions uint64) types.Currency {
	time := MDMTimeInitProgram + MDMTimeInitSingleInstruction*numInstructions
	return MDMMemoryCost(pt, programLen, time).Add(pt.InitBaseCost)
}

// MDMHasSectorCost is the cost of executing a 'HasSector' instruction.
func MDMHasSectorCost(pt *RPCPriceTable) types.Currency {
	cost := pt.HasSectorBaseCost
	return cost
}

// MDMReadCost is the cost of executing a 'Read' instruction. It is defined as:
// 'readBaseCost' + 'readLengthCost' * `readLength`
func MDMReadCost(pt *RPCPriceTable, readLength uint64) types.Currency {
	cost := pt.ReadLengthCost.Mul64(readLength).Add(pt.ReadBaseCost)
	return cost
}

// MDMRevisionCost is the cost of executing a 'Revision' instruction.
func MDMRevisionCost(pt *RPCPriceTable) types.Currency {
	cost := pt.RevisionBaseCost
	return cost
}

// MDMSwapSectorCost is the cost of executing a 'SwapSector' instruction.
func MDMSwapSectorCost(pt *RPCPriceTable) types.Currency {
	return pt.SwapSectorCost
}

// MDMUpdateRegistryCost is the cost of executing a 'UpdateRegistry' instruction.
func MDMUpdateRegistryCost(pt *RPCPriceTable) types.Currency {
	// Cost is the same as uploading a 4MiB sector of new data for a month but
	// it's paid at once instead of differentiating between write and storage
	// cost.
	writeCost, storeCost := MDMAppendCost(pt, types.BlocksPerMonth)
	return writeCost.Add(storeCost)
}

// MDMWriteCost is the cost of executing a 'Write' instruction of a certain length.
func MDMWriteCost(pt *RPCPriceTable, writeLength uint64) types.Currency {
	writeCost := pt.WriteLengthCost.Mul64(writeLength).Add(pt.WriteBaseCost)
	return writeCost
}

// MDMSwapCost is the cost of executing a 'Swap' instruction.
func MDMSwapCost(pt *RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// MDMTruncateCost is the cost of executing a 'Truncate' instruction.
func MDMTruncateCost(pt *RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// MDMAppendMemory returns the additional memory consumption of a 'Append'
// instruction.
func MDMAppendMemory() uint64 {
	return SectorSize // A full sector is added to the program's memory until the program is finalized.
}

// MDMDropSectorsMemory returns the additional memory consumption of a
// `DropSectors` instruction
func MDMDropSectorsMemory() uint64 {
	return 0 // 'DropSectors' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MDMInitMemory returns the memory consumed by a program before considering the
// size of the program input.
func MDMInitMemory() uint64 {
	return 1 << 20 // 1 MiB
}

// MDMHasSectorMemory returns the additional memory consumption of a 'HasSector'
// instruction.
func MDMHasSectorMemory() uint64 {
	return 0 // 'HasSector' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MDMReadMemory returns the additional memory consumption of a 'Read' instruction.
func MDMReadMemory() uint64 {
	return 0 // 'Read' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MDMRevisionMemory returns the additional memory consumption of a 'Revision'
// instruction.
func MDMRevisionMemory() uint64 {
	return 0 // 'Revision' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MDMSwapSectorMemory returns the additional memory consumption of a
// 'SwapSector' instruction.
func MDMSwapSectorMemory() uint64 {
	return 0 // 'SwapSector' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MDMUpdateRegistryMemory returns the additional memory consumption of a
// 'UpdateRegistry' instruction.
func MDMUpdateRegistryMemory() uint64 {
	return 0 // 'UpdateRegistry' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MDMBandwidthCost computes the total bandwidth cost given a price table and
// used up- and download bandwidth.
func MDMBandwidthCost(pt RPCPriceTable, uploadBandwidth, downloadBandwidth uint64) types.Currency {
	uploadCost := pt.UploadBandwidthCost.Mul64(uploadBandwidth)
	downloadCost := pt.DownloadBandwidthCost.Mul64(downloadBandwidth)
	return uploadCost.Add(downloadCost)
}

// MDMMemoryCost computes the memory cost given a price table, memory and time.
func MDMMemoryCost(pt *RPCPriceTable, usedMemory, time uint64) types.Currency {
	return pt.MemoryTimeCost.Mul64(usedMemory * time)
}

// MDMDropSectorsTime returns the time for a `DropSectors` instruction given
// `numSectorsDropped`.
func MDMDropSectorsTime(numSectorsDropped uint64) uint64 {
	return MDMTimeDropSectorsBase + MDMTimeDropSingleSector*numSectorsDropped
}

// MDMAppendCollateral returns the additional collateral a 'Append' instruction
// requires the host to put up.
func MDMAppendCollateral(pt *RPCPriceTable) types.Currency {
	return pt.CollateralCost.Mul64(SectorSize)
}

// MDMDropSectorsCollateral returns the additional collateral a 'DropSectors'
// instruction requires the host to put up.
func MDMDropSectorsCollateral() types.Currency {
	return types.ZeroCurrency
}

// MDMHasSectorCollateral returns the additional collateral a 'HasSector'
// instruction requires the host to put up.
func MDMHasSectorCollateral() types.Currency {
	return types.ZeroCurrency
}

// MDMReadCollateral returns the additional collateral a 'Read' instruction
// requires the host to put up.
func MDMReadCollateral() types.Currency {
	return types.ZeroCurrency
}

// MDMRevisionCollateral returns the additional collateral a 'Revision'
// instruction requires the host to put up.
func MDMRevisionCollateral() types.Currency {
	return types.ZeroCurrency
}

// MDMSwapSectorCollateral returns the additional collateral a 'SwapSector'
// instruction requires the host to put up.
func MDMSwapSectorCollateral() types.Currency {
	return types.ZeroCurrency
}

// MDMUpdateRegistryCollateral returns the additional collateral a 'UpdateRegistry'
// instruction requires the host to put up.
func MDMUpdateRegistryCollateral() types.Currency {
	return types.ZeroCurrency
}

// ReadOnly returns true if the program consists of no write instructions.
func (p Program) ReadOnly() bool {
	for _, instruction := range p {
		switch instruction.Specifier {
		case SpecifierAppend:
			return false
		case SpecifierDropSectors:
			return false
		case SpecifierHasSector:
		case SpecifierReadOffset:
		case SpecifierReadSector:
		case SpecifierRevision:
		case SpecifierSwapSector:
			return false
		case SpecifierUpdateRegistry:
			// considered read-only cause it doesn't update a contract
		default:
			build.Critical("ReadOnly: unknown instruction")
		}
	}
	return true
}

// RequiresSnapshot returns true if an instruction requires access to the sector
// roots of a filecontract and therefore requires the host to load a snapshot
// from disk to provide that information.
func (p Program) RequiresSnapshot() bool {
	// Only certain read programs require a snapshot.
	for _, instruction := range p {
		switch instruction.Specifier {
		case SpecifierAppend:
			return true
		case SpecifierDropSectors:
			return true
		case SpecifierHasSector:
		case SpecifierReadOffset:
			return true
		case SpecifierReadSector:
		case SpecifierRevision:
			return true
		case SpecifierSwapSector:
			return true
		case SpecifierUpdateRegistry:
		default:
			build.Critical("RequiresSnapshot: unknown instruction")
		}
	}
	return false
}

// RPCBudget is a helper type for threadsafe budget handling.
type RPCBudget struct {
	budget types.Currency
	mu     sync.Mutex
}

// NewBudget creates a new budget from a types.Currency.
func NewBudget(budget types.Currency) *RPCBudget {
	return &RPCBudget{
		budget: budget,
	}
}

// Remaining returns the remaining value in the budget.
func (b *RPCBudget) Remaining() types.Currency {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.budget
}

// Withdraw withdraws from a budget. Returns 'true' on success and 'false'
// otherwise.
func (b *RPCBudget) Withdraw(c types.Currency) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.budget.Cmp(c) < 0 {
		return false
	}
	b.budget = b.budget.Sub(c)
	return true
}

// BudgetLimit is an implementation of the BandwidthLimit interface which uses
// an RPCBudget to determine whether to allow for more bandwidth consumption or
// not.
type BudgetLimit struct {
	budget          *RPCBudget
	staticReadCost  types.Currency
	staticWriteCost types.Currency

	atomicDownloaded uint64
	atomicUploaded   uint64
}

// NewBudgetLimit creates a new limit from a budget and priceTable.
func NewBudgetLimit(budget *RPCBudget, readCost, writeCost types.Currency) mux.BandwidthLimit {
	return &BudgetLimit{
		budget:          budget,
		staticReadCost:  readCost,
		staticWriteCost: writeCost,
	}
}

// Downloaded implements the mux.BandwidthLimit interface.
func (bl *BudgetLimit) Downloaded() uint64 {
	return atomic.LoadUint64(&bl.atomicDownloaded)
}

// Uploaded implements the mux.BandwidthLimit interface.
func (bl *BudgetLimit) Uploaded() uint64 {
	return atomic.LoadUint64(&bl.atomicUploaded)
}

// RecordDownload implements the mux.BandwidthLimit interface.
func (bl *BudgetLimit) RecordDownload(bytes uint64) error {
	cost := bl.staticReadCost.Mul64(bytes)
	if !bl.budget.Withdraw(cost) {
		return ErrInsufficientBandwidthBudget
	}
	atomic.AddUint64(&bl.atomicDownloaded, bytes)
	return nil
}

// RecordUpload implements the mux.BandwidthLimit interface.
func (bl *BudgetLimit) RecordUpload(bytes uint64) error {
	cost := bl.staticWriteCost.Mul64(bytes)
	if !bl.budget.Withdraw(cost) {
		return ErrInsufficientBandwidthBudget
	}
	atomic.AddUint64(&bl.atomicUploaded, bytes)
	return nil
}
