package scrape

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	minInterval    = time.Second
	minConcurrency = 1
	maxConcurrency = 4096
)

// Config is the scraper configuration file described by contract 05.
type Config struct {
	// Interval is how often each target is pulled.
	Interval time.Duration `yaml:"interval"`
	// Timeout is the per-request HTTP timeout. It must be shorter than Interval.
	Timeout time.Duration `yaml:"timeout"`
	// Concurrency is the maximum number of simultaneous scrapes.
	Concurrency int `yaml:"concurrency"`
	// Targets are statically configured exporter base URLs.
	Targets []Target `yaml:"targets"`
	// Discovery lists HTTP service-discovery sources.
	Discovery Discovery `yaml:"discovery"`
}

// Target is one statically configured exporter base URL.
type Target struct {
	URL string `yaml:"url"`
}

// Discovery lists HTTP service-discovery sources.
type Discovery struct {
	HTTP []DiscoverySource `yaml:"http"`
}

// DiscoverySource is one HTTP endpoint that returns targets, and how often the
// scraper refreshes it.
type DiscoverySource struct {
	URL     string        `yaml:"url"`
	Refresh time.Duration `yaml:"refresh"`
}

// LoadConfig reads, parses and validates a scraper YAML config file.
func LoadConfig(path string) (Config, error) {
	// The path comes from the operator via SCRAPER_CONFIG.
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied config path
	if err != nil {
		return Config{}, fmt.Errorf("read scraper config: %w", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse scraper config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("scraper config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks the durations, concurrency and every target and discovery
// URL against contract 05.
func (c Config) Validate() error {
	switch {
	case c.Interval < minInterval:
		return fmt.Errorf("interval %s must be at least %s", c.Interval, minInterval)
	case c.Timeout <= 0:
		return fmt.Errorf("timeout %s must be positive", c.Timeout)
	case c.Timeout >= c.Interval:
		return fmt.Errorf("timeout %s must be shorter than interval %s", c.Timeout, c.Interval)
	case c.Concurrency < minConcurrency || c.Concurrency > maxConcurrency:
		return fmt.Errorf("concurrency %d must be between %d and %d", c.Concurrency, minConcurrency, maxConcurrency)
	}

	for i, target := range c.Targets {
		if err := validateHTTPURL(target.URL); err != nil {
			return fmt.Errorf("targets[%d].url: %w", i, err)
		}
	}

	seen := make(map[string]struct{}, len(c.Discovery.HTTP))
	for i, source := range c.Discovery.HTTP {
		if err := validateHTTPURL(source.URL); err != nil {
			return fmt.Errorf("discovery.http[%d].url: %w", i, err)
		}
		if source.Refresh < minInterval {
			return fmt.Errorf("discovery.http[%d].refresh %s must be at least %s", i, source.Refresh, minInterval)
		}
		if _, ok := seen[source.URL]; ok {
			return fmt.Errorf("discovery.http[%d].url %q is duplicated", i, source.URL)
		}
		seen[source.URL] = struct{}{}
	}

	return nil
}

func validateHTTPURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("url is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url %q must use http or https", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("url %q has no host", raw)
	}
	return nil
}
