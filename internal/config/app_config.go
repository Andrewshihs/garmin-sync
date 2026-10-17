package config

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type AppConfig struct {
	Format  string `toml:"format"`
	Profile string `toml:"profile"`

	// HTTPTimeout is the timeout for regular API requests (seconds).
	// Default: 30s
	HTTPTimeout int `toml:"http_timeout"`

	// AuthTimeout is the timeout for authentication-related requests
	// (OAuth, S3 consumer fetch) (seconds). Default: 20s
	AuthTimeout int `toml:"auth_timeout"`
}

// GetHTTPTimeout returns the configured HTTP timeout or the default (30s).
func (c AppConfig) GetHTTPTimeout() time.Duration {
	if c.HTTPTimeout > 0 {
		return time.Duration(c.HTTPTimeout) * time.Second
	}
	return 30 * time.Second
}

// GetAuthTimeout returns the configured auth timeout or the default (20s).
func (c AppConfig) GetAuthTimeout() time.Duration {
	if c.AuthTimeout > 0 {
		return time.Duration(c.AuthTimeout) * time.Second
	}
	return 20 * time.Second
}

func ConfigPath(configDir string) string {
	return filepath.Join(configDir, "config.toml")
}

func LoadAppConfig(configDir string) (AppConfig, error) {
	path := ConfigPath(configDir)

	var cfg AppConfig
	_, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		// If the file doesn't exist, treat as empty config.
		var pErr *os.PathError
		if errors.As(err, &pErr) && errors.Is(pErr.Err, os.ErrNotExist) {
			return AppConfig{}, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return AppConfig{}, nil
		}
		return AppConfig{}, err
	}

	return cfg, nil
}
