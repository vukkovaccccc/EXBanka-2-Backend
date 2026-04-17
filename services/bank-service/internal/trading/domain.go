package trading

// =============================================================================
// domain.go — core types, DTOs, and interfaces for the trading domain.
//
// This file is the single source of truth for every contract in the package:
//   - Enums / sentinel errors
//   - Domain entity structs (Order, OrderTransaction)
//   - Input/output DTOs (used by the service layer and, later, by handlers)
//   - OrderRepository interface (implemented in internal/repository/)
//   - TradingService interface (implemented in service.go)
//
// Nothing here imports sqlc or the DB driver; those concerns belong in the
// repository implementation layer.
// =============================================================================

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ─── Sentinel errors ──────────────────────────────────────────────────────────

var (
	// ErrOrderNotFound is returned when a requested order does not exist.
	ErrOrderNotFound = errors.New("nalog nije pronađen")

	// ErrLimitPriceRequired is returned when a LIMIT order is submitted without
	// a price_per_unit value.
	ErrLimitPriceRequired = errors.New("limit nalog zahteva price_per_unit")

	// ErrInvalidOrderType is returned for unrecognised or unsupported order type values.
	ErrInvalidOrderType = errors.New("nepodržan ili nepoznat tip naloga")

	// ErrInvalidDirection is returned when direction is neither BUY nor SELL.
	ErrInvalidDirection = errors.New("nepoznat smer naloga; očekuje se BUY ili SELL")

	// ErrStopPriceRequired is returned when a STOP or STOP_LIMIT order is
	// submitted without a stop_price value.
	ErrStopPriceRequired = errors.New("stop nalog zahteva stop_price")

	// ErrInsufficientMargin is returned when the account's available balance is
	// less than the required initial margin cost for a margin order.
	ErrInsufficientMargin = errors.New("nedovoljno sredstava za margin nalog")

	// ErrInvalidOrderState is returned when the requested action is not valid
	// for the order's current status (e.g., approving a DONE order, or canceling
	// an already-CANCELED order).  The wrapping error message provides the
	// current status so callers can surface a useful message.
	ErrInvalidOrderState = errors.New("nevažeće stanje naloga za ovu operaciju")

	// ErrPermissionDenied is returned when the caller is not authorized to
	// perform the requested action (e.g., a client trying to cancel another
	// user's order).
	ErrPermissionDenied = errors.New("nemate ovlašćenje za ovu operaciju")

	// ErrSettlementDatePassed is returned when a FUTURE or OPTION order is
	// submitted after the instrument's settlement_date has expired.
	ErrSettlementDatePassed = errors.New("datum dospeća hartije je prošao; nalog je automatski odbijen")

	// ErrListingTypeNotAllowed is returned when a CLIENT tries to create an
	// order for a listing type that is not available to clients (e.g. FOREX).
	ErrListingTypeNotAllowed = errors.New("klijenti ne mogu da trguju FOREX instrumentima")

	// ErrInsufficientHoldings is returned when a SELL order is submitted but
	// the user does not own enough of the asset (net holdings < requested quantity).
	ErrInsufficientHoldings = errors.New("nemate dovoljno hartija za prodaju")

	// ErrInsufficientFunds is returned when a BUY order is submitted but
	// the account's free balance (stanje_racuna − rezervisana_sredstva) is
	// less than the order's notional value.
	ErrInsufficientFunds = errors.New("nedovoljno sredstava na računu")

	// ── Forex-specific decline errors ─────────────────────────────────────────
	// These are returned by ForexSwap and cause the order to be Declined.

	ErrForexAccountNotFound  = errors.New("forex: račun za valutu nije pronađen ili ne pripada korisniku")
	ErrForexCurrencyMismatch = errors.New("forex: valuta računa ne odgovara paru BASE/QUOTE")
	ErrForexSameCurrency     = errors.New("forex: BASE i QUOTE valuta su iste")
	ErrForexSameAccount      = errors.New("forex: BASE i QUOTE račun su isti")
)

// ─── Enums ────────────────────────────────────────────────────────────────────

// OrderType mirrors the CHECK constraint on orders.order_type.
type OrderType string

const (
	OrderTypeMarket    OrderType = "MARKET"
	OrderTypeLimit     OrderType = "LIMIT"
	OrderTypeStop      OrderType = "STOP"
	OrderTypeStopLimit OrderType = "STOP_LIMIT"
)

// OrderDirection mirrors the CHECK constraint on orders.direction.
type OrderDirection string

const (
	OrderDirectionBuy  OrderDirection = "BUY"
	OrderDirectionSell OrderDirection = "SELL"
)

// OrderStatus mirrors the CHECK constraint on orders.status.
type OrderStatus string

// ApprovedByNoApproval is persisted when an order is approved without supervisor review (clients, auto-approved agents).
const ApprovedByNoApproval = "No need for approval"

// DeclinedBySettlementExpiry is persisted as approved_by when an order is created but immediately declined
// because the instrument's settlement_date has already passed.
const DeclinedBySettlementExpiry = "System (settlement date passed)"

const (
	OrderStatusPending  OrderStatus = "PENDING"
	OrderStatusApproved OrderStatus = "APPROVED"
	OrderStatusDeclined OrderStatus = "DECLINED"
	OrderStatusDone     OrderStatus = "DONE"

	// OrderStatusCanceled is assigned when an owner or supervisor manually
	// stops an order that is PENDING or APPROVED (possibly mid-execution).
	//
	// Semantic distinction from DECLINED:
	//   DECLINED  — supervisor rejected a PENDING order before any execution.
	//   CANCELED  — owner or supervisor actively stopped a PENDING or APPROVED
	//               order; may follow partial fills recorded in order_transactions.
	//
	// !! MIGRATION REQUIRED before deploying !!
	// The orders.status CHECK constraint must be extended to include 'CANCELED':
	//
	//   ALTER TABLE core_banking.orders
	//       DROP CONSTRAINT chk_orders_status,
	//       ADD  CONSTRAINT chk_orders_status
	//            CHECK (status IN ('PENDING','APPROVED','DECLINED','DONE','CANCELED'));
	//
	// Add this as migration 000028_add_canceled_order_status.up.sql.
	OrderStatusCanceled OrderStatus = "CANCELED"
)

// ─── Domain entities ──────────────────────────────────────────────────────────

// Order is the central domain entity for the trading module.
// It maps 1-to-1 with the orders table but uses Go-native types instead of
// the sql.NullString / sql.NullInt64 produced by sqlc.
type Order struct {
	ID           int64
	UserID       int64 // cross-service ref to user-service; no FK in DB
	AccountID    int64 // references core_banking.racun(id)
	ListingID    int64 // references core_banking.listing(id)
	OrderType    OrderType
	Direction    OrderDirection
	Quantity     int32
	ContractSize int32

	// PricePerUnit is nil for MARKET orders (fill price is unknown at placement
	// and is recorded per-chunk in OrderTransaction instead).
	PricePerUnit *decimal.Decimal

	// StopPrice is nil for MARKET and LIMIT orders.
	// Populated for STOP and STOP_LIMIT orders (future sprint).
	StopPrice *decimal.Decimal

	Status    OrderStatus
	// ApprovedBy is nil for PENDING orders. Auto-approved orders store the literal
	// "No need for approval". Supervisor actions store the supervisor's user ID as decimal text.
	ApprovedBy *string

	IsDone            bool
	RemainingPortions int32 // starts == Quantity; decremented on each partial fill
	AfterHours        bool
	AllOrNone         bool
	Margin            bool
	IsClient          bool // true = nalog kreirao klijent; engine koristi prodajni kurs za konverziju

	LastModified time.Time
	CreatedAt    time.Time
}

// OrderTransaction records a single partial-fill execution chunk.
// The sum of ExecutedQuantity across all rows for an order reaches Quantity
// when Order.IsDone becomes true.
type OrderTransaction struct {
	ID               int64
	OrderID          int64
	ExecutedQuantity int32
	ExecutedPrice    decimal.Decimal
	ExecutionTime    time.Time
}

// ─── Input / Output DTOs ──────────────────────────────────────────────────────

// OrderCalculationRequest carries the inputs needed to compute an approximate
// price and commission before the user confirms order placement.
// No UserID is required here — calculation is stateless and user-agnostic.
type OrderCalculationRequest struct {
	OrderType    OrderType
	Direction    OrderDirection
	ListingID    int64
	Quantity     int32
	ContractSize int32

	// PricePerUnit must be set for LIMIT and STOP_LIMIT orders; ignored for
	// MARKET orders (the current Ask / Bid is fetched from the listing instead).
	PricePerUnit *decimal.Decimal

	// StopPrice is the trigger threshold for STOP and STOP_LIMIT orders.
	// Ignored for MARKET and LIMIT orders.
	StopPrice *decimal.Decimal

	// Margin indicates whether the order uses margin financing.
	// When true, CalculateOrderDetails populates InitialMarginCost in the
	// response so the user can review the full capital requirement up front.
	Margin bool

	// AllOrNone signals that the order must be filled in its entirety or not
	// at all. Included for frontend display; does not affect the calculation.
	AllOrNone bool
}

// OrderCalculationResponse is returned to the frontend before order confirmation
// so the user can review costs before submitting the real CreateOrder request.
type OrderCalculationResponse struct {
	// PricePerUnit is the effective price used in the calculation
	// (current Ask/Bid for MARKET; the supplied limit value for LIMIT).
	PricePerUnit decimal.Decimal

	// ApproximatePrice = ContractSize × PricePerUnit × Quantity
	ApproximatePrice decimal.Decimal

	// Commission is the fee applied on top of ApproximatePrice.
	Commission decimal.Decimal

	// InitialMarginCost is only populated when the request had Margin=true.
	// Equals MaintenanceMargin × 1.1; represents the funds the account must
	// hold at order placement time.  Nil when Margin is false.
	InitialMarginCost *decimal.Decimal
}

// CreateOrderRequest is the full set of fields submitted when a user confirms
// order placement. The service derives InitialStatus internally.
type CreateOrderRequest struct {
	// UserID is the caller's ID from user-service.
	// The service uses it to look up actuary_info and determine the approval
	// workflow (Client → auto-approve; Supervisor → auto-approve;
	// Agent → check limit and need_approval flag).
	UserID    int64
	AccountID int64
	ListingID int64

	OrderType    OrderType
	Direction    OrderDirection
	Quantity     int32
	ContractSize int32

	// PricePerUnit is nil for MARKET orders; required for LIMIT orders.
	PricePerUnit *decimal.Decimal

	// StopPrice is nil for MARKET and LIMIT orders (future sprint).
	StopPrice *decimal.Decimal

	AfterHours bool
	AllOrNone  bool
	Margin     bool

	// IsSupervisor is derived from JWT claims (permissions contains "SUPERVISOR"
	// or userType is "ADMIN"). When true, the order is auto-approved regardless
	// of the actuary_info DB state, which may be stale or inconsistent.
	IsSupervisor bool

	// IsClient is true when the caller's JWT userType is "CLIENT".
	// Stored in the DB so the async execution engine can apply the correct
	// exchange rate (prodajni kurs) when converting USD fills to account currency.
	IsClient bool

	// IsForex is true when the listing is of type FOREX.
	// Set by the handler; used by the service to skip checks that don't apply to FOREX
	// (sell ownership validation, pre-creation funds check, BUY fund reservation).
	// Execution-time validation in ForexSwap covers all of these instead.
	IsForex bool

	// SettlementExpired is true when the listing's settlement_date has already passed.
	// When set, the service skips all validation and creates the order immediately as DECLINED.
	SettlementExpired bool

	// ApprovedByOverride, when non-nil, overrides the default approved_by value stored in the DB.
	// Used for system-declined orders (e.g., settlement date passed) to record the reason.
	ApprovedByOverride *string
}

// ─── External service interfaces ─────────────────────────────────────────────

// MarginChecker validates that a user has sufficient resources to cover the
// initial margin cost of a margin order.
//
// Per spec, a margin order is accepted when the user satisfies AT LEAST ONE of:
//  1. Client: has an approved (active) credit whose amount > InitialMarginCost.
//  2. Client: selected account's free balance >= InitialMarginCost.
//     Actuary: bank trezor account for the listing currency has free balance >= InitialMarginCost.
type MarginChecker interface {
	// HasSufficientMargin returns (true, nil) when the account's free balance
	// (stanje_racuna − rezervisana_sredstva) is greater than or equal to
	// required.  Returns (false, nil) when insufficient — the service layer
	// converts this to ErrInsufficientMargin.  Returns (false, err) on any
	// DB-level failure.
	HasSufficientMargin(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error)

	// HasApprovedCreditForMargin returns (true, nil) when the user has at least
	// one approved (ODOBREN) credit whose iznos_kredita exceeds required.
	// This is the credit-based margin condition from the spec (condition 1, clients only).
	// Returns (false, nil) when no qualifying credit exists.
	// Returns (false, err) on any DB-level failure.
	HasApprovedCreditForMargin(ctx context.Context, userID int64, required decimal.Decimal) (bool, error)

	// HasSufficientMarginTrezor returns (true, nil) when the bank trezor account
	// for the given currency (id_vlasnika=2) has free balance >= required.
	// Used for actuary margin checks — actuaries don't pick a personal account;
	// the backend auto-resolves the bank's account for that currency.
	// Returns (false, err) when no active trezor account exists for that currency.
	HasSufficientMarginTrezor(ctx context.Context, currency string, required decimal.Decimal) (bool, error)
}

// FundsManager handles account balance mutations that occur during the order
// lifecycle.  All operations are applied atomically at the SQL level so that
// a crash mid-update cannot leave the account in an inconsistent state.
//
// Lifecycle mapping:
//
//	BUY order APPROVED  → ReserveFunds  (stanje unchanged; rezervisana ↑)
//	BUY fill executed   → SettleBuyFill (stanje ↓; rezervisana ↓ same amount)
//	BUY order CANCELED  → ReleaseFunds  (stanje unchanged; rezervisana ↓)
//	SELL fill executed  → CreditSellFill (stanje ↑)
type FundsManager interface {
	// ReserveFunds increases rezervisana_sredstva by amount.
	// Called when a BUY order transitions to APPROVED.
	ReserveFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error

	// ReleaseFunds decreases rezervisana_sredstva by amount (clamped to 0).
	// Called when an APPROVED BUY order is canceled before full execution.
	ReleaseFunds(ctx context.Context, accountID int64, amount decimal.Decimal) error

	// SettleBuyFill atomically decreases both stanje_racuna and
	// rezervisana_sredstva by amount.
	// Called per partial fill of a BUY order.
	SettleBuyFill(ctx context.Context, accountID int64, amount decimal.Decimal) error

	// CreditSellFill increases stanje_racuna by amount.
	// Called per partial fill of a SELL order.
	CreditSellFill(ctx context.Context, accountID int64, amount decimal.Decimal) error

	// ChargeCommission decreases stanje_racuna by the commission amount without
	// touching rezervisana_sredstva (commission was never part of the reservation).
	// Called once when an order is fully executed (MarkDone).
	ChargeCommission(ctx context.Context, accountID int64, amount decimal.Decimal) error

	// HasSufficientFunds returns (true, nil) when the account's free balance
	// (stanje_racuna − rezervisana_sredstva), converted to the account's currency,
	// is greater than or equal to the required USD amount.
	// Returns (false, nil) when funds are insufficient — the service layer
	// converts this to ErrInsufficientFunds.
	// Returns (false, err) on any DB-level or conversion failure.
	HasSufficientFunds(ctx context.Context, accountID int64, usdAmount decimal.Decimal) (bool, error)

	// HasSufficientFreeBalance returns (true, nil) when the account's free balance
	// (stanje_racuna − rezervisana_sredstva) is >= required in the account's native
	// currency. No USD conversion is applied.
	// Used for FOREX SELL pre-validation where the amount is already in BASE currency.
	HasSufficientFreeBalance(ctx context.Context, accountID int64, required decimal.Decimal) (bool, error)

	// ConvertUSDToRSD converts a USD amount to RSD using the mid-rate exchange
	// rate (no fee, same as the menjačnica without provizija).
	// Used by the trading service to convert order notionals to RSD before
	// comparing against an agent's daily limit (which is stored in RSD).
	// Returns the original amount if the USD rate cannot be fetched.
	ConvertUSDToRSD(ctx context.Context, usdAmount decimal.Decimal) (decimal.Decimal, error)

	// ForexSwap atomically executes a currency swap for a forex order.
	//
	// direction=BUY:  debit (nominalBase × rate) from fromAccount (QUOTE currency),
	//                 credit nominalBase to user's BASE account.
	// direction=SELL: debit nominalBase from fromAccount (BASE currency),
	//                 credit (nominalBase × rate) to user's QUOTE account.
	//
	// fromAccountID is the AccountID stored on the order (the "debit" account).
	// The counterpart account is found automatically among the user's active accounts.
	// Both accounts are locked in id-ASC order before balances are validated (TOCTOU fix).
	//
	// Decline-class errors (order should be set to DECLINED):
	//   ErrForexAccountNotFound, ErrForexCurrencyMismatch,
	//   ErrForexSameAccount, ErrForexSameCurrency, ErrInsufficientFunds.
	ForexSwap(ctx context.Context, userID int64, fromAccountID int64,
		baseCurrency, quoteCurrency string,
		nominalBase, rate decimal.Decimal,
		direction OrderDirection) error

	// WithDB vraća novu instancu koja koristi dati *gorm.DB (može biti transakcija).
	// Neophodan za atomično izvršavanje fill operacija u engine.go.
	WithDB(db *gorm.DB) FundsManager
}

// ─── Repository interface ─────────────────────────────────────────────────────

// OrderRepository defines the data-access contract for the trading domain.
// The concrete implementation in internal/repository/ wraps *sqlc.Queries and
// handles the NUMERIC-string ↔ decimal.Decimal conversions at that boundary.
type OrderRepository interface {
	// Create persists a new order row. status is determined by the service
	// approval workflow before this is called.
	// RemainingPortions is initialised to req.Quantity inside the implementation.
	Create(ctx context.Context, req CreateOrderRequest, status OrderStatus) (*Order, error)

	// GetByID returns the order for the given PK.
	// Returns ErrOrderNotFound when no row exists.
	GetByID(ctx context.Context, id int64) (*Order, error)

	// UpdateStatus atomically sets the status and optional approver.
	// Used by supervisor approve/decline actions and by the system
	// auto-decline worker (expired settlement dates).
	UpdateStatus(ctx context.Context, id int64, status OrderStatus, approvedBy *string) (*Order, error)

	// UpdateRemainingPortions decrements remaining portions and flips IsDone.
	// Called by the async execution engine after each partial fill.
	UpdateRemainingPortions(ctx context.Context, id int64, remaining int32, isDone bool) (*Order, error)

	// ListByUserID returns all orders for a user, newest first.
	// Pass nil statusFilter to return orders of every status.
	ListByUserID(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error)

	// ListByStatus returns all orders matching status, newest first.
	// Pass nil to return orders of every status (supervisor overview).
	ListByStatus(ctx context.Context, status *OrderStatus) ([]Order, error)

	// ListActiveByListing returns all APPROVED, non-finished orders for a
	// listing. Used by the execution engine on each market-data tick.
	ListActiveByListing(ctx context.Context, listingID int64) ([]Order, error)

	// CreateTransaction records a single partial-fill chunk.
	// Must be called inside the same logical unit-of-work as
	// UpdateRemainingPortions to keep the two tables consistent.
	CreateTransaction(ctx context.Context, orderID int64, qty int32, price decimal.Decimal) (*OrderTransaction, error)

	// GetTransactionsByOrderID returns all fill records for an order, oldest first.
	GetTransactionsByOrderID(ctx context.Context, orderID int64) ([]OrderTransaction, error)

	// MarkDone atomically sets status=DONE, is_done=true, remaining_portions=0,
	// and bumps last_modified. Called by the execution engine when every portion
	// of an order has been filled.  Using a single UPDATE is mandatory: if the
	// process crashes between the last CreateTransaction call and a separate
	// UpdateStatus call, the order would be left in an ambiguous state
	// (is_done=true but status=APPROVED).
	//
	// Requires a new sqlc query — add to trading.sql and re-run `sqlc generate`:
	//
	//   -- name: MarkOrderDone :one
	//   UPDATE core_banking.orders
	//   SET
	//       status             = 'DONE',
	//       is_done            = TRUE,
	//       remaining_portions = 0,
	//       last_modified      = NOW()
	//   WHERE id = $1
	//   RETURNING id, user_id, account_id, listing_id, order_type, direction,
	//             quantity, contract_size, price_per_unit, stop_price, status,
	//             approved_by, is_done, remaining_portions, after_hours,
	//             all_or_none, margin, last_modified, created_at;
	MarkDone(ctx context.Context, id int64) (*Order, error)

	// Cancel atomically sets status to CANCELED, remaining_portions to 0,
	// is_done to true, and bumps last_modified in a single UPDATE statement.
	//
	// This atomicity is required because a partially-filled order that is being
	// canceled must not be left in an intermediate state (e.g., status=CANCELED
	// but remaining_portions still non-zero) if the process crashes mid-update.
	//
	// A dedicated sqlc query is required — add the following to trading.sql and
	// re-run `sqlc generate` from services/bank-service/:
	//
	//   -- name: CancelOrder :one
	//   UPDATE core_banking.orders
	//   SET
	//       status             = $2,
	//       remaining_portions = 0,
	//       is_done            = TRUE,
	//       last_modified      = NOW()
	//   WHERE id = $1
	//   RETURNING id, user_id, account_id, listing_id, order_type, direction,
	//             quantity, contract_size, price_per_unit, stop_price, status,
	//             approved_by, is_done, remaining_portions, after_hours,
	//             all_or_none, margin, last_modified, created_at;
	Cancel(ctx context.Context, id int64) (*Order, error)

	// GetNetHoldings returns the effective owned quantity for a user and listing,
	// accounting for all relevant order states:
	//
	//   + DONE BUY orders         (already delivered to portfolio)
	//   - DONE SELL orders        (already removed from portfolio)
	//   - PENDING SELL orders     (reserved to prevent over-selling)
	//   - APPROVED SELL orders    (reserved to prevent over-selling)
	//
	// Used by the SELL ownership validation in the service layer to reject orders
	// when the user does not hold (or has already committed to sell) enough shares.
	GetNetHoldings(ctx context.Context, userID, listingID int64) (int64, error)

	// WithDB vraća novu instancu koja koristi dati *gorm.DB (može biti transakcija).
	// Neophodan za atomično izvršavanje fill operacija u engine.go.
	WithDB(db *gorm.DB) OrderRepository
}

// ─── Service interface ────────────────────────────────────────────────────────

// TradingService defines the business-logic contract for the trading domain.
// Implemented by tradingService in service.go.
type TradingService interface {
	// ─── Order placement ──────────────────────────────────────────────────────

	// CalculateOrderDetails returns the approximate price, commission, and —
	// when Margin=true — the initial margin cost for a prospective order.
	// Supports all four order types: MARKET, LIMIT, STOP, STOP_LIMIT.
	CalculateOrderDetails(ctx context.Context, req *OrderCalculationRequest) (*OrderCalculationResponse, error)

	// CreateOrder persists a new order and applies the approval workflow:
	//   - Client (not in actuary_info)  → APPROVED immediately
	//   - Supervisor                     → APPROVED immediately
	//   - Agent:
	//       need_approval == true                   → PENDING
	//       used_limit + approx_price > daily limit → PENDING
	//       otherwise                               → APPROVED; used_limit incremented
	CreateOrder(ctx context.Context, req *CreateOrderRequest) (*Order, error)

	// ─── Supervisor dashboard ─────────────────────────────────────────────────

	// ListOrders returns all orders, optionally filtered by status.
	// Pass nil to return orders of every status (full supervisor overview).
	// Pass &OrderStatusPending to list only orders awaiting approval.
	ListOrders(ctx context.Context, statusFilter *OrderStatus) ([]Order, error)

	// ListOrdersByUser returns orders belonging to a single user, newest first.
	// Used by the client-facing "Moji nalozi" view.
	// Pass nil statusFilter to return orders of every status.
	ListOrdersByUser(ctx context.Context, userID int64, statusFilter *OrderStatus) ([]Order, error)

	// ApproveOrder transitions a PENDING order to APPROVED and records the
	// reviewing supervisor's ID in approved_by.
	//
	// NOTE: When the order belongs to an AGENT, used_limit is incremented via
	// IncrementUsedLimitIfWithin.  If the agent's limit is already exceeded at
	// the time of approval (edge case: limit was lowered after the order was
	// created), the increment is silently skipped — the supervisor made the
	// conscious decision to approve regardless.
	//
	// Returns ErrOrderNotFound if the order does not exist.
	// Returns ErrInvalidOrderState if the order is not currently PENDING.
	ApproveOrder(ctx context.Context, orderID int64, supervisorID int64) (*Order, error)

	// DeclineOrder transitions a PENDING order to DECLINED and records the
	// reviewing supervisor's ID in approved_by.
	//
	// Returns ErrOrderNotFound if the order does not exist.
	// Returns ErrInvalidOrderState if the order is not currently PENDING.
	DeclineOrder(ctx context.Context, orderID int64, supervisorID int64) (*Order, error)

	// ─── Cancelation ─────────────────────────────────────────────────────────

	// CancelOrder manually stops an order that is PENDING or APPROVED,
	// regardless of whether partial fills have already been recorded.
	// It atomically sets status=CANCELED, is_done=true, and preserves
	// remaining_portions so callers can compute executedQty = Quantity − RemainingPortions.
	//
	// Permission: requestedBy must be either the order's original owner
	// (order.UserID == requestedBy) or a Supervisor.  callerIsSupervisor is
	// the JWT-derived flag (same as CreateOrderRequest.IsSupervisor) and is
	// checked first; the actuary_info DB lookup is skipped when it is true.
	// This matches the pattern established by CreateOrder and prevents stale
	// actuary_info records from blocking legitimate supervisors.
	//
	// Returns ErrOrderNotFound if the order does not exist.
	// Returns ErrPermissionDenied if the caller is not the owner and not a supervisor.
	// Returns ErrInvalidOrderState if the order is already DONE, DECLINED,
	// or CANCELED (i.e., no longer active).
	CancelOrder(ctx context.Context, orderID int64, requestedBy int64, callerIsSupervisor bool) (*Order, error)
}
