-- =============================================================================
-- Migration: 000043_add_tax_record_status_and_payment_tracking (UP)
-- Service:   bank-service
-- Schema:    core_banking
--
-- Dovršava tax_records tabelu tako da podržava:
--   - statusni lifecycle (OPEN → COLLECTED | PARTIAL | UNPAID)
--   - delimičnu naplatu (paid_amount_rsd, remaining_debt_rsd)
--   - evidenciju pokušaja naplate (collection_attempts, last_attempt_at)
--   - razlikovanje izvora obračuna (CRON vs MANUAL) radi audit traga
--
-- Stara kolona `paid` / `paid_at` se zadržava kao denormalizovano ogledalo koje
-- TaxService drži u sinhronizaciji sa `status` radi backward-compat sa bilo kojim
-- izveštajem koji i dalje čita `paid`.
-- =============================================================================

ALTER TABLE core_banking.tax_records
    ADD COLUMN IF NOT EXISTS paid_amount_rsd      NUMERIC(18, 4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS remaining_debt_rsd   NUMERIC(18, 4) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS status               VARCHAR(16)    NOT NULL DEFAULT 'OPEN',
    ADD COLUMN IF NOT EXISTS collection_attempts  INTEGER        NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_attempt_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS triggered_by         VARCHAR(16);

-- Backfill postojećih redova: paid=TRUE → COLLECTED, paid=FALSE → OPEN.
UPDATE core_banking.tax_records
SET status             = CASE WHEN paid THEN 'COLLECTED' ELSE 'OPEN' END,
    paid_amount_rsd    = CASE WHEN paid THEN amount_rsd ELSE 0 END,
    remaining_debt_rsd = CASE WHEN paid THEN 0 ELSE amount_rsd END
WHERE status = 'OPEN' AND paid_amount_rsd = 0 AND remaining_debt_rsd = 0;

-- Guard nad dozvoljenim vrednostima statusa.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'chk_tax_status'
    ) THEN
        ALTER TABLE core_banking.tax_records
            ADD CONSTRAINT chk_tax_status
            CHECK (status IN ('OPEN', 'COLLECTED', 'PARTIAL', 'UNPAID'));
    END IF;
END $$;

-- Index za brzo listanje svih nenaplaćenih obračuna u porez tracking portalu.
CREATE INDEX IF NOT EXISTS idx_tax_records_status_open
    ON core_banking.tax_records (status)
    WHERE status <> 'COLLECTED';
