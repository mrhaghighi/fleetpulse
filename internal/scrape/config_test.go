package scrape

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func validConfig() Config {
	return Config{
		Interval:    10 * time.Second,
		Timeout:     5 * time.Second,
		Concurrency: 256,
		Targets:     []Target{{URL: "http://10.0.0.5:9100"}},
		Discovery: Discovery{HTTP: []DiscoverySource{
			{URL: "http://simulator:9200/targets", Refresh: 30 * time.Second},
		}},
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "interval too short", mutate: func(c *Config) { c.Interval = 500 * time.Millisecond }, wantErr: true},
		{name: "timeout zero", mutate: func(c *Config) { c.Timeout = 0 }, wantErr: true},
		{name: "timeout equals interval", mutate: func(c *Config) { c.Timeout = c.Interval }, wantErr: true},
		{name: "timeout longer than interval", mutate: func(c *Config) { c.Timeout = 20 * time.Second }, wantErr: true},
		{name: "concurrency zero", mutate: func(c *Config) { c.Concurrency = 0 }, wantErr: true},
		{name: "concurrency too high", mutate: func(c *Config) { c.Concurrency = 4097 }, wantErr: true},
		{name: "empty target url", mutate: func(c *Config) { c.Targets = []Target{{URL: ""}} }, wantErr: true},
		{name: "target without scheme", mutate: func(c *Config) { c.Targets = []Target{{URL: "10.0.0.5:9100"}} }, wantErr: true},
		{name: "target with ftp scheme", mutate: func(c *Config) { c.Targets = []Target{{URL: "ftp://10.0.0.5:9100"}} }, wantErr: true},
		{name: "discovery url empty", mutate: func(c *Config) { c.Discovery.HTTP[0].URL = "" }, wantErr: true},
		{name: "discovery refresh too short", mutate: func(c *Config) { c.Discovery.HTTP[0].Refresh = time.Millisecond }, wantErr: true},
		{name: "duplicate discovery url", mutate: func(c *Config) {
			c.Discovery.HTTP = append(c.Discovery.HTTP, c.Discovery.HTTP[0])
		}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := validConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "scraper.yaml")
	data := []byte(`interval: 10s
timeout: 5s
concurrency: 256
targets:
  - url: http://10.0.0.5:9100
discovery:
  http:
    - url: http://simulator:9200/targets
      refresh: 30s
`)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	cfg, err := LoadConfig(path)
	require.NoError(t, err)
	require.Equal(t, validConfig(), cfg)
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "scraper.yaml")
	data := []byte(`interval: 10s
timeout: 5s
concurrency: 256
unknown: true
`)
	require.NoError(t, os.WriteFile(path, data, 0o600))

	_, err := LoadConfig(path)
	require.Error(t, err)
}
