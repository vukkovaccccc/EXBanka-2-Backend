-- =============================================================================
-- Migration: 000035_seed_option_listings
-- Service:   bank-service
-- Schema:    core_banking
--
-- Uklanja pogrešno ugnježdenu "options" matricu iz details_json polja na
-- STOCK listinzima za AAPL i MSFT.
--
-- Opcije su bile pogrešno smeštene kao JSON niz unutar details_json matičnog
-- STOCK listinga. Ispravno rešenje je da svaka opcija bude zaseban red u
-- tabeli listing sa listing_type='OPTION' — što worker sada radi automatski
-- pri pokretanju dohvatajući realne podatke sa Yahoo Finance.
-- =============================================================================

UPDATE core_banking.listing
SET details_json = (details_json::jsonb - 'options')::text
WHERE listing_type = 'STOCK'
  AND ticker IN ('AAPL', 'MSFT')
  AND details_json::jsonb ? 'options';
