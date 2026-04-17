package worker

import (
	"fmt"
	"math"
	"time"
)

// ─── Konstante ────────────────────────────────────────────────────────────────

const (
	// bsIV je inicijalna implicirana volatilnost po specifikaciji ("inicijalno postavljena na 1").
	bsIV = 1.0

	// bsRiskFreeRate je bezrizična kamatna stopa (5 %).
	bsRiskFreeRate = 0.05

	// bsMinPrice je donja granica izračunate cene opcije (ispod ove vrednosti
	// numerički rezultat BS formule je beznačajan).
	bsMinPrice = 0.01

	// bsSpreadPct je relativna veličina bid-ask razlike.
	bsSpreadPct = 0.04

	// bsMinSpread je minimalna apsolutna bid-ask razlika ($0.05).
	bsMinSpread = 0.05
)

// ─── Black-Scholes ────────────────────────────────────────────────────────────

// normalCDF vraća vrednost standardne normalne kumulativne raspodele u tački x.
func normalCDF(x float64) float64 {
	return 0.5 * math.Erfc(-x/math.Sqrt2)
}

// BSCall izračunava cenu CALL opcije po Black-Scholes modelu.
//
//   - s     = trenutna cena underlying aktive
//   - k     = strike cena
//   - r     = bezrizična kamatna stopa (godišnja, decimalna)
//   - t     = vreme do isteka u godinama
//   - sigma = implicirana volatilnost (godišnja, decimalna; 1.0 = 100 %)
func BSCall(s, k, r, t, sigma float64) float64 {
	if t <= 0 {
		return math.Max(s-k, 0)
	}
	d1 := (math.Log(s/k) + (r+0.5*sigma*sigma)*t) / (sigma * math.Sqrt(t))
	d2 := d1 - sigma*math.Sqrt(t)
	price := s*normalCDF(d1) - k*math.Exp(-r*t)*normalCDF(d2)
	return math.Max(price, bsMinPrice)
}

// BSPut izračunava cenu PUT opcije po Black-Scholes modelu.
func BSPut(s, k, r, t, sigma float64) float64 {
	if t <= 0 {
		return math.Max(k-s, 0)
	}
	d1 := (math.Log(s/k) + (r+0.5*sigma*sigma)*t) / (sigma * math.Sqrt(t))
	d2 := d1 - sigma*math.Sqrt(t)
	price := k*math.Exp(-r*t)*normalCDF(-d2) - s*normalCDF(-d1)
	return math.Max(price, bsMinPrice)
}

// BSSpread vraća POLUVREDNOST bid-ask razlike za datu cenu opcije.
// Bid i ask se računaju kao: bid = mid - BSSpread(mid), ask = mid + BSSpread(mid).
// Ukupni bid-ask raspon je 2 × BSSpread, a half-spread je:
//
//	max( bsSpreadPct/2 × price, bsMinSpread/2 )
func BSSpread(price float64) float64 {
	half := price * (bsSpreadPct / 2)
	if half < bsMinSpread/2 {
		return bsMinSpread / 2
	}
	return half
}

// ─── Generisanje datuma isteka (per specifikacija) ────────────────────────────

// GenerateOptionExpiries generiše datume isteka po pravilima iz specifikacije:
//
//  1. Kratkoročni: počev od today+6, svakih 6 dana, dok razlika prvog i poslednjeg
//     ne dostigne 30 dana. (rezultuje u 6 datuma: +6, +12, +18, +24, +30, +36)
//
//  2. Dugoročni: 6 dodatnih datuma sa razmakom od 30 dana od poslednjeg kratkoročnog.
//     (+66, +96, +126, +156, +186, +216)
//
// Ukupno: 12 datuma isteka po stock-u.
func GenerateOptionExpiries(today time.Time) []time.Time {
	var expiries []time.Time

	// Kratkoročni blok
	first := today.AddDate(0, 0, 6)
	expiries = append(expiries, first)
	for {
		next := expiries[len(expiries)-1].AddDate(0, 0, 6)
		diffDays := int(next.Sub(first).Hours() / 24)
		if diffDays > 30 {
			break
		}
		expiries = append(expiries, next)
	}

	// Dugoročni blok: 6 datuma sa 30-dnevnim razmakom
	last := expiries[len(expiries)-1]
	for i := 0; i < 6; i++ {
		last = last.AddDate(0, 0, 30)
		expiries = append(expiries, last)
	}

	return expiries
}

// ─── Generisanje strike cena (per specifikacija) ──────────────────────────────

// GenerateStrikes generiše 11 strike cena: 5 ispod, ATM, 5 iznad.
// Bazna cena se zaokružuje na najbliži celi broj (npr. 112.4 → 112, 112.6 → 113).
//
// Primer: cena=112 → [107, 108, 109, 110, 111, 112, 113, 114, 115, 116, 117]
func GenerateStrikes(price float64) []float64 {
	base := math.Round(price)
	strikes := make([]float64, 11)
	for i := 0; i < 11; i++ {
		strikes[i] = base - 5 + float64(i)
	}
	return strikes
}

// ─── OCC ticker ───────────────────────────────────────────────────────────────

// OCCTicker generiše standardni OCC simbol opcijskog ugovora.
//
// Format: {UNDERLYING}{YYMMDD}{C|P}{8-cifreni-strike×1000}
//
// Primer: ("AAPL", 2026-04-19, true, 200.0) → "AAPL260419C00200000"
func OCCTicker(underlying string, expiry time.Time, isCall bool, strike float64) string {
	typeChar := "C"
	if !isCall {
		typeChar = "P"
	}
	strikeInt := int(math.Round(strike * 1000))
	return fmt.Sprintf("%s%s%s%08d", underlying, expiry.Format("060102"), typeChar, strikeInt)
}

// occTickerLen vraća dužinu OCC tickera bez alociranja stringa — korisno za validaciju.
func occTickerLen(underlying string) int {
	// {underlying} + 6 (YYMMDD) + 1 (C/P) + 8 (strike) = len(underlying) + 15
	return len(underlying) + 15
}
