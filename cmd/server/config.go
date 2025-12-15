package server

import (
	"strings"

	"github.com/b-open-io/bsv21-overlay/config"
	"github.com/spf13/viper"
)

// AppConfig holds all configuration for the standalone server application.
type AppConfig struct {
	// Server settings (standalone server only)
	Server ServerConfig `mapstructure:"server"`

	// BSV21 embeds the shared library configuration
	config.Config `mapstructure:",squash"`
}

// ServerConfig holds HTTP server configuration (standalone server only).
type ServerConfig struct {
	Port      int    `mapstructure:"port"`
	PProfPort string `mapstructure:"pprof_port"`
}

// SetDefaults sets viper defaults for the standalone server configuration.
func (c *AppConfig) SetDefaults(v *viper.Viper, prefix string) {
	p := ""
	if prefix != "" {
		p = prefix + "."
	}

	// Server defaults (standalone server only)
	v.SetDefault(p+"server.port", 3000)
	v.SetDefault(p+"server.pprof_port", "")

	// Cascade to library config defaults
	c.Config.SetDefaults(v, prefix)
}

// Load reads configuration from file and environment variables.
func Load() (*AppConfig, error) {
	v := viper.New()

	cfg := &AppConfig{}

	// Set all defaults via SetDefaults
	cfg.SetDefaults(v, "")

	// Config file settings
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("$HOME/.bsv21")
	v.AddConfigPath("/etc/bsv21")

	// Environment variable settings
	v.SetEnvPrefix("BSV21")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file (optional - env vars can provide everything)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}
