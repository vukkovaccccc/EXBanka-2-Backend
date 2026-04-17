-- =============================================================================
-- Migration: 000036_fix_aapl_msft_details
-- Service:   bank-service
-- Schema:    core_banking
--
-- Vraća ispravne STOCK detalje za AAPL i MSFT.
--
-- Migracije 000019 i 000033 su greškom zamenile ceo details_json sa nízom opcija,
-- uništavajući outstanding_shares i dividend_yield. Migracija 000035 je uklonila
-- taj niz, ali bez obnavljanja originalnih polja.
--
-- Vrednosti: aproksimacije za april 2026 (worker ih ažurira pri sledećem pokretanju
-- ako je ALPHAVANTAGE_API_KEY konfigurisan).
-- =============================================================================

UPDATE core_banking.listing
SET details_json = jsonb_build_object(
    'outstanding_shares', CASE ticker
        WHEN 'AAPL' THEN 15500000000.0
        WHEN 'MSFT' THEN  7430000000.0
    END,
    'dividend_yield', CASE ticker
        WHEN 'AAPL' THEN 0.0055
        WHEN 'MSFT' THEN 0.0082
    END
)::text
WHERE listing_type = 'STOCK'
  AND ticker IN ('AAPL', 'MSFT');
