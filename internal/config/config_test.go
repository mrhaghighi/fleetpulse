package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testConfig struct {
	Addr     string        `env:"FLEETPULSE_TEST_T02_ADDR" envDefault:":9100"`
	Timeout  time.Duration `env:"FLEETPULSE_TEST_T02_TIMEOUT" envDefault:"5s"`
	Replicas int           `env:"FLEETPULSE_TEST_T02_REPLICAS" envDefault:"1"`
}

func TestLoadUsesDefaults(t *testing.T) {
	var cfg testConfig
	require.NoError(t, Load(&cfg))

	require.Equal(t, ":9100", cfg.Addr)
	require.Equal(t, 5*time.Second, cfg.Timeout)
	require.Equal(t, 1, cfg.Replicas)
}

func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("FLEETPULSE_TEST_T02_ADDR", ":9200")
	t.Setenv("FLEETPULSE_TEST_T02_TIMEOUT", "2s")
	t.Setenv("FLEETPULSE_TEST_T02_REPLICAS", "3")

	var cfg testConfig
	require.NoError(t, Load(&cfg))

	require.Equal(t, ":9200", cfg.Addr)
	require.Equal(t, 2*time.Second, cfg.Timeout)
	require.Equal(t, 3, cfg.Replicas)
}

func TestLoadReportsInvalidField(t *testing.T) {
	type badConfig struct {
		Port int `env:"FLEETPULSE_TEST_T02_PORT"`
	}

	t.Setenv("FLEETPULSE_TEST_T02_PORT", "not-a-number")

	var cfg badConfig
	err := Load(&cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "Port")
}

func TestLoadReportsMissingRequired(t *testing.T) {
	type requiredConfig struct {
		Token string `env:"FLEETPULSE_TEST_T02_TOKEN,required"`
	}

	var cfg requiredConfig
	err := Load(&cfg)
	require.Error(t, err)
	require.ErrorContains(t, err, "FLEETPULSE_TEST_T02_TOKEN")
}

func TestLoadRejectsNil(t *testing.T) {
	var cfg *testConfig
	require.Error(t, Load(cfg))
}
