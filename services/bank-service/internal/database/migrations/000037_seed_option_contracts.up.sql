-- =============================================================================
-- Migration: 000037_seed_option_contracts
-- Service:   bank-service
-- Schema:    core_banking
--
-- Briše eventualne statički seeded opcije sa fiksnim datumima isteka
-- (artefakti prethodnih pristupa) kako bi worker pri pokretanju generisao
-- svež opcijski lanac koristeći Black-Scholes model sa aktuelnim cenama i
-- dinamičkim datumima isteka per specifikacije (Pristup 2).
-- =============================================================================

-- Brisanje opcija sa hardkodovanim datumom 2026-07-17 koje su mogle biti
-- unesene prethodnom verzijom migracije (ON CONFLICT DO NOTHING čuva ostale).
DELETE FROM core_banking.listing
WHERE listing_type = 'OPTION'
  AND details_json::jsonb->>'settlement_date' = '2026-07-17';
