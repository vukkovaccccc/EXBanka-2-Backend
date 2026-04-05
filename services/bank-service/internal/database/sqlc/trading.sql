-- =============================================================================
-- SQLC queries for orders & order_transactions — bank-service trading domain.
-- Run `sqlc generate` from services/bank-service/ to regenerate Go code.
--
-- Naming convention:
--   :one   → returns a single row  (sql.ErrNoRows if not found)
--   :exec  → returns no rows       (only checks for execution error)
--   :many  → returns []Row
-- =============================================================================


-- ── orders ───────────────────────────────────────────────────────────────────

-- name: CreateOrder :one
-- Inserts a new order and returns the full row.
-- remaining_portions must equal quantity at creation time (enforced by caller).
-- The caller is responsible for validating that user_id exists in user-service
-- and that account_id belongs to that user before calling this query.
INSERT INTO core_banking.orders (
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    remaining_portions,
    after_hours,
    all_or_none,
    margin
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
RETURNING
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at;

-- name: GetOrderById :one
-- Returns the full order row for the given PK.
-- Returns sql.ErrNoRows when no matching record exists.
SELECT
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at
FROM core_banking.orders
WHERE id = $1;

-- name: UpdateOrderStatus :one
-- Updates the status and optional approver of an order.
-- Used by: supervisor approve/decline actions, and the system auto-decline
-- workflow (expired settlement dates). Bumps last_modified atomically.
-- Returns sql.ErrNoRows when the given id does not exist.
UPDATE core_banking.orders
SET
    status        = $2,
    approved_by   = $3,
    last_modified = NOW()
WHERE id = $1
RETURNING
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at;

-- name: UpdateOrderRemainingPortions :one
-- Decrements remaining_portions and flips is_done when the order is fully
-- filled. Called by the async execution engine after each partial fill is
-- recorded in order_transactions.
-- The caller must ensure new_remaining >= 0 before calling.
UPDATE core_banking.orders
SET
    remaining_portions = $2,
    is_done            = $3,
    last_modified      = NOW()
WHERE id = $1
RETURNING
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at;

-- name: ListOrdersByUserId :many
-- Returns all orders placed by a specific user, newest first.
-- Used by the client-facing order history endpoint.
SELECT
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at
FROM core_banking.orders
WHERE user_id = $1
ORDER BY created_at DESC;

-- name: ListOrdersByStatus :many
-- Returns all orders matching the given status, newest first.
-- Used by the supervisor dashboard to list PENDING orders awaiting approval
-- and by the system worker to find APPROVED orders ready for execution.
-- Pass sqlc.narg to allow NULL (returns all statuses) or a specific status string.
SELECT
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at
FROM core_banking.orders
WHERE (sqlc.narg('status')::VARCHAR IS NULL OR status = sqlc.narg('status'))
ORDER BY created_at DESC;

-- name: ListActiveOrdersByListing :many
-- Returns all APPROVED, non-finished orders for a given listing.
-- Called by the execution engine on each market-data tick to find orders
-- whose price conditions have been met (uses idx_orders_active partial index).
SELECT
    id,
    user_id,
    account_id,
    listing_id,
    order_type,
    direction,
    quantity,
    contract_size,
    price_per_unit,
    stop_price,
    status,
    approved_by,
    is_done,
    remaining_portions,
    after_hours,
    all_or_none,
    margin,
    last_modified,
    created_at
FROM core_banking.orders
WHERE listing_id = $1
  AND status     = 'APPROVED'
  AND is_done    = FALSE
ORDER BY created_at ASC;


-- ── order_transactions ────────────────────────────────────────────────────────

-- name: CreateOrderTransaction :one
-- Records a single partial-fill execution chunk.
-- After inserting, the caller must call UpdateOrderRemainingPortions in the
-- same database transaction to keep orders.remaining_portions consistent.
INSERT INTO core_banking.order_transactions (
    order_id,
    executed_quantity,
    executed_price
) VALUES (
    $1, $2, $3
)
RETURNING
    id,
    order_id,
    executed_quantity,
    executed_price,
    execution_time;

-- name: GetTransactionsByOrderId :many
-- Returns all execution chunks for a given order, oldest fill first.
-- Used to reconstruct the full execution history and compute average fill price.
SELECT
    id,
    order_id,
    executed_quantity,
    executed_price,
    execution_time
FROM core_banking.order_transactions
WHERE order_id = $1
ORDER BY execution_time ASC;
