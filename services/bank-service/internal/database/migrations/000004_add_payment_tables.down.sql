-- =============================================================================
-- Migration: 000004_add_payment_tables (rollback)
-- =============================================================================

DROP TABLE IF EXISTS core_banking.payment_intent;
DROP TABLE IF EXISTS core_banking.payment_recipient;

-- Vraćamo pending_action type check na originalni.
ALTER TABLE core_banking.pending_action
    DROP CONSTRAINT IF EXISTS pending_action_type_check,
    ADD CONSTRAINT pending_action_type_check
        CHECK (action_type IN ('PROMENA_LIMITA'));
