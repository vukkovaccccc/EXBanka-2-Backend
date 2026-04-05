-- =============================================================================
-- Migration: 000027_add_orders_table (DOWN)
-- Drops the trading domain tables in reverse-dependency order.
-- order_transactions must be dropped before orders (FK constraint).
-- =============================================================================

DROP TABLE IF EXISTS core_banking.order_transactions;
DROP TABLE IF EXISTS core_banking.orders;
