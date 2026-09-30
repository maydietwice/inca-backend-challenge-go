package binance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc, dryRun bool) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(server.Client(), Options{
		BaseURL:             server.URL + "/api/v3",
		OrderBookDepth:      1000,
		InfoEndpointTimeout: 5 * time.Second,
		MaxResponseSize:     1 << 20,
		DryRun:              dryRun,
	})
}

func exchangeInfoJSON(symbols int) string {
	var sb strings.Builder
	sb.WriteString(`{"rateLimits":[` +
		`{"rateLimitType":"REQUEST_WEIGHT","interval":"MINUTE","intervalNum":1,"limit":6000},` +
		`{"rateLimitType":"ORDERS","interval":"SECOND","intervalNum":10,"limit":100}],"symbols":[`)
	for i := range symbols {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"symbol":"SYM%dUSDT","baseAsset":"SYM%d","quoteAsset":"USDT","status":"TRADING"}`, i, i)
	}
	sb.WriteString("]}")
	return sb.String()
}

func TestGetSymbols(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/exchangeInfo" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, exchangeInfoJSON(12))
	}

	for _, tc := range []struct {
		name   string
		dryRun bool
		want   int
	}{
		{"all symbols", false, 12},
		{"dry run", true, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			symbols, err := newTestClient(t, handler, tc.dryRun).GetSymbols(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(symbols) != tc.want {
				t.Fatalf("got %d symbols, want %d", len(symbols), tc.want)
			}
			if symbols[0].Symbol != "SYM0USDT" || symbols[0].QuoteAsset != "USDT" {
				t.Fatalf("unexpected first symbol: %+v", symbols[0])
			}
		})
	}
}

func TestGetRateLimits(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-MBX-USED-WEIGHT-1M", "42")
			fmt.Fprint(w, exchangeInfoJSON(1))
		}, false)

		got, err := client.GetRateLimits(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if want := (RateLimits{UsedWeight: 42, TotalWeight: 6000}); got != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, exchangeInfoJSON(1))
		}, false)

		if _, err := client.GetRateLimits(context.Background()); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("missing REQUEST_WEIGHT", func(t *testing.T) {
		client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-MBX-USED-WEIGHT-1M", "42")
			fmt.Fprint(w, `{"rateLimits":[],"symbols":[]}`)
		}, false)

		if _, err := client.GetRateLimits(context.Background()); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestGetOrderBook(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/depth" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("symbol"); got != "ETHBTC" {
			t.Errorf("symbol = %q, want ETHBTC", got)
		}
		if got := r.URL.Query().Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		fmt.Fprint(w, `{"lastUpdateId":1027024,`+
			`"bids":[["4.00000000","431.00000000"]],`+
			`"asks":[["4.00000200","12.00000000"],["4.10000000","0.50000000"]]}`)
	}, false)

	orderBook, err := client.GetOrderBook(context.Background(), Symbol{Symbol: "ETHBTC"})
	if err != nil {
		t.Fatal(err)
	}
	if orderBook.LastUpdateID != 1027024 || len(orderBook.Bids) != 1 || len(orderBook.Asks) != 2 {
		t.Fatalf("unexpected order book: %+v", orderBook)
	}
	if got, want := orderBook.Bids[0], (Order{Price: 4, Volume: 431}); got != want {
		t.Fatalf("bid = %+v, want %+v", got, want)
	}
	if got, want := orderBook.Asks[1], (Order{Price: 4.1, Volume: 0.5}); got != want {
		t.Fatalf("ask = %+v, want %+v", got, want)
	}
}

func TestAPIError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"code":-1003,"msg":"Too many requests."}`)
	}, false)

	_, err := client.GetOrderBook(context.Background(), Symbol{Symbol: "ETHBTC"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Code != -1003 || apiErr.Header.Get("Retry-After") != "30" {
		t.Fatalf("unexpected error: %+v", apiErr)
	}
}

func TestMaxResponseSize(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"lastUpdateId":1,"bids":[],"asks":[],"padding":"%s"}`, strings.Repeat("x", 2<<20))
	}, false)

	if _, err := client.GetOrderBook(context.Background(), Symbol{Symbol: "ETHBTC"}); err == nil {
		t.Fatal("expected an error for an oversized response")
	}
}

func TestOrderUnmarshalJSONErrors(t *testing.T) {
	for _, input := range []string{`"4.0"`, `["abc","1.0"]`, `["1.0","abc"]`, `["1.0"]`} {
		var o Order
		if err := o.UnmarshalJSON([]byte(input)); err == nil {
			t.Errorf("UnmarshalJSON(%s): expected an error", input)
		}
	}
}
