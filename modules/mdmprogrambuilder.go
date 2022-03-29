package modules

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

type (
	// ProgramBuilder is a helper type to easily create programs and compute
	// their cost.
	ProgramBuilder struct {
		staticDuration types.BlockHeight
		staticPT       *RPCPriceTable

		readonly    bool
		program     Program
		programData *bytes.Buffer

		// Cost related fields.
		executionCost     types.Currency
		additionalStorage types.Currency
		riskedCollateral  types.Currency
		usedMemory        uint64
	}
)

// NewProgramBuilder creates an empty program builder.
func NewProgramBuilder(pt *RPCPriceTable, duration types.BlockHeight) *ProgramBuilder {
	pb := &ProgramBuilder{
		programData:    new(bytes.Buffer),
		readonly:       true, // every program starts readonly
		staticDuration: duration,
		staticPT:       pt,
		usedMemory:     MDMInitMemory(),
	}
	return pb
}

// AddAppendInstruction adds an Append instruction to the program.
func (pb *ProgramBuilder) AddAppendInstruction(data []byte, merkleProof bool, duration types.BlockHeight) error {
	if uint64(len(data)) != SectorSize {
		return fmt.Errorf("expected appended data to have size %v but was %v", SectorSize, len(data))
	}
	// Compute the argument offsets.
	dataOffset := uint64(pb.programData.Len())
	// Extend the programData.
	binary.Write(pb.programData, binary.LittleEndian, data)
	// Create the instruction.
	i := NewAppendInstruction(dataOffset, merkleProof)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMAppendCollateral(pb.staticPT, duration)
	cost, refund := MDMAppendCost(pb.staticPT, pb.staticDuration)
	memory := MDMAppendMemory()
	time := uint64(MDMTimeAppend)
	pb.addInstruction(collateral, cost, refund, memory, time)
	pb.readonly = false
	return nil
}

// AddDropSectorsInstruction adds a DropSectors instruction to the program.
func (pb *ProgramBuilder) AddDropSectorsInstruction(numSectors uint64, merkleProof bool) {
	// Compute the argument offsets.
	numSectorsOffset := uint64(pb.programData.Len())
	// Extend the programData.
	binary.Write(pb.programData, binary.LittleEndian, numSectors)
	// Create the instruction.
	i := NewDropSectorsInstruction(numSectorsOffset, merkleProof)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMDropSectorsCollateral()
	cost := MDMDropSectorsCost(pb.staticPT, numSectors)
	memory := MDMDropSectorsMemory()
	time := MDMDropSectorsTime(numSectors)
	pb.addInstruction(collateral, cost, types.ZeroCurrency, memory, time)
	pb.readonly = false
}

// AddHasSectorInstruction adds a HasSector instruction to the program.
func (pb *ProgramBuilder) AddHasSectorInstruction(merkleRoot crypto.Hash) {
	// Compute the argument offsets.
	merkleRootOffset := uint64(pb.programData.Len())
	// Extend the programData.
	binary.Write(pb.programData, binary.LittleEndian, merkleRoot[:])
	// Create the instruction.
	i := NewHasSectorInstruction(merkleRootOffset)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMHasSectorCollateral()
	cost := MDMHasSectorCost(pb.staticPT)
	memory := MDMHasSectorMemory()
	time := uint64(MDMTimeHasSector)
	pb.addInstruction(collateral, cost, types.ZeroCurrency, memory, time)
}

// AddReadOffsetInstruction adds a ReadOffset instruction to the program.
func (pb *ProgramBuilder) AddReadOffsetInstruction(length, offset uint64, merkleProof bool) {
	// Compute the argument offsets.
	lengthOffset := uint64(pb.programData.Len())
	offsetOffset := lengthOffset + 8
	// Extend the programData.
	binary.Write(pb.programData, binary.LittleEndian, length)
	binary.Write(pb.programData, binary.LittleEndian, offset)
	// Create the instruction.
	i := NewReadOffsetInstruction(lengthOffset, offsetOffset, merkleProof)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMReadCollateral()
	cost := MDMReadCost(pb.staticPT, length)
	memory := MDMReadMemory()
	time := uint64(MDMTimeReadOffset)
	pb.addInstruction(collateral, cost, types.ZeroCurrency, memory, time)
}

// AddReadSectorInstruction adds a ReadSector instruction to the program.
func (pb *ProgramBuilder) AddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) {
	// Compute the argument offsets.
	lengthOffset := uint64(pb.programData.Len())
	offsetOffset := lengthOffset + 8
	merkleRootOffset := offsetOffset + 8
	// Extend the programData.
	binary.Write(pb.programData, binary.LittleEndian, length)
	binary.Write(pb.programData, binary.LittleEndian, offset)
	binary.Write(pb.programData, binary.LittleEndian, merkleRoot[:])
	// Create the instruction.
	i := NewReadSectorInstruction(lengthOffset, offsetOffset, merkleRootOffset, merkleProof)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMReadCollateral()
	cost := MDMReadCost(pb.staticPT, length)
	memory := MDMReadMemory()
	time := uint64(MDMTimeReadSector)
	pb.addInstruction(collateral, cost, types.ZeroCurrency, memory, time)
}

// AddRevisionInstruction adds a Revision instruction to the program.
func (pb *ProgramBuilder) AddRevisionInstruction() {
	// Compute the argument offsets.
	merkleRootOffset := uint64(pb.programData.Len())
	// Create the instruction.
	i := NewRevisionInstruction(merkleRootOffset)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMRevisionCollateral()
	cost := MDMRevisionCost(pb.staticPT)
	memory := MDMRevisionMemory()
	time := uint64(MDMTimeRevision)
	pb.addInstruction(collateral, cost, types.ZeroCurrency, memory, time)
}

// AddSwapSectorInstruction adds a SwapSector instruction to the program.
func (pb *ProgramBuilder) AddSwapSectorInstruction(sector1Idx, sector2Idx uint64, merkleProof bool) {
	// Compute the argument offsets.
	sector1Offset := uint64(pb.programData.Len())
	sector2Offset := sector1Offset + 8
	// Extend the programData.
	binary.Write(pb.programData, binary.LittleEndian, sector1Idx)
	binary.Write(pb.programData, binary.LittleEndian, sector2Idx)
	// Create the instruction.
	i := NewSwapSectorInstruction(sector1Offset, sector2Offset, merkleProof)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMSwapSectorCollateral()
	cost := MDMSwapSectorCost(pb.staticPT)
	memory := MDMSwapSectorMemory()
	time := uint64(MDMTimeSwapSector)
	pb.addInstruction(collateral, cost, types.ZeroCurrency, memory, time)
	pb.readonly = false
}

// V156AddUpdateRegistryInstruction adds an UpdateRegistry instruction to the
// program.
func (pb *ProgramBuilder) V156AddUpdateRegistryInstruction(spk types.SiaPublicKey, rv SignedRegistryValue) error {
	// Marshal pubKey.
	pk := encoding.Marshal(spk)
	// Compute the argument offsets.
	tweakOff := uint64(pb.programData.Len())
	revisionOff := tweakOff + crypto.HashSize
	signatureOff := revisionOff + 8
	pubKeyOff := signatureOff + crypto.SignatureSize
	pubKeyLen := uint64(len(pk))
	dataOff := pubKeyOff + pubKeyLen
	dataLen := uint64(len(rv.Data))
	// Extend the programData.
	_, err1 := pb.programData.Write(rv.Tweak[:])
	err2 := binary.Write(pb.programData, binary.LittleEndian, rv.Revision)
	_, err3 := pb.programData.Write(rv.Signature[:])
	_, err4 := pb.programData.Write(pk)
	_, err5 := pb.programData.Write(rv.Data)
	if err := errors.Compose(err1, err2, err3, err4, err5); err != nil {
		return errors.AddContext(err, "AddUpdateRegistryInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewUpdateRegistryInstruction(tweakOff, revisionOff, signatureOff, pubKeyOff, pubKeyLen, dataOff, dataLen, nil)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMUpdateRegistryCollateral()
	cost, refund := MDMUpdateRegistryCost(pb.staticPT)
	memory := MDMUpdateRegistryMemory()
	time := uint64(MDMTimeUpdateRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return nil
}

// AddUpdateRegistryInstruction adds an UpdateRegistry instruction to the
// program.
func (pb *ProgramBuilder) AddUpdateRegistryInstruction(spk types.SiaPublicKey, rv SignedRegistryValue) error {
	// Marshal pubKey.
	pk := encoding.Marshal(spk)
	// Compute the argument offsets.
	tweakOff := uint64(pb.programData.Len())
	revisionOff := tweakOff + crypto.HashSize
	signatureOff := revisionOff + 8
	pubKeyOff := signatureOff + crypto.SignatureSize
	pubKeyLen := uint64(len(pk))
	dataOff := pubKeyOff + pubKeyLen
	dataLen := uint64(len(rv.Data))
	// Extend the programData.
	_, err1 := pb.programData.Write(rv.Tweak[:])
	err2 := binary.Write(pb.programData, binary.LittleEndian, rv.Revision)
	_, err3 := pb.programData.Write(rv.Signature[:])
	_, err4 := pb.programData.Write(pk)
	_, err5 := pb.programData.Write(rv.Data)
	if err := errors.Compose(err1, err2, err3, err4, err5); err != nil {
		return errors.AddContext(err, "AddUpdateRegistryInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewUpdateRegistryInstruction(tweakOff, revisionOff, signatureOff, pubKeyOff, pubKeyLen, dataOff, dataLen, &rv.Type)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMUpdateRegistryCollateral()
	cost, refund := MDMUpdateRegistryCost(pb.staticPT)
	memory := MDMUpdateRegistryMemory()
	time := uint64(MDMTimeUpdateRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return nil
}

// V154AddUpdateRegistryInstruction adds an UpdateRegistry instruction to the
// program as done in host version 1.5.4 and below.
func (pb *ProgramBuilder) V154AddUpdateRegistryInstruction(spk types.SiaPublicKey, rv SignedRegistryValue) error {
	// Marshal pubKey.
	pk := encoding.Marshal(spk)
	// Compute the argument offsets.
	tweakOff := uint64(pb.programData.Len())
	revisionOff := tweakOff + crypto.HashSize
	signatureOff := revisionOff + 8
	pubKeyOff := signatureOff + crypto.SignatureSize
	pubKeyLen := uint64(len(pk))
	dataOff := pubKeyOff + pubKeyLen
	dataLen := uint64(len(rv.Data))
	// Extend the programData.
	_, err1 := pb.programData.Write(rv.Tweak[:])
	err2 := binary.Write(pb.programData, binary.LittleEndian, rv.Revision)
	_, err3 := pb.programData.Write(rv.Signature[:])
	_, err4 := pb.programData.Write(pk)
	_, err5 := pb.programData.Write(rv.Data)
	if err := errors.Compose(err1, err2, err3, err4, err5); err != nil {
		return errors.AddContext(err, "AddUpdateRegistryInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewUpdateRegistryInstruction(tweakOff, revisionOff, signatureOff, pubKeyOff, pubKeyLen, dataOff, dataLen, nil)
	// Trim off the version byte to be compatible with v154.
	i.Args = i.Args[:RPCIUpdateRegistryLen]
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMUpdateRegistryCollateral()
	cost, refund := V154MDMUpdateRegistryCost(pb.staticPT)
	memory := MDMUpdateRegistryMemory()
	time := uint64(MDMTimeUpdateRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return nil
}

// V154AddReadRegistryInstruction adds an ReadRegistry instruction to the
// program for pre 155 hosts.
func (pb *ProgramBuilder) V154AddReadRegistryInstruction(spk types.SiaPublicKey, tweak crypto.Hash) (types.Currency, error) {
	// Marshal pubKey.
	pk := encoding.Marshal(spk)
	// Compute the argument offsets.
	pubKeyOff := uint64(pb.programData.Len())
	pubKeyLen := uint64(len(pk))
	tweakOff := pubKeyOff + pubKeyLen
	// Extend the programData.
	_, err1 := pb.programData.Write(pk)
	_, err2 := pb.programData.Write(tweak[:])
	if err := errors.Compose(err1, err2); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "AddReadRegistryInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewReadRegistryInstruction(pubKeyOff, pubKeyLen, tweakOff, nil)
	// Append instruction
	pb.program = append(pb.program, i)
	// Read cost, collateral and memory usage.
	collateral := MDMReadRegistryCollateral()
	cost := V154MDMReadRegistryCost(pb.staticPT)
	refund := types.ZeroCurrency
	memory := MDMReadRegistryMemory()
	time := uint64(MDMTimeReadRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return refund, nil
}

// AddReadRegistryInstruction adds an ReadRegistry instruction to the program.
func (pb *ProgramBuilder) AddReadRegistryInstruction(spk types.SiaPublicKey, tweak crypto.Hash, version ReadRegistryVersion) (types.Currency, error) {
	// Marshal pubKey.
	pk := encoding.Marshal(spk)
	// Compute the argument offsets.
	pubKeyOff := uint64(pb.programData.Len())
	pubKeyLen := uint64(len(pk))
	tweakOff := pubKeyOff + pubKeyLen
	// Extend the programData.
	_, err1 := pb.programData.Write(pk)
	_, err2 := pb.programData.Write(tweak[:])
	if err := errors.Compose(err1, err2); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "AddReadRegistryInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewReadRegistryInstruction(pubKeyOff, pubKeyLen, tweakOff, &version)
	// Append instruction
	pb.program = append(pb.program, i)
	// Read cost, collateral and memory usage.
	collateral := MDMReadRegistryCollateral()
	cost, refund := MDMReadRegistryCost(pb.staticPT)
	memory := MDMReadRegistryMemory()
	time := uint64(MDMTimeReadRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return refund, nil
}

// V156AddReadRegistryInstruction adds an ReadRegistry instruction to the program.
func (pb *ProgramBuilder) V156AddReadRegistryInstruction(spk types.SiaPublicKey, tweak crypto.Hash) (types.Currency, error) {
	// Marshal pubKey.
	pk := encoding.Marshal(spk)
	// Compute the argument offsets.
	pubKeyOff := uint64(pb.programData.Len())
	pubKeyLen := uint64(len(pk))
	tweakOff := pubKeyOff + pubKeyLen
	// Extend the programData.
	_, err1 := pb.programData.Write(pk)
	_, err2 := pb.programData.Write(tweak[:])
	if err := errors.Compose(err1, err2); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "AddReadRegistryInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewReadRegistryInstruction(pubKeyOff, pubKeyLen, tweakOff, nil)
	// Append instruction
	pb.program = append(pb.program, i)
	// Read cost, collateral and memory usage.
	collateral := MDMReadRegistryCollateral()
	cost, refund := MDMReadRegistryCost(pb.staticPT)
	memory := MDMReadRegistryMemory()
	time := uint64(MDMTimeReadRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return refund, nil
}

// AddReadRegistryEIDInstruction adds an ReadRegistry instruction to the program.
func (pb *ProgramBuilder) AddReadRegistryEIDInstruction(sid RegistryEntryID, needPKAndTweak bool, version ReadRegistryVersion) (types.Currency, error) {
	// Marshal sid.
	subID := encoding.Marshal(sid)
	// Compute the argument offsets.
	subIDOff := uint64(pb.programData.Len())
	// Extend the programData.
	_, err := pb.programData.Write(subID)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "AddReadRegistryEIDInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewReadRegistryEIDInstruction(subIDOff, needPKAndTweak, &version)
	// Append instruction
	pb.program = append(pb.program, i)
	// Read cost, collateral and memory usage.
	collateral := MDMReadRegistryCollateral()
	cost, refund := MDMReadRegistryCost(pb.staticPT)
	memory := MDMReadRegistryMemory()
	time := uint64(MDMTimeReadRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return refund, nil
}

// V156AddReadRegistryEIDInstruction adds an ReadRegistry instruction to the program.
func (pb *ProgramBuilder) V156AddReadRegistryEIDInstruction(sid RegistryEntryID, needPKAndTweak bool) (types.Currency, error) {
	// Marshal sid.
	subID := encoding.Marshal(sid)
	// Compute the argument offsets.
	subIDOff := uint64(pb.programData.Len())
	// Extend the programData.
	_, err := pb.programData.Write(subID)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "AddReadRegistryEIDInstruction: failed to extend programData")
	}
	// Create the instruction.
	i := NewReadRegistryEIDInstruction(subIDOff, needPKAndTweak, nil)
	// Append instruction
	pb.program = append(pb.program, i)
	// Read cost, collateral and memory usage.
	collateral := MDMReadRegistryCollateral()
	cost, refund := MDMReadRegistryCost(pb.staticPT)
	memory := MDMReadRegistryMemory()
	time := uint64(MDMTimeReadRegistry)
	pb.addInstruction(collateral, cost, refund, memory, time)
	return refund, nil
}

// Cost returns the current cost of the program being built by the builder. If
// 'finalized' is 'true', the memory cost of finalizing the program is included.
func (pb *ProgramBuilder) Cost(finalized bool) (cost, storage, collateral types.Currency) {
	// Calculate the init cost.
	cost = MDMInitCost(pb.staticPT, uint64(pb.programData.Len()), uint64(len(pb.program)))

	// Add the cost of the added instructions
	cost = cost.Add(pb.executionCost)

	// Add the cost of finalizing the program.
	if !pb.readonly && finalized {
		cost = cost.Add(MDMMemoryCost(pb.staticPT, pb.usedMemory, MDMTimeCommit))
	}
	return cost, pb.additionalStorage, pb.riskedCollateral
}

// Program returns the built program and programData.
func (pb *ProgramBuilder) Program() (Program, ProgramData) {
	return pb.program, pb.programData.Bytes()
}

// addInstruction adds the collateral, cost, refund and memory cost of an
// instruction to the builder's state.
func (pb *ProgramBuilder) addInstruction(collateral, cost, storage types.Currency, memory, time uint64) {
	// Update collateral
	pb.riskedCollateral = pb.riskedCollateral.Add(collateral)
	// Update memory and memory cost.
	pb.usedMemory += memory
	memoryCost := MDMMemoryCost(pb.staticPT, pb.usedMemory, time)
	pb.executionCost = pb.executionCost.Add(memoryCost)
	// Update execution cost and storage.
	pb.executionCost = pb.executionCost.Add(cost)
	pb.additionalStorage = pb.additionalStorage.Add(storage)
}

// NewAppendInstruction creates an Instruction from arguments.
func NewAppendInstruction(dataOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierAppend,
		Args:      make([]byte, RPCIAppendLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], dataOffset)
	if merkleProof {
		i.Args[8] = 1
	}
	return i
}

// NewUpdateRegistryInstruction creates an Instruction from arguments.
func NewUpdateRegistryInstruction(tweakOff, revisionOff, signatureOff, pubKeyOff, pubKeyLen, dataOff, dataLen uint64, entryType *RegistryEntryType) Instruction {
	i := Instruction{
		Specifier: SpecifierUpdateRegistry,
	}
	if entryType == nil {
		i.Args = make([]byte, RPCIUpdateRegistryLen)
	} else {
		i.Args = make([]byte, RPCIUpdateRegistryWithVersionLen)
		i.Args[56] = byte(*entryType)
	}
	binary.LittleEndian.PutUint64(i.Args[:8], tweakOff)
	binary.LittleEndian.PutUint64(i.Args[8:16], revisionOff)
	binary.LittleEndian.PutUint64(i.Args[16:24], signatureOff)
	binary.LittleEndian.PutUint64(i.Args[24:32], pubKeyOff)
	binary.LittleEndian.PutUint64(i.Args[32:40], pubKeyLen)
	binary.LittleEndian.PutUint64(i.Args[40:48], dataOff)
	binary.LittleEndian.PutUint64(i.Args[48:56], dataLen)
	return i
}

// NewReadRegistryInstruction creates an Instruction from arguments.
func NewReadRegistryInstruction(pubKeyOff, pubKeyLen, tweakOff uint64, version *ReadRegistryVersion) Instruction {
	i := Instruction{
		Specifier: SpecifierReadRegistry,
	}
	if version == nil {
		i.Args = make([]byte, RPCIReadRegistryLen)
	} else {
		i.Args = make([]byte, RPCIReadRegistryWithVersionLen)
		i.Args[24] = uint8(*version)
	}
	binary.LittleEndian.PutUint64(i.Args[:8], pubKeyOff)
	binary.LittleEndian.PutUint64(i.Args[8:16], pubKeyLen)
	binary.LittleEndian.PutUint64(i.Args[16:24], tweakOff)
	return i
}

// NewReadRegistryEIDInstruction creates an Instruction from arguments.
func NewReadRegistryEIDInstruction(sidOffset uint64, needPKAndTweakOffset bool, version *ReadRegistryVersion) Instruction {
	i := Instruction{
		Specifier: SpecifierReadRegistryEID,
	}
	if version == nil {
		i.Args = make([]byte, RPCIReadRegistryEIDLen)
	} else {
		i.Args = make([]byte, RPCIReadRegistryEIDWithVersionLen)
		i.Args[9] = uint8(*version)
	}
	binary.LittleEndian.PutUint64(i.Args[:8], sidOffset)
	if needPKAndTweakOffset {
		i.Args[8] = 1
	}
	return i
}

// NewDropSectorsInstruction creates an Instruction from arguments.
func NewDropSectorsInstruction(numSectorsOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierDropSectors,
		Args:      make([]byte, RPCIDropSectorsLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], numSectorsOffset)
	if merkleProof {
		i.Args[8] = 1
	}
	return i
}

// NewHasSectorInstruction creates a modules.Instruction from arguments.
func NewHasSectorInstruction(merkleRootOffset uint64) Instruction {
	i := Instruction{
		Specifier: SpecifierHasSector,
		Args:      make([]byte, RPCIHasSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	return i
}

// NewReadOffsetInstruction creates a modules.Instruction from arguments.
func NewReadOffsetInstruction(lengthOffset, offsetOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierReadOffset,
		Args:      make([]byte, RPCIReadOffsetLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], offsetOffset)
	binary.LittleEndian.PutUint64(i.Args[8:16], lengthOffset)
	if merkleProof {
		i.Args[16] = 1
	}
	return i
}

// NewReadSectorInstruction creates a modules.Instruction from arguments.
func NewReadSectorInstruction(lengthOffset, offsetOffset, merkleRootOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierReadSector,
		Args:      make([]byte, RPCIReadSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	binary.LittleEndian.PutUint64(i.Args[8:16], offsetOffset)
	binary.LittleEndian.PutUint64(i.Args[16:24], lengthOffset)
	if merkleProof {
		i.Args[24] = 1
	}
	return i
}

// NewSwapSectorInstruction creates a modules.Instruction from arguments.
func NewSwapSectorInstruction(sector1Offset, sector2Offset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierSwapSector,
		Args:      make([]byte, RPCISwapSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], sector1Offset)
	binary.LittleEndian.PutUint64(i.Args[8:16], sector2Offset)
	if merkleProof {
		i.Args[16] = 1
	}
	return i
}

// NewRevisionInstruction creates a modules.Instruction from arguments.
func NewRevisionInstruction(merkleRootOffset uint64) Instruction {
	return Instruction{
		Specifier: SpecifierRevision,
		Args:      nil,
	}
}
