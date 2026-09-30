package calculator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"inca-backend-challenge/internal/binance"
)

// fakeAPI serves fixed data. It is read-only, so it is safe for concurrent use.
type fakeAPI struct {
	symbols    []binance.Symbol
	orderBooks map[string]binance.OrderBook
	err        error // returned by GetOrderBook for the symbol "FAIL"
}

func (f *fakeAPI) GetSymbols(context.Context) ([]binance.Symbol, error) {
	return f.symbols, nil
}

func (f *fakeAPI) GetRateLimits(context.Context) (binance.RateLimits, error) {
	return binance.RateLimits{UsedWeight: 1, TotalWeight: 6000}, nil
}

func (f *fakeAPI) GetOrderBook(_ context.Context, symbol binance.Symbol) (binance.OrderBook, error) {
	if symbol.Symbol == "FAIL" {
		return binance.OrderBook{}, f.err
	}
	return f.orderBooks[symbol.Symbol], nil
}

func orders(volumes ...float64) []binance.Order {
	result := make([]binance.Order, len(volumes))
	for i, v := range volumes {
		result[i] = binance.Order{Price: 1, Volume: v}
	}
	return result
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRun(t *testing.T) {
	api := &fakeAPI{
		symbols: []binance.Symbol{{Symbol: "AAA"}, {Symbol: "BBB"}, {Symbol: "CCC"}},
		orderBooks: map[string]binance.OrderBook{
			"AAA": {Asks: orders(1, 2), Bids: orders(2)},
			"BBB": {Asks: orders(3), Bids: orders(4, 6)},
			"CCC": {Asks: orders(6)},
		},
	}

	got, err := New(api, discardLogger()).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// asks: (1+2+3+6)/4 = 3, bids: (2+4+6)/3 = 4
	if want := (VolumeMetrics{AsksAvg: 3, BidsAvg: 4}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunReturnsAPIErrors(t *testing.T) {
	wantErr := errors.New("boom")
	api := &fakeAPI{symbols: []binance.Symbol{{Symbol: "FAIL"}}, err: wantErr}

	if _, err := New(api, discardLogger()).Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}

func TestRunWithoutOrders(t *testing.T) {
	api := &fakeAPI{symbols: []binance.Symbol{{Symbol: "AAA"}}, orderBooks: map[string]binance.OrderBook{}}

	if _, err := New(api, discardLogger()).Run(context.Background()); !errors.Is(err, errNoOrders) {
		t.Fatalf("got %v, want %v", err, errNoOrders)
	}
}
