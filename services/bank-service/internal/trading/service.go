package trading

// =============================================================================
// service.go — TradingService implementation.
//
// tradingService is intentionally thin: it orchestrates four injected
// dependencies and delegates all pure arithmetic to calculations.go.
// No SQL, no HTTP, no gRPC in this file.
//
// Dependency graph (all interfaces — no concrete types):
//
//   tradingService
//     ├── OrderRepository           (internal/repository/order_repository.go)
//     ├── domain.ListingService     (internal/service/listing_service.go)
//     │     └── used for Ask/Bid (price resolution) AND MaintenanceMargin
//     ├── domain.ActuaryRepository  (internal/repository/actuary_repository.go)
//     │     └── used for approval workflow AND supervisor identity checks
//     └── MarginChecker             (internal/repository/margin_checker.go — future)
//
// Wire-up happens in cmd/server/main.go; this file has zero knowledge of
// how the dependencies are constructed or which DB is behind them.
// =============================================================================

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/shopspring/decimal"
)

// ─── Constructor ──────────────────────────────────────────────────────────────

type tradingService struct {
	orders    OrderRepository
	listings  domain.ListingService  // ListingService (not Repository) — provides MaintenanceMargin
	actuaries domain.ActuaryRepository
	margin    MarginChecker
	funds     FundsManager
}

// NewTradingService wires the service with its five dependencies.
// All parameters are interfaces; concrete implementations are provided by the
// caller (main.go) at startup.
func NewTradingService(
	orders OrderRepository,
	listings domain.ListingService,
	actuaries domain.ActuaryRepository,
	margin MarginChecker,
	funds FundsManager,
) TradingService {
	return &tradingService{
		orders:    orders,
		listings:  listings,
		actuaries: actuaries,
		margin:    margin,
		funds:     funds,
	}
}

// ─── CalculateOrderDetails ────────────────────────────────────────────────────

// CalculateOrderDetails resolves the effective price per unit for the given
// order type, computes the approximate notional and commission, and — when
// Margin=true — also computes and returns the initial margin cost.
//
// Price-per-unit resolution per order type:
//   MARKET     → current Ask (BUY) or Bid (SELL) from live listing data
//   LIMIT      → user-supplied PricePerUnit (limit value)
//   STOP       → user-supplied StopPrice   (stop trigger value)
//   STOP_LIMIT → user-supplied PricePerUnit (limit value; stop is only the trigger)
func (s *tradingService) CalculateOrderDetails(ctx context.Context, req *OrderCalculationRequest) (*OrderCalculationResponse, error) {
	if req.Direction != OrderDirectionBuy && req.Direction != OrderDirectionSell {
		return nil, ErrInvalidDirection
	}

	var resp *OrderCalculationResponse
	var err error

	switch req.OrderType {
	case OrderTypeMarket:
		resp, err = s.calculateMarket(ctx, req)
	case OrderTypeLimit:
		resp, err = s.calculateLimit(req)
	case OrderTypeStop:
		resp, err = s.calculateStop(req)
	case OrderTypeStopLimit:
		resp, err = s.calculateStopLimit(req)
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidOrderType, req.OrderType)
	}

	if err != nil {
		return nil, err
	}

	// Append the initial margin cost when the user intends to use margin
	// financing. We fetch the listing here if it wasn't already fetched by
	// the order-type branch (LIMIT / STOP / STOP_LIMIT paths).
	if req.Margin {
		imc, err := s.initialMarginCostForListing(ctx, req.ListingID)
		if err != nil {
			return nil, err
		}
		resp.InitialMarginCost = &imc
	}

	return resp, nil
}

// calculateMarket handles the MARKET branch of CalculateOrderDetails.
// Fetches live Ask / Bid from the listing service.
func (s *tradingService) calculateMarket(ctx context.Context, req *OrderCalculationRequest) (*OrderCalculationResponse, error) {
	listing, err := s.listings.GetListingByID(ctx, req.ListingID)
	if err != nil {
		return nil, fmt.Errorf("dohvatanje hartije %d za kalkulaciju: %w", req.ListingID, err)
	}

	// Ask/Bid arrive as float64 from the GORM-backed listing repository.
	// decimal.NewFromFloat is acceptable here: the source values are already
	// rounded to NUMERIC(18,6) precision in the database.
	var pricePerUnit decimal.Decimal
	if req.Direction == OrderDirectionBuy {
		pricePerUnit = decimal.NewFromFloat(listing.Ask)
	} else {
		pricePerUnit = decimal.NewFromFloat(listing.Bid)
	}

	notional, commission := calcMarketOrder(req.ContractSize, pricePerUnit, req.Quantity)
	return &OrderCalculationResponse{
		PricePerUnit:     pricePerUnit,
		ApproximatePrice: notional,
		Commission:       commission,
	}, nil
}

// calculateLimit handles the LIMIT branch of CalculateOrderDetails.
// No listing fetch required — the user supplies the price directly.
func (s *tradingService) calculateLimit(req *OrderCalculationRequest) (*OrderCalculationResponse, error) {
	if req.PricePerUnit == nil {
		return nil, ErrLimitPriceRequired
	}

	notional, commission := calcLimitOrder(req.ContractSize, *req.PricePerUnit, req.Quantity)
	return &OrderCalculationResponse{
		PricePerUnit:     *req.PricePerUnit,
		ApproximatePrice: notional,
		Commission:       commission,
	}, nil
}

// calculateStop handles the STOP branch of CalculateOrderDetails.
// The approximate price preview uses the stop trigger value (StopPrice), not
// the current market price, because the execution price is unknown until
// triggered.  Commission follows the MARKET schedule.
func (s *tradingService) calculateStop(req *OrderCalculationRequest) (*OrderCalculationResponse, error) {
	if req.StopPrice == nil {
		return nil, ErrStopPriceRequired
	}

	notional, commission := calcStopOrder(req.ContractSize, *req.StopPrice, req.Quantity)
	return &OrderCalculationResponse{
		PricePerUnit:     *req.StopPrice,
		ApproximatePrice: notional,
		Commission:       commission,
	}, nil
}

// calculateStopLimit handles the STOP_LIMIT branch of CalculateOrderDetails.
// The approximate price uses the limit value (PricePerUnit) because that is
// the worst-case fill price once the order is activated.  The stop trigger
// (StopPrice) is validated but not used in the notional arithmetic.
// Commission follows the LIMIT schedule.
func (s *tradingService) calculateStopLimit(req *OrderCalculationRequest) (*OrderCalculationResponse, error) {
	if req.StopPrice == nil {
		return nil, ErrStopPriceRequired
	}
	if req.PricePerUnit == nil {
		return nil, ErrLimitPriceRequired
	}

	notional, commission := calcStopLimitOrder(req.ContractSize, *req.PricePerUnit, req.Quantity)
	return &OrderCalculationResponse{
		PricePerUnit:     *req.PricePerUnit,
		ApproximatePrice: notional,
		Commission:       commission,
	}, nil
}

// initialMarginCostForListing fetches the listing's MaintenanceMargin and
// derives the initial margin cost (MaintenanceMargin × 1.1).
// Called from CalculateOrderDetails and validateMargin whenever Margin=true.
func (s *tradingService) initialMarginCostForListing(ctx context.Context, listingID int64) (decimal.Decimal, error) {
	listing, err := s.listings.GetListingByID(ctx, listingID)
	if err != nil {
		return decimal.Zero, fmt.Errorf("dohvatanje hartije %d za margin kalkulaciju: %w", listingID, err)
	}
	maintenanceMargin := decimal.NewFromFloat(listing.MaintenanceMargin)
	return CalcInitialMarginCost(maintenanceMargin), nil
}

// ─── CreateOrder ──────────────────────────────────────────────────────────────

// CreateOrder validates inputs, checks margin funds when required, applies
// the approval workflow, then persists the order via OrderRepository.
func (s *tradingService) CreateOrder(ctx context.Context, req *CreateOrderRequest) (*Order, error) {
	// ── 0. Settlement expiry fast-path ────────────────────────────────────────
	//
	// Hartija je istekla: sačuvaj nalog odmah kao DECLINED (audit trail),
	// preskočivši svu validaciju i approval workflow.
	if req.SettlementExpired {
		return s.orders.Create(ctx, *req, OrderStatusDeclined)
	}

	// ── 1. Input validation ───────────────────────────────────────────────────

	if req.Direction != OrderDirectionBuy && req.Direction != OrderDirectionSell {
		return nil, ErrInvalidDirection
	}

	if err := validateOrderTypeFields(req); err != nil {
		return nil, err
	}

	// ── 2a. SELL ownership / balance check ──────────────────────────────────
	//
	// Verify the user owns enough of this asset before accepting a SELL order.
	// Runs before resolveStatus so rejected SELL orders never affect the agent's
	// used_limit or trigger fund operations.
	//
	// FOREX SELL: holdings are in bank accounts; check the BASE account free balance.
	// All other SELL: check net order-based holdings (GetNetHoldings subtracts
	// active SELL orders to prevent concurrent overselling).
	if req.Direction == OrderDirectionSell {
		if req.IsForex {
			if err := s.validateForexSellBalance(ctx, req); err != nil {
				return nil, err
			}
		} else {
			if err := s.validateSellOwnership(ctx, req); err != nil {
				return nil, err
			}
		}
	}

	// ── 2b. Sufficient funds check for BUY orders ────────────────────────────
	//
	// Verify the account has enough free balance (stanje − rezervisano) before
	// accepting a BUY order. This prevents over-reservation and ensures the
	// user cannot buy without money. Runs before resolveStatus so that rejected
	// BUY orders never affect the agent's used_limit.
	// Margin orders are exempt because they use credit, not the account balance;
	// validateMargin below handles the initial margin cost check for those.
	// FOREX BUY is exempt: the exchange rate at placement differs from execution;
	// ForexSwap performs a locked TOCTOU check at execution time instead.
	if req.Direction == OrderDirectionBuy && !req.Margin && !req.IsForex {
		if err := s.validateSufficientFunds(ctx, req); err != nil {
			return nil, err
		}
	}

	// ── 2c. Margin check (before touching actuary limit) ─────────────────────
	//
	// Margin je dozvoljen samo pri kupovini (BUY). Za SELL nema short-sellinga.
	// Rejected margin orders must never be counted against an agent's used_limit,
	// so we validate funds first, before resolveStatus runs.

	if req.Margin {
		if req.Direction == OrderDirectionSell {
			return nil, fmt.Errorf("margin se koristi samo pri kupovini")
		}
		if err := s.validateMargin(ctx, req); err != nil {
			return nil, err
		}
	}

	// ── 3. Approval workflow ──────────────────────────────────────────────────

	status, err := s.resolveStatus(ctx, req)
	if err != nil {
		return nil, err
	}

	// ── 4. Persist ────────────────────────────────────────────────────────────

	order, err := s.orders.Create(ctx, *req, status)
	if err != nil {
		return nil, fmt.Errorf("kreiranje naloga: %w", err)
	}

	// ── 5. Reserve funds for APPROVED BUY orders ──────────────────────────────
	// PENDING orders are not reserved yet — funds are reserved when the
	// supervisor approves them (see ApproveOrder).
	// FOREX BUY orders skip reservation: currency amounts are not USD-normalised,
	// and ForexSwap performs a locked balance check at execution time.
	if status == OrderStatusApproved && req.Direction == OrderDirectionBuy && !req.IsForex {
		if err := s.reserveFundsForOrder(ctx, order); err != nil {
			// Order is persisted but reservation failed — cancel it to keep DB consistent.
			if _, cancelErr := s.orders.Cancel(ctx, order.ID); cancelErr != nil {
				log.Printf("[trading] auto-otkazivanje naloga %d nakon greške rezervacije: %v", order.ID, cancelErr)
			}
			return nil, fmt.Errorf("rezervacija sredstava: %w", err)
		}
	}

	return order, nil
}

// validateOrderTypeFields enforces the price-field preconditions for each
// order type.  All branches must be reached before any I/O is performed.
//
//	MARKET     → no price fields required
//	LIMIT      → PricePerUnit required
//	STOP       → StopPrice required
//	STOP_LIMIT → both StopPrice and PricePerUnit required
func validateOrderTypeFields(req *CreateOrderRequest) error {
	switch req.OrderType {
	case OrderTypeMarket:
		// No price fields are required; Ask/Bid are resolved at execution time.
		return nil

	case OrderTypeLimit:
		if req.PricePerUnit == nil {
			return ErrLimitPriceRequired
		}
		return nil

	case OrderTypeStop:
		if req.StopPrice == nil {
			return ErrStopPriceRequired
		}
		return nil

	case OrderTypeStopLimit:
		if req.StopPrice == nil {
			return ErrStopPriceRequired
		}
		if req.PricePerUnit == nil {
			return ErrLimitPriceRequired
		}
		return nil

	default:
		return fmt.Errorf("%w: %q", ErrInvalidOrderType, req.OrderType)
	}
}

// validateSellOwnership verifies that the caller owns at least Quantity contracts
// of the requested listing (measured in order units, not underlying shares).
//
// Net holdings are computed as:
//
//	DONE BUY − DONE SELL − (PENDING|APPROVED SELL, not done)
//
// Subtracting active SELL orders from the available balance prevents
// concurrent overselling: if the user has 3 contracts and already has an
// APPROVED SELL for 2, they cannot create a new SELL for 2.
func (s *tradingService) validateSellOwnership(ctx context.Context, req *CreateOrderRequest) error {
	net, err := s.orders.GetNetHoldings(ctx, req.UserID, req.ListingID)
	if err != nil {
		return fmt.Errorf("provera vlasništva hartije: %w", err)
	}
	needed := int64(req.Quantity)
	if net < needed {
		return fmt.Errorf("%w: raspoloživo %d, traženo %d", ErrInsufficientHoldings, net, needed)
	}
	return nil
}

// validateForexSellBalance verifies that the BASE currency account has enough
// free balance (stanje − rezervisana_sredstva) to cover the FOREX SELL nominal
// (quantity × contractSize, in BASE currency units).
//
// Non-clients (employees/supervisors) are skipped — ForexSwap auto-resolves the
// bank trezor account and performs a TOCTOU-safe balance check at execution time.
func (s *tradingService) validateForexSellBalance(ctx context.Context, req *CreateOrderRequest) error {
	if !req.IsClient {
		return nil
	}
	needed := decimal.NewFromInt(int64(req.Quantity) * int64(req.ContractSize))
	ok, err := s.funds.HasSufficientFreeBalance(ctx, req.AccountID, needed)
	if err != nil {
		return fmt.Errorf("provera sredstava za forex prodaju: %w", err)
	}
	if !ok {
		return ErrInsufficientFunds
	}
	return nil
}

// validateSufficientFunds verifies that the account's free balance (stanje −
// rezervisano, converted to the account's currency) covers the order's notional
// plus commission (oba u USD). Called for BUY non-margin orders before any actuary limit is touched.
func (s *tradingService) validateSufficientFunds(ctx context.Context, req *CreateOrderRequest) error {
	totalUSD, err := s.resolveTotalBuyDebitUSD(ctx, req)
	if err != nil {
		return fmt.Errorf("izračunavanje iznosa za provjeru sredstava: %w", err)
	}
	ok, err := s.funds.HasSufficientFunds(ctx, req.AccountID, totalUSD)
	if err != nil {
		return fmt.Errorf("provjera sredstava za račun %d: %w", req.AccountID, err)
	}
	if !ok {
		return ErrInsufficientFunds
	}
	return nil
}

// resolveTotalBuyDebitUSD vraća notional + proviziju u USD (valuta berze).
func (s *tradingService) resolveTotalBuyDebitUSD(ctx context.Context, req *CreateOrderRequest) (decimal.Decimal, error) {
	notional, err := s.resolveNotional(ctx, req)
	if err != nil {
		return decimal.Zero, err
	}
	var comm decimal.Decimal
	switch req.OrderType {
	case OrderTypeMarket, OrderTypeStop:
		comm = CalcMarketCommission(notional)
	case OrderTypeLimit, OrderTypeStopLimit:
		comm = CalcLimitCommission(notional)
	default:
		comm = decimal.Zero
	}
	return notional.Add(comm), nil
}

// validateMargin fetches the listing's InitialMarginCost and validates whether
// the user satisfies the margin condition from the spec:
//
//   Klijent:  (1) ima odobren kredit > IMC  ILI  (2) slobodan balans računa >= IMC
//   Aktuar:   slobodan balans bankinog trezor računa u USD >= IMC
//             (IMC je izračunat iz USD cene hartije; aktuar ne bira lični račun —
//              backend automatski pronalazi USD trezor banke)
//
// Returns ErrInsufficientMargin when the condition is not satisfied.
// Returns a descriptive error when the trezor account does not exist for the currency.
func (s *tradingService) validateMargin(ctx context.Context, req *CreateOrderRequest) error {
	imc, err := s.initialMarginCostForListing(ctx, req.ListingID)
	if err != nil {
		return err
	}

	// ── Aktuar: provera bankinog trezor računa u USD ───────────────────────────
	// Actuaries do not select a personal account; the backend auto-resolves
	// the bank trezor for the listing's currency (USD, since all prices are USD).
	// Credit check is skipped — actuaries do not have personal credit lines.
	if !req.IsClient {
		trezorOK, trezorErr := s.margin.HasSufficientMarginTrezor(ctx, "USD", imc)
		if trezorErr != nil {
			return fmt.Errorf("margin provera trezora: %w", trezorErr)
		}
		if !trezorOK {
			return ErrInsufficientMargin
		}
		return nil
	}

	// ── Klijent: uslov 1 — odobren kredit ────────────────────────────────────
	creditOK, creditErr := s.margin.HasApprovedCreditForMargin(ctx, req.UserID, imc)
	if creditErr != nil {
		log.Printf("[trading] provjera kredita za margin (user=%d): %v", req.UserID, creditErr)
		// Non-fatal: fall through to balance check.
	}
	if creditOK {
		return nil
	}

	// ── Klijent: uslov 2 — slobodan balans ličnog računa ─────────────────────
	balanceOK, balanceErr := s.margin.HasSufficientMargin(ctx, req.AccountID, imc)
	if balanceErr != nil {
		return fmt.Errorf("provjera margin sredstava za račun %d: %w", req.AccountID, balanceErr)
	}
	if balanceOK {
		return nil
	}

	return ErrInsufficientMargin
}

// ─── Approval workflow ────────────────────────────────────────────────────────

// resolveStatus determines the initial OrderStatus according to the caller's role:
//
//	Client (not in actuary_info)  → APPROVED immediately
//	Supervisor                     → APPROVED immediately
//	Agent:
//	    need_approval == true                   → PENDING
//	    used_limit + approx_price > daily_limit → PENDING
//	    otherwise                               → APPROVED; used_limit incremented
//
// NOTE: The used_limit increment and the subsequent orders.Create are NOT
// wrapped in a single DB transaction.  Under concurrent load, two requests for
// the same agent could both pass the limit check simultaneously (TOCTOU race).
// A SELECT FOR UPDATE or an atomic counter must be introduced before this path
// goes to production.
func (s *tradingService) resolveStatus(ctx context.Context, req *CreateOrderRequest) (OrderStatus, error) {
	// JWT permissions are the authoritative source: if the caller holds the
	// SUPERVISOR role (or is an ADMIN), auto-approve regardless of the DB state.
	// This prevents stale actuary_info records from blocking legitimate supervisors.
	if req.IsSupervisor {
		return OrderStatusApproved, nil
	}

	actuary, err := s.actuaries.GetByEmployeeID(ctx, req.UserID)

	if errors.Is(err, domain.ErrActuaryNotFound) {
		// No actuary_info row → regular Client → auto-approve.
		return OrderStatusApproved, nil
	}
	if err != nil {
		return "", fmt.Errorf("provjera aktuar zapisa za korisnika %d: %w", req.UserID, err)
	}

	if actuary.ActuaryType == domain.ActuaryTypeSupervisor {
		return OrderStatusApproved, nil
	}

	// ── Agent path ────────────────────────────────────────────────────────────

	notional, err := s.resolveNotional(ctx, req)
	if err != nil {
		return "", err
	}

	// Konvertujemo notional iz USD u RSD jer je dnevni limit agenta uvek u RSD.
	// Koristimo srednji kurs bez provizije (isto kao menjačnica za zaposlene).
	notionalRSD, convErr := s.funds.ConvertUSDToRSD(ctx, notional)
	if convErr != nil {
		log.Printf("[trading] konverzija notional USD→RSD za agenta (employee_id=%d): %v — koristi se USD iznos", req.UserID, convErr)
		notionalRSD = notional // fallback: poredi u USD ako konverzija ne uspe
	}

	// need_approval flag znači da agent uvek mora da čeka odobrenje supervizora,
	// ali potrošnja se svejedno beleži (used_limit se inkrementira).
	if actuary.NeedApproval {
		if _, _, incErr := s.actuaries.IncrementUsedLimitAlways(ctx, req.UserID, notionalRSD); incErr != nil {
			if !errors.Is(incErr, domain.ErrActuaryNotFound) {
				log.Printf("[trading] evidencija potrošnje za agenta (need_approval, employee_id=%d): %v", req.UserID, incErr)
			}
		}
		return OrderStatusPending, nil
	}

	// Atomično povećavamo used_limit (uvek) i dobijamo exceeded zastavicu.
	// Jedan UPDATE iskaz eliminiše TOCTOU race condition.
	// Potrošnja se beleži bez obzira na to da li nalog ide u PENDING ili APPROVED.
	_, exceeded, err := s.actuaries.IncrementUsedLimitAlways(ctx, req.UserID, notionalRSD)
	if errors.Is(err, domain.ErrActuaryNotFound) {
		// Aktuar je obrisan između prve provjere i ove operacije — tretiramo kao klijenta.
		return OrderStatusApproved, nil
	}
	if err != nil {
		return "", fmt.Errorf("ažuriranje used_limit za agenta (employee_id=%d): %w", req.UserID, err)
	}

	if exceeded {
		// Novi used_limit premašuje dnevni limit → nalog čeka odobrenje supervizora.
		return OrderStatusPending, nil
	}

	return OrderStatusApproved, nil
}

// resolveNotional returns the approximate order value used for the agent's
// daily-limit check.  The logic is intentionally consistent with the price
// resolution in CalculateOrderDetails so that the preview and the actual check
// use the same number.
//
//	MARKET     → live Ask/Bid × ContractSize × Quantity
//	LIMIT      → PricePerUnit × ContractSize × Quantity
//	STOP       → StopPrice    × ContractSize × Quantity
//	STOP_LIMIT → PricePerUnit × ContractSize × Quantity  (same as LIMIT)
func (s *tradingService) resolveNotional(ctx context.Context, req *CreateOrderRequest) (decimal.Decimal, error) {
	switch req.OrderType {
	case OrderTypeMarket:
		listing, err := s.listings.GetListingByID(ctx, req.ListingID)
		if err != nil {
			return decimal.Zero, fmt.Errorf("dohvatanje hartije %d za provjeru limita: %w", req.ListingID, err)
		}
		var pricePerUnit decimal.Decimal
		if req.Direction == OrderDirectionBuy {
			pricePerUnit = decimal.NewFromFloat(listing.Ask)
		} else {
			pricePerUnit = decimal.NewFromFloat(listing.Bid)
		}
		return approxPrice(req.ContractSize, pricePerUnit, req.Quantity), nil

	case OrderTypeLimit, OrderTypeStopLimit:
		// PricePerUnit is guaranteed non-nil — validateOrderTypeFields ran first.
		return approxPrice(req.ContractSize, *req.PricePerUnit, req.Quantity), nil

	case OrderTypeStop:
		// StopPrice is guaranteed non-nil — validateOrderTypeFields ran first.
		return approxPrice(req.ContractSize, *req.StopPrice, req.Quantity), nil

	default:
		// Unreachable: validateOrderTypeFields already rejected unknown types.
		return decimal.Zero, fmt.Errorf("%w: %s", ErrInvalidOrderType, req.OrderType)
	}
}

// ─── Supervisor dashboard ─────────────────────────────────────────────────────

// ListOrders returns all orders, optionally filtered by status.
// Delegates directly to the repository; no additional business logic needed.
func (s *tradingService) ListOrders(ctx context.Context, statusFilter *OrderStatus) ([]Order, error) {
	orders, err := s.orders.ListByStatus(ctx, statusFilter)
	if err != nil {
		return nil, fmt.Errorf("listanje naloga: %w", err)
	}
	return orders, nil
}

// ListOrdersByUser returns orders belonging to a single user, newest first.
func (s *tradingService) ListOrdersByUser(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error) {
	orders, err := s.orders.ListByUserID(ctx, userID, statusFilter)
	if err != nil {
		return nil, fmt.Errorf("listanje naloga korisnika: %w", err)
	}
	return orders, nil
}

// ApproveOrder transitions a PENDING order to APPROVED and records the
// supervisor's ID.  When the order belongs to an AGENT, their used_limit is
// incremented by the order's notional value (spec §4: "Used Limit se menja
// pri svakoj transakciji").
func (s *tradingService) ApproveOrder(ctx context.Context, orderID int64, supervisorID int64) (*Order, error) {
	order, err := s.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err // ErrOrderNotFound already set by repository
	}

	if order.Status != OrderStatusPending {
		return nil, fmt.Errorf("%w: nalog je u statusu %s, a ne PENDING", ErrInvalidOrderState, order.Status)
	}

	sup := strconv.FormatInt(supervisorID, 10)
	updated, err := s.orders.UpdateStatus(ctx, orderID, OrderStatusApproved, &sup)
	if err != nil {
		return nil, fmt.Errorf("odobravanje naloga %d: %w", orderID, err)
	}

	// Potrošnja (used_limit) je već evidentirana atomski u trenutku kreiranja naloga
	// (IncrementUsedLimitAlways u resolveStatus). Nema duplog inkrementa ovde —
	// supervisor samo odobrava nalog koji je agent već "platio" iz svog dnevnog limita.

	// Reserve funds now that the order transitions from PENDING to APPROVED.
	// updated.Quantity is the full original quantity (no fills yet for PENDING orders).
	if err := s.reserveFundsForOrder(ctx, updated); err != nil {
		// Revert nalog na PENDING da supervisor može ponovo pokušati odobravanje.
		if _, revertErr := s.orders.UpdateStatus(ctx, orderID, OrderStatusPending, nil); revertErr != nil {
			log.Printf("[trading] KRITIČNO: nalog %d je APPROVED bez rezervacije, revert na PENDING nije uspeo: %v", orderID, revertErr)
		}
		return nil, fmt.Errorf("rezervacija sredstava pri odobravanju naloga %d: %w", orderID, err)
	}

	return updated, nil
}

// DeclineOrder transitions a PENDING order to DECLINED and records the
// supervisor's ID.
func (s *tradingService) DeclineOrder(ctx context.Context, orderID int64, supervisorID int64) (*Order, error) {
	order, err := s.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.Status != OrderStatusPending {
		return nil, fmt.Errorf("%w: nalog je u statusu %s, a ne PENDING", ErrInvalidOrderState, order.Status)
	}

	sup := strconv.FormatInt(supervisorID, 10)
	updated, err := s.orders.UpdateStatus(ctx, orderID, OrderStatusDeclined, &sup)
	if err != nil {
		return nil, fmt.Errorf("odbijanje naloga %d: %w", orderID, err)
	}
	return updated, nil
}

// ─── Cancelation ─────────────────────────────────────────────────────────────

// CancelOrder validates permissions and state, then atomically sets the order
// to CANCELED with is_done=true (remaining_portions is preserved so callers can
// compute executedQty = Quantity − RemainingPortions).
//
// Cancelable states: PENDING, APPROVED (and is_done=false).
// Non-cancelable states: DONE, DECLINED, CANCELED.
//
// callerIsSupervisor is the JWT-derived flag passed by the handler (same as
// CreateOrderRequest.IsSupervisor).  When true, the actuary_info DB lookup is
// skipped, which prevents stale records from blocking legitimate supervisors.
func (s *tradingService) CancelOrder(ctx context.Context, orderID int64, requestedBy int64, callerIsSupervisor bool) (*Order, error) {
	order, err := s.orders.GetByID(ctx, orderID)
	if err != nil {
		return nil, err
	}

	// ── Permission check ──────────────────────────────────────────────────────
	// JWT is authoritative (same reasoning as resolveStatus in CreateOrder):
	// if the caller is the owner OR holds the SUPERVISOR JWT role, allow.
	// Only fall back to the actuary_info DB lookup when the JWT flag is false,
	// to handle ADMIN and legacy clients that don't carry the flag.
	if order.UserID != requestedBy && !callerIsSupervisor {
		isSuper, err := s.isSupervisor(ctx, requestedBy)
		if err != nil {
			return nil, fmt.Errorf("provjera privilegija za korisnika %d: %w", requestedBy, err)
		}
		if !isSuper {
			return nil, ErrPermissionDenied
		}
	}

	// ── State check ───────────────────────────────────────────────────────────
	// Only active orders (PENDING or APPROVED, not yet finished) can be canceled.
	// DONE      → execution already completed; nothing to stop.
	// DECLINED  → supervisor already rejected; no execution ever started.
	// CANCELED  → already canceled; idempotent rejection is clearer than a no-op.
	cancelable := (order.Status == OrderStatusPending || order.Status == OrderStatusApproved) && !order.IsDone
	if !cancelable {
		return nil, fmt.Errorf("%w: nalog je u statusu %s (is_done=%v)", ErrInvalidOrderState, order.Status, order.IsDone)
	}

	// Atomično otkaži nalog PRVO — engine odmah prestaje da procesira nalog.
	// Tek onda oslobodi fondove (samo APPROVED BUY nalozi imaju rezervisana sredstva).
	canceled, err := s.orders.Cancel(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("otkazivanje naloga %d: %w", orderID, err)
	}

	if order.Status == OrderStatusApproved && order.Direction == OrderDirectionBuy {
		if releaseErr := s.releaseFundsForOrder(ctx, order); releaseErr != nil {
			// Nalog je CANCELED — engine više neće procesirati. Fondovi ostaju locked do cleanup workera.
			log.Printf("[trading] oslobađanje holdova za otkazani nalog %d nije uspelo: %v", orderID, releaseErr)
		}
	}
	return canceled, nil
}

// ─── Private helpers ──────────────────────────────────────────────────────────

// isSupervisor returns true when userID is registered in actuary_info with
// type SUPERVISOR.  Clients (no actuary_info row) and Agents return false.
// Used by CancelOrder for permission validation.
func (s *tradingService) isSupervisor(ctx context.Context, userID int64) (bool, error) {
	actuary, err := s.actuaries.GetByEmployeeID(ctx, userID)
	if errors.Is(err, domain.ErrActuaryNotFound) {
		return false, nil // regular Client
	}
	if err != nil {
		return false, fmt.Errorf("dohvatanje aktuar zapisa za korisnika %d: %w", userID, err)
	}
	return actuary.ActuaryType == domain.ActuaryTypeSupervisor, nil
}

// ─── Funds helpers ────────────────────────────────────────────────────────────

// computeNotionalPlusCommission — ukupan USD iznos (notional + provizija) za rezervaciju / oslobađanje.
func (s *tradingService) computeNotionalPlusCommission(ctx context.Context, order *Order, qty int32) (decimal.Decimal, error) {
	n, err := s.computeNotional(ctx, order, qty)
	if err != nil {
		return decimal.Zero, err
	}
	var comm decimal.Decimal
	switch order.OrderType {
	case OrderTypeMarket, OrderTypeStop:
		comm = CalcMarketCommission(n)
	case OrderTypeLimit, OrderTypeStopLimit:
		comm = CalcLimitCommission(n)
	default:
		comm = decimal.Zero
	}
	return n.Add(comm), nil
}

// computeNotional calculates the notional value for a given order and quantity.
// For MARKET orders, fetches the current Ask/Bid price from the listing service.
// Used to determine how much to reserve or release from the account.
func (s *tradingService) computeNotional(ctx context.Context, order *Order, qty int32) (decimal.Decimal, error) {
	switch order.OrderType {
	case OrderTypeMarket:
		listing, err := s.listings.GetListingByID(ctx, order.ListingID)
		if err != nil {
			return decimal.Zero, fmt.Errorf("dohvatanje hartije %d za kalkulaciju iznosa: %w", order.ListingID, err)
		}
		var unitPrice decimal.Decimal
		if order.Direction == OrderDirectionBuy {
			unitPrice = decimal.NewFromFloat(listing.Ask)
		} else {
			unitPrice = decimal.NewFromFloat(listing.Bid)
		}
		return approxPrice(order.ContractSize, unitPrice, qty), nil

	case OrderTypeLimit, OrderTypeStopLimit:
		if order.PricePerUnit == nil {
			return decimal.Zero, ErrLimitPriceRequired
		}
		return approxPrice(order.ContractSize, *order.PricePerUnit, qty), nil

	case OrderTypeStop:
		if order.StopPrice == nil {
			return decimal.Zero, ErrStopPriceRequired
		}
		return approxPrice(order.ContractSize, *order.StopPrice, qty), nil

	default:
		return decimal.Zero, fmt.Errorf("%w: %s", ErrInvalidOrderType, order.OrderType)
	}
}

// reserveFundsForOrder reserves the full order notional (based on Quantity) on
// the account.  Only applicable to BUY orders.  Returns an error so callers can
// react (cancel the order or log a visible warning) instead of silently skipping.
func (s *tradingService) reserveFundsForOrder(ctx context.Context, order *Order) error {
	if order.Direction != OrderDirectionBuy {
		return nil
	}
	total, err := s.computeNotionalPlusCommission(ctx, order, order.Quantity)
	if err != nil {
		return fmt.Errorf("izračunavanje iznosa za rezervaciju (nalog %d): %w", order.ID, err)
	}
	if err := s.funds.ReserveFunds(ctx, order.AccountID, total); err != nil {
		return fmt.Errorf("rezervacija sredstava za nalog %d: %w", order.ID, err)
	}
	return nil
}

// releaseFundsForOrder releases the remaining notional (based on RemainingPortions)
// from the account reservation.  Only applicable to APPROVED BUY orders.
// Vraća grešku kako bi pozivalac mogao da je propagira ili loguje (BUG-8).
func (s *tradingService) releaseFundsForOrder(ctx context.Context, order *Order) error {
	if order.Direction != OrderDirectionBuy {
		return nil
	}
	total, err := s.computeNotionalPlusCommission(ctx, order, order.RemainingPortions)
	if err != nil {
		return fmt.Errorf("izračunavanje iznosa za oslobađanje (nalog %d): %w", order.ID, err)
	}
	if err := s.funds.ReleaseFunds(ctx, order.AccountID, total); err != nil {
		return fmt.Errorf("oslobađanje sredstava za nalog %d: %w", order.ID, err)
	}
	return nil
}
