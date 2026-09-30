// Package calculator contains the order book metrics job.
//
// As in the original challenge, changes are expected in this package only.
package calculator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"inca-backend-challenge/internal/binance"
)

// jobTimeout limits the whole job, as block(Duration.ofMinutes(20)) does in
// the original application.
const jobTimeout = 20 * time.Minute

// API is the subset of the Binance client the calculator depends on.
type API interface {
	GetSymbols(ctx context.Context) ([]binance.Symbol, error)
	GetRateLimits(ctx context.Context) (binance.RateLimits, error)
	GetOrderBook(ctx context.Context, symbol binance.Symbol) (binance.OrderBook, error)
}

// VolumeMetrics holds the average order volume across all order books.
type VolumeMetrics struct {
	AsksAvg float64
	BidsAvg float64
}

// OrderBookMetricsCalculator requests order books for all symbols and
// calculates the average volumes for asks and bids.
type OrderBookMetricsCalculator struct {
	api API
	log *slog.Logger
}

// New returns a calculator that uses api for requests and log for progress.
func New(api API, log *slog.Logger) *OrderBookMetricsCalculator {
	return &OrderBookMetricsCalculator{api: api, log: log}
}

// Run executes the job:
//  1. retrieve all symbols and rate limits from the Binance API;
//  2. retrieve the order books for all symbols one by one;
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

	rateLimits, err := c.api.GetRateLimits(ctx)
	if err != nil {
		return VolumeMetrics{}, err
	}
	c.log.Info("retrieved rate limits", "usedWeight", rateLimits.UsedWeight, "totalWeight", rateLimits.TotalWeight)

	c.log.Info("retrieving order books...")
	orderBooks := make([]binance.OrderBook, 0, len(symbols))
	for _, symbol := range symbols {
		orderBook, err := c.api.GetOrderBook(ctx, symbol)
		if err != nil {
			return VolumeMetrics{}, err
		}
		orderBooks = append(orderBooks, orderBook)
		c.log.Info("retrieved order book", "symbol", symbol.Symbol, "n", len(orderBooks), "total", len(symbols))
	}
	c.log.Info("successfully retrieved", "orderBooks", len(orderBooks))

	var asks, bids []binance.Order
	for _, orderBook := range orderBooks {
		asks = append(asks, orderBook.Asks...)
		bids = append(bids, orderBook.Bids...)
	}
	asksAvg, err := averageVolume(asks)
	if err != nil {
		return VolumeMetrics{}, fmt.Errorf("asks: %w", err)
	}
	bidsAvg, err := averageVolume(bids)
	if err != nil {
		return VolumeMetrics{}, fmt.Errorf("bids: %w", err)
	}
	metrics := VolumeMetrics{AsksAvg: asksAvg, BidsAvg: bidsAvg}

	c.log.Info("volume metrics", "asksAvg", metrics.AsksAvg, "bidsAvg", metrics.BidsAvg)
	c.log.Info("volume metrics done", "took", time.Since(start).Round(time.Millisecond))
	return metrics, nil
}

// errNoOrders is returned when there is nothing to average.
var errNoOrders = errors.New("no orders to average")

func averageVolume(orders []binance.Order) (float64, error) {
	if len(orders) == 0 {
		return 0, errNoOrders
	}
	var sum float64
	for _, order := range orders {
		sum += order.Volume
	}
	return sum / float64(len(orders)), nil
}
