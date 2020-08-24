package fixtures

import (
	"encoding/json"
	"errors"
	"os"

	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// Fixture paths:
	// These are relative paths to the fixtures data. They are relative to the
	// currently running test's home directory and do not depend on the location
	// of this implementation. This allows us to load different data for
	// different tests.

	// skylinkFixturesPath points to fixtures representing skylinks when they
	// are being downloaded. See the SkylinkFixture struct.
	skylinkFixturesPath = "testdata/skylink_fixtures.json"
)

type (
	// SkylinkFixture holds the download representation of a Skylink
	SkylinkFixture struct {
		Metadata modules.SkyfileMetadata `json:"metadata"`
		Content  []byte                  `json:"content"`
	}
)

// LoadSkylinkFixture returns the SkylinkFixture representation of a Skylink.
//
// NOTES: Each test is run with its own directory as a working directory. This
// means that we can load a relative path and each test will load its own data
// or, at least, the data of its own directory.
func LoadSkylinkFixture(link modules.Skylink) (SkylinkFixture, error) {
	f, err := os.Open(skylinkFixturesPath)
	if err != nil {
		return SkylinkFixture{}, err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return SkylinkFixture{}, err
	}
	b := make([]byte, fi.Size())
	n, err := f.Read(b)
	if err != nil {
		return SkylinkFixture{}, err
	}
	skylinkFixtures := make(map[string]SkylinkFixture)
	err = json.Unmarshal(b[:n], &skylinkFixtures)
	if err != nil {
		return SkylinkFixture{}, err
	}
	fs, exists := skylinkFixtures[link.String()]
	if !exists {
		return SkylinkFixture{}, errors.New("fixture not found")
	}
	return fs, nil
}
