package worker

import (
	"errors"
	"testing"
	"time"

	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
)

// ─── commissionForOrderType ───────────────────────────────────────────────────

func TestCommissionForOrderType_Market(t *testing.T) {
	notional := decimal.NewFromFloat(100.0)
	c := commissionForOrderType(trading.OrderTypeMarket, notional)
	expected := trading.CalcMarketCommission(notional)
	if !c.Equal(expected) {
		t.Errorf("MARKET: expected %s, got %s", expected, c)
	}
}

func TestCommissionForOrderType_Stop(t *testing.T) {
	notional := decimal.NewFromFloat(100.0)
	c := commissionForOrderType(trading.OrderTypeStop, notional)
	expected := trading.CalcMarketCommission(notional)
	if !c.Equal(expected) {
		t.Errorf("STOP: expected market schedule %s, got %s", expected, c)
	}
}

func TestCommissionForOrderType_Limit(t *testing.T) {
	notional := decimal.NewFromFloat(100.0)
	c := commissionForOrderType(trading.OrderTypeLimit, notional)
	expected := trading.CalcLimitCommission(notional)
	if !c.Equal(expected) {
		t.Errorf("LIMIT: expected %s, got %s", expected, c)
	}
}

func TestCommissionForOrderType_StopLimit(t *testing.T) {
	notional := decimal.NewFromFloat(100.0)
	c := commissionForOrderType(trading.OrderTypeStopLimit, notional)
	expected := trading.CalcLimitCommission(notional)
	if !c.Equal(expected) {
		t.Errorf("STOP_LIMIT: expected %s, got %s", expected, c)
	}
}

func TestCommissionForOrderType_Unknown(t *testing.T) {
	c := commissionForOrderType("UNKNOWN", decimal.NewFromFloat(100.0))
	if !c.IsZero() {
		t.Errorf("unknown type: expected zero, got %s", c)
	}
}

// ─── isSettlementExpired ──────────────────────────────────────────────────────

func TestIsSettlementExpired_NilDate(t *testing.T) {
	snap := MarketSnapshot{SettlementDate: nil}
	if isSettlementExpired(snap) {
		t.Error("nil SettlementDate should not be expired")
	}
}

func TestIsSettlementExpired_PastDate(t *testing.T) {
	past := time.Now().UTC().Add(-24 * time.Hour)
	snap := MarketSnapshot{SettlementDate: &past}
	if !isSettlementExpired(snap) {
		t.Error("past date should be expired")
	}
}

func TestIsSettlementExpired_FutureDate(t *testing.T) {
	future := time.Now().UTC().Add(24 * time.Hour)
	snap := MarketSnapshot{SettlementDate: &future}
	if isSettlementExpired(snap) {
		t.Error("future date should not be expired")
	}
}

// ─── calcWaitSeconds ──────────────────────────────────────────────────────────

func TestCalcWaitSeconds_ZeroVolume(t *testing.T) {
	w := calcWaitSeconds(0, 5)
	if w != defaultWaitSeconds {
		t.Errorf("zero volume: expected defaultWaitSeconds %f, got %f", defaultWaitSeconds, w)
	}
}

func TestCalcWaitSeconds_ZeroRemaining(t *testing.T) {
	w := calcWaitSeconds(1000, 0)
	if w != defaultWaitSeconds {
		t.Errorf("zero remaining: expected defaultWaitSeconds %f, got %f", defaultWaitSeconds, w)
	}
}

func TestCalcWaitSeconds_Normal(t *testing.T) {
	// Should return a non-negative value within [0, maxWaitSeconds]
	w := calcWaitSeconds(1000, 10)
	if w < 0 {
		t.Errorf("wait seconds must be non-negative, got %f", w)
	}
	if w > maxWaitSeconds {
		t.Errorf("wait seconds must be <= maxWaitSeconds (%f), got %f", maxWaitSeconds, w)
	}
}

func TestCalcWaitSeconds_ClampedToMin(t *testing.T) {
	// Very high volume, low remaining → small maxWait → clamp to minWaitSeconds
	// volume=1e9, remaining=1 → turnsPerPortion=1e9 → maxWait=1440/1e9 << minWaitSeconds
	for i := 0; i < 10; i++ {
		w := calcWaitSeconds(1_000_000_000, 1)
		if w > minWaitSeconds*2 {
			t.Errorf("expected near-min wait, got %f", w)
		}
	}
}

// ─── calcChunkSize ────────────────────────────────────────────────────────────

func TestCalcChunkSize_LessThan10(t *testing.T) {
	for i := 0; i < 20; i++ {
		c := calcChunkSize(5)
		if c < 1 || c > 5 {
			t.Errorf("expected [1,5], got %d", c)
		}
	}
}

func TestCalcChunkSize_MoreThan10(t *testing.T) {
	for i := 0; i < 20; i++ {
		c := calcChunkSize(50)
		if c < 1 || c > 10 {
			t.Errorf("expected [1,10] for remaining=50, got %d", c)
		}
	}
}

func TestCalcChunkSize_One(t *testing.T) {
	c := calcChunkSize(1)
	if c != 1 {
		t.Errorf("remaining=1: expected 1, got %d", c)
	}
}

// ─── resolveExecutionPrice ────────────────────────────────────────────────────

func TestResolveExecutionPrice_Market_Buy(t *testing.T) {
	order := &trading.Order{
		ContractSize: 2,
		Direction:    trading.OrderDirectionBuy,
		OrderType:    trading.OrderTypeMarket,
	}
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	price, err := resolveExecutionPrice(order, trading.OrderTypeMarket, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 × Ask(10) = 20
	expected := decimal.NewFromFloat(20.0)
	if !price.Equal(expected) {
		t.Errorf("expected 20, got %s", price)
	}
}

func TestResolveExecutionPrice_Market_Sell(t *testing.T) {
	order := &trading.Order{
		ContractSize: 2,
		Direction:    trading.OrderDirectionSell,
		OrderType:    trading.OrderTypeMarket,
	}
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	price, err := resolveExecutionPrice(order, trading.OrderTypeMarket, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 × Bid(9) = 18
	expected := decimal.NewFromFloat(18.0)
	if !price.Equal(expected) {
		t.Errorf("expected 18, got %s", price)
	}
}

func TestResolveExecutionPrice_Limit_Buy_LimitBeforeAsk(t *testing.T) {
	limit := decimal.NewFromFloat(8.0)
	order := &trading.Order{
		ContractSize: 1,
		Direction:    trading.OrderDirectionBuy,
		PricePerUnit: &limit,
	}
	// Ask=10, limit=8 → min(8,10)=8
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	price, err := resolveExecutionPrice(order, trading.OrderTypeLimit, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !price.Equal(decimal.NewFromFloat(8.0)) {
		t.Errorf("expected 8, got %s", price)
	}
}

func TestResolveExecutionPrice_Limit_Sell_LimitAboveBid(t *testing.T) {
	limit := decimal.NewFromFloat(12.0)
	order := &trading.Order{
		ContractSize: 1,
		Direction:    trading.OrderDirectionSell,
		PricePerUnit: &limit,
	}
	// Bid=9, limit=12 → max(12,9)=12
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	price, err := resolveExecutionPrice(order, trading.OrderTypeLimit, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !price.Equal(decimal.NewFromFloat(12.0)) {
		t.Errorf("expected 12, got %s", price)
	}
}

func TestResolveExecutionPrice_Limit_NilPrice(t *testing.T) {
	order := &trading.Order{
		ContractSize: 1,
		Direction:    trading.OrderDirectionBuy,
		PricePerUnit: nil,
	}
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	_, err := resolveExecutionPrice(order, trading.OrderTypeLimit, snap)
	if !errors.Is(err, trading.ErrLimitPriceRequired) {
		t.Errorf("expected ErrLimitPriceRequired, got %v", err)
	}
}

func TestResolveExecutionPrice_UnknownType(t *testing.T) {
	order := &trading.Order{ContractSize: 1, Direction: trading.OrderDirectionBuy}
	snap := MarketSnapshot{Ask: 10.0}
	_, err := resolveExecutionPrice(order, "UNKNOWN", snap)
	if !errors.Is(err, trading.ErrInvalidOrderType) {
		t.Errorf("expected ErrInvalidOrderType, got %v", err)
	}
}

// ─── isForexDeclineError ──────────────────────────────────────────────────────

func TestIsForexDeclineError_AccountNotFound(t *testing.T) {
	if !isForexDeclineError(trading.ErrForexAccountNotFound) {
		t.Error("ErrForexAccountNotFound should be a decline error")
	}
}

func TestIsForexDeclineError_CurrencyMismatch(t *testing.T) {
	if !isForexDeclineError(trading.ErrForexCurrencyMismatch) {
		t.Error("ErrForexCurrencyMismatch should be a decline error")
	}
}

func TestIsForexDeclineError_SameAccount(t *testing.T) {
	if !isForexDeclineError(trading.ErrForexSameAccount) {
		t.Error("ErrForexSameAccount should be a decline error")
	}
}

func TestIsForexDeclineError_SameCurrency(t *testing.T) {
	if !isForexDeclineError(trading.ErrForexSameCurrency) {
		t.Error("ErrForexSameCurrency should be a decline error")
	}
}

func TestIsForexDeclineError_InsufficientFunds(t *testing.T) {
	if !isForexDeclineError(trading.ErrInsufficientFunds) {
		t.Error("ErrInsufficientFunds should be a decline error")
	}
}

func TestIsForexDeclineError_OtherError(t *testing.T) {
	if isForexDeclineError(errors.New("random error")) {
		t.Error("generic error should not be a decline error")
	}
}

// ─── resolveExecutionPrice — missing branches ─────────────────────────────────

func TestResolveExecutionPrice_Limit_Buy_LimitAboveAsk(t *testing.T) {
	limit := decimal.NewFromFloat(15.0)
	order := &trading.Order{
		ContractSize: 2,
		Direction:    trading.OrderDirectionBuy,
		PricePerUnit: &limit,
	}
	// Ask=10, limit=15 → min(15,10)=10 (Ask is cheaper)
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	price, err := resolveExecutionPrice(order, trading.OrderTypeLimit, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 2 × 10 = 20
	expected := decimal.NewFromFloat(20.0)
	if !price.Equal(expected) {
		t.Errorf("expected 20, got %s", price)
	}
}

func TestResolveExecutionPrice_Limit_Sell_LimitBelowBid(t *testing.T) {
	limit := decimal.NewFromFloat(7.0)
	order := &trading.Order{
		ContractSize: 1,
		Direction:    trading.OrderDirectionSell,
		PricePerUnit: &limit,
	}
	// Bid=9, limit=7 → max(7,9)=9 (Bid is better for seller)
	snap := MarketSnapshot{Ask: 10.0, Bid: 9.0}
	price, err := resolveExecutionPrice(order, trading.OrderTypeLimit, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := decimal.NewFromFloat(9.0)
	if !price.Equal(expected) {
		t.Errorf("expected 9, got %s", price)
	}
}

// ─── calcWaitSeconds — max clamp path ────────────────────────────────────────

func TestCalcWaitSeconds_ClampedToMax(t *testing.T) {
	// Low volume, high remaining → maxWait >> maxWaitSeconds → clamped to maxWaitSeconds
	// volume=1, remaining=100 → turnsPerPortion=0.01 → maxWait=144000 > 86400
	for i := 0; i < 5; i++ {
		w := calcWaitSeconds(1, 100)
		if w > maxWaitSeconds {
			t.Errorf("expected clamped to maxWaitSeconds (%f), got %f", maxWaitSeconds, w)
		}
		if w < 0 {
			t.Errorf("expected non-negative, got %f", w)
		}
	}
}
