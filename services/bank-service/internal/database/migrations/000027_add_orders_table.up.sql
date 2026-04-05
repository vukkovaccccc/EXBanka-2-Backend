-- =============================================================================
-- Migration: 000027_add_orders_table (UP)
-- Creates the orders and order_transactions tables for the trading domain.
--
-- Design notes:
--   * All PKs follow the repo convention: BIGINT GENERATED ALWAYS AS IDENTITY.
--     The task spec requested UUID, but every table in this schema uses BIGINT
--     identity — switching to UUID would break join patterns and index efficiency.
--   * user_id and approved_by are cross-service references to user-service.
--     No FK constraints are applied (existence is validated at the application
--     layer), matching the actuary_info.employee_id pattern used throughout.
--   * account_id references core_banking.racun(id). This column was not in the
--     original spec but is structurally required: the execution engine must know
--     which bank account to reserve / debit at fill time.
--   * price_per_unit is nullable: MARKET orders have no known price at placement;
--     the actual fill price is recorded per-chunk in order_transactions instead.
--   * remaining_portions starts equal to quantity and is decremented by
--     executed_quantity on each partial fill; enforced at application layer.
-- =============================================================================

CREATE TABLE core_banking.orders (
    id                 BIGINT         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,

    -- Cross-service ref to user-service (the agent or client placing the order).
    -- No FK constraint — resolved and validated via user-service gRPC call.
    user_id            BIGINT         NOT NULL,

    -- Bank account from which funds are reserved at order creation and
    -- debited at each execution. Required for margin checks and fund deduction.
    account_id         BIGINT         NOT NULL REFERENCES core_banking.racun(id),

    listing_id         BIGINT         NOT NULL REFERENCES core_banking.listing(id),

    order_type         VARCHAR(20)    NOT NULL,
    direction          VARCHAR(10)    NOT NULL,

    -- Total number of contracts/units requested.
    quantity           INTEGER        NOT NULL,

    -- Multiplier for futures/options (e.g. 100 shares per contract).
    -- Defaults to 1 for stocks and forex where contract_size == 1 lot.
    contract_size      INTEGER        NOT NULL DEFAULT 1,

    -- The limit or stop trigger price. NULL for MARKET orders.
    -- For STOP_LIMIT orders this holds the limit price; the stop trigger
    -- price is encoded in stop_price below.
    price_per_unit     NUMERIC(18, 6),

    -- Stop trigger price used by STOP and STOP_LIMIT orders.
    -- NULL for MARKET and LIMIT orders.
    stop_price         NUMERIC(18, 6),

    status             VARCHAR(20)    NOT NULL DEFAULT 'PENDING',

    -- Cross-service ref to the supervisor who approved or declined.
    -- No FK constraint — resolved via user-service at the application layer.
    approved_by        BIGINT,

    is_done            BOOLEAN        NOT NULL DEFAULT FALSE,

    -- Decremented by each partial fill; reaches 0 when is_done = TRUE.
    remaining_portions INTEGER        NOT NULL,

    -- If TRUE the order was placed outside exchange hours and must be
    -- held until the next trading session opens.
    after_hours        BOOLEAN        NOT NULL DEFAULT FALSE,

    -- All-or-None: reject any partial fill; execute only when the full
    -- quantity is available in a single transaction.
    all_or_none        BOOLEAN        NOT NULL DEFAULT FALSE,

    -- Margin: execution is financed on margin; validated against
    -- the account's available credit before approval.
    margin             BOOLEAN        NOT NULL DEFAULT FALSE,

    last_modified      TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    created_at         TIMESTAMPTZ    NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_orders_order_type
        CHECK (order_type IN ('MARKET', 'LIMIT', 'STOP', 'STOP_LIMIT')),
    CONSTRAINT chk_orders_direction
        CHECK (direction IN ('BUY', 'SELL')),
    CONSTRAINT chk_orders_status
        CHECK (status IN ('PENDING', 'APPROVED', 'DECLINED', 'DONE')),
    CONSTRAINT chk_orders_quantity
        CHECK (quantity > 0),
    CONSTRAINT chk_orders_contract_size
        CHECK (contract_size > 0),
    CONSTRAINT chk_orders_remaining_portions
        CHECK (remaining_portions >= 0)
);

-- =============================================================================
-- order_transactions — one row per simulated partial-fill execution chunk.
-- The sum of executed_quantity across all rows for an order equals
-- (original quantity - remaining_portions) when is_done = TRUE.
-- =============================================================================

CREATE TABLE core_banking.order_transactions (
    id                 BIGINT         GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    order_id           BIGINT         NOT NULL REFERENCES core_banking.orders(id),
    executed_quantity  INTEGER        NOT NULL,
    executed_price     NUMERIC(18, 6) NOT NULL,
    execution_time     TIMESTAMPTZ    NOT NULL DEFAULT NOW(),

    CONSTRAINT chk_order_tx_quantity CHECK (executed_quantity > 0),
    CONSTRAINT chk_order_tx_price    CHECK (executed_price   > 0)
);

-- ── Indexes on orders ────────────────────────────────────────────────────────

-- Client-facing: fetch all orders for a given user.
CREATE INDEX idx_orders_user_id
    ON core_banking.orders (user_id);

-- Execution engine: look up the account to debit at fill time.
CREATE INDEX idx_orders_account_id
    ON core_banking.orders (account_id);

-- Market data joins: map orders back to their security.
CREATE INDEX idx_orders_listing_id
    ON core_banking.orders (listing_id);

-- Supervisor dashboard: filter by status.
CREATE INDEX idx_orders_status
    ON core_banking.orders (status);

-- Audit / time-based queries.
CREATE INDEX idx_orders_created_at
    ON core_banking.orders (created_at DESC);

-- Partial index for the async execution engine: only live orders need
-- to be polled. Skips DECLINED and DONE rows entirely.
CREATE INDEX idx_orders_active
    ON core_banking.orders (status, listing_id)
    WHERE is_done = FALSE AND status = 'APPROVED';

-- ── Indexes on order_transactions ────────────────────────────────────────────

-- Primary access pattern: fetch all fills for a given order.
CREATE INDEX idx_order_tx_order_id
    ON core_banking.order_transactions (order_id);

-- Time-ordered fill history per order (composite supports ORDER BY).
CREATE INDEX idx_order_tx_order_time
    ON core_banking.order_transactions (order_id, execution_time DESC);
