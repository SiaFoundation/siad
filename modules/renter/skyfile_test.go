package renter

import (
	"math"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestSkyfileLayoutEncoding checks that encoding and decoding a skyfile
// layout always results in the same struct.
func TestSkyfileLayoutEncoding(t *testing.T) {
	// Try encoding an decoding a simple example.
	llOriginal := skyfileLayout{
		version:            SkyfileVersion,
		filesize:           1e6,
		metadataSize:       14e3,
		fanoutSize:         75e3,
		fanoutDataPieces:   10,
		fanoutParityPieces: 20,
		cipherType:         crypto.TypePlain,
	}
	rand := fastrand.Bytes(64)
	copy(llOriginal.cipherKey[:], rand)
	encoded := llOriginal.encode()
	var llRecovered skyfileLayout
	llRecovered.decode(encoded)
	if llOriginal != llRecovered {
		t.Fatal("encoding and decoding of skyfileLayout does not match")
	}
}

// TestParseSkyfileMetadata checks that the skyfile metadata parser correctly
// catches malformed skyfile layout data.
//
// NOTE: this test will become invalid once the skyfile metadata parser is able
// to fetch larger fanouts and larger metadata than what can fit in the base
// chunk.
func TestParseSkyfileMetadata(t *testing.T) {
	// Try some chosen skyfile layouts.
	//
	// Standard layout, nothing tricky.
	layout := skyfileLayout{
		version:            SkyfileVersion,
		filesize:           1e6,
		metadataSize:       14e3,
		fanoutSize:         75e3,
		fanoutDataPieces:   1,
		fanoutParityPieces: 10,
		cipherType:         crypto.TypePlain,
	}
	layoutBytes := layout.encode()
	randData := fastrand.Bytes(int(modules.SectorSize))
	copy(randData, layoutBytes)
	parseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic
	// Overflow the fanout.
	layout = skyfileLayout{
		version:            SkyfileVersion,
		filesize:           1e6,
		metadataSize:       14e3,
		fanoutSize:         math.MaxUint64 - 14e3 - 1,
		fanoutDataPieces:   1,
		fanoutParityPieces: 10,
		cipherType:         crypto.TypePlain,
	}
	layoutBytes = layout.encode()
	randData = fastrand.Bytes(int(modules.SectorSize))
	copy(randData, layoutBytes)
	parseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic
	// Overflow the metadata size
	layout = skyfileLayout{
		version:            SkyfileVersion,
		filesize:           1e6,
		metadataSize:       math.MaxUint64 - 75e3 - 1,
		fanoutSize:         75e3,
		fanoutDataPieces:   1,
		fanoutParityPieces: 10,
		cipherType:         crypto.TypePlain,
	}
	layoutBytes = layout.encode()
	randData = fastrand.Bytes(int(modules.SectorSize))
	copy(randData, layoutBytes)
	parseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic

	// Try a bunch of random data.
	for i := 0; i < 10e3; i++ {
		randData := fastrand.Bytes(int(modules.SectorSize))
		parseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic

		// Only do 1 iteration for short testing.
		if testing.Short() {
			t.SkipNow()
		}
	}
}
