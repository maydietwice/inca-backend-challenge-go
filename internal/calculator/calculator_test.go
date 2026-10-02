package calculator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"inca-backend-challenge/internal/binance"
)

// testDepth is the order book depth used in tests.
const testDepth = 1000

// fakeAPI serves fixed data. It is safe for concurrent use.
type fakeAPI struct {
	symbols     []binance.Symbol
	orderBooks  map[string]binance.OrderBook
	err         error // returned by GetOrderBook for the symbol "FAIL"
	rateLimited int
	calls       int
	mu          sync.Mutex
}

func (f *fakeAPI) GetSymbols(context.Context) ([]binance.Symbol, error) {
	return f.symbols, nil
}

func (f *fakeAPI) GetRateLimits(context.Context) (binance.RateLimits, error) {
	return binance.RateLimits{UsedWeight: 1, TotalWeight: 6000}, nil
}

func (f *fakeAPI) GetOrderBook(_ context.Context, symbol binance.Symbol) (binance.OrderBook, error) {
	f.mu.Lock()
	f.calls++
	calls := f.calls
	f.mu.Unlock()

	if calls <= f.rateLimited {
		header := http.Header{}
		header.Add("Retry-After", "1")
		return binance.OrderBook{}, &binance.APIError{StatusCode: http.StatusTooManyRequests, Header: header}
	}

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
		symbols: []binance.Symbol{{Symbol: "AAA", Status: "TRADING"}, {Symbol: "BBB", Status: "BREAK"}, {Symbol: "CCC", Status: "TRADING"}},
		orderBooks: map[string]binance.OrderBook{
			"AAA": {Asks: orders(1, 2), Bids: orders(2)},
			"BBB": {Asks: orders(3)},
			"CCC": {Asks: orders(4, 1), Bids: orders(4, 6)},
		},
	}

	got, err := New(api, discardLogger(), testDepth).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// asks: (1+2+4+1)/4 = 2, bids: (2+4+6)/3 = 4
	if want := (VolumeMetrics{AsksAvg: 2, BidsAvg: 4}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRunReturnsAPIErrors(t *testing.T) {
	wantErr := errors.New("boom")
	api := &fakeAPI{symbols: []binance.Symbol{{Symbol: "FAIL", Status: "TRADING"}}, err: wantErr}

	if _, err := New(api, discardLogger(), testDepth).Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
}

func TestRunWithoutOrders(t *testing.T) {
	api := &fakeAPI{symbols: []binance.Symbol{{Symbol: "AAA", Status: "TRADING"}}, orderBooks: map[string]binance.OrderBook{}}

	if _, err := New(api, discardLogger(), testDepth).Run(context.Background()); !errors.Is(err, errNoOrders) {
		t.Fatalf("got %v, want %v", err, errNoOrders)
	}
}

func TestRunRetriesAfter429(t *testing.T) {
	api := &fakeAPI{
		symbols:     []binance.Symbol{{Symbol: "AAA", Status: "TRADING"}},
		orderBooks:  map[string]binance.OrderBook{"AAA": {Asks: orders(2), Bids: orders(4)}},
		rateLimited: 2,
	}

	got, err := New(api, discardLogger(), testDepth).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if want := (VolumeMetrics{AsksAvg: 2, BidsAvg: 4}); want != got {
		t.Fatalf("got %v, want %v", got, want)
	}

	if api.calls != api.rateLimited+1 {
		t.Fatalf("got %v, want %v", api.calls, api.rateLimited+1)
	}
}

func TestRunGivesUpAfterMaxAttempts(t *testing.T) {
	api := &fakeAPI{
		symbols:     []binance.Symbol{{Symbol: "AAA", Status: "TRADING"}},
		orderBooks:  map[string]binance.OrderBook{"AAA": {Asks: orders(2), Bids: orders(4)}},
		rateLimited: maxAPIRequestAttempts,
	}

	_, err := New(api, discardLogger(), testDepth).Run(context.Background())

	apiErr := &binance.APIError{}

	if !errors.As(err, &apiErr) {
		t.Fatalf("want err as binance.APIError, got %v", err)
	}

	if apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("got %v, want %v", apiErr.StatusCode, http.StatusTooManyRequests)
	}

	if api.calls != maxAPIRequestAttempts {
		t.Fatalf("got %v, want %v", api.calls, maxAPIRequestAttempts)
	}
}
