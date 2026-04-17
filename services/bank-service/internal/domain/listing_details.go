package domain

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ─── Typed detail structs ─────────────────────────────────────────────────────
//
// Svaki tip hartije čuva type-specifične podatke u details_json TEXT koloni.
// Ove strukture omogućavaju type-safe čitanje i validaciju tog JSON-a.

// StockDetails sadrži specifična polja za hartije tipa STOCK.
type StockDetails struct {
	OutstandingShares int     `json:"outstanding_shares"`
	DividendYield     float64 `json:"dividend_yield"`
}

// ForexDetails sadrži specifična polja za hartije tipa FOREX.
type ForexDetails struct {
	BaseCurrency  string `json:"base_currency"`
	QuoteCurrency string `json:"quote_currency"`
	Liquidity     string `json:"liquidity"` // "High" | "Medium" | "Low"
}

// FutureDetails sadrži specifična polja za hartije tipa FUTURE.
type FutureDetails struct {
	ContractSize int    `json:"contract_size"`
	ContractUnit string `json:"contract_unit"` // e.g. "Barrel", "Troy Ounce"
}

// OptionDetails sadrži specifična polja za hartije tipa OPTION.
type OptionDetails struct {
	OptionType        string  `json:"option_type"`        // "CALL" or "PUT"
	StrikePrice       float64 `json:"strike_price"`       // Cena izvršenja (u USD)
	SettlementDate    string  `json:"settlement_date"`    // Datum dospeća (YYYY-MM-DD)
	StockListingID    int64   `json:"stock_listing_id"`   // ID matičnog STOCK listinga
	UnderlyingPrice   float64 `json:"underlying_price"`   // Cena underlying akcije u trenutku osvežavanja
	ImpliedVolatility float64 `json:"implied_volatility"` // Implicirana volatilnost
	OpenInterest      int     `json:"open_interest"`      // Otvoreni interes
	InitialPrice      float64 `json:"initial_price"`      // BS cena u trenutku seedinga — referenca za računanje promene %
}

// ─── ValidateListingDetails ───────────────────────────────────────────────────

// ValidateListingDetails pokušava da deserijalizuje rawJSON u odgovarajući struct
// za dati listingType i proverava da obavezna polja nisu prazna/nulta.
//
// Vraća nil ako je JSON validan i sadrži sva obavezna polja.
// Vraća grešku:
//   - ako je listingType nepoznat
//   - ako rawJSON nije validan JSON
//   - ako nedostaje neko obavezno polje za taj tip
//
// Namenjen je za pozivanje pre upisa u bazu (npr. u HTTP handlerima ili servisima).
func ValidateListingDetails(listingType string, rawJSON []byte) error {
	if len(rawJSON) == 0 {
		rawJSON = []byte("{}")
	}

	switch listingType {
	case "STOCK":
		var d StockDetails
		if err := json.Unmarshal(rawJSON, &d); err != nil {
			return fmt.Errorf("invalid STOCK details JSON: %w", err)
		}
		if d.OutstandingShares == 0 {
			return errors.New("STOCK details: outstanding_shares is required and must be > 0")
		}
		return nil

	case "FOREX":
		var d ForexDetails
		if err := json.Unmarshal(rawJSON, &d); err != nil {
			return fmt.Errorf("invalid FOREX details JSON: %w", err)
		}
		if d.BaseCurrency == "" {
			return errors.New("FOREX details: base_currency is required")
		}
		if d.QuoteCurrency == "" {
			return errors.New("FOREX details: quote_currency is required")
		}
		return nil

	case "FUTURE":
		var d FutureDetails
		if err := json.Unmarshal(rawJSON, &d); err != nil {
			return fmt.Errorf("invalid FUTURE details JSON: %w", err)
		}
		if d.ContractSize == 0 {
			return errors.New("FUTURE details: contract_size is required and must be > 0")
		}
		if d.ContractUnit == "" {
			return errors.New("FUTURE details: contract_unit is required")
		}
		return nil

	case "OPTION":
		var d OptionDetails
		if err := json.Unmarshal(rawJSON, &d); err != nil {
			return fmt.Errorf("invalid OPTION details JSON: %w", err)
		}
		if d.OptionType != "CALL" && d.OptionType != "PUT" {
			return errors.New("OPTION details: option_type must be 'CALL' or 'PUT'")
		}
		if d.StrikePrice <= 0 {
			return errors.New("OPTION details: strike_price is required and must be > 0")
		}
		return nil

	default:
		return fmt.Errorf("unknown listing type: %q", listingType)
	}
}
