// Command challenge requests order books for all Binance symbols and
// calculates the average volumes for asks and bids.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"inca-backend-challenge/internal/binance"
	"inca-backend-challenge/internal/calculator"
	"inca-backend-challenge/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("order book metrics failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	flag.BoolVar(&cfg.Binance.DryRun, "dry-run", cfg.Binance.DryRun, "process only the first 10 symbols")
	flag.IntVar(&cfg.Binance.OrderBookDepth, "depth", cfg.Binance.OrderBookDepth, "order book depth (limit parameter of GET /depth)")
	flag.StringVar(&cfg.Binance.BaseURL, "base-url", cfg.Binance.BaseURL, "Binance REST API base URL")
	flag.Parse()
	if err := cfg.Validate(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := binance.NewClient(binance.NewHTTPClient(cfg.ConnectTimeout, cfg.ReadTimeout), cfg.Binance)
	_, err = calculator.New(client, logger).Run(ctx)
	return err
}
