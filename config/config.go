// Package config provides configuration types for bsv21-overlay.
package config

import (
	"os"
	"path/filepath"

	chaintracksconfig "github.com/bsv-blockchain/go-chaintracks/config"
	"github.com/spf13/viper"
)

// Config holds library configuration for bsv21 (embeddable in other projects).
type Config struct {
	Network  string `mapstructure:"network"`   // "main", "test", "stn" - Bitcoin network
	LogLevel string `mapstructure:"log_level"` // Log level (debug, info, warn, error)

	Events      StorageURLConfig  `mapstructure:"events"`
	Beef        StorageURLConfig  `mapstructure:"beef"`
	Queue       StorageURLConfig  `mapstructure:"queue"`
	PubSub      StorageURLConfig  `mapstructure:"pubsub"`
	Arc         ArcConfig         `mapstructure:"arc"`
	Hosting     HostingConfig     `mapstructure:"hosting"`
	Sync        SyncConfig        `mapstructure:"sync"`
	Chaintracks chaintracksconfig.Config `mapstructure:"chaintracks"`
}

// StorageURLConfig holds a URL for storage backends
type StorageURLConfig struct {
	URL string `mapstructure:"url"`
}

// ArcConfig holds Arc broadcaster configuration
type ArcConfig struct {
	URL      string `mapstructure:"url"`
	APIKey   string `mapstructure:"api_key"`
	Token    string `mapstructure:"callback_token"`
}

// HostingConfig holds hosting/callback configuration
type HostingConfig struct {
	URL string `mapstructure:"url"`
}

// SyncConfig holds sync-related configuration
type SyncConfig struct {
	GASP   bool `mapstructure:"gasp"`
	LibP2P bool `mapstructure:"libp2p"`
}

// SetDefaults sets viper defaults for bsv21 configuration.
func (c *Config) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	// Top-level defaults
	v.SetDefault(p+"network", "main")
	v.SetDefault(p+"log_level", "info")

	// Storage URL defaults (empty = use default backends)
	v.SetDefault(p+"events.url", "./overlay.db")
	v.SetDefault(p+"beef.url", "")
	v.SetDefault(p+"queue.url", "./queue.db")
	v.SetDefault(p+"pubsub.url", "channels://")

	// Arc defaults
	v.SetDefault(p+"arc.url", "https://arc.gorillapool.io/v1")
	v.SetDefault(p+"arc.api_key", "")
	v.SetDefault(p+"arc.callback_token", "")

	// Hosting defaults
	v.SetDefault(p+"hosting.url", "")

	// Sync defaults
	v.SetDefault(p+"sync.gasp", false)
	v.SetDefault(p+"sync.libp2p", false)

	// Cascade to chaintracks config
	c.Chaintracks.SetDefaults(v, p+"chaintracks")
}

// ExpandPath expands ~ to user's home directory
func ExpandPath(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(homeDir, path[2:])
	}
	return path
}
