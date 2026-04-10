package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

const yahooChartURLFmt = "https://query1.finance.yahoo.com/v8/finance/chart/%s"

// yahooChartBar je jedna sveća iz Yahoo Finance Chart API-ja (v8).
type yahooChartBar struct {
	TimestampUnix int64
	Open          float64
	High          float64
	Low           float64
	Close         float64
	Volume        int64
}

// yahooChartEnvelope mapira minimalan deo JSON odgovora /v8/finance/chart.
type yahooChartEnvelope struct {
	Chart struct {
		Error *struct {
			Description string `json:"description"`
		} `json:"error"`
		Result []struct {
			Timestamp []int64 `json:"timestamp"`
			Indicators  struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

// yahooIntervalForRange bira granularnost: kraći period → finija sveća.
func yahooIntervalForRange(from, to time.Time) string {
	d := to.Sub(from).Hours() / 24
	switch {
	case d <= 1.5:
		return "5m"
	case d <= 7:
		return "1h"
	case d <= 730:
		return "1d"
	default:
		return "1wk"
	}
}

// fetchYahooChartBars dohvata istorijske sveće za Yahoo simbol u [from, to] (UTC).
func fetchYahooChartBars(ctx context.Context, client *http.Client, yahooSymbol string, from, to time.Time) ([]yahooChartBar, error) {
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	if yahooSymbol == "" {
		return nil, fmt.Errorf("yahoo: prazan simbol")
	}

	interval := yahooIntervalForRange(from, to)
	period1 := from.Unix()
	period2 := to.Unix()
	if period2 <= period1 {
		return nil, fmt.Errorf("yahoo: neispravan opseg")
	}

	pathSym := url.PathEscape(yahooSymbol)
	rawURL := fmt.Sprintf("%s?period1=%d&period2=%d&interval=%s",
		fmt.Sprintf(yahooChartURLFmt, pathSym), period1, period2, interval)

	req, err := newYahooRequest(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("yahoo chart %s: http: %w", yahooSymbol, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("yahoo chart %s: read: %w", yahooSymbol, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo chart %s: HTTP %d: %s", yahooSymbol, resp.StatusCode, string(body))
	}

	env, err := decodeJSON[yahooChartEnvelope](bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("yahoo chart %s: decode: %w", yahooSymbol, err)
	}
	if env.Chart.Error != nil && env.Chart.Error.Description != "" {
		return nil, fmt.Errorf("yahoo chart %s: %s", yahooSymbol, env.Chart.Error.Description)
	}
	if len(env.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo chart %s: prazan rezultat", yahooSymbol)
	}

	r := env.Chart.Result[0]
	if len(r.Timestamp) == 0 {
		return nil, fmt.Errorf("yahoo chart %s: nema tačaka", yahooSymbol)
	}
	if len(r.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo chart %s: nema quote", yahooSymbol)
	}

	q := r.Indicators.Quote[0]
	n := len(r.Timestamp)
	out := make([]yahooChartBar, 0, n)
	for i := 0; i < n; i++ {
		closeVal := nanIfNil(q.Close, i)
		if math.IsNaN(closeVal) || closeVal <= 0 {
			continue
		}
		ts := r.Timestamp[i]
		out = append(out, yahooChartBar{
			TimestampUnix: ts,
			Open:          nanIfNil(q.Open, i),
			High:          firstPositive(nanIfNil(q.High, i), closeVal),
			Low:           firstPositive(nanIfNil(q.Low, i), closeVal),
			Close:         closeVal,
			Volume:        int64PtrAt(q.Volume, i),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("yahoo chart %s: sve tačke su prazne", yahooSymbol)
	}
	return out, nil
}

func nanIfNil(arr []*float64, i int) float64 {
	if i >= len(arr) || arr[i] == nil {
		return math.NaN()
	}
	return *arr[i]
}

func int64PtrAt(arr []*int64, i int) int64 {
	if i >= len(arr) || arr[i] == nil {
		return 0
	}
	return *arr[i]
}

func firstPositive(v, fallback float64) float64 {
	if math.IsNaN(v) || v <= 0 {
		return fallback
	}
	return v
}
