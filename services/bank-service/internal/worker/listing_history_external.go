package worker

import (
	"context"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

// NormalizeHistoryRange usklađuje from/to na ceo dan UTC; gornja granica ne prelazi sada.
func NormalizeHistoryRange(from, to time.Time) (time.Time, time.Time) {
	loc := time.UTC
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, loc)
	now := time.Now().UTC()
	toEnd := time.Date(to.Year(), to.Month(), to.Day(), 23, 59, 59, 999999999, loc)
	if toEnd.After(now) {
		toEnd = now
	}
	if from.After(toEnd) {
		from = toEnd.Add(-24 * time.Hour)
	}
	return from, toEnd
}

// listingYahooChartSymbol mapira hartiju na Yahoo Finance simbol za Chart API.
func listingYahooChartSymbol(l domain.Listing) string {
	switch l.ListingType {
	case domain.ListingTypeStock:
		return strings.TrimSpace(l.Ticker)
	case domain.ListingTypeForex:
		return strings.ReplaceAll(strings.TrimSpace(l.Ticker), "/", "") + "=X"
	case domain.ListingTypeFuture:
		prefix, _, _ := parseFutureTicker(l.Ticker)
		if prefix == "" {
			return ""
		}
		return yahooContinuousFutureSymbol(prefix)
	case domain.ListingTypeOption:
		return strings.TrimSpace(l.Ticker)
	default:
		return ""
	}
}

// yahooContinuousFutureSymbol: Yahoo koristi npr. CL=F, ES=F za kontinuirane futures.
func yahooContinuousFutureSymbol(prefix string) string {
	if s, ok := map[string]string{
		"CL": "CL=F", "GC": "GC=F", "SI": "SI=F", "NG": "NG=F", "HG": "HG=F",
		"ES": "ES=F", "NQ": "NQ=F", "YM": "YM=F", "ZC": "ZC=F", "ZW": "ZW=F", "ZS": "ZS=F",
		"RB": "RB=F", "HO": "HO=F",
	}[prefix]; ok {
		return s
	}
	return prefix + "=F"
}

func listingToEODHDSymbol(l domain.Listing) string {
	switch l.ListingType {
	case domain.ListingTypeStock:
		return eodhdStockSymbol(l.Ticker)
	case domain.ListingTypeForex:
		return eodhdForexSymbol(l.Ticker)
	case domain.ListingTypeFuture:
		prefix, _, _ := parseFutureTicker(l.Ticker)
		if prefix == "" {
			return ""
		}
		return eodhdCommoditySymbol(prefix)
	default:
		return ""
	}
}

// FetchListingHistoryFromMarkets pokušava Yahoo Finance (granularnost po opsegu), zatim EODHD dnevne sveće.
// Vraća (podaci, true) ako je bar jedan izvor uspeo; inače (nil, false).
func FetchListingHistoryFromMarkets(
	ctx context.Context,
	httpClient *http.Client,
	eodhdAPIKey string,
	listing domain.Listing,
	from, to time.Time,
) ([]domain.ListingDailyPriceInfo, bool) {
	from, to = NormalizeHistoryRange(from, to)

	sym := listingYahooChartSymbol(listing)
	if sym != "" {
		bars, err := fetchYahooChartBars(ctx, httpClient, sym, from, to)
		if err == nil && len(bars) > 0 {
			return yahooBarsToDomain(listing.ID, bars), true
		}
		if err != nil {
			log.Printf("[listing-history] Yahoo %s (%s): %v", sym, listing.Ticker, err)
		}
		if listing.ListingType == domain.ListingTypeOption {
			u := extractOptionUnderlying(listing.Ticker)
			if u != "" && !strings.EqualFold(u, sym) {
				b2, err2 := fetchYahooChartBars(ctx, httpClient, u, from, to)
				if err2 == nil && len(b2) > 0 {
					log.Printf("[listing-history] opcija %s: korišćen underlying %s za graf", listing.Ticker, u)
					return yahooBarsToDomain(listing.ID, b2), true
				}
				if err2 != nil {
					log.Printf("[listing-history] Yahoo underlying %s: %v", u, err2)
				}
			}
		}
	}

	if eodhdAPIKey != "" {
		eodSym := listingToEODHDSymbol(listing)
		if eodSym != "" {
			eod := newEODHDClient(eodhdAPIKey)
			bars, err := eod.EODHistory(ctx, eodSym, from, to)
			if err == nil && len(bars) > 0 {
				return eodBarsToDomain(listing.ID, bars), true
			}
			if err != nil {
				log.Printf("[listing-history] EODHD %s: %v", eodSym, err)
			}
		}
	}

	return nil, false
}

func yahooBarsToDomain(listingID int64, bars []yahooChartBar) []domain.ListingDailyPriceInfo {
	out := make([]domain.ListingDailyPriceInfo, len(bars))
	var prevClose float64
	for i, b := range bars {
		t := time.Unix(b.TimestampUnix, 0).UTC()
		chg := 0.0
		if i > 0 {
			chg = b.Close - prevClose
		} else if !math.IsNaN(b.Open) && b.Open > 0 {
			chg = b.Close - b.Open
		}
		prevClose = b.Close
		out[i] = domain.ListingDailyPriceInfo{
			ListingID:   listingID,
			Date:        t,
			Price:       b.Close,
			AskHigh:     b.High,
			BidLow:      b.Low,
			PriceChange: chg,
			Volume:      b.Volume,
		}
	}
	return out
}

func eodBarsToDomain(listingID int64, bars []eodhdBar) []domain.ListingDailyPriceInfo {
	out := make([]domain.ListingDailyPriceInfo, 0, len(bars))
	var prevClose float64
	var hasPrev bool
	for _, bar := range bars {
		t, err := time.Parse("2006-01-02", bar.Date)
		if err != nil {
			continue
		}
		t = t.UTC()
		chg := 0.0
		if hasPrev {
			chg = bar.Close - prevClose
		} else {
			chg = bar.Close - bar.Open
		}
		prevClose = bar.Close
		hasPrev = true
		out = append(out, domain.ListingDailyPriceInfo{
			ListingID:   listingID,
			Date:        t,
			Price:       bar.Close,
			AskHigh:     bar.High,
			BidLow:      bar.Low,
			PriceChange: chg,
			Volume:      int64(bar.Volume),
		})
	}
	return out
}
