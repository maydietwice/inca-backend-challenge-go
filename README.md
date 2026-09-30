# Binance order book metrics — Go port

A Go port of the [Inca Digital backend challenge](https://github.com/1712n/backend-challenge),
which was archived in March 2023. The original is a Kotlin / Spring WebFlux application;
this repository reproduces the same baseline in Go (standard library only), so the
challenge can be solved in Go.

## What the application does

`OrderBookMetricsCalculator` in `internal/calculator` runs the following job:

1. retrieves all symbols and the current API rate limits from Binance;
2. retrieves the order book of every symbol, one request at a time;
3. calculates the average volume of asks and bids across all order books;
4. logs the metrics and the elapsed time, then exits.

The job works, but it doesn't scale: a full run doesn't fit into the 20-minute job timeout.

## The problem

1. **Speed.** Requesting order books one by one is too slow. Make the requests
   concurrent without exceeding the Binance API rate limits: every endpoint has a
   request weight (see the [Binance Spot API docs](https://github.com/binance/binance-spot-api-docs)),
   and `binance.Client.GetRateLimits` reports the weight already used. Other
   applications may share the same IP, so the used weight can grow independently.
2. **Memory.** The job keeps every order book in memory before computing the
   averages, so it can't evolve into a streaming application. Reduce memory
   consumption.

As in the original challenge, keep the changes focused on `internal/calculator`.

## Project layout

| Path | Description |
| --- | --- |
| `cmd/challenge` | entry point |
| `internal/calculator` | the metrics job, the part to improve |
| `internal/binance` | Binance REST API client and response types |
| `internal/config` | settings; defaults mirror the original `application.yml` |

## Running

Requires Go 1.22 or newer.

```sh
go run ./cmd/challenge -dry-run   # first 10 symbols only
go run ./cmd/challenge            # all symbols
```

| Flag | Environment variable | Default |
| --- | --- | --- |
| `-dry-run` | `BINANCE_DRY_RUN` | `false` |
| `-depth` | `BINANCE_ORDER_BOOK_DEPTH` | `1000` |
| `-base-url` | `BINANCE_BASE_URL` | `https://api.binance.com/api/v3` |

Binance rejects requests from some regions, including the US, with HTTP 451.
Set a proxy via `HTTPS_PROXY`, e.g. `HTTPS_PROXY=socks5://host:port`
(`http://` and `https://` proxies work too).

## Tests

```sh
# unit tests, no network access needed
go test ./...

# the job against the real Binance API: 10 symbols, then the full run
BINANCE_INTEGRATION=1 BINANCE_DRY_RUN=true go test ./internal/calculator -run TestRunAgainstBinance -v -count=1
BINANCE_INTEGRATION=1 go test ./internal/calculator -run TestRunAgainstBinance -v -count=1 -timeout 30m
```

The GitHub Actions workflow in `.github/workflows/run-tests.yml` runs the unit tests and
the full run against Binance for every pull request into `main`. Store the proxy URL in
the `PROXY_URL` repository secret.
