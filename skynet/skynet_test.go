package skynet

import (
	"math"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/fastrand"
)

// newTestSkyfileLayout is a helper that returns a SkyfileLayout with some
// default settings for testing.
func newTestSkyfileLayout() SkyfileLayout {
	return SkyfileLayout{
		Version:            SkyfileVersion,
		Filesize:           1e6,
		MetadataSize:       14e3,
		FanoutSize:         75e3,
		FanoutDataPieces:   1,
		FanoutParityPieces: 10,
		CipherType:         crypto.TypePlain,
	}
}

// TestSkyfileLayoutEncoding checks that encoding and decoding a skyfile
// layout always results in the same struct.
func TestSkyfileLayoutEncoding(t *testing.T) {
	// Try encoding an decoding a simple example.
	llOriginal := newTestSkyfileLayout()
	rand := fastrand.Bytes(64)
	copy(llOriginal.KeyData[:], rand)
	encoded := llOriginal.Encode()
	var llRecovered SkyfileLayout
	llRecovered.Decode(encoded)
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
	layout := newTestSkyfileLayout()
	layoutBytes := layout.Encode()
	randData := fastrand.Bytes(int(modules.SectorSize))
	copy(randData, layoutBytes)
	ParseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic
	// Overflow the fanout.
	layout.FanoutSize = math.MaxUint64 - 14e3 - 1
	layoutBytes = layout.Encode()
	randData = fastrand.Bytes(int(modules.SectorSize))
	copy(randData, layoutBytes)
	ParseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic
	// Overflow the metadata size
	layout.MetadataSize = math.MaxUint64 - 75e3 - 1
	layout.FanoutSize = 75e3
	layoutBytes = layout.Encode()
	randData = fastrand.Bytes(int(modules.SectorSize))
	copy(randData, layoutBytes)
	ParseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic

	// Try a bunch of random data.
	for i := 0; i < 10e3; i++ {
		randData := fastrand.Bytes(int(modules.SectorSize))
		ParseSkyfileMetadata(randData) // no error check, just want to know it doesn't panic

		// Only do 1 iteration for short testing.
		if testing.Short() {
			t.SkipNow()
		}
	}
}
