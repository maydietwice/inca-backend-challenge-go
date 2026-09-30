// Package config holds the application settings. Defaults mirror
// application.yml of the original Kotlin challenge.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"inca-backend-challenge/internal/binance"
)

// Config is the application configuration.
type Config struct {
	Binance        binance.Options
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
}

// Default returns the settings of the original challenge.
func Default() Config {
	return Config{
		Binance: binance.Options{
			BaseURL:             "https://api.binance.com/api/v3",
			OrderBookDepth:      1000,
			InfoEndpointTimeout: 10 * time.Second,
			MaxResponseSize:     64 << 20, // safety cap on a single response body
		},
		ConnectTimeout: 30 * time.Second,
		ReadTimeout:    time.Minute,
	}
}

// Load returns the default settings overridden by environment variables:
//
//	BINANCE_BASE_URL          REST API base URL
//	BINANCE_ORDER_BOOK_DEPTH  limit parameter of GET /depth
//	BINANCE_DRY_RUN           "true" to process only the first 10 symbols
func Load() (Config, error) {
	cfg := Default()
	if v := os.Getenv("BINANCE_BASE_URL"); v != "" {
		cfg.Binance.BaseURL = v
	}
	if v := os.Getenv("BINANCE_ORDER_BOOK_DEPTH"); v != "" {
		depth, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: BINANCE_ORDER_BOOK_DEPTH: %w", err)
		}
		cfg.Binance.OrderBookDepth = depth
	}
	if v := os.Getenv("BINANCE_DRY_RUN"); v != "" {
		dryRun, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("config: BINANCE_DRY_RUN: %w", err)
		}
		cfg.Binance.DryRun = dryRun
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate reports whether the settings are usable.
func (c Config) Validate() error {
	switch {
	case c.Binance.BaseURL == "":
		return errors.New("config: base URL is empty")
	case c.Binance.OrderBookDepth <= 0:
		return fmt.Errorf("config: order book depth must be positive, got %d", c.Binance.OrderBookDepth)
	case c.Binance.InfoEndpointTimeout <= 0, c.ConnectTimeout <= 0, c.ReadTimeout <= 0:
		return errors.New("config: timeouts must be positive")
	case c.Binance.MaxResponseSize <= 0:
		return errors.New("config: max response size must be positive")
	}
	return nil
}
