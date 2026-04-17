-- 000040_add_order_audit_log.up.sql
-- Audit trail za sve promene statusa naloga (NEW→APPROVED, APPROVED→DONE, itd.)
-- Komplementira core_banking.transakcija (novčani tokovi) sa order-level historijom.

CREATE TABLE core_banking.order_audit_log (
    id          BIGSERIAL    PRIMARY KEY,
    order_id    BIGINT       NOT NULL REFERENCES core_banking.orders(id),
    old_status  VARCHAR(20),
    new_status  VARCHAR(20)  NOT NULL,
    changed_by  BIGINT,
    changed_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    note        TEXT
);

CREATE INDEX idx_order_audit_log_order_id   ON core_banking.order_audit_log (order_id);
CREATE INDEX idx_order_audit_log_changed_at ON core_banking.order_audit_log (changed_at DESC);
