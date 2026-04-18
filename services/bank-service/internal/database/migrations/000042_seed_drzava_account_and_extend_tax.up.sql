-- =============================================================================
-- Migration: 000042_seed_drzava_account_and_extend_tax
-- Service:   bank-service
-- Schema:    core_banking
--
-- Two related concerns bundled because they share the same spec rule:
--
--   1. Open the mandatory RSD account for "Država" — the cross-service CLIENT
--      user seeded by user-service/000009_seed_drzava_user (drzava@exbanka.rs).
--      All capital-gains tax collected by bank-service is credited here.
--
--   2. Extend core_banking.tax_records with audit / snapshot columns and
--      replace the old UNIQUE(user_id, account_id, year, month) with a
--      period-based UNIQUE so that a new "rollover" record can be created
--      within the same month after a manual collection has already marked
--      an earlier record as paid=TRUE.
--
-- Cross-service user-id note:
--   The user-service migration chain produces ids in this order:
--     000001 admin@raf.rs         → id = 1
--     000002 trezor@exbanka.rs    → id = 2
--     000007 kknezevic4622rn@raf.rs → id = 3
--     000007 kseniakenny@gmail.com → id = 4
--     000009 drzava@exbanka.rs    → id = 5
--   Bank-service cannot FK to user-service tables, so id=5 is hardcoded here
--   following the same convention used in 000010_seed_banka_firma.up.sql.
--   The runtime resolver in TaxService looks the account up by the owner's
--   email (via user-service gRPC), so if the id ever drifts, no code change
--   is needed — only this seed row has to be re-applied with the new id.
-- =============================================================================

-- ─── 1. RSD račun države ─────────────────────────────────────────────────────
-- broj_racuna derivation (bankCode 666 + branchCode 0001 + TEKUCI 12 + 8 digits + Luhn-mod-11 check digit):
--   prefix = 666000112 + 00000000 → digitSum = 21, check = (11 - 21 % 11) % 11 = 1
--   final  = 666000112000000001

INSERT INTO core_banking.racun (
    broj_racuna,
    id_zaposlenog,
    id_vlasnika,
    id_firme,
    id_valute,
    kategorija_racuna,
    vrsta_racuna,
    podvrsta,
    naziv_racuna,
    stanje_racuna,
    datum_kreiranja,
    datum_isteka,
    status
)
SELECT
    '666000112000000001',
    2,                                -- id_zaposlenog = trezor@exbanka.rs (banking operator)
    5,                                -- id_vlasnika   = drzava@exbanka.rs  (see note above)
    NULL,                             -- personal account (no firma)
    v.id,
    'TEKUCI',
    'LICNI',
    'STANDARDNI',
    'Država — Prihodi od poreza',
    0,
    NOW(),
    NOW() + INTERVAL '100 years',
    'AKTIVAN'
FROM core_banking.valuta v
WHERE v.oznaka = 'RSD'
ON CONFLICT (broj_racuna) DO NOTHING;

-- ─── 2. Ne dozvoliti višestruke račune države (application-level guard) ─────
-- Partial unique index garantuje da korisnik id=5 može imati samo jedan račun
-- bez obzira koliko puta se ova migracija ili paralelno seed logika primene.
CREATE UNIQUE INDEX IF NOT EXISTS uq_drzava_single_account
    ON core_banking.racun (id_vlasnika)
    WHERE id_vlasnika = 5;

-- ─── 3. Proširenje tax_records ───────────────────────────────────────────────
-- Nove kolone evidentiraju ulazne vrednosti obračuna za auditability
-- i podržavaju rollover unutar istog meseca.

ALTER TABLE core_banking.tax_records
    ADD COLUMN IF NOT EXISTS period_start             TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS period_end               TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS profit_base_amount       NUMERIC(18, 4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS source_currency          VARCHAR(10)    NOT NULL DEFAULT 'USD',
    ADD COLUMN IF NOT EXISTS exchange_rate_used       NUMERIC(18, 6),
    ADD COLUMN IF NOT EXISTS taxable_transactions_cnt INTEGER        NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS updated_at               TIMESTAMPTZ    NOT NULL DEFAULT NOW();

-- Popuni period_start/period_end za postojeće redove (first ≤ period ≤ last)
UPDATE core_banking.tax_records
SET period_start = make_timestamptz(year, month, 1, 0, 0, 0),
    period_end   = make_timestamptz(year, month, 1, 0, 0, 0)
                   + INTERVAL '1 month' - INTERVAL '1 microsecond'
WHERE period_start IS NULL OR period_end IS NULL;

ALTER TABLE core_banking.tax_records
    ALTER COLUMN period_start SET NOT NULL,
    ALTER COLUMN period_end   SET NOT NULL;

-- ── Zameni stari UNIQUE (year, month) novim (period_start, period_end) ─────
-- Stari constraint ne dozvoljava rollover u istom mesecu; novi dozvoljava
-- više redova ako su period_end/period_start različiti (različiti prozori).
ALTER TABLE core_banking.tax_records
    DROP CONSTRAINT IF EXISTS uq_tax_user_account_period;

ALTER TABLE core_banking.tax_records
    ADD CONSTRAINT uq_tax_user_account_window
        UNIQUE (user_id, account_id, period_start, period_end);

CREATE INDEX IF NOT EXISTS idx_tax_records_period_end
    ON core_banking.tax_records (period_end);
