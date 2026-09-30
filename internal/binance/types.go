package binance

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ExchangeInfo is the subset of the GET /exchangeInfo response used by the app.
type ExchangeInfo struct {
	RateLimits []RateLimit `json:"rateLimits"`
	Symbols    []Symbol    `json:"symbols"`
}

// RateLimit describes one of the API limits, e.g. REQUEST_WEIGHT per 1 MINUTE.
type RateLimit struct {
	RateLimitType string `json:"rateLimitType"`
	Interval      string `json:"interval"`
	IntervalNum   int    `json:"intervalNum"`
	Limit         int    `json:"limit"`
}

// Symbol is an exchange pair (financial instrument), e.g. ETHBTC.
type Symbol struct {
	Symbol     string `json:"symbol"`
	BaseAsset  string `json:"baseAsset"`
	QuoteAsset string `json:"quoteAsset"`
	Status     string `json:"status"`
}

// OrderBook is the GET /depth response.
type OrderBook struct {
	LastUpdateID int64   `json:"lastUpdateId"`
	Bids         []Order `json:"bids"`
	Asks         []Order `json:"asks"`
}

// Order is a single price level of an order book.
//
// The original Kotlin application keeps prices and volumes as BigDecimal;
// float64 is precise enough for averaged volume metrics.
type Order struct {
	Price  float64
	Volume float64
}

// UnmarshalJSON decodes a price level, which Binance sends as a JSON array
// of two decimal strings: ["price", "quantity"].
func (o *Order) UnmarshalJSON(data []byte) error {
	var level [2]string
	if err := json.Unmarshal(data, &level); err != nil {
		return fmt.Errorf("decode order: %w", err)
	}
	price, err := strconv.ParseFloat(level[0], 64)
	if err != nil {
		return fmt.Errorf("decode order price: %w", err)
	}
	volume, err := strconv.ParseFloat(level[1], 64)
	if err != nil {
		return fmt.Errorf("decode order volume: %w", err)
	}
	o.Price, o.Volume = price, volume
	return nil
}

// RateLimits is the request weight already used by this IP in the current
// minute and the total weight Binance allows per minute.
type RateLimits struct {
	UsedWeight  int
	TotalWeight int
}
