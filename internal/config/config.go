package config

import (
	"errors"
	"fmt"

	"github.com/caarlos0/env/v11"
)

// Load parses environment variables into cfg using caarlos0/env.
//
// cfg must be a non-nil pointer to a struct with env tags. Parse errors include
// the offending field or environment variable name.
func Load[T any](cfg *T) error {
	if cfg == nil {
		return errors.New("load config: nil config")
	}
	if err := env.Parse(cfg); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	return nil
}
