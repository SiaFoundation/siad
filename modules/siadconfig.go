package modules

import (
	"errors"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/ratelimit"
)

type (
	// SiadConfig is a helper type to manage the global siad config.
	SiadConfig struct {
		// Ratelimit related fields
		ReadBPS    int64  `json:"readbps"`
		WriteBPS   int64  `json:"writeps"`
		PacketSize uint64 `json:"packetsize"`

		// path of config on disk.
		path string
		mu   sync.Mutex
	}
)

var (
	// GlobalRateLimits is the global object for regulating ratelimits
	// throughout siad. It is set using the gateway module.
	GlobalRateLimits = ratelimit.NewRateLimit(0, 0, 0)

	configMetadata = persist.Metadata{
		Header:  "siad.config",
		Version: "1.0.0",
	}
)

// SetRatelimit sets the ratelimit related fields in the config and persists it
// to disk.
func (cfg *SiadConfig) SetRatelimit(readBPS, writeBPS int64) error {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	// Input validation.
	if readBPS < 0 || writeBPS < 0 {
		return errors.New("download/upload rate can't be below 0")
	}
	// Check for sentinel "no limits" value.
	if readBPS == 0 && writeBPS == 0 {
		GlobalRateLimits.SetLimits(0, 0, 0)
	} else {
		GlobalRateLimits.SetLimits(readBPS, writeBPS, 0)
	}
	// Persist settings.
	cfg.ReadBPS, cfg.WriteBPS, cfg.PacketSize = GlobalRateLimits.Limits()
	return cfg.save()
}

// save saves the config to disk.
func (cfg *SiadConfig) save() error {
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
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if os.IsNotExist(err) {
		// Otherwise init with default values.
		cfg.ReadBPS = 0    // unlimited
		cfg.WriteBPS = 0   // unlimited
		cfg.PacketSize = 0 // unlimited
	}
	// Init the global ratelimit.
	GlobalRateLimits.SetLimits(cfg.ReadBPS, cfg.WriteBPS, cfg.PacketSize)
	return &cfg, nil
}
