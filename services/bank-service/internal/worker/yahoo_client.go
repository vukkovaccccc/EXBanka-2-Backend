package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const yahooOptionsURLFmt = "https://query1.finance.yahoo.com/v6/finance/options/%s"

// ─── Response structs ────────────────────────────────────────────────────────

type yahooOptionsResp struct {
	OptionChain yahooOptionChain `json:"optionChain"`
}

type yahooOptionChain struct {
	Result []yahooOptionResult `json:"result"`
}

type yahooOptionResult struct {
	UnderlyingSymbol string              `json:"underlyingSymbol"`
	Quote            yahooQuote          `json:"quote"`
	Options          []yahooOptionExpiry `json:"options"`
}

type yahooQuote struct {
	RegularMarketPrice float64 `json:"regularMarketPrice"`
}

type yahooOptionExpiry struct {
	Calls []yahooContract `json:"calls"`
	Puts  []yahooContract `json:"puts"`
}

type yahooContract struct {
	ContractSymbol    string  `json:"contractSymbol"`
	Strike            float64 `json:"strike"`
	LastPrice         float64 `json:"lastPrice"`
	Bid               float64 `json:"bid"`
	Ask               float64 `json:"ask"`
	Volume            int64   `json:"volume"`
	OpenInterest      int64   `json:"openInterest"`
	ImpliedVolatility float64 `json:"impliedVolatility"`
}

// ─── Client function ─────────────────────────────────────────────────────────

// fetchYahooOptions dohvata opcijski lanac za dati underlying ticker sa Yahoo Finance.
// Yahoo Finance ne zahteva API ključ; koristimo browser User-Agent da izbegnemo blokadu.
// Pravi do 2 pokušaja sa pauzom jer Yahoo sporadično vraća 500 na prvom zahtevu.
func fetchYahooOptions(ctx context.Context, client *http.Client, underlyingSymbol string) (*yahooOptionsResp, error) {
	url := fmt.Sprintf(yahooOptionsURLFmt, underlyingSymbol)

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		req, err := newYahooRequest(ctx, url)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("yahoo options %s: http: %w", underlyingSymbol, err)
			time.Sleep(2 * time.Second)
			continue
		}

		if resp.StatusCode == 200 {
			result, err := decodeJSON[yahooOptionsResp](resp.Body)
			resp.Body.Close()
			if err != nil {
				return nil, fmt.Errorf("yahoo options %s: decode: %w", underlyingSymbol, err)
			}
			return result, nil
		}

		// Potrošite body i zatvorite konekciju pre retry-a
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
		lastErr = fmt.Errorf("yahoo options %s: HTTP %d", underlyingSymbol, resp.StatusCode)

		// Ne pokušavaj ponovo za 4xx (osim 429) — Yahoo nam ne želi dati te podatke
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
			break
		}
		time.Sleep(3 * time.Second)
	}
	return nil, lastErr
}

// newYahooRequest kreira GET zahtev sa realističnim browser User-Agent i Accept headerima
// da bi se izbegla bot-detekcija Yahoo Finance API-ja.
func newYahooRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("yahoo: create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://finance.yahoo.com/")
	return req, nil
}

// decodeJSON čita i parsira JSON iz io.Reader.
func decodeJSON[T any](r io.Reader) (*T, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return &result, nil
}
