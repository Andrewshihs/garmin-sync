package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAppConfig_MissingFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadAppConfig(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Format != "" || cfg.Profile != "" {
		t.Fatalf("expected empty config, got: %#v", cfg)
	}
}

func TestLoadAppConfig_ParsesTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("format = \"json\"\nprofile = \"work\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadAppConfig(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.Format != "json" || cfg.Profile != "work" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadAppConfig_InvalidTOMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("this is not toml"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := LoadAppConfig(dir); err == nil {
		t.Fatalf("expected error")
	}
}

func TestAppConfig_GetHTTPTimeout(t *testing.T) {
	tests := []struct {
		name     string
		config   AppConfig
		expected time.Duration
	}{
		{"default when zero", AppConfig{}, 30 * time.Second},
		{"custom value", AppConfig{HTTPTimeout: 60}, 60 * time.Second},
		{"custom value 120", AppConfig{HTTPTimeout: 120}, 120 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetHTTPTimeout(); got != tt.expected {
				t.Errorf("GetHTTPTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAppConfig_GetAuthTimeout(t *testing.T) {
	tests := []struct {
		name     string
		config   AppConfig
		expected time.Duration
	}{
		{"default when zero", AppConfig{}, 20 * time.Second},
		{"custom value", AppConfig{AuthTimeout: 40}, 40 * time.Second},
		{"custom value 60", AppConfig{AuthTimeout: 60}, 60 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.GetAuthTimeout(); got != tt.expected {
				t.Errorf("GetAuthTimeout() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLoadAppConfig_ParsesTimeouts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `format = "markdown"
http_timeout = 60
auth_timeout = 40
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := LoadAppConfig(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.HTTPTimeout != 60 {
		t.Errorf("HTTPTimeout = %d, want 60", cfg.HTTPTimeout)
	}
	if cfg.AuthTimeout != 40 {
		t.Errorf("AuthTimeout = %d, want 40", cfg.AuthTimeout)
	}
	if got := cfg.GetHTTPTimeout(); got != 60*time.Second {
		t.Errorf("GetHTTPTimeout() = %v, want 60s", got)
	}
	if got := cfg.GetAuthTimeout(); got != 40*time.Second {
		t.Errorf("GetAuthTimeout() = %v, want 40s", got)
	}
}
