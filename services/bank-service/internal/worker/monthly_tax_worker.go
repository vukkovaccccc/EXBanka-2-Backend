package worker

// =============================================================================
// monthly_tax_worker.go — cron koji okida obračun + naplatu poreza na kapitalnu
// dobit za prethodni kalendarski mesec. Cela poslovna logika živi u
// service.TaxService; worker je agnostičan i prima callback kako bi se izbegao
// cyclic import (service paket već uvozi worker zbog market data fetcher-a).
//
// Pokreće se 1. u mesecu u 00:01 po lokalnom vremenu.
// =============================================================================

import (
	"context"
	"log"
	"time"
)

// TaxRunner je funkcija koja obračunava i naplaćuje porez za zadati
// (year, month). Implementaciju (adapter ka service.TaxService) postavlja
// main.go pri wire-up-u.
type TaxRunner func(ctx context.Context, year int, month time.Month) error

// MonthlyTaxWorker runs on the 1st of each month at 00:01 and triggers
// capital gains tax calculation for the previous month.
type MonthlyTaxWorker struct {
	runTax TaxRunner
}

// NewMonthlyTaxWorker constructs the worker with a tax-run callback.
func NewMonthlyTaxWorker(runTax TaxRunner) *MonthlyTaxWorker {
	return &MonthlyTaxWorker{runTax: runTax}
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
			prev := next.AddDate(0, -1, 0)
			if err := w.RunFor(ctx, prev.Year(), prev.Month()); err != nil {
				log.Printf("[worker] MonthlyTaxWorker ERROR for %d-%02d: %v",
					prev.Year(), int(prev.Month()), err)
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
	first := time.Date(now.Year(), now.Month()+1, 1, 0, 1, 0, 0, now.Location())
	if now.Before(first) {
		return first
	}
	return first.AddDate(0, 1, 0)
}

// RunFor delegira obračun + naplatu za zadati (year, month) na injektovanu
// TaxRunner funkciju. Idempotentno preko (period_start, period_end) uniqueness-a
// u tax_records — ako je supervizor već ručno obradio deo meseca, servis
// prepoznaje COLLECTED redove i preskače ih.
func (w *MonthlyTaxWorker) RunFor(ctx context.Context, year int, month time.Month) error {
	log.Printf("[worker] MonthlyTaxWorker: running tax for %d-%02d", year, int(month))
	if w.runTax == nil {
		log.Printf("[worker] MonthlyTaxWorker: no TaxRunner configured — skipping")
		return nil
	}
	return w.runTax(ctx, year, month)
}
