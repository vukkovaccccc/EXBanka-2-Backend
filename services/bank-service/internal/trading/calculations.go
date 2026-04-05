package trading

// =============================================================================
// calculations.go — pure, stateless business-logic functions.
//
// All functions in this file are side-effect-free: they accept values and
// return values with no I/O, no DB calls, and no shared state.  This makes
// them trivially unit-testable without any mocking infrastructure.
//
// Commission rules (Sprint 5 spec):
//   MARKET     order: min( 14% × approxPrice,  $7 )
//   LIMIT      order: min( 24% × approxPrice, $12 )
//   STOP       order: min( 14% × approxPrice,  $7 )  — same as MARKET (converts on trigger)
//   STOP_LIMIT order: min( 24% × approxPrice, $12 )  — same as LIMIT  (converts on trigger)
//
// Approximate price formula (all order types):
//   approxPrice = ContractSize × PricePerUnit × Quantity
//
// Where PricePerUnit is sourced as follows:
//   MARKET BUY   → current Ask of the listing (fetched from DB)
//   MARKET SELL  → current Bid of the listing (fetched from DB)
//   LIMIT        → user-supplied Limit Value (PricePerUnit)
//   STOP         → user-supplied Stop Value  (StopPrice)
//   STOP_LIMIT   → user-supplied Limit Value (PricePerUnit); Stop Value is only the trigger
//
// Initial margin cost (when Margin=true):
//   InitialMarginCost = MaintenanceMargin × 1.1
// =============================================================================

import "github.com/shopspring/decimal"

// ─── Package-level constants ──────────────────────────────────────────────────
//
// Declared as decimal.Decimal (not float64) to avoid floating-point drift
// when they participate in financial arithmetic.

var (
	marketCommissionRate = decimal.NewFromFloat(0.14) // 14 %
	marketCommissionCap  = decimal.NewFromFloat(7.0)  // $7

	limitCommissionRate = decimal.NewFromFloat(0.24) // 24 %
	limitCommissionCap  = decimal.NewFromFloat(12.0) // $12
)

// ─── Core formula ─────────────────────────────────────────────────────────────

// approxPrice computes the total notional value of an order:
//
//	ContractSize × pricePerUnit × Quantity
//
// This formula is shared across all order types; the only difference is how
// pricePerUnit is sourced (market data vs. user input).
func approxPrice(contractSize int32, pricePerUnit decimal.Decimal, quantity int32) decimal.Decimal {
	cs := decimal.NewFromInt(int64(contractSize))
	qty := decimal.NewFromInt(int64(quantity))
	return cs.Mul(pricePerUnit).Mul(qty)
}

// ─── Commission helpers ───────────────────────────────────────────────────────

// marketCommissionFor returns the fee for a MARKET order:
//
//	min( 14% × notional, $7 )
func marketCommissionFor(notional decimal.Decimal) decimal.Decimal {
	pct := notional.Mul(marketCommissionRate)
	if pct.LessThan(marketCommissionCap) {
		return pct
	}
	return marketCommissionCap
}

// limitCommissionFor returns the fee for a LIMIT order:
//
//	min( 24% × notional, $12 )
func limitCommissionFor(notional decimal.Decimal) decimal.Decimal {
	pct := notional.Mul(limitCommissionRate)
	if pct.LessThan(limitCommissionCap) {
		return pct
	}
	return limitCommissionCap
}

// ─── Order-type calculations ──────────────────────────────────────────────────

// calcMarketOrder returns the approximate price and commission for a MARKET order.
//
// pricePerUnit must already be resolved to the listing's Ask (BUY) or Bid (SELL)
// by the caller — this function is agnostic to direction.
func calcMarketOrder(contractSize int32, pricePerUnit decimal.Decimal, quantity int32) (notional, commission decimal.Decimal) {
	notional = approxPrice(contractSize, pricePerUnit, quantity)
	commission = marketCommissionFor(notional)
	return
}

// calcLimitOrder returns the approximate price and commission for a LIMIT order.
//
// pricePerUnit is the user-supplied limit value.
func calcLimitOrder(contractSize int32, pricePerUnit decimal.Decimal, quantity int32) (notional, commission decimal.Decimal) {
	notional = approxPrice(contractSize, pricePerUnit, quantity)
	commission = limitCommissionFor(notional)
	return
}

// calcStopOrder returns the approximate price and commission for a STOP order.
//
// Per spec, the pre-placement preview uses the user-supplied Stop Value as the
// effective price per unit.  Commission follows the MARKET schedule because a
// STOP order converts into a MARKET order once the trigger price is reached.
//
// stopPrice is the user-supplied stop trigger value.
func calcStopOrder(contractSize int32, stopPrice decimal.Decimal, quantity int32) (notional, commission decimal.Decimal) {
	notional = approxPrice(contractSize, stopPrice, quantity)
	commission = marketCommissionFor(notional) // MARKET schedule: min(14%, $7)
	return
}

// calcStopLimitOrder returns the approximate price and commission for a
// STOP_LIMIT order.
//
// Per spec, the pre-placement preview uses the user-supplied Limit Value as
// the effective price per unit.  The Stop Value is only the activation
// trigger and does not appear in the notional calculation.  Commission follows
// the LIMIT schedule because the order converts to a LIMIT order on activation.
//
// limitPrice is the user-supplied limit value (PricePerUnit), not the stop trigger.
func calcStopLimitOrder(contractSize int32, limitPrice decimal.Decimal, quantity int32) (notional, commission decimal.Decimal) {
	notional = approxPrice(contractSize, limitPrice, quantity)
	commission = limitCommissionFor(notional) // LIMIT schedule: min(24%, $12)
	return
}

// CalcInitialMarginCost computes the capital an account must hold to open a
// margin position.  The initial margin is set to 110 % of the maintenance
// margin to provide a buffer above the liquidation threshold.
//
//	InitialMarginCost = maintenanceMargin × 1.1
//
// Exported so it can be called from outside the package (e.g., a dedicated
// margin-management module or handler-level tests).
func CalcInitialMarginCost(maintenanceMargin decimal.Decimal) decimal.Decimal {
	return maintenanceMargin.Mul(decimal.NewFromFloat(1.1))
}
