// Package calculator contains the order book metrics job.
//
// As in the original challenge, changes are expected in this package only.
package calculator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"inca-backend-challenge/internal/binance"
)

// jobTimeout limits the whole job, as block(Duration.ofMinutes(20)) does in
// the original application.
const jobTimeout = 20 * time.Minute

// budgetShare is the share of the Binance request weight limit the job may use;
// the rest is left for exchangeInfo calls and other clients on the same IP.
const budgetShare = 0.8

// workerLimit is the number of concurrent order book requests. The pace is set
// by the rate limiter; workers only have to keep up with it when responses are slow.
const workerLimit = 5

// statusTrading is a status for symbols that are currently trading.
const statusTrading = "TRADING"

// maxAPIRequestAttempts is the maximum number of requests to Binance API
// when it answers 429, counting the first try.
const maxAPIRequestAttempts = 5

// defaultAPICooldown is the pause after a 429 response when the Retry-After
// header is missing or invalid.
const defaultAPICooldown = 5 * time.Second

// API is the subset of the Binance client the calculator depends on.
type API interface {
	GetSymbols(ctx context.Context) ([]binance.Symbol, error)
	GetRateLimits(ctx context.Context) (binance.RateLimits, error)
	GetOrderBook(ctx context.Context, symbol binance.Symbol) (binance.OrderBook, error)
}

// cooldown is a pause shared by all workers: after a 429 response, nobody
// sends requests to the Binance API until it is over, to avoid an IP ban.
type cooldown struct {
	resumeAt time.Time
	mu       sync.Mutex
}

// pauseFor extends the pause to last at least d from now.
// It never shortens a pause that already ends later.
func (c *cooldown) pauseFor(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	unlockTime := time.Now().Add(d)
	if c.resumeAt.Before(unlockTime) {
		c.resumeAt = unlockTime
	}
}

// waitIfPaused waits until resumeAt and returns nil.
// It returns ctx.Err() if ctx is done first.
func (c *cooldown) waitIfPaused(ctx context.Context) error {
	for {
		c.mu.Lock()
		d := time.Until(c.resumeAt)
		c.mu.Unlock()

		if d <= 0 {
			return nil
		}

		timer := time.NewTimer(d)

		select {
		case <-timer.C:
			// loop to check whether the cooldown was extended while we waited.
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// VolumeMetrics holds the average order volume across all order books.
type VolumeMetrics struct {
	AsksAvg float64
	BidsAvg float64
}

// volumeStats accumulates the sum and count of order volumes.
type volumeStats struct {
	sum   float64
	count int
}

func (s *volumeStats) add(orders []binance.Order) {
	for _, order := range orders {
		s.sum += order.Volume
	}
	s.count += len(orders)
}

func (s *volumeStats) average() (float64, error) {
	if s.count == 0 {
		return 0, errNoOrders
	}

	if s.count < 0 {
		return 0, fmt.Errorf("volumeStats: unexpected count of orders %d", s.count)
	}

	return s.sum / float64(s.count), nil
}

// merge adds the sum and count of other to s.
func (s *volumeStats) merge(other volumeStats) {
	s.count += other.count
	s.sum += other.sum
}

// OrderBookMetricsCalculator requests order books for currently trading symbols and
// calculates the average volumes for asks and bids.
type OrderBookMetricsCalculator struct {
	api   API
	log   *slog.Logger
	depth int
}

// New returns a calculator that uses api for requests and log for progress.
func New(api API, log *slog.Logger, depth int) *OrderBookMetricsCalculator {
	return &OrderBookMetricsCalculator{api: api, log: log, depth: depth}
}

// filterTradingSymbols returns only the symbols with the TRADING status.
func filterTradingSymbols(s []binance.Symbol) []binance.Symbol {
	filteredSymbols := make([]binance.Symbol, 0, len(s))

	for _, symbol := range s {
		if symbol.Status == statusTrading {
			filteredSymbols = append(filteredSymbols, symbol)
		}
	}

	return filteredSymbols
}

// orderBookWeight returns the request weight of GET /depth for the given limit,
// as listed in the Binance Spot API docs.
func orderBookWeight(limit int) int {
	switch {
	case limit <= 100:
		return 5
	case limit <= 500:
		return 25
	case limit <= 1000:
		return 50
	default:
		return 250
	}
}

// Run executes the job:
//  1. retrieve all symbols and rate limits from the Binance API;
//  2. retrieve the order books for trading symbols concurrently;
//  3. calculate the average volume for asks and bids;
//  4. log the metrics and statistics.
func (c *OrderBookMetricsCalculator) Run(ctx context.Context) (VolumeMetrics, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	symbols, err := c.api.GetSymbols(ctx)
	if err != nil {
		return VolumeMetrics{}, err
	}
	c.log.Info("retrieved symbols", "count", len(symbols))

	symbols = filterTradingSymbols(symbols)
	c.log.Info("trading symbols", "count", len(symbols))

	rateLimits, err := c.api.GetRateLimits(ctx)
	if err != nil {
		return VolumeMetrics{}, err
	}
	c.log.Info("retrieved rate limits", "usedWeight", rateLimits.UsedWeight, "totalWeight", rateLimits.TotalWeight)

	var asks, bids volumeStats
	var mu sync.Mutex
	var orderBooksCount int

	budget := float64(rateLimits.TotalWeight) * budgetShare
	weight := orderBookWeight(c.depth)
	r := rate.Limit(budget / time.Minute.Seconds())
	limiter := rate.NewLimiter(r, weight)
	c.log.Info("rate limiter", "weight", weight, "requestsPerMinute", budget/float64(weight))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(workerLimit)

	var cd cooldown

	c.log.Info("retrieving order books...")

	for _, symbol := range symbols {
		if ctx.Err() != nil {
			break
		}
		g.Go(func() error {
			orderBook, err := c.fetchOrderBook(ctx, symbol, &cd, limiter, weight)
			if err != nil {
				return err
			}

			var localAsks, localBids volumeStats

			localAsks.add(orderBook.Asks)
			localBids.add(orderBook.Bids)

			mu.Lock()
			asks.merge(localAsks)
			bids.merge(localBids)
			orderBooksCount++
			c.log.Info("retrieved order book", "symbol", symbol.Symbol, "n", orderBooksCount, "total", len(symbols))
			mu.Unlock()

			return nil
		})
	}
	if err = g.Wait(); err != nil {
		return VolumeMetrics{}, err
	}

	c.log.Info("successfully retrieved", "orderBooks", orderBooksCount)

	asksAvg, err := asks.average()
	if err != nil {
		return VolumeMetrics{}, fmt.Errorf("asks: %w", err)
	}
	bidsAvg, err := bids.average()
	if err != nil {
		return VolumeMetrics{}, fmt.Errorf("bids: %w", err)
	}
	metrics := VolumeMetrics{AsksAvg: asksAvg, BidsAvg: bidsAvg}

	c.log.Info("volume metrics", "asksAvg", metrics.AsksAvg, "bidsAvg", metrics.BidsAvg)
	c.log.Info("volume metrics done", "took", time.Since(start).Round(time.Millisecond))
	return metrics, nil
}

// fetchOrderBook requests the order book for symbol, waiting for the rate
// limiter and the shared cooldown before each try. On a 429 response it pauses
// all workers and retries, up to maxAPIRequestAttempts in total; any other
// error is returned at once.
func (c *OrderBookMetricsCalculator) fetchOrderBook(ctx context.Context, symbol binance.Symbol, cd *cooldown, limiter *rate.Limiter, weight int) (binance.OrderBook, error) {
	var err error
	var orderBook binance.OrderBook
	for attempt := 0; attempt < maxAPIRequestAttempts; attempt++ {
		if err = limiter.WaitN(ctx, weight); err != nil {
			return binance.OrderBook{}, fmt.Errorf("rate limit wait for %s: %w", symbol.Symbol, err)
		}

		err = cd.waitIfPaused(ctx)
		if err != nil {
			return binance.OrderBook{}, fmt.Errorf("wait for 429 cooldown before %s: %w", symbol.Symbol, err)
		}

		orderBook, err = c.api.GetOrderBook(ctx, symbol)
		if err == nil {
			return orderBook, nil
		}
		var apiErr *binance.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusTooManyRequests {
			c.log.Warn("Binance API request 429 responded", "symbol", symbol.Symbol)
			retryAfter := c.retryAfterDuration(apiErr.Header, symbol.Symbol)

			cd.pauseFor(retryAfter)
			continue
		}
		return binance.OrderBook{}, fmt.Errorf("get orderbook for %s: %w", symbol.Symbol, err)
	}
	return binance.OrderBook{}, fmt.Errorf("get orderbook retries limit exhausted %s, total attempts %d: %w", symbol.Symbol, maxAPIRequestAttempts, err)
}

// retryAfterDuration returns the pause from the Retry-After header of a 429
// response, or defaultAPICooldown if the header is missing or invalid.
func (c *OrderBookMetricsCalculator) retryAfterDuration(h http.Header, symbol string) time.Duration {
	val := h.Get("Retry-After")
	if val == "" {
		c.log.Warn("empty Retry-After header for 429 response", "symbol", symbol)
		return defaultAPICooldown
	}

	retryAfter, err := strconv.Atoi(val)
	if err != nil {
		c.log.Error("Retry-After header conversion error", "val", val, "symbol", symbol)
		return defaultAPICooldown
	}
	if retryAfter < 1 {
		c.log.Warn("Retry-After header unexpected value", "retryAfter", retryAfter, "symbol", symbol)
		return defaultAPICooldown
	}

	return time.Duration(retryAfter) * time.Second
}

// errNoOrders is returned when there is nothing to average.
var errNoOrders = errors.New("no orders to average")
