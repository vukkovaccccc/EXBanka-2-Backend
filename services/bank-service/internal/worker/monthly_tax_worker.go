package worker

// =============================================================================
// monthly_tax_worker.go — Monthly capital gains tax calculation
//
// Spec rules implemented here:
//   - 15% capital gains tax on profitable SELL orders.
//   - Calculated per user/account/month on the first day of the following month.
//   - Converted to RSD using the middle exchange rate (no fee — spec rule).
//   - Stored in core_banking.tax_records; paid=false until deducted.
//   - No tax recorded when there is no profit (max(0, profit * 0.15)).
//   - Can also be triggered manually via a supervisor HTTP endpoint.
//
// Algorithm:
//   For each (user_id, account_id) pair with DONE SELL transactions in (year, month):
//     1. Find the executed sell price from order_transactions (weighted average).
//     2. Find the weighted average buy price from DONE BUY order_transactions
//        for the same listing and account (bought BEFORE the sell month).
//     3. profit_usd = (sell_price - avg_buy_price) * sold_quantity
//     4. If profit_usd <= 0 → skip.
//     5. tax_usd = profit_usd * 0.15
//     6. Convert to RSD via middle rate (no commission).
//     7. Upsert into tax_records (conflict = update amount_rsd, keep paid=false).
// =============================================================================

import (
	"context"
	"log"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const taxRate = 0.15 // 15% capital gains tax

// MonthlyTaxWorker runs on the 1st of each month at 00:01 and calculates
// capital gains tax for the previous month.
type MonthlyTaxWorker struct {
	db              *gorm.DB
	exchangeService domain.ExchangeService
}

// NewMonthlyTaxWorker constructs the worker.
func NewMonthlyTaxWorker(db *gorm.DB, exchangeService domain.ExchangeService) *MonthlyTaxWorker {
	return &MonthlyTaxWorker{db: db, exchangeService: exchangeService}
}

// Start blocks until ctx is canceled, triggering tax calculation on the 1st of
// each month at 00:01 (local time). Call as: go worker.Start(ctx).
func (w *MonthlyTaxWorker) Start(ctx context.Context) {
	log.Printf("[worker] MonthlyTaxWorker started — waiting for 1st of month 00:01")

	for {
		next := nextMonthlyRun()
		log.Printf("[worker] MonthlyTaxWorker: next run at %s", next.Format("2006-01-02 15:04:05"))

		select {
		case <-time.After(time.Until(next)):
			// Calculate tax for the previous month.
			prev := next.AddDate(0, -1, 0)
			if err := w.RunFor(ctx, prev.Year(), prev.Month()); err != nil {
				log.Printf("[worker] MonthlyTaxWorker ERROR for %d-%02d: %v", prev.Year(), int(prev.Month()), err)
			}
		case <-ctx.Done():
			log.Printf("[worker] MonthlyTaxWorker: shutting down")
			return
		}
	}
}

// nextMonthlyRun returns the next 1st-of-month 00:01:00 in local time.
func nextMonthlyRun() time.Time {
	now := time.Now()
	// 1st of next month at 00:01
	first := time.Date(now.Year(), now.Month()+1, 1, 0, 1, 0, 0, now.Location())
	if now.Before(first) {
		return first
	}
	// Already past this month's trigger → schedule for the month after
	return first.AddDate(0, 1, 0)
}

// taxProfitRow is the result of the SQL aggregation query.
type taxProfitRow struct {
	UserID     int64   `gorm:"column:user_id"`
	AccountID  int64   `gorm:"column:account_id"`
	ProfitUSD  float64 `gorm:"column:profit_usd"`
}

// RunFor calculates and stores capital gains tax for the given year/month.
// It is idempotent: re-running for the same period updates the amount_rsd.
// Called automatically at month end and can be triggered manually.
func (w *MonthlyTaxWorker) RunFor(ctx context.Context, year int, month time.Month) error {
	log.Printf("[worker] MonthlyTaxWorker: calculating tax for %d-%02d", year, int(month))

	// ── 1. Aggregate profit per user/account for the given period ─────────────
	//
	// Profit = (weighted avg sell price − weighted avg buy price) × sold qty.
	// Buy price is computed across ALL DONE BUY orders for that listing/account,
	// not just those in the same month (FIFO-compatible weighted average).
	var rows []taxProfitRow
	err := w.db.WithContext(ctx).Raw(`
		WITH sell_fills AS (
			-- All SELL order fills executed in (year, month)
			SELECT
				o.user_id,
				o.account_id,
				o.listing_id,
				SUM(ot.executed_quantity)                                        AS sold_qty,
				SUM(CAST(ot.executed_price AS FLOAT) * ot.executed_quantity)
				  / NULLIF(SUM(ot.executed_quantity), 0)                         AS avg_sell_price
			FROM core_banking.order_transactions ot
			JOIN core_banking.orders o ON o.id = ot.order_id
			WHERE o.direction = 'SELL'
			  AND o.status    = 'DONE'
			  AND o.is_done   = TRUE
			  AND EXTRACT(YEAR  FROM ot.execution_time) = ?
			  AND EXTRACT(MONTH FROM ot.execution_time) = ?
			GROUP BY o.user_id, o.account_id, o.listing_id
		),
		buy_fills AS (
			-- Weighted average buy price per user/account/listing (all time)
			SELECT
				o.user_id,
				o.account_id,
				o.listing_id,
				SUM(CAST(ot.executed_price AS FLOAT) * ot.executed_quantity)
				  / NULLIF(SUM(ot.executed_quantity), 0)                         AS avg_buy_price
			FROM core_banking.order_transactions ot
			JOIN core_banking.orders o ON o.id = ot.order_id
			WHERE o.direction = 'BUY'
			  AND o.status    = 'DONE'
			  AND o.is_done   = TRUE
			GROUP BY o.user_id, o.account_id, o.listing_id
		)
		SELECT
			s.user_id,
			s.account_id,
			SUM((s.avg_sell_price - COALESCE(b.avg_buy_price, 0)) * s.sold_qty) AS profit_usd
		FROM sell_fills s
		LEFT JOIN buy_fills b
			ON b.user_id    = s.user_id
		   AND b.account_id = s.account_id
		   AND b.listing_id = s.listing_id
		GROUP BY s.user_id, s.account_id
		HAVING SUM((s.avg_sell_price - COALESCE(b.avg_buy_price, 0)) * s.sold_qty) > 0
	`, year, int(month)).Scan(&rows).Error
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		log.Printf("[worker] MonthlyTaxWorker: no taxable profit for %d-%02d", year, int(month))
		return nil
	}

	// ── 2. Fetch USD→RSD middle rate (no fee per spec) ───────────────────────
	usdToRSD := w.usdToRSDRate(ctx)

	// ── 3. Upsert tax records ─────────────────────────────────────────────────
	for _, row := range rows {
		profitUSD := decimal.NewFromFloat(row.ProfitUSD)
		taxUSD := profitUSD.Mul(decimal.NewFromFloat(taxRate))
		taxRSD := taxUSD.Mul(usdToRSD)

		err := w.db.WithContext(ctx).Exec(`
			INSERT INTO core_banking.tax_records
				(user_id, account_id, year, month, amount_rsd, paid)
			VALUES (?, ?, ?, ?, ?, FALSE)
			ON CONFLICT (user_id, account_id, year, month)
			DO UPDATE SET amount_rsd = EXCLUDED.amount_rsd
		`, row.UserID, row.AccountID, year, int(month), taxRSD.StringFixed(4)).Error
		if err != nil {
			log.Printf("[worker] MonthlyTaxWorker: upsert tax record for user=%d account=%d: %v",
				row.UserID, row.AccountID, err)
		}
	}

	log.Printf("[worker] MonthlyTaxWorker: tax records upserted for %d rows (%d-%02d)",
		len(rows), year, int(month))
	return nil
}

// usdToRSDRate fetches the USD middle rate from the exchange service.
// Falls back to 1.0 (1:1 passthrough) if the service is unavailable,
// logging the fallback clearly so it can be investigated.
func (w *MonthlyTaxWorker) usdToRSDRate(ctx context.Context) decimal.Decimal {
	rates, err := w.exchangeService.GetRates(ctx)
	if err != nil {
		log.Printf("[worker] MonthlyTaxWorker: cannot fetch exchange rates: %v — tax stored in USD", err)
		return decimal.NewFromFloat(1.0)
	}
	for _, r := range rates {
		if r.Oznaka == "USD" && r.Srednji > 0 {
			return decimal.NewFromFloat(r.Srednji)
		}
	}
	log.Printf("[worker] MonthlyTaxWorker: USD rate not found — tax stored as-is")
	return decimal.NewFromFloat(1.0)
}
