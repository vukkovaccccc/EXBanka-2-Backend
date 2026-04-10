package worker

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"
	"unicode"

	"banka-backend/services/bank-service/internal/domain"
)

// ─── Worker ──────────────────────────────────────────────────────────────────

// ListingRefresherWorker periodično osvežava cene hartija od vrednosti.
//
// Podržani tipovi i hijerarhija izvora:
//   - STOCK:  EODHD real-time (primarni) → Finnhub Quote → (opciono sintetika ako LISTING_REQUIRE_LIVE_QUOTES=false)
//             AlphaVantage Company Overview jednom dnevno (detalji)
//             EODHD EOD istorija za seed → Finnhub Candles (fallback)
//   - FOREX:  EODHD real-time (primarni) → AlphaVantage → Finnhub → (sintetika samo ako requireLive=false)
//   - FUTURE: EODHD real-time commodity (primarni) → (random walk samo ako requireLive=false)
//   - OPTION: Yahoo Finance opcijski lanac (EODHD options su paid addon)
type ListingRefresherWorker struct {
	repo        domain.ListingRepository
	interval    time.Duration
	eodhd       *eodhdClient        // nil ako nema ključa
	finnhub     *finnhubClient      // nil ako nema ključa
	av          *alphaVantageClient // nil ako nema ključa
	httpClient  *http.Client        // deljeni klijent za Yahoo Finance
	avLastDaily map[int64]time.Time // throttle: AV Company Overview jednom dnevno
	// requireLiveQuotes: ako true (podrazumevano), bez mock/sintetike — preskoči ažuriranje ako API nema podatke.
	requireLiveQuotes bool
}

func NewListingRefresherWorker(
	repo domain.ListingRepository,
	interval time.Duration,
	eodhd_api_key string,
	finnhubAPIKey string,
	alphaVantageKey string,
	requireLiveQuotes bool,
) *ListingRefresherWorker {
	var ec *eodhdClient
	if eodhd_api_key != "" {
		ec = newEODHDClient(eodhd_api_key)
		log.Printf("[worker] ListingRefresherWorker: EODHD API konfigurisan (primarni izvor)")
	} else {
		log.Printf("[worker] ListingRefresherWorker: EODHD_API_KEY nije postavljen — koristiće se Finnhub ili preskok (LISTING_REQUIRE_LIVE_QUOTES=true)")
	}

	var fc *finnhubClient
	if finnhubAPIKey != "" {
		fc = newFinnhubClient(finnhubAPIKey)
		log.Printf("[worker] ListingRefresherWorker: Finnhub API konfigurisan (sekundarni izvor)")
	} else {
		log.Printf("[worker] ListingRefresherWorker: FINNHUB_API_KEY nije postavljen")
	}

	var avc *alphaVantageClient
	if alphaVantageKey != "" {
		avc = newAlphaVantageClient(alphaVantageKey)
		log.Printf("[worker] ListingRefresherWorker: AlphaVantage API konfigurisan (Company Overview)")
	} else {
		log.Printf("[worker] ListingRefresherWorker: ALPHAVANTAGE_API_KEY nije postavljen — preskače se AV logika")
	}

	if requireLiveQuotes {
		log.Printf("[worker] ListingRefresherWorker: LISTING_REQUIRE_LIVE_QUOTES — bez sintetičkih cena za sve tipove ako eksterni izvori padnu")
	} else {
		log.Printf("[worker] ListingRefresherWorker: LISTING_REQUIRE_LIVE_QUOTES=false — dozvoljena sintetika/mock pri padu API-ja (samo dev)")
	}
	return &ListingRefresherWorker{
		repo:              repo,
		interval:          interval,
		eodhd:             ec,
		finnhub:           fc,
		av:                avc,
		httpClient:        &http.Client{Timeout: 15 * time.Second},
		avLastDaily:       make(map[int64]time.Time),
		requireLiveQuotes: requireLiveQuotes,
	}
}

// Start pokreće worker petlju. Blokira dok se ctx ne otkaže.
func (w *ListingRefresherWorker) Start(ctx context.Context) {
	log.Printf("[worker] ListingRefresherWorker pokrenut (interval=%s)", w.interval)
	w.run(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.run(ctx)
		case <-ctx.Done():
			log.Printf("[worker] ListingRefresherWorker zaustavljen")
			return
		}
	}
}

// run osvežava sve hartije jednom.
func (w *ListingRefresherWorker) run(ctx context.Context) {
	listings, err := w.repo.ListAll(ctx)
	if err != nil {
		log.Printf("[worker] ListingRefresher: greška pri učitavanju hartija: %v", err)
		return
	}
	if len(listings) == 0 {
		return
	}

	now := time.Now().UTC()
	refreshed := 0

	for _, l := range listings {
		// Validate ticker format before processing
		if !ValidateTickerFormat(string(l.ListingType), l.Ticker) {
			log.Printf("[worker] ListingRefresher: neispravan format tickera %q (tip=%s) — preskačem", l.Ticker, l.ListingType)
			continue
		}

		switch l.ListingType {
		case domain.ListingTypeStock:
			w.refreshStock(ctx, l, now)
		case domain.ListingTypeForex:
			w.refreshForex(ctx, l, now)
		case domain.ListingTypeFuture:
			w.refreshFuture(ctx, l, now)
		case domain.ListingTypeOption:
			w.refreshOption(ctx, l, now)
		default:
			log.Printf("[worker] ListingRefresher: nepoznat tip %q za %s — preskačem", l.ListingType, l.Ticker)
			continue
		}
		refreshed++
		// Globalni rate-limit: max ~3 zahteva/sec, sigurno ispod Finnhub 60/min
		time.Sleep(300 * time.Millisecond)
	}

	log.Printf("[worker] ListingRefresher: osveženo %d/%d hartija", refreshed, len(listings))
}

// ─── STOCK ───────────────────────────────────────────────────────────────────

func (w *ListingRefresherWorker) refreshStock(ctx context.Context, l domain.Listing, now time.Time) {
	// Seedovati istoriju ako je listing nov
	if w.finnhub != nil {
		w.ensureHistory(ctx, l, now)
	}

	price, ask, bid, volume, change := w.fetchStockPrice(ctx, l, now)
	if price < 0 {
		log.Printf("[worker] STOCK %s: preskočeno ažuriranje (nema live podataka, strict ili prazni ključevi)", l.Ticker)
		return
	}

	// Jednom dnevno: dohvati Company Overview sa AlphaVantage
	details := w.maybeUpdateStockDetails(ctx, l, now)

	if err := w.saveWithDetails(ctx, l, price, ask, bid, volume, now, details); err != nil {
		log.Printf("[worker] STOCK %s: greška pri čuvanju: %v", l.Ticker, err)
		return
	}

	daily := domain.ListingDailyPriceInfo{
		ListingID: l.ID, Date: now,
		Price: price, AskHigh: ask, BidLow: bid,
		PriceChange: change, Volume: volume,
	}
	if err := w.repo.AppendDailyPrice(ctx, daily); err != nil {
		log.Printf("[worker] STOCK %s: greška pri upisu dnevne cene: %v", l.Ticker, err)
	}
}

func (w *ListingRefresherWorker) fetchStockPrice(ctx context.Context, l domain.Listing, now time.Time) (price, ask, bid float64, volume int64, change float64) {
	// ── Primarni izvor: EODHD real-time ──────────────────────────────────────
	if w.eodhd != nil {
		q, err := w.eodhd.RealTimeQuote(ctx, eodhdStockSymbol(l.Ticker))
		if err == nil && q.Close > 0 {
			price = q.Close
			change = q.Change
			ask = q.Ask
			bid = q.Bid
			// Ako API ne vrati bid/ask, koristi mali spread
			if ask <= 0 || bid <= 0 {
				spread := price * 0.0005
				ask = price + spread
				bid = price - spread
			}
			if bid < 0 {
				bid = 0
			}
			if q.Volume > 0 {
				volume = q.Volume
			} else {
				volume = l.Volume
			}
			log.Printf("[worker] STOCK %s = $%.4f (Δ%.4f) vol=%d [EODHD]", l.Ticker, price, change, volume)
			return price, ask, bid, volume, change
		}
		log.Printf("[worker] STOCK %s: EODHD greška: %v — pokušavam Finnhub", l.Ticker, err)
	}

	// ── Sekundarni izvor: Finnhub ─────────────────────────────────────────────
	if w.finnhub != nil {
		q, err := w.finnhub.Quote(ctx, l.Ticker)
		if err == nil && q.C > 0 {
			price = q.C
			change = q.D
			spread := price * 0.0005
			ask = price + spread
			bid = price - spread
			if bid < 0 {
				bid = 0
			}
			volume = w.fetchTodayVolume(ctx, l.Ticker, now, l.Volume)
			log.Printf("[worker] STOCK %s = $%.4f (Δ%.2f) vol=%d [Finnhub]", l.Ticker, price, q.Dp, volume)
			return price, ask, bid, volume, change
		}
		log.Printf("[worker] STOCK %s: Finnhub greška: %v", l.Ticker, err)
	}

	if w.requireLiveQuotes {
		log.Printf("[worker] STOCK %s: nema dostupnog live izvora — preskačem ažuriranje (REQUIRE_LIVE)", l.Ticker)
		return -1, 0, 0, 0, 0
	}
	return mockPrice(l)
}

// maybeUpdateStockDetails dohvata Company Overview jednom dnevno i vraća novi JSON (ili "").
func (w *ListingRefresherWorker) maybeUpdateStockDetails(ctx context.Context, l domain.Listing, now time.Time) string {
	if w.av == nil {
		return ""
	}
	last, ok := w.avLastDaily[l.ID]
	if ok && now.Sub(last) < 23*time.Hour {
		return "" // previše rano za novi AV poziv
	}

	overview, err := w.av.CompanyOverview(ctx, l.Ticker)
	time.Sleep(500 * time.Millisecond) // AV rate-limit pauza
	if err != nil {
		log.Printf("[worker] STOCK %s: AV Company Overview greška: %v", l.Ticker, err)
		return ""
	}

	shares, _ := parseAVFloat(overview.SharesOutstanding)
	divYield, _ := parseAVFloat(overview.DividendYield)
	if shares == 0 {
		return ""
	}

	w.avLastDaily[l.ID] = now

	// Spajamo sa postojećim detaljima da ne izgubimo ostala polja
	existing := parseJSONMap(l.DetailsJSON)
	existing["outstanding_shares"] = shares
	existing["dividend_yield"] = divYield

	b, err := json.Marshal(existing)
	if err != nil {
		return ""
	}
	log.Printf("[worker] STOCK %s: AV detalji ažurirani (shares=%.0f, div=%.4f)", l.Ticker, shares, divYield)
	return string(b)
}

// ─── FOREX ───────────────────────────────────────────────────────────────────

func (w *ListingRefresherWorker) refreshForex(ctx context.Context, l domain.Listing, now time.Time) {
	base, quote := parseForexTicker(l.Ticker)
	if base == "" || quote == "" {
		log.Printf("[worker] FOREX %s: neispravan format tickera (očekivano BASE/QUOTE)", l.Ticker)
		return
	}

	price, ask, bid, change := w.fetchForexPrice(ctx, l, base, quote)
	if price <= 0 {
		log.Printf("[worker] FOREX %s: preskočeno ažuriranje (nema live podataka)", l.Ticker)
		return
	}

	// Osigurati da DetailsJSON sadrži valute i contract_size
	details := w.ensureForexDetails(l.DetailsJSON, base, quote)

	if err := w.saveWithDetails(ctx, l, price, ask, bid, l.Volume, now, details); err != nil {
		log.Printf("[worker] FOREX %s: greška pri čuvanju: %v", l.Ticker, err)
		return
	}

	daily := domain.ListingDailyPriceInfo{
		ListingID: l.ID, Date: now,
		Price: price, AskHigh: ask, BidLow: bid,
		PriceChange: change, Volume: l.Volume,
	}
	if err := w.repo.AppendDailyPrice(ctx, daily); err != nil {
		log.Printf("[worker] FOREX %s: greška pri upisu dnevne cene: %v", l.Ticker, err)
	}
}

func (w *ListingRefresherWorker) fetchForexPrice(ctx context.Context, l domain.Listing, base, quote string) (price, ask, bid, change float64) {
	// ── Primarni izvor: EODHD real-time forex ────────────────────────────────
	if w.eodhd != nil {
		q, err := w.eodhd.RealTimeQuote(ctx, eodhdForexSymbol(l.Ticker))
		if err == nil && q.Close > 0 {
			change = q.Close - l.Price
			ask = q.Ask
			bid = q.Bid
			if ask <= 0 || bid <= 0 {
				spread := q.Close * 0.0005
				ask = q.Close + spread
				bid = q.Close - spread
			}
			log.Printf("[worker] FOREX %s = %.5f [EODHD]", l.Ticker, q.Close)
			return q.Close, ask, bid, change
		}
		log.Printf("[worker] FOREX %s: EODHD greška: %v — pokušavam AV", l.Ticker, err)
	}

	// ── Sekundarni: AlphaVantage ─────────────────────────────────────────────
	if w.av != nil {
		rate, err := w.av.ForexRate(ctx, base, quote)
		time.Sleep(500 * time.Millisecond)
		if err == nil && rate.ExchangeRate > 0 {
			change = rate.ExchangeRate - l.Price
			log.Printf("[worker] FOREX %s = %.5f [AV]", l.Ticker, rate.ExchangeRate)
			return rate.ExchangeRate, rate.AskPrice, rate.BidPrice, change
		}
		log.Printf("[worker] FOREX %s: AV greška — pokušavam Finnhub", l.Ticker)
	}

	// ── Tercijarni: Finnhub (OANDA format) ───────────────────────────────────
	if w.finnhub != nil {
		symbol := "OANDA:" + base + "_" + quote
		q, err := w.finnhub.Quote(ctx, symbol)
		if err == nil && q.C > 0 {
			spread := q.C * 0.0005
			change = q.D
			log.Printf("[worker] FOREX %s = %.5f [Finnhub]", l.Ticker, q.C)
			return q.C, q.C + spread, q.C - spread, change
		}
		log.Printf("[worker] FOREX %s: Finnhub greška", l.Ticker)
	}

	if w.requireLiveQuotes {
		log.Printf("[worker] FOREX %s: nema live podataka — preskačem ažuriranje (REQUIRE_LIVE)", l.Ticker)
		return 0, 0, 0, 0
	}
	// Sintetički fallback samo kada LISTING_REQUIRE_LIVE_QUOTES=false (lokalni dev)
	base2 := l.Price
	if base2 == 0 {
		base2 = 1.0
	}
	delta := (rand.Float64() - 0.5) * base2 * 0.005 //nolint:gosec
	price = base2 + delta
	spread := price * 0.0005
	return price, price + spread, price - spread, delta
}

// forexLiquidity vraća procenu likvidnosti valutnog para.
// Major pairs (sa USD, EUR, GBP, JPY) → High; ostali → Medium.
func forexLiquidity(base, quote string) string {
	majors := map[string]bool{"USD": true, "EUR": true, "GBP": true, "JPY": true}
	if majors[base] && majors[quote] {
		return "High"
	}
	semi := map[string]bool{"CHF": true, "AUD": true, "CAD": true, "NZD": true}
	if majors[base] || majors[quote] || semi[base] || semi[quote] {
		return "Medium"
	}
	return "Low"
}

func (w *ListingRefresherWorker) ensureForexDetails(detailsJSON, base, quote string) string {
	m := parseJSONMap(detailsJSON)
	changed := false
	if _, ok := m["base_currency"]; !ok {
		m["base_currency"] = base
		changed = true
	}
	if _, ok := m["quote_currency"]; !ok {
		m["quote_currency"] = quote
		changed = true
	}
	if _, ok := m["contract_size"]; !ok {
		m["contract_size"] = float64(1000)
		changed = true
	}
	if _, ok := m["liquidity"]; !ok {
		m["liquidity"] = forexLiquidity(base, quote)
		changed = true
	}
	if !changed {
		return ""
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// ─── FUTURE ──────────────────────────────────────────────────────────────────

// futureContractSizes mapira prefix tickera na veličinu ugovora.
var futureContractSizes = map[string]float64{
	"CL": 1000,  // Sirova nafta (WTI)
	"GC": 100,   // Zlato
	"SI": 5000,  // Srebro
	"NG": 10000, // Prirodni gas
	"HG": 25000, // Bakar
	"ES": 50,    // E-mini S&P 500
	"NQ": 20,    // E-mini Nasdaq-100
	"YM": 5,     // E-mini Dow Jones
	"ZC": 5000,  // Kukuruz
	"ZW": 5000,  // Pšenica
	"ZS": 5000,  // Soja
	"RB": 42000, // RBOB benzin
	"HO": 42000, // Lož ulje
}

// futureContractUnits mapira prefix tickera na jedinicu mere ugovora.
var futureContractUnits = map[string]string{
	"CL": "Barrel",      // Sirova nafta (WTI)
	"GC": "Troy Ounce",  // Zlato
	"SI": "Troy Ounce",  // Srebro
	"NG": "MMBtu",       // Prirodni gas
	"HG": "Pound",       // Bakar
	"ES": "Index Point", // E-mini S&P 500
	"NQ": "Index Point", // E-mini Nasdaq-100
	"YM": "Index Point", // E-mini Dow Jones
	"ZC": "Bushel",      // Kukuruz
	"ZW": "Bushel",      // Pšenica
	"ZS": "Bushel",      // Soja
	"RB": "Gallon",      // RBOB benzin
	"HO": "Gallon",      // Lož ulje
}

// futureMonthCodes mapira slovo meseca na time.Month prema CME konvenciji.
var futureMonthCodes = map[byte]time.Month{
	'F': time.January, 'G': time.February, 'H': time.March,
	'J': time.April, 'K': time.May, 'M': time.June,
	'N': time.July, 'Q': time.August, 'U': time.September,
	'V': time.October, 'X': time.November, 'Z': time.December,
}

func (w *ListingRefresherWorker) refreshFuture(ctx context.Context, l domain.Listing, now time.Time) {
	prefix, monthLetter, year2 := parseFutureTicker(l.Ticker)

	// ── Primarni izvor: EODHD real-time commodity ────────────────────────────
	var price, ask, bid, change float64
	var volume int64

	if w.eodhd != nil && prefix != "" {
		commSymbol := eodhdCommoditySymbol(prefix)
		q, err := w.eodhd.RealTimeQuote(ctx, commSymbol)
		if err == nil && q.Close > 0 {
			price = q.Close
			change = q.Change
			ask = q.Ask
			bid = q.Bid
			if ask <= 0 || bid <= 0 {
				ask = price * 1.001
				bid = price * 0.999
			}
			if q.Volume > 0 {
				volume = q.Volume
			} else {
				volume = l.Volume
			}
			log.Printf("[worker] FUTURE %s = %.4f (prefix=%s→%s) [EODHD]", l.Ticker, price, prefix, commSymbol)
		} else {
			log.Printf("[worker] FUTURE %s: EODHD greška za %s: %v", l.Ticker, commSymbol, err)
		}
	}

	if price == 0 && w.requireLiveQuotes {
		log.Printf("[worker] FUTURE %s: nema live podataka — preskačem ažuriranje (REQUIRE_LIVE)", l.Ticker)
		return
	}

	// ── Fallback: random walk ±1% (samo LISTING_REQUIRE_LIVE_QUOTES=false) ─────
	if price == 0 {
		base := l.Price
		if base == 0 {
			base = 100.0
		}
		delta := (rand.Float64()*2 - 1) * base * 0.01 //nolint:gosec
		price = base + delta
		if price < 0.01 {
			price = 0.01
		}
		ask = price * 1.001
		bid = price * 0.999
		change = price - base
		volume = l.Volume + int64(rand.Intn(5000)) //nolint:gosec
	}

	// Ažurirati details: contract_size i settlement_date
	m := parseJSONMap(l.DetailsJSON)

	if cs, ok := futureContractSizes[prefix]; ok {
		m["contract_size"] = cs
	} else if _, exists := m["contract_size"]; !exists {
		m["contract_size"] = float64(1) // fallback
	}

	if cu, ok := futureContractUnits[prefix]; ok {
		m["contract_unit"] = cu
	} else if _, exists := m["contract_unit"]; !exists {
		m["contract_unit"] = "Unit"
	}

	if month, ok := futureMonthCodes[monthLetter]; ok && year2 >= 0 {
		fullYear := 2000 + year2
		settlDate := time.Date(fullYear, month, 1, 0, 0, 0, 0, time.UTC)
		m["settlement_date"] = settlDate.Format("2006-01-02")
	}

	detailsJSON := ""
	if b, err := json.Marshal(m); err == nil {
		detailsJSON = string(b)
	}

	if err := w.saveWithDetails(ctx, l, price, ask, bid, volume, now, detailsJSON); err != nil {
		log.Printf("[worker] FUTURE %s: greška pri čuvanju: %v", l.Ticker, err)
		return
	}

	daily := domain.ListingDailyPriceInfo{
		ListingID: l.ID, Date: now,
		Price: price, AskHigh: ask, BidLow: bid,
		PriceChange: change, Volume: volume,
	}
	if err := w.repo.AppendDailyPrice(ctx, daily); err != nil {
		log.Printf("[worker] FUTURE %s: greška pri upisu dnevne cene: %v", l.Ticker, err)
	}

	cs, _ := m["contract_size"].(float64)
	log.Printf("[worker] FUTURE %s = %.4f (CS=%.0f) [EODHD ili sintetika]", l.Ticker, price, cs)
}

// ─── OPTION ──────────────────────────────────────────────────────────────────

// refreshOption koristi Yahoo Finance lanac. Sa LISTING_REQUIRE_LIVE_QUOTES=true nema
// sintetičkih cena; alternativa iz speca (BS/generisani lanac) ostaje van produkcijskog
// puta dok se eksplicitno ne uvede kao odvojen modul — ne mešati sa live quote-om.
func (w *ListingRefresherWorker) refreshOption(ctx context.Context, l domain.Listing, now time.Time) {
	underlying := extractOptionUnderlying(l.Ticker)
	if underlying == "" {
		log.Printf("[worker] OPTION %s: ne mogu izvući underlying ticker", l.Ticker)
		w.optionFallbackOrSkip(ctx, l, now)
		return
	}

	yahooResp, err := fetchYahooOptions(ctx, w.httpClient, underlying)
	time.Sleep(500 * time.Millisecond) // Yahoo rate-limit pauza
	if err != nil {
		log.Printf("[worker] OPTION %s: Yahoo greška: %v", l.Ticker, err)
		w.optionFallbackOrSkip(ctx, l, now)
		return
	}

	if len(yahooResp.OptionChain.Result) == 0 {
		log.Printf("[worker] OPTION %s: Yahoo prazan odgovor", l.Ticker)
		w.optionFallbackOrSkip(ctx, l, now)
		return
	}

	result := yahooResp.OptionChain.Result[0]
	underlyingPrice := result.Quote.RegularMarketPrice

	// Pronaći kontrakt koji odgovara našem tickeru u calls i puts
	var contract *yahooContract
	for i := range result.Options {
		for j := range result.Options[i].Calls {
			if strings.EqualFold(result.Options[i].Calls[j].ContractSymbol, l.Ticker) {
				c := result.Options[i].Calls[j]
				contract = &c
				break
			}
		}
		if contract == nil {
			for j := range result.Options[i].Puts {
				if strings.EqualFold(result.Options[i].Puts[j].ContractSymbol, l.Ticker) {
					c := result.Options[i].Puts[j]
					contract = &c
					break
				}
			}
		}
		if contract != nil {
			break
		}
	}

	if contract == nil {
		log.Printf("[worker] OPTION %s: kontrakt nije pronađen u Yahoo lancu (underlying=%s)", l.Ticker, underlying)
		w.optionFallbackOrSkip(ctx, l, now)
		return
	}

	price := contract.LastPrice
	ask := contract.Ask
	bid := contract.Bid
	volume := contract.Volume
	if price == 0 {
		price = (ask + bid) / 2
	}
	change := price - l.Price

	// Ažurirati DetailsJSON: underlying_price, implied_volatility, open_interest
	m := parseJSONMap(l.DetailsJSON)
	if underlyingPrice > 0 {
		m["underlying_price"] = underlyingPrice
	}
	m["implied_volatility"] = contract.ImpliedVolatility
	m["open_interest"] = contract.OpenInterest

	detailsJSON := ""
	if b, err := json.Marshal(m); err == nil {
		detailsJSON = string(b)
	}

	if err := w.saveWithDetails(ctx, l, price, ask, bid, volume, now, detailsJSON); err != nil {
		log.Printf("[worker] OPTION %s: greška pri čuvanju: %v", l.Ticker, err)
		return
	}

	daily := domain.ListingDailyPriceInfo{
		ListingID: l.ID, Date: now,
		Price: price, AskHigh: ask, BidLow: bid,
		PriceChange: change, Volume: volume,
	}
	if err := w.repo.AppendDailyPrice(ctx, daily); err != nil {
		log.Printf("[worker] OPTION %s: greška pri upisu dnevne cene: %v", l.Ticker, err)
	}

	log.Printf("[worker] OPTION %s = $%.4f (IV=%.4f, OI=%d, underlying=%s $%.2f)",
		l.Ticker, price, contract.ImpliedVolatility, contract.OpenInterest, underlying, underlyingPrice)
}

// optionFallbackOrSkip: uz REQUIRE_LIVE ne upisuje sintetičke cene; inače mock za dev.
func (w *ListingRefresherWorker) optionFallbackOrSkip(ctx context.Context, l domain.Listing, now time.Time) {
	if w.requireLiveQuotes {
		log.Printf("[worker] OPTION %s: preskačem ažuriranje (REQUIRE_LIVE, bez Yahoo/mock)", l.Ticker)
		return
	}
	price, ask, bid, volume, change := mockPrice(l)
	if err := w.repo.UpdatePrices(ctx, l.ID, price, ask, bid, volume, now); err != nil {
		log.Printf("[worker] OPTION %s: greška pri mock čuvanju: %v", l.Ticker, err)
		return
	}
	daily := domain.ListingDailyPriceInfo{
		ListingID: l.ID, Date: now,
		Price: price, AskHigh: ask, BidLow: bid,
		PriceChange: change, Volume: volume,
	}
	_ = w.repo.AppendDailyPrice(ctx, daily)
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// saveWithDetails poziva UpdatePrices, i ako details nije prazan, ažurira i DetailsJSON.
func (w *ListingRefresherWorker) saveWithDetails(
	ctx context.Context, l domain.Listing,
	price, ask, bid float64, volume int64, at time.Time,
	details string,
) error {
	if err := w.repo.UpdatePrices(ctx, l.ID, price, ask, bid, volume, at); err != nil {
		return err
	}
	if details != "" {
		if err := w.repo.UpdateDetails(ctx, l.ID, details); err != nil {
			log.Printf("[worker] %s: greška pri ažuriranju details_json: %v", l.Ticker, err)
		}
	}
	return nil
}

// fetchTodayVolume pokušava da dohvati dnevni volumen sa Finnhub-a.
func (w *ListingRefresherWorker) fetchTodayVolume(ctx context.Context, symbol string, now time.Time, fallback int64) int64 {
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	candles, err := w.finnhub.Candles(ctx, symbol, from.Add(-24*time.Hour), now)
	if err != nil || candles.S != "ok" || len(candles.V) == 0 {
		return fallback
	}
	return int64(candles.V[len(candles.V)-1])
}

// ensureHistory seeduje 30 dana istorije ako listing još nema podataka.
// Primarni izvor: EODHD EOD API. Fallback: Finnhub Candles.
func (w *ListingRefresherWorker) ensureHistory(ctx context.Context, l domain.Listing, now time.Time) {
	existing, err := w.repo.GetHistory(ctx, l.ID, now.AddDate(-1, 0, 0), now)
	if err != nil || len(existing) > 0 {
		return
	}

	log.Printf("[worker] ListingRefresher: seeding 30 dana istorije za %s...", l.Ticker)
	from := now.AddDate(0, 0, -30)

	// ── Primarni: EODHD EOD istorija ─────────────────────────────────────────
	if w.eodhd != nil {
		bars, eodhd_err := w.eodhd.EODHistory(ctx, eodhdStockSymbol(l.Ticker), from, now)
		if eodhd_err == nil && len(bars) > 0 {
			seeded := 0
			for i, bar := range bars {
				t, parseErr := time.Parse("2006-01-02", bar.Date)
				if parseErr != nil {
					continue
				}
				prevClose := bar.Close
				if i > 0 {
					prevClose = bars[i-1].Close
				}
				daily := domain.ListingDailyPriceInfo{
					ListingID:   l.ID,
					Date:        t.UTC(),
					Price:       bar.Close,
					AskHigh:     bar.High,
					BidLow:      bar.Low,
					PriceChange: bar.Close - prevClose,
					Volume:      int64(bar.Volume),
				}
				if err := w.repo.AppendDailyPrice(ctx, daily); err != nil {
					log.Printf("[worker] ListingRefresher: greška upisa EODHD istorije za %s [%s]: %v", l.Ticker, bar.Date, err)
					continue
				}
				seeded++
			}
			log.Printf("[worker] ListingRefresher: seedovano %d dnevnih zapisa za %s [EODHD]", seeded, l.Ticker)
			return
		}
		log.Printf("[worker] ListingRefresher: EODHD EOD greška za %s: %v — pokušavam Finnhub", l.Ticker, eodhd_err)
	}

	// ── Fallback: Finnhub Candles ─────────────────────────────────────────────
	if w.finnhub == nil {
		return
	}
	candles, err := w.finnhub.Candles(ctx, l.Ticker, from, now)
	if err != nil {
		log.Printf("[worker] ListingRefresher: Finnhub candles greška za %s: %v", l.Ticker, err)
		return
	}
	if candles.S != "ok" {
		log.Printf("[worker] ListingRefresher: Finnhub candles status=%s za %s", candles.S, l.Ticker)
		return
	}

	seeded := 0
	for i, ts := range candles.T {
		if i >= len(candles.C) || i >= len(candles.H) || i >= len(candles.L) || i >= len(candles.V) {
			break
		}
		prevClose := candles.C[i]
		if i > 0 {
			prevClose = candles.C[i-1]
		}
		daily := domain.ListingDailyPriceInfo{
			ListingID:   l.ID,
			Date:        time.Unix(ts, 0).UTC(),
			Price:       candles.C[i],
			AskHigh:     candles.H[i],
			BidLow:      candles.L[i],
			PriceChange: candles.C[i] - prevClose,
			Volume:      int64(candles.V[i]),
		}
		if err := w.repo.AppendDailyPrice(ctx, daily); err != nil {
			log.Printf("[worker] ListingRefresher: greška upisa istorije za %s [%d]: %v", l.Ticker, ts, err)
			continue
		}
		seeded++
	}
	log.Printf("[worker] ListingRefresher: seedovano %d dnevnih zapisa za %s [Finnhub]", seeded, l.Ticker)
}

// ─── Ticker parsers ───────────────────────────────────────────────────────────

// parseForexTicker parsira "EUR/USD" → ("EUR", "USD").
func parseForexTicker(ticker string) (base, quote string) {
	parts := strings.SplitN(ticker, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

// parseFutureTicker parsira npr. "CLJ22" → ("CL", 'J', 22).
// Vraća ("", 0, -1) ako format nije prepoznat.
//
// Format tickera po CME konvenciji: PREFIX + MONTH_CODE + YY
// gde je PREFIX 1-5 velikih slova, MONTH_CODE jedno slovo iz seta FGHJKMNQUVXZ,
// a YY dvocifrena godina. Parsiramo od KRAJA: zadnje 2 cifre = godina,
// prethodni bajt = mesečni kod, ostatak = prefiks.
func parseFutureTicker(ticker string) (prefix string, monthLetter byte, year2 int) {
	n := len(ticker)
	// Minimum: 1 prefiks slovo + 1 mesec + 2 cifre = 4 znaka
	if n < 4 {
		return "", 0, -1
	}

	// Zadnje 2 cifre = godina
	d1, d2 := ticker[n-2], ticker[n-1]
	if d1 < '0' || d1 > '9' || d2 < '0' || d2 > '9' {
		return "", 0, -1
	}
	yr := int(d1-'0')*10 + int(d2-'0')

	// Predzadnji bajt = mesečni kod (mora biti slovo)
	ml := ticker[n-3]
	if ml < 'A' || ml > 'Z' {
		return "", 0, -1
	}

	// Sve pre mesečnog koda = prefiks (mora biti barem 1 slovo)
	pfx := ticker[:n-3]
	if pfx == "" {
		return "", 0, -1
	}
	for _, ch := range pfx {
		if ch < 'A' || ch > 'Z' {
			return "", 0, -1
		}
	}

	return pfx, ml, yr
}

// extractOptionUnderlying izvlači underlying ticker iz opcijskog simbola.
// Npr. "MSFT220404C00180000" → "MSFT"
func extractOptionUnderlying(ticker string) string {
	end := 0
	for end < len(ticker) && unicode.IsLetter(rune(ticker[end])) {
		end++
	}
	if end == 0 {
		return ""
	}
	return ticker[:end]
}

// ─── JSON helper ─────────────────────────────────────────────────────────────

// parseJSONMap čita DetailsJSON u map[string]any, ili vraća praznu mapu pri grešci.
func parseJSONMap(detailsJSON string) map[string]any {
	m := make(map[string]any)
	if detailsJSON == "" || detailsJSON == "{}" {
		return m
	}
	_ = json.Unmarshal([]byte(detailsJSON), &m)
	return m
}

// ─── Mock fallback ────────────────────────────────────────────────────────────

// mockPrice generiše simuliranu cenu (±2% od poslednje poznate cene).
func mockPrice(l domain.Listing) (price, ask, bid float64, volume int64, change float64) {
	base := l.Price
	if base == 0 {
		base = 100.0
	}
	spread := base * 0.001
	price = base + (rand.Float64()-0.5)*base*0.02 //nolint:gosec
	if price < 0.01 {
		price = 0.01
	}
	change = price - base
	ask = price + spread
	bid = price - spread
	if bid < 0 {
		bid = 0
	}
	volume = l.Volume + int64(rand.Intn(10000)) //nolint:gosec
	return price, ask, bid, volume, change
}
