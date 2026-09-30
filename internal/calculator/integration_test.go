package calculator_test

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"inca-backend-challenge/internal/binance"
	"inca-backend-challenge/internal/calculator"
	"inca-backend-challenge/internal/config"
)

// TestRunAgainstBinance runs the whole job against the real Binance API, like
// the Spring Boot test of the original application. Enable it with
// BINANCE_INTEGRATION=1; add BINANCE_DRY_RUN=true to process only 10 symbols.
func TestRunAgainstBinance(t *testing.T) {
	if os.Getenv("BINANCE_INTEGRATION") == "" {
		t.Skip("set BINANCE_INTEGRATION=1 to run against the real Binance API")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	client := binance.NewClient(binance.NewHTTPClient(cfg.ConnectTimeout, cfg.ReadTimeout), cfg.Binance)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	metrics, err := calculator.New(client, logger).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if metrics.AsksAvg <= 0 || metrics.BidsAvg <= 0 {
		t.Fatalf("expected positive averages, got %+v", metrics)
	}
}
