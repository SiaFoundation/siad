package fixtures

import (
	"gitlab.com/NebulousLabs/Sia/modules"
)

type (
	// SkylinkFixture holds the download representation of a Skylink
	SkylinkFixture struct {
		Metadata modules.SkyfileMetadata
		Streamer modules.Streamer
	}
)

var (
	skylinkFixtures = map[modules.Skylink]SkylinkFixture{
		//modules.Skylink{}: {
		//	Metadata: modules.SkyfileMetadata{},
		//	Streamer: nil,
		//},
	}
)

// LoadSkylinkFixture returns the SkylinkFixture representation of a Skylink. It
// returns `true` if a fixture was found for this skylink or `false` otherwise.
func LoadSkylinkFixture(link modules.Skylink) (SkylinkFixture, bool) {
	fs, exists := skylinkFixtures[link]
	return fs, exists
}
