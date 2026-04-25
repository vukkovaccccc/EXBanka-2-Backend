package trading

import (
	"testing"

	"github.com/shopspring/decimal"
)

// ─── approxPrice ─────────────────────────────────────────────────────────────

func TestApproxPrice_Basic(t *testing.T) {
	result := approxPrice(1, decimal.NewFromFloat(10.0), 5)
	expected := decimal.NewFromFloat(50.0)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

func TestApproxPrice_ContractSizeMultiplier(t *testing.T) {
	// 100 contracts × $2.50 × 3 qty = 750
	result := approxPrice(100, decimal.NewFromFloat(2.5), 3)
	expected := decimal.NewFromFloat(750.0)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

func TestApproxPrice_ZeroQuantity(t *testing.T) {
	result := approxPrice(1, decimal.NewFromFloat(10.0), 0)
	if !result.Equal(decimal.Zero) {
		t.Errorf("expected 0 got %s", result)
	}
}

func TestApproxPrice_ZeroPrice(t *testing.T) {
	result := approxPrice(5, decimal.Zero, 10)
	if !result.Equal(decimal.Zero) {
		t.Errorf("expected 0 got %s", result)
	}
}

// ─── marketCommissionFor ──────────────────────────────────────────────────────

func TestMarketCommissionFor_BelowCap(t *testing.T) {
	// 14% of 10 = 1.4, below $7 cap
	result := marketCommissionFor(decimal.NewFromFloat(10.0))
	expected := decimal.NewFromFloat(1.4)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

func TestMarketCommissionFor_ExactlyAtCap(t *testing.T) {
	// 14% of 50 = 7.0, exactly at cap → returns pct (< condition is strict)
	result := marketCommissionFor(decimal.NewFromFloat(50.0))
	expected := decimal.NewFromFloat(7.0)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

func TestMarketCommissionFor_AboveCap(t *testing.T) {
	// 14% of 1000 = 140, capped at $7
	result := marketCommissionFor(decimal.NewFromFloat(1000.0))
	if !result.Equal(marketCommissionCap) {
		t.Errorf("expected cap %s got %s", marketCommissionCap, result)
	}
}

func TestMarketCommissionFor_SmallAmount(t *testing.T) {
	// 14% of 1 = 0.14
	result := marketCommissionFor(decimal.NewFromFloat(1.0))
	expected := decimal.NewFromFloat(0.14)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

// ─── limitCommissionFor ───────────────────────────────────────────────────────

func TestLimitCommissionFor_BelowCap(t *testing.T) {
	// 24% of 10 = 2.4, below $12 cap
	result := limitCommissionFor(decimal.NewFromFloat(10.0))
	expected := decimal.NewFromFloat(2.4)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

func TestLimitCommissionFor_AboveCap(t *testing.T) {
	// 24% of 1000 = 240, capped at $12
	result := limitCommissionFor(decimal.NewFromFloat(1000.0))
	if !result.Equal(limitCommissionCap) {
		t.Errorf("expected cap %s got %s", limitCommissionCap, result)
	}
}

func TestLimitCommissionFor_SmallAmount(t *testing.T) {
	// 24% of 2 = 0.48
	result := limitCommissionFor(decimal.NewFromFloat(2.0))
	expected := decimal.NewFromFloat(0.48)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}

// ─── calcMarketOrder ──────────────────────────────────────────────────────────

func TestCalcMarketOrder_BelowCap(t *testing.T) {
	// 1 × 5.0 × 2 = 10; 14% of 10 = 1.4
	notional, commission := calcMarketOrder(1, decimal.NewFromFloat(5.0), 2)
	if !notional.Equal(decimal.NewFromFloat(10.0)) {
		t.Errorf("notional: expected 10 got %s", notional)
	}
	if !commission.Equal(decimal.NewFromFloat(1.4)) {
		t.Errorf("commission: expected 1.4 got %s", commission)
	}
}

func TestCalcMarketOrder_CommissionCapped(t *testing.T) {
	// 1 × 100.0 × 5 = 500; 14% of 500 = 70 → capped at 7
	notional, commission := calcMarketOrder(1, decimal.NewFromFloat(100.0), 5)
	if !notional.Equal(decimal.NewFromFloat(500.0)) {
		t.Errorf("notional: expected 500 got %s", notional)
	}
	if !commission.Equal(decimal.NewFromFloat(7.0)) {
		t.Errorf("commission: expected cap 7 got %s", commission)
	}
}

// ─── calcLimitOrder ───────────────────────────────────────────────────────────

func TestCalcLimitOrder_BelowCap(t *testing.T) {
	// 1 × 5.0 × 2 = 10; 24% of 10 = 2.4
	notional, commission := calcLimitOrder(1, decimal.NewFromFloat(5.0), 2)
	if !notional.Equal(decimal.NewFromFloat(10.0)) {
		t.Errorf("notional: expected 10 got %s", notional)
	}
	if !commission.Equal(decimal.NewFromFloat(2.4)) {
		t.Errorf("commission: expected 2.4 got %s", commission)
	}
}

func TestCalcLimitOrder_CommissionCapped(t *testing.T) {
	// 1 × 200.0 × 5 = 1000; 24% = 240 → capped at 12
	notional, commission := calcLimitOrder(1, decimal.NewFromFloat(200.0), 5)
	if !notional.Equal(decimal.NewFromFloat(1000.0)) {
		t.Errorf("notional: expected 1000 got %s", notional)
	}
	if !commission.Equal(decimal.NewFromFloat(12.0)) {
		t.Errorf("commission: expected cap 12 got %s", commission)
	}
}

// ─── calcStopOrder ────────────────────────────────────────────────────────────

func TestCalcStopOrder_UsesMarketSchedule(t *testing.T) {
	// STOP uses MARKET schedule (min 14%, $7)
	// 1 × 5.0 × 2 = 10; 14% of 10 = 1.4
	notional, commission := calcStopOrder(1, decimal.NewFromFloat(5.0), 2)
	if !notional.Equal(decimal.NewFromFloat(10.0)) {
		t.Errorf("notional: expected 10 got %s", notional)
	}
	if !commission.Equal(decimal.NewFromFloat(1.4)) {
		t.Errorf("commission: expected 1.4 got %s", commission)
	}
}

func TestCalcStopOrder_CommissionCapped(t *testing.T) {
	// Large notional: cap at $7
	_, commission := calcStopOrder(1, decimal.NewFromFloat(1000.0), 10)
	if !commission.Equal(decimal.NewFromFloat(7.0)) {
		t.Errorf("expected cap 7 got %s", commission)
	}
}

// ─── calcStopLimitOrder ───────────────────────────────────────────────────────

func TestCalcStopLimitOrder_UsesLimitSchedule(t *testing.T) {
	// STOP_LIMIT uses LIMIT schedule (min 24%, $12)
	// 1 × 5.0 × 2 = 10; 24% of 10 = 2.4
	notional, commission := calcStopLimitOrder(1, decimal.NewFromFloat(5.0), 2)
	if !notional.Equal(decimal.NewFromFloat(10.0)) {
		t.Errorf("notional: expected 10 got %s", notional)
	}
	if !commission.Equal(decimal.NewFromFloat(2.4)) {
		t.Errorf("commission: expected 2.4 got %s", commission)
	}
}

func TestCalcStopLimitOrder_CommissionCapped(t *testing.T) {
	_, commission := calcStopLimitOrder(1, decimal.NewFromFloat(1000.0), 10)
	if !commission.Equal(decimal.NewFromFloat(12.0)) {
		t.Errorf("expected cap 12 got %s", commission)
	}
}

// ─── Exported helpers ─────────────────────────────────────────────────────────

func TestCalcMarketCommission_Exported(t *testing.T) {
	c := CalcMarketCommission(decimal.NewFromFloat(10.0))
	if !c.Equal(decimal.NewFromFloat(1.4)) {
		t.Errorf("expected 1.4 got %s", c)
	}
}

func TestCalcMarketCommission_Exported_Capped(t *testing.T) {
	c := CalcMarketCommission(decimal.NewFromFloat(5000.0))
	if !c.Equal(decimal.NewFromFloat(7.0)) {
		t.Errorf("expected 7 got %s", c)
	}
}

func TestCalcLimitCommission_Exported(t *testing.T) {
	c := CalcLimitCommission(decimal.NewFromFloat(10.0))
	if !c.Equal(decimal.NewFromFloat(2.4)) {
		t.Errorf("expected 2.4 got %s", c)
	}
}

func TestCalcLimitCommission_Exported_Capped(t *testing.T) {
	c := CalcLimitCommission(decimal.NewFromFloat(5000.0))
	if !c.Equal(decimal.NewFromFloat(12.0)) {
		t.Errorf("expected 12 got %s", c)
	}
}

func TestCalcInitialMarginCost_Basic(t *testing.T) {
	// 100 × 1.1 = 110
	result := CalcInitialMarginCost(decimal.NewFromFloat(100.0))
	if !result.Equal(decimal.NewFromFloat(110.0)) {
		t.Errorf("expected 110 got %s", result)
	}
}

func TestCalcInitialMarginCost_Zero(t *testing.T) {
	result := CalcInitialMarginCost(decimal.Zero)
	if !result.Equal(decimal.Zero) {
		t.Errorf("expected 0 got %s", result)
	}
}

func TestCalcInitialMarginCost_Fractional(t *testing.T) {
	// 50.50 × 1.1 = 55.55
	result := CalcInitialMarginCost(decimal.NewFromFloat(50.50))
	expected := decimal.NewFromFloat(55.55)
	if !result.Equal(expected) {
		t.Errorf("expected %s got %s", expected, result)
	}
}
