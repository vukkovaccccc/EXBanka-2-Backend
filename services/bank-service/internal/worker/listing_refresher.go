package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"banka-backend/services/bank-service/internal/domain"
)

// ─── Worker ──────────────────────────────────────────────────────────────────

// PriceTickPublisher is an optional sink for listing price updates.
// It is called after every successful price save inside saveWithDetails.
// Implementations must be non-blocking (e.g. use buffered channels internally).
// The concrete implementation lives in internal/trading/worker.PriceTickBus.
type PriceTickPublisher interface {
	Publish(listingID int64, ask, bid float64)
}

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
	// tickPublisher, ako je postavljen, prima obaveštenje o svakom uspešno sačuvanom
	// cenovnom ažuriranju.  Koristi ga trading engine za event-driven LIMIT okidanje.
	tickPublisher PriceTickPublisher // može biti nil
}

func NewListingRefresherWorker(
	repo domain.ListingRepository,
	interval time.Duration,
	eodhd_api_key string,
	finnhubAPIKey string,
	alphaVantageKey string,
	requireLiveQuotes bool,
	tickPublisher PriceTickPublisher, // može biti nil; ako je postavljen, šalje cenovne tikove trading engine-u
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
		tickPublisher:     tickPublisher,
	}
}

// Start pokreće worker petlju. Blokira dok se ctx ne otkaže.
func (w *ListingRefresherWorker) Start(ctx context.Context) {
	log.Printf("[worker] ListingRefresherWorker pokrenut (interval=%s)", w.interval)
	w.initOptionListings(ctx)
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

	// Keš cena svih stock listinga — koristi se u refreshOptionWithBS
	// umesto N×GetByID poziva (jednom po ciklusu, ne po opciji).
	stockPrices := make(map[int64]float64, len(listings))
	for _, l := range listings {
		if l.ListingType == domain.ListingTypeStock && l.Price > 0 {
			stockPrices[l.ID] = l.Price
		}
	}

	refreshed := 0
	for _, l := range listings {
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
			// Opcije se osvežavaju direktno BS-om (bez Yahoo pokušaja):
			// naši BS tickeri imaju rolling expiry datume koji ne postoje
			// u Yahoo-ovom standardnom lancu, pa bi Yahoo uvek vratio grešku.
			w.refreshOptionWithBS(ctx, l, now, stockPrices)
			// Bez 300ms rate-limit sleep-a za opcije — nema external API poziva
			refreshed++
			continue
		default:
			log.Printf("[worker] ListingRefresher: nepoznat tip %q za %s — preskačem", l.ListingType, l.Ticker)
			continue
		}
		refreshed++
		// Rate-limit samo za external API tipove (STOCK/FOREX/FUTURE)
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
			// Koristimo PreviousClose iz API odgovora za standardnu formulu:
			// Change = LastPrice - PreviousClose
			// Ne koristimo l.Price jer se ažurira svakim refresh ciklusom,
			// a ne čuva stvarni prethodni dnevni zatvarač.
			if q.PreviousClose > 0 {
				change = q.Close - q.PreviousClose
			} else {
				change = q.Change
			}
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
			// AlphaVantage ne vraća PreviousClose za Forex; dnevna promena
			// biće ispravno izračunata u sledećem EODHD osvežavanju.
			change = 0
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
			// Finnhub vraća q.Pc (previous close) — koristimo za standardnu formulu
			if q.Pc > 0 {
				change = q.C - q.Pc
			} else {
				change = q.D
			}
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
			// Preferujemo eksplicitno: Change = LastPrice - PreviousClose.
			// q.Change od EODHD može biti 0 za futures kada je tržište zatvoreno
			// ili API ne popuni to polje; q.PreviousClose je pouzdaniji izvor.
			if q.PreviousClose > 0 {
				change = q.Close - q.PreviousClose
			} else {
				change = q.Change
			}
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

// ─── Shared helpers ───────────────────────────────────────────────────────────

// saveWithDetails poziva UpdatePrices, i ako details nije prazan, ažurira i DetailsJSON.
// Nakon uspešnog čuvanja objavljuje cenovni tik trading engine-u (event-driven LIMIT okidanje).
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
	// Obavesti trading engine o novim cenama kako bi odmah proverio LIMIT uslove.
	if w.tickPublisher != nil && ask > 0 && bid > 0 {
		w.tickPublisher.Publish(l.ID, ask, bid)
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

// optionMaxUnderlyings je maksimalan broj stock-ova za koje se generišu opcije.
// U realnim tržištima options postoje samo za najlikvidnije akcije.
// 5 stockova × 264 opcije = 1320 option listinga ukupno.
const optionMaxUnderlyings = 5

// ─── Option seeder (Black-Scholes, per specifikacija Pristup 2) ──────────────

// initOptionListings se poziva jednom pri pokretanju workera.
// Generiše opcijski lanac za top N stockova po volumenu koji još nemaju opcije,
// koristeći Black-Scholes model per specifikacije:
// 12 datuma isteka × 11 strike cena × 2 tipa (CALL+PUT) = 264 opcije po stock-u.
func (w *ListingRefresherWorker) initOptionListings(ctx context.Context) {
	all, err := w.repo.ListAll(ctx)
	if err != nil {
		log.Printf("[worker] initOptionListings: greška pri učitavanju listinga: %v", err)
		return
	}

	// Skupi underlying tickers za koje OPTION listinzi već postoje
	seededUnderlyings := make(map[string]bool)
	var stocks []domain.Listing
	for _, l := range all {
		switch l.ListingType {
		case domain.ListingTypeOption:
			u := extractOptionUnderlying(l.Ticker)
			if u != "" {
				seededUnderlyings[u] = true
			}
		case domain.ListingTypeStock:
			if l.Price > 0 {
				stocks = append(stocks, l)
			}
		}
	}

	// Sortiraj po volumenu opadajuće — opcije generišemo samo za najlikvidnije
	sort.Slice(stocks, func(i, j int) bool {
		return stocks[i].Volume > stocks[j].Volume
	})

	now := time.Now().UTC()
	seeded := 0
	for _, stock := range stocks {
		if seeded >= optionMaxUnderlyings {
			break
		}
		if seededUnderlyings[stock.Ticker] {
			seeded++ // već postoji, ali se računa u kvotu
			continue
		}
		w.seedOptionsForStock(ctx, stock, now)
		seeded++
	}
}

// seedOptionsForStock generiše opcijski lanac za datu akciju koristeći Black-Scholes model.
//
// Datumi isteka i strike cene se određuju po specifikaciji (Pristup 2):
//   - 12 datuma isteka (6 kratkoročnih svakih 6 dana + 6 dugoročnih svakih 30 dana)
//   - 11 strike cena (5 ispod + ATM + 5 iznad, sa korakom od 1)
//   - Cena: Black-Scholes sa IV=1.0 (per spec)
func (w *ListingRefresherWorker) seedOptionsForStock(ctx context.Context, stock domain.Listing, now time.Time) {
	expiries := GenerateOptionExpiries(now)
	strikes := GenerateStrikes(stock.Price)

	created, skipped := 0, 0
	for _, expiry := range expiries {
		// T u godinama: koristimo Duration direktno (DST-otporno) i 365.25 (leap-year-ispravno)
		T := float64(expiry.Sub(now)) / float64(365.25*24*float64(time.Hour))
		for _, strike := range strikes {
			if strike <= 0 {
				skipped += 2 // call + put
				continue
			}
			callPrice := BSCall(stock.Price, strike, bsRiskFreeRate, T, bsIV)
			putPrice := BSPut(stock.Price, strike, bsRiskFreeRate, T, bsIV)

			if w.createBSOptionListing(ctx, stock, expiry, strike, "CALL", callPrice, now) {
				created++
			} else {
				skipped++
			}
			if w.createBSOptionListing(ctx, stock, expiry, strike, "PUT", putPrice, now) {
				created++
			} else {
				skipped++
			}
		}
	}

	log.Printf("[worker] seedOptionsForStock %s: kreirano=%d preskočeno=%d (BS, S=%.2f, IV=%.0f%%)",
		stock.Ticker, created, skipped, stock.Price, bsIV*100)
}

// createBSOptionListing upisuje jedan Black-Scholes opcijski ugovor kao novi listing.
// Vraća true ako je red uspešno kreiran.
func (w *ListingRefresherWorker) createBSOptionListing(
	ctx context.Context,
	stock domain.Listing,
	expiry time.Time,
	strike float64,
	optType string,
	price float64,
	now time.Time,
) bool {
	isCall := optType == "CALL"
	ticker := OCCTicker(stock.Ticker, expiry, isCall, strike)
	if occTickerLen(stock.Ticker) > 20 {
		// Ticker bi prelazio VARCHAR(20) — preskačemo ovaj underlying
		return false
	}

	spread := BSSpread(price)
	bid := math.Max(price-spread, bsMinPrice)
	ask := price + spread

	details := domain.OptionDetails{
		OptionType:        optType,
		StrikePrice:       strike,
		SettlementDate:    expiry.Format("2006-01-02"),
		StockListingID:    stock.ID,
		UnderlyingPrice:   stock.Price,
		ImpliedVolatility: bsIV,
		OpenInterest:      0,
		InitialPrice:      price,
	}
	detailsBytes, err := json.Marshal(details)
	if err != nil {
		return false
	}

	name := fmt.Sprintf("%s %s $%.2f %s", stock.Ticker, optType, strike, expiry.Format("2006-01-02"))

	l := domain.Listing{
		Ticker:      ticker,
		Name:        name,
		ExchangeID:  stock.ExchangeID,
		ListingType: domain.ListingTypeOption,
		Price:       price,
		Ask:         ask,
		Bid:         bid,
		Volume:      0,
		DetailsJSON: string(detailsBytes),
	}

	if _, err := w.repo.Create(ctx, l); err != nil {
		log.Printf("[worker] createBSOptionListing %s: greška upisa: %v", ticker, err)
		return false
	}
	return true
}

// refreshOptionWithBS osveži cenu postojeće opcije koristeći Black-Scholes.
// Poziva se kada Yahoo Finance nije dostupan.
// Dohvata aktuelnu cenu underlying akcije iz baze (via stock_listing_id).
func (w *ListingRefresherWorker) refreshOptionWithBS(ctx context.Context, l domain.Listing, now time.Time, stockPrices map[int64]float64) {
	var details domain.OptionDetails
	if err := json.Unmarshal([]byte(l.DetailsJSON), &details); err != nil || details.StrikePrice <= 0 {
		return
	}

	expiryDate, err := time.Parse("2006-01-02", details.SettlementDate)
	if err != nil {
		return
	}
	T := float64(expiryDate.Sub(now)) / float64(365.25*24*float64(time.Hour))
	if T <= 0 {
		return // opcija je istekla
	}

	// Dohvati aktuelnu cenu underlying-a iz keša (jednom učitan po ciklusu)
	underlyingPrice := details.UnderlyingPrice
	if details.StockListingID > 0 {
		if p, ok := stockPrices[details.StockListingID]; ok && p > 0 {
			underlyingPrice = p
		}
	}
	if underlyingPrice <= 0 {
		return
	}

	var newPrice float64
	if details.OptionType == "CALL" {
		newPrice = BSCall(underlyingPrice, details.StrikePrice, bsRiskFreeRate, T, bsIV)
	} else {
		newPrice = BSPut(underlyingPrice, details.StrikePrice, bsRiskFreeRate, T, bsIV)
	}

	spread := BSSpread(newPrice)
	bid := math.Max(newPrice-spread, bsMinPrice)

	// Promena se računa u odnosu na početnu seed cenu (InitialPrice),
	// a ne u odnosu na prethodnu vrednost iz ciklusa (l.Price).
	// Ovo daje smislenu % promenu koja odražava kretanje underlying-a od trenutka kreiranja opcije.
	// Fallback na l.Price za opcije koje su seedirane pre uvođenja initial_price polja.
	referencePrice := details.InitialPrice
	if referencePrice <= 0 {
		referencePrice = l.Price
	}
	change := newPrice - referencePrice

	// Ažuriraj cene i underlying_price u details_json
	details.UnderlyingPrice = underlyingPrice
	if detailsBytes, err := json.Marshal(details); err == nil {
		_ = w.repo.UpdateDetails(ctx, l.ID, string(detailsBytes))
	}

	if err := w.repo.UpdatePrices(ctx, l.ID, newPrice, newPrice+spread, bid, l.Volume, now); err != nil {
		log.Printf("[worker] OPTION %s: BS osvežavanje greška: %v", l.Ticker, err)
		return
	}
	if w.tickPublisher != nil && newPrice+spread > 0 && bid > 0 {
		w.tickPublisher.Publish(l.ID, newPrice+spread, bid)
	}

	daily := domain.ListingDailyPriceInfo{
		ListingID: l.ID, Date: now,
		Price: newPrice, AskHigh: newPrice + spread, BidLow: bid,
		PriceChange: change, Volume: l.Volume,
	}
	_ = w.repo.AppendDailyPrice(ctx, daily)

	log.Printf("[worker] OPTION %s = $%.4f (BS, K=%.2f, T=%.4f, S=%.2f)",
		l.Ticker, newPrice, details.StrikePrice, T, underlyingPrice)
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
