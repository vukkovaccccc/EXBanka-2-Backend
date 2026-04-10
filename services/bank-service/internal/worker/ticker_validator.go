package worker

import "regexp"

// Ticker format regex patterns per spec:
//   - STOCK:   1–5 uppercase letters (e.g. "AAPL", "T")
//   - FOREX:   BASE/QUOTE where each part is exactly 3 uppercase letters (e.g. "EUR/USD")
//   - OPTION:  OCC-style: 1–5 uppercase letters + 6 digits (YYMMDD) + C or P + 8 digits (e.g. "MSFT220404C00180000")
//   - FUTURE:  product code (1–5 uppercase letters) + month code (CME standard) + 2 digit year (e.g. "CLJ22")
var (
	stockTickerRE  = regexp.MustCompile(`^[A-Z]{1,5}$`)
	forexTickerRE  = regexp.MustCompile(`^[A-Z]{3}/[A-Z]{3}$`)
	optionTickerRE = regexp.MustCompile(`^[A-Z]{1,5}\d{6}[CP]\d{8}$`)
	futureTickerRE = regexp.MustCompile(`^[A-Z]{1,5}[FGHJKMNQUVXZ]\d{2}$`)
)

// ValidateTickerFormat returns true when ticker matches the expected format for listingType.
// listingType must be one of "STOCK", "FOREX", "OPTION", "FUTURE".
func ValidateTickerFormat(listingType, ticker string) bool {
	switch listingType {
	case "STOCK":
		return stockTickerRE.MatchString(ticker)
	case "FOREX":
		return forexTickerRE.MatchString(ticker)
	case "OPTION":
		return optionTickerRE.MatchString(ticker)
	case "FUTURE":
		return futureTickerRE.MatchString(ticker)
	default:
		return false
	}
}
