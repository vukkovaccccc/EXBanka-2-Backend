-- Audit trail when a supervisor changes an agent's daily limit (Scenario 3).
CREATE TABLE core_banking.actuary_limit_audit (
    id                  BIGSERIAL PRIMARY KEY,
    actor_employee_id   BIGINT         NOT NULL,
    target_employee_id  BIGINT         NOT NULL,
    old_limit           NUMERIC(15, 2) NOT NULL,
    new_limit           NUMERIC(15, 2) NOT NULL,
    created_at          TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_actuary_limit_audit_target
    ON core_banking.actuary_limit_audit (target_employee_id);

CREATE INDEX idx_actuary_limit_audit_created
    ON core_banking.actuary_limit_audit (created_at DESC);
