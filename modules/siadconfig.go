package modules

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/ratelimit"
)

type (
	// SiadConfig is a helper type to manage the globald siad config.
	SiadConfig struct {
		// Ratelimit related fields
		ReadBPS    int64  `json:"readbps"`
		WriteBPS   int64  `json:"writeps"`
		PacketSize uint64 `json:"packetsize"`

		// path of config on disk.
		path string
	}
)

var (
	configMetadata = persist.Metadata{
		Header:  "siad.config",
		Version: "1.0.0",
	}
)

// SetRatelimit sets the ratelimit related fields in the config and persists it
// to disk.
func (cfg *SiadConfig) SetRatelimit(readBPS, writeBPS int64, packetSize uint64) error {
	cfg.ReadBPS = readBPS
	cfg.WriteBPS = writeBPS
	cfg.PacketSize = packetSize
	return cfg.save()
}

// Ratelimit returns the ratelimit stored in the config.
func (cfg *SiadConfig) Ratelimit() *ratelimit.RateLimit {
	return ratelimit.NewRateLimit(cfg.ReadBPS, cfg.WriteBPS, cfg.PacketSize)
}

// save saves the config to disk.
func (cfg SiadConfig) save() error {
	return persist.SaveJSON(configMetadata, cfg, cfg.path)
}

// load loads the config from disk.
func (cfg *SiadConfig) load(path string) error {
	return persist.LoadJSON(configMetadata, cfg, path)
}

// NewConfig loads a config from disk or creates a new one if no config exists
// yet.
func NewConfig(path string) (*SiadConfig, error) {
	var cfg SiadConfig
	cfg.path = path
	// Try loading the config from disk first.
	err := cfg.load(cfg.path)
	if err == nil {
		return &cfg, nil
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	// Otherwise init with default values.
	cfg.ReadBPS = 0    // unlimited
	cfg.WriteBPS = 0   // unlimited
	cfg.PacketSize = 0 // unlimited
	return &cfg, nil
}
