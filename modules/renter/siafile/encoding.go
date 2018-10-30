package siafile

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// marshalChunk binary encodes a chunk. It only allocates memory a single time
// for the whole chunk.
func marshalChunk(chunk chunk) (chunkBytes []byte, err error) {
	chunkBytes = make([]byte, 0, marshaledChunkSize(chunk.numPieces()))

	// Write the extension info.
	chunkBytes = append(chunkBytes, chunk.ExtensionInfo[:]...)

	// Write the pieces length prefix.
	chunkBytes = chunkBytes[:len(chunkBytes)+2]
	binary.LittleEndian.PutUint16(chunkBytes[len(chunk.ExtensionInfo):], uint16(chunk.numPieces()))

	// Write the pieces.
	for pieceIndex, pieceSet := range chunk.Pieces {
		for _, piece := range pieceSet {
			chunkBytes, err = marshalPiece(chunkBytes, uint32(pieceIndex), piece)
			if err != nil {
				return
			}
		}
	}
	return
}

// marshalErasureCoder marshals an erasure coder into its type and params.
func marshalErasureCoder(ec modules.ErasureCoder) ([4]byte, [8]byte) {
	// Since we only support one type we assume it is ReedSolomon for now.
	ecType := ecReedSolomon
	// Read params from ec.
	ecParams := [8]byte{}
	binary.LittleEndian.PutUint32(ecParams[:4], uint32(ec.MinPieces()))
	binary.LittleEndian.PutUint32(ecParams[4:], uint32(ec.NumPieces()-ec.MinPieces()))
	return ecType, ecParams
}

// marshalMetadata marshals the metadata of the SiaFile using json encoding.
func marshalMetadata(md metadata) ([]byte, error) {
	// Encode the metadata.
	jsonMD, err := json.Marshal(md)
	if err != nil {
		return nil, err
	}
	return jsonMD, nil
}

// marshalPiece uses binary encoding to marshal a piece and append the
// marshaled piece to out. That way when marshaling a chunk, the whole chunk's
// memory can be allocated with a single allocation.
func marshalPiece(out []byte, pieceIndex uint32, piece piece) ([]byte, error) {
	// Check if out has enough capacity left for the piece. If not, we extend
	// out.
	if cap(out)-len(out) < marshaledPieceSize {
		extendedOut := make([]byte, len(out), len(out)+marshaledPieceSize)
		copy(extendedOut, out)
		out = extendedOut
	}
	pieceBytes := out[len(out) : len(out)+marshaledPieceSize]
	binary.LittleEndian.PutUint32(pieceBytes[:4], pieceIndex)
	binary.LittleEndian.PutUint32(pieceBytes[4:8], piece.HostTableOffset)
	copy(pieceBytes[8:], piece.MerkleRoot[:])
	return out[:len(out)+marshaledPieceSize], nil
}

// marshalPubKeyTable marshals the public key table of the SiaFile using Sia
// encoding.
func marshalPubKeyTable(pubKeyTable []HostPublicKey) ([]byte, error) {
	// Create a buffer.
	buf := bytes.NewBuffer(nil)
	// Marshal all the data into the buffer
	for _, pk := range pubKeyTable {
		if err := pk.MarshalSia(buf); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// numChunkPagesRequired calculates the number of pages on disk we need to
// reserve for each chunk to store numPieces.
func numChunkPagesRequired(numPieces int) int8 {
	chunkSize := marshaledChunkSize(numPieces)
	numPages := chunkSize / pageSize
	if chunkSize%pageSize != 0 {
		numPages++
	}
	return int8(numPages)
}

// unmarshalChunk unmarshals a chunk which was previously marshaled using
// marshalChunk. It also requires the number of piec as an argument to know how
// many unique pieces to expect when reading the pieces which we can easily
// find out by taking a look at the erasure coder within the siafile header.
// Unfortunately it's not enoug to simply look at the piece indices when
// reading the pieces from disk, since there is no guarantee that we already
// uploaded a piece for each index.
func unmarshalChunk(numPieces uint32, raw []byte) (chunk chunk, err error) {
	// initialize the pieces.
	chunk.Pieces = make([][]piece, numPieces)

	// read the ExtensionInfo first.
	buf := bytes.NewBuffer(raw)
	if _, err = io.ReadFull(buf, chunk.ExtensionInfo[:]); err != nil {
		return chunk, errors.AddContext(err, "failed to unmarshal ExtensionInfo")
	}

	// read the pieces length prefix.
	prefixLen := 2
	prefixBytes := buf.Next(prefixLen)
	if len(prefixBytes) != prefixLen {
		return chunk, errors.New("length prefix missing")
	}
	piecesToLoad := binary.LittleEndian.Uint16(prefixBytes)

	// read the pieces one by one.
	var loadedPieces uint16
	for pieceBytes := buf.Next(marshaledPieceSize); loadedPieces < piecesToLoad; pieceBytes = buf.Next(marshaledPieceSize) {
		pieceIndex, piece, err := unmarshalPiece(pieceBytes)
		if err != nil {
			return chunk, err
		}
		if pieceIndex >= numPieces {
			return chunk, fmt.Errorf("unexpected piece index, should be below %v but was %v", numPieces, pieceIndex)
		}
		chunk.Pieces[pieceIndex] = append(chunk.Pieces[pieceIndex], piece)
		loadedPieces++
	}
	return
}

// unmarshalErasureCoder unmarshals an ErasureCoder from the given params.
func unmarshalErasureCoder(ecType [4]byte, ecParams [8]byte) (modules.ErasureCoder, error) {
	if ecType != ecReedSolomon {
		return nil, errors.New("unknown erasure code type")
	}
	dataPieces := int(binary.LittleEndian.Uint32(ecParams[:4]))
	parityPieces := int(binary.LittleEndian.Uint32(ecParams[4:]))
	return NewRSCode(dataPieces, parityPieces)
}

// unmarshalMetadata unmarshals the json encoded metadata of the SiaFile.
func unmarshalMetadata(raw []byte) (md metadata, err error) {
	err = json.Unmarshal(raw, &md)

	// We also need to create the erasure coder object.
	md.staticErasureCode, err = unmarshalErasureCoder(md.StaticErasureCodeType, md.StaticErasureCodeParams)
	if err != nil {
		return
	}
	return
}

// unmarshalPiece unmarshals a piece from a byte slice which was previously
// marshaled using marshalPiece.
func unmarshalPiece(raw []byte) (pieceIndex uint32, piece piece, err error) {
	if len(raw) != marshaledPieceSize {
		err = fmt.Errorf("unexpected piece size, should be %v but was %v", marshaledPieceSize, len(raw))
		return
	}
	buf := bytes.NewBuffer(raw)
	err1 := binary.Read(buf, binary.LittleEndian, &pieceIndex)
	err2 := binary.Read(buf, binary.LittleEndian, &piece.HostTableOffset)
	_, err3 := io.ReadFull(buf, piece.MerkleRoot[:])
	err = errors.Compose(err1, err2, err3)
	return
}

// unmarshalPubKeyTable unmarshals a sia encoded public key table.
func unmarshalPubKeyTable(raw []byte) (keys []HostPublicKey, err error) {
	// Create the buffer.
	r := bytes.NewBuffer(raw)
	// Unmarshal the keys one by one until EOF or a different error occur.
	for {
		var key HostPublicKey
		if err = key.UnmarshalSia(r); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}
