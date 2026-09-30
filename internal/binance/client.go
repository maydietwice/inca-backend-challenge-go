// Package binance is a minimal client for the Binance Spot REST API.
package binance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// dryRunSymbols is how many symbols GetSymbols returns in dry-run mode.
	dryRunSymbols = 10
	// usedWeightHeader holds the request weight used by this IP in the current minute.
	usedWeightHeader = "X-Mbx-Used-Weight-1m"
)

// Options configures a Client. Defaults are defined in the config package.
type Options struct {
	BaseURL             string        // REST API base URL, e.g. https://api.binance.com/api/v3
	OrderBookDepth      int           // limit parameter of GET /depth
	InfoEndpointTimeout time.Duration // timeout of GET /exchangeInfo calls
	MaxResponseSize     int64         // larger responses are rejected
	DryRun              bool          // process only the first 10 symbols
}

// Client is a service for interaction with the Binance REST API.
// It is safe for concurrent use.
type Client struct {
	http *http.Client
	opts Options
}

// NewClient returns a Client that sends requests with httpClient.
func NewClient(httpClient *http.Client, opts Options) *Client {
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")
	return &Client{http: httpClient, opts: opts}
}

// NewHTTPClient returns an HTTP client with the given timeouts. The proxy is
// taken from the HTTPS_PROXY, HTTP_PROXY and NO_PROXY environment variables;
// http://, https:// and socks5:// proxy URLs are supported.
func NewHTTPClient(connectTimeout, readTimeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.DialContext = (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = readTimeout
	return &http.Client{Transport: transport}
}

// GetSymbols retrieves the symbols dictionary. A symbol is an exchange pair,
// e.g. ETHBTC. In dry-run mode only the first 10 symbols are returned.
func (c *Client) GetSymbols(ctx context.Context) ([]Symbol, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.InfoEndpointTimeout)
	defer cancel()

	var info ExchangeInfo
	if _, err := c.get(ctx, "/exchangeInfo", nil, &info); err != nil {
		return nil, fmt.Errorf("get symbols: %w", err)
	}
	if c.opts.DryRun && len(info.Symbols) > dryRunSymbols {
		return info.Symbols[:dryRunSymbols], nil
	}
	return info.Symbols, nil
}

// GetRateLimits retrieves the actual API rate limits, which can help to avoid
// exceeding the allowed request weight.
func (c *Client) GetRateLimits(ctx context.Context) (RateLimits, error) {
	ctx, cancel := context.WithTimeout(ctx, c.opts.InfoEndpointTimeout)
	defer cancel()

	var info ExchangeInfo
	header, err := c.get(ctx, "/exchangeInfo", nil, &info)
	if err != nil {
		return RateLimits{}, fmt.Errorf("get rate limits: %w", err)
	}
	raw := header.Get(usedWeightHeader)
	if raw == "" {
		return RateLimits{}, fmt.Errorf("get rate limits: %s header not found", usedWeightHeader)
	}
	used, err := strconv.Atoi(raw)
	if err != nil {
		return RateLimits{}, fmt.Errorf("get rate limits: parse %s header: %w", usedWeightHeader, err)
	}
	for _, limit := range info.RateLimits {
		if limit.RateLimitType == "REQUEST_WEIGHT" {
			return RateLimits{UsedWeight: used, TotalWeight: limit.Limit}, nil
		}
	}
	return RateLimits{}, errors.New("get rate limits: REQUEST_WEIGHT limit not found")
}

// GetOrderBook retrieves the order book of the symbol, limited to the
// configured depth.
func (c *Client) GetOrderBook(ctx context.Context, symbol Symbol) (OrderBook, error) {
	query := url.Values{}
	query.Set("symbol", symbol.Symbol)
	query.Set("limit", strconv.Itoa(c.opts.OrderBookDepth))

	var orderBook OrderBook
	if _, err := c.get(ctx, "/depth", query, &orderBook); err != nil {
		return OrderBook{}, fmt.Errorf("get order book %s: %w", symbol.Symbol, err)
	}
	return orderBook, nil
}

// APIError is returned when Binance responds with a non-2xx status code.
type APIError struct {
	StatusCode int         `json:"-"`
	Header     http.Header `json:"-"`
	Code       int         `json:"code"` // Binance error code, e.g. -1003
	Message    string      `json:"msg"`
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("binance: HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("binance: HTTP %d: %s (code %d)", e.StatusCode, e.Message, e.Code)
}

// get sends GET {BaseURL}{path}?{query} and decodes the JSON response into dst.
func (c *Client) get(ctx context.Context, path string, query url.Values, dst any) (http.Header, error) {
	endpoint := c.opts.BaseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.opts.MaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > c.opts.MaxResponseSize {
		return nil, fmt.Errorf("response is larger than %d bytes", c.opts.MaxResponseSize)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{StatusCode: resp.StatusCode, Header: resp.Header}
		_ = json.Unmarshal(body, apiErr) // error bodies look like {"code":-1003,"msg":"..."}
		return nil, apiErr
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.Header, nil
}
