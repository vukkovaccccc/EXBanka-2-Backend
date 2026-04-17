-- =============================================================================
-- Migration: 000041_optimize_option_lookup
-- Service:   bank-service
-- Schema:    core_banking
--
-- Dodaje indekse koji ubrzavaju učitavanje opcijskog lanca po underlying-u.
--
-- Opcije se generišu automatski pri pokretanju workera (initOptionListings)
-- koristeći Black-Scholes model per specifikacije:
--   - 12 datuma isteka (6 kratkoročnih × 6 dana + 6 dugoročnih × 30 dana)
--   - 11 strike cena (5 ispod + ATM + 5 iznad)
--   - 2 tipa (CALL + PUT) po kombinaciji
--   = 264 opcije po underlying akciji (top 5 akcija po volumenu)
--
-- Format tickera: OCC standard — {UNDERLYING}{YYMMDD}{C|P}{8-digit-strike×1000}
-- Primer: AAPL260419C00200000 = AAPL CALL, istice 2026-04-19, strike $200
--
-- Podaci opcije u details_json (tip: OptionDetails):
--   option_type:        "CALL" | "PUT"
--   strike_price:       cena izvrsenja (USD)
--   settlement_date:    datum isteka (YYYY-MM-DD)
--   stock_listing_id:   ID matičnog STOCK listinga
--   underlying_price:   cena akcije u trenutku osvezavanja
--   implied_volatility: pocetna vrednost 1.0 (100%)
--   open_interest:      pocetna vrednost 0
--   initial_price:      BS cena u trenutku seedinga (referenca za racunanje promene %)
-- =============================================================================

-- Indeks za brzo pretrazivanje opcija po underlying prefiksu tickera
-- (npr. sve AAPL opcije: listing_type='OPTION' AND LOWER(ticker) LIKE 'aapl%')
CREATE INDEX IF NOT EXISTS idx_listing_type_ticker_lower
    ON core_banking.listing (listing_type, lower(ticker));

-- Izraz indeks na details_json->>'stock_listing_id' omogucava brzo
-- pronalazenje svih opcija za datu underlying akciju po ID-u.
CREATE INDEX IF NOT EXISTS idx_listing_option_stock_id
    ON core_banking.listing ((details_json::jsonb->>'stock_listing_id'))
    WHERE listing_type = 'OPTION';
