-- =============================================================================
-- Migration: 000043_add_tax_record_status_and_payment_tracking (DOWN)
-- Uklanja statusni lifecycle i tracking polja naplate poreza na kapitalnu dobit.
-- =============================================================================

DROP INDEX IF EXISTS core_banking.idx_tax_records_status_open;

ALTER TABLE core_banking.tax_records
    DROP CONSTRAINT IF EXISTS chk_tax_status;

ALTER TABLE core_banking.tax_records
    DROP COLUMN IF EXISTS triggered_by,
    DROP COLUMN IF EXISTS last_attempt_at,
    DROP COLUMN IF EXISTS collection_attempts,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS remaining_debt_rsd,
    DROP COLUMN IF EXISTS paid_amount_rsd;
