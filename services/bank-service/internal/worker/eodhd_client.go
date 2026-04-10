package worker

// eodhd_client.go — HTTP klijent za EODHD Historical Data API.
//
// Dokumentacija: https://eodhd.com/financial-apis/
//
// Korišćeni endpoint-i:
//   GET /api/real-time/{SYMBOL}?api_token=KEY&fmt=json   — live/delayed quote
//   GET /api/eod/{SYMBOL}?from=…&to=…&period=d&order=a&fmt=json&api_token=KEY — EOD istorija
//
// Format simbola:
//   Stocks  → TICKER.US          (npr. "AAPL.US")
//   Forex   → BASEQUOTE.FOREX    (npr. "EURUSD.FOREX"; konvertovano iz "EUR/USD")
//   Futures → PREFIX.COMM        (npr. "CL.COMM"; prefiks izvučen iz tickera "CLJ22")

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const eodhdBaseURL = "https://eodhd.com/api"

type eodhdClient struct {
	apiKey     string
	httpClient *http.Client
}

func newEODHDClient(apiKey string) *eodhdClient {
	return &eodhdClient{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 12 * time.Second},
	}
}

// ─── Real-time quote ─────────────────────────────────────────────────────────

// eodhdQuote mapira odgovor /api/real-time/{SYMBOL} endpoint-a.
type eodhdQuote struct {
	Code          string  `json:"code"`
	Timestamp     int64   `json:"timestamp"`
	Open          float64 `json:"open"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Close         float64 `json:"close"`
	Volume        int64   `json:"volume"`
	Bid           float64 `json:"bid"`
	Ask           float64 `json:"ask"`
	PreviousClose float64 `json:"previous_close"`
	Change        float64 `json:"change"`
	ChangeP       float64 `json:"change_p"`
}

// RealTimeQuote dohvata live/delayed cenu za symbol.
// Symbol formati: "AAPL.US", "EURUSD.FOREX", "CL.COMM".
func (c *eodhdClient) RealTimeQuote(ctx context.Context, symbol string) (*eodhdQuote, error) {
	u := fmt.Sprintf("%s/real-time/%s?api_token=%s&fmt=json", eodhdBaseURL, symbol, c.apiKey)
	return eodhdGetJSON[eodhdQuote](ctx, c.httpClient, u)
}

// ─── EOD history ─────────────────────────────────────────────────────────────

// eodhdBar mapira jedan dnevni OHLCV red iz /api/eod/{SYMBOL} odgovora.
type eodhdBar struct {
	Date          string  `json:"date"`
	Open          float64 `json:"open"`
	High          float64 `json:"high"`
	Low           float64 `json:"low"`
	Close         float64 `json:"close"`
	AdjustedClose float64 `json:"adjusted_close"`
	Volume        float64 `json:"volume"`
}

// EODHistory dohvata dnevne OHLCV sveće za symbol u datom periodu.
func (c *eodhdClient) EODHistory(ctx context.Context, symbol string, from, to time.Time) ([]eodhdBar, error) {
	u := fmt.Sprintf(
		"%s/eod/%s?from=%s&to=%s&period=d&order=a&fmt=json&api_token=%s",
		eodhdBaseURL,
		symbol,
		from.Format("2006-01-02"),
		to.Format("2006-01-02"),
		c.apiKey,
	)
	return eodhdGetJSONSlice[eodhdBar](ctx, c.httpClient, u)
}

// ─── Symbol helpers ───────────────────────────────────────────────────────────

// eodhdStockSymbol pretvara kratak ticker u EODHD US stock format: "AAPL" → "AAPL.US".
func eodhdStockSymbol(ticker string) string {
	if strings.Contains(ticker, ".") {
		return ticker // već ima exchange sufiks
	}
	return ticker + ".US"
}

// eodhdForexSymbol pretvara "EUR/USD" → "EURUSD.FOREX".
func eodhdForexSymbol(ticker string) string {
	return strings.ReplaceAll(ticker, "/", "") + ".FOREX"
}

// eodhdCommoditySymbol pretvara prefiks futures tickera u EODHD commodity format:
// "CL" → "CL.COMM", "GC" → "GC.COMM".
func eodhdCommoditySymbol(prefix string) string {
	return prefix + ".COMM"
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func eodhdGetJSON[T any](ctx context.Context, client *http.Client, rawURL string) (*T, error) {
	body, err := eodhdFetch(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}
	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("eodhd unmarshal: %w", err)
	}
	return &result, nil
}

func eodhdGetJSONSlice[T any](ctx context.Context, client *http.Client, rawURL string) ([]T, error) {
	body, err := eodhdFetch(ctx, client, rawURL)
	if err != nil {
		return nil, err
	}
	var result []T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("eodhd unmarshal slice: %w", err)
	}
	return result, nil
}

func eodhdFetch(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("eodhd request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("eodhd http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("eodhd read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("eodhd HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}
