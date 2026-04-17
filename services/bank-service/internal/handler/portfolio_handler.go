package handler

// portfolio_handler.go — HTTP handlers for the "Moj Portfolio" portal.
//
// Endpoints:
//   GET  /bank/portfolio/my        — returns the caller's current holdings
//   POST /bank/portfolio/publish   — marks stock shares as publicly visible for OTC
//   POST /bank/portfolio/exercise  — exercises an option (actuaries only)
//
// All endpoints require a valid JWT access token.
// Auth is validated directly against jwtSecret (same pattern as exchange_handler.go).

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"

	"gorm.io/gorm"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// holdingRow is the raw result of the portfolio aggregation query.
type holdingRow struct {
	ListingID    int64     `gorm:"column:listing_id"`
	AccountID    int64     `gorm:"column:account_id"`
	NetShares    int64     `gorm:"column:net_shares"`
	AvgBuyPrice  float64   `gorm:"column:avg_buy_price"`
	LastModified time.Time `gorm:"column:last_modified"`
}

// publicShareRow represents one entry in the public_shares table.
type publicShareRow struct {
	ID        int64 `gorm:"column:id;primaryKey"`
	ListingID int64 `gorm:"column:listing_id"`
	UserID    int64 `gorm:"column:user_id"`
	Quantity  int   `gorm:"column:quantity"`
}

func (publicShareRow) TableName() string { return "core_banking.public_shares" }

// PortfolioHandler serves all /bank/portfolio/* endpoints.
type PortfolioHandler struct {
	db             *gorm.DB
	listingService domain.ListingService
	jwtSecret      string
}

// NewPortfolioHandler constructs the handler with its dependencies.
func NewPortfolioHandler(
	db *gorm.DB,
	listingService domain.ListingService,
	jwtSecret string,
) *PortfolioHandler {
	return &PortfolioHandler{
		db:             db,
		listingService: listingService,
		jwtSecret:      jwtSecret,
	}
}

// ServeHTTP dispatches to the correct sub-handler based on the path.
func (h *PortfolioHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// auth
	claims, ok := h.verifyClaims(w, r)
	if !ok {
		return
	}

	path := r.URL.Path
	switch {
	case path == "/bank/portfolio/my" && r.Method == http.MethodGet:
		h.getMyPortfolio(w, r, claims)
	case path == "/bank/portfolio/publish" && r.Method == http.MethodPost:
		h.publishShares(w, r, claims)
	case path == "/bank/portfolio/exercise" && r.Method == http.MethodPost:
		h.exerciseOption(w, r, claims)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// ─── GET /bank/portfolio/my ───────────────────────────────────────────────────

type holdingResponse struct {
	ListingID         string  `json:"listingId"`
	Ticker            string  `json:"ticker"`
	Name              string  `json:"name"`
	ListingType       string  `json:"listingType"`
	Quantity          int64   `json:"quantity"`
	AvailableQuantity int64   `json:"availableQuantity"`
	CurrentPrice      float64 `json:"currentPrice"`
	AvgBuyPrice       float64 `json:"avgBuyPrice"`
	Profit            float64 `json:"profit"`
	LastModified      string  `json:"lastModified"`
	AccountID         string  `json:"accountId"`
	PublicShares      int     `json:"publicShares"`
	DetailsJSON       string  `json:"detailsJson"`
}

type portfolioResponse struct {
	Holdings    []holdingResponse `json:"holdings"`
	TotalProfit float64           `json:"totalProfit"`
	TaxPaidRSD  float64           `json:"taxPaidRsd"`
	TaxUnpaid   float64           `json:"taxUnpaid"`
}

func (h *PortfolioHandler) getMyPortfolio(w http.ResponseWriter, r *http.Request, claims *auth.AccessClaims) {
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "invalid user id in token")
		return
	}

	ctx := r.Context()

	// ── 1. Aggregate net holdings from DONE and partially-filled CANCELED orders ──
	//
	// Net shares per listing: sum of BUY quantities minus sum of SELL quantities.
	// CANCELED BUY orders with partial fills are included: the executed portion is
	// quantity - remaining_portions (remaining_portions is preserved on cancel).
	// Average buy price: weighted average of executed_price from order_transactions
	// for BUY orders; falls back to price_per_unit if no transactions recorded.
	var rows []holdingRow
	err = h.db.WithContext(ctx).Raw(`
		WITH buy_agg AS (
			SELECT
				o.listing_id,
				o.account_id,
				SUM(
					CASE
						WHEN o.status = 'DONE'
							THEN o.quantity
						ELSE
							(o.quantity - o.remaining_portions)
					END
				)                                  AS bought,
				MAX(o.last_modified)               AS last_mod,
				CASE
					WHEN SUM(tx.qty) > 0
					THEN SUM(tx.value) / SUM(tx.qty)
					ELSE AVG(CAST(o.price_per_unit AS FLOAT))
				END AS avg_buy
			FROM core_banking.orders o
			LEFT JOIN (
				SELECT
					ot.order_id,
					SUM(CAST(ot.executed_price AS FLOAT) * ot.executed_quantity) AS value,
					SUM(ot.executed_quantity) AS qty
				FROM core_banking.order_transactions ot
				GROUP BY ot.order_id
			) tx ON tx.order_id = o.id
			WHERE o.user_id = ? AND o.direction = 'BUY'
			  AND (
			      (o.status = 'DONE' AND o.is_done = TRUE)
			      OR (o.status = 'CANCELED' AND (o.quantity - o.remaining_portions) > 0)
			  )
			GROUP BY o.listing_id, o.account_id
		),
		sell_agg AS (
			SELECT listing_id,
				SUM(
					CASE
						WHEN status = 'DONE'
							THEN quantity
						ELSE
							(quantity - remaining_portions)
					END
				) AS sold
			FROM core_banking.orders
			WHERE user_id = ? AND direction = 'SELL'
			  AND (
			      (status = 'DONE' AND is_done = TRUE)
			      OR (status = 'CANCELED' AND (quantity - remaining_portions) > 0)
			  )
			GROUP BY listing_id
		)
		SELECT
			b.listing_id,
			b.account_id,
			(b.bought - COALESCE(s.sold, 0)) AS net_shares,
			COALESCE(b.avg_buy, 0)           AS avg_buy_price,
			b.last_mod                        AS last_modified
		FROM buy_agg b
		LEFT JOIN sell_agg s ON s.listing_id = b.listing_id
		WHERE (b.bought - COALESCE(s.sold, 0)) > 0
	`, userID, userID).Scan(&rows).Error
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "query error: "+err.Error())
		return
	}

	// ── 2. Load active (pending/approved) SELL quantities per listing ─────────
	type activeSellRow struct {
		ListingID  int64 `gorm:"column:listing_id"`
		ActiveSell int64 `gorm:"column:active_sell"`
	}
	var activeSellRows []activeSellRow
	h.db.WithContext(ctx).Raw(`
		SELECT listing_id, SUM(quantity) AS active_sell
		FROM core_banking.orders
		WHERE user_id = ? AND direction = 'SELL'
		  AND status IN ('PENDING', 'APPROVED')
		  AND is_done = FALSE
		GROUP BY listing_id
	`, userID).Scan(&activeSellRows)
	activeSellMap := make(map[int64]int64, len(activeSellRows))
	for _, a := range activeSellRows {
		activeSellMap[a.ListingID] = a.ActiveSell
	}

	// ── 3. Load public share counts ────────────────────────────────────────────
	type pubRow struct {
		ListingID int64 `gorm:"column:listing_id"`
		Total     int   `gorm:"column:total"`
	}
	var pubRows []pubRow
	h.db.WithContext(ctx).Raw(`
		SELECT listing_id, SUM(quantity) AS total
		FROM core_banking.public_shares
		WHERE user_id = ?
		GROUP BY listing_id
	`, userID).Scan(&pubRows)
	pubMap := make(map[int64]int, len(pubRows))
	for _, p := range pubRows {
		pubMap[p.ListingID] = p.Total
	}

	// ── 4. Load tax data (paid this year, unpaid this month) ─────────────────
	now := time.Now()
	var taxPaid float64
	h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(amount_rsd), 0)
		FROM core_banking.tax_records
		WHERE user_id = ? AND year = ? AND paid = TRUE
	`, userID, now.Year()).Scan(&taxPaid)

	var taxUnpaid float64
	h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(amount_rsd), 0)
		FROM core_banking.tax_records
		WHERE user_id = ? AND year = ? AND month = ? AND paid = FALSE
	`, userID, now.Year(), int(now.Month())).Scan(&taxUnpaid)

	// ── 5. Enrich with current listing data ────────────────────────────────────
	holdings := make([]holdingResponse, 0, len(rows))
	var totalProfit float64

	for _, row := range rows {
		listing, err := h.listingService.GetListingByID(ctx, row.ListingID)
		if err != nil {
			continue // skip stale listing references
		}

		// FOREX orders are excluded from the portfolio — balances are tracked
		// through bank accounts in different currencies, not as portfolio holdings.
		if listing.ListingType == domain.ListingTypeForex {
			continue
		}

		profit := (listing.Price - row.AvgBuyPrice) * float64(row.NetShares)
		// For STOCKs only: accumulate profit for the "profit section"
		if listing.ListingType == domain.ListingTypeStock {
			totalProfit += profit
		}

		activeSell := activeSellMap[row.ListingID]
		available := row.NetShares - activeSell
		if available < 0 {
			available = 0
		}

		holdings = append(holdings, holdingResponse{
			ListingID:         strconv.FormatInt(row.ListingID, 10),
			Ticker:            listing.Ticker,
			Name:              listing.Name,
			ListingType:       string(listing.ListingType),
			Quantity:          row.NetShares,
			AvailableQuantity: available,
			CurrentPrice:      listing.Price,
			AvgBuyPrice:       row.AvgBuyPrice,
			Profit:            profit,
			LastModified:      row.LastModified.UTC().Format(time.RFC3339),
			AccountID:         strconv.FormatInt(row.AccountID, 10),
			PublicShares:      pubMap[row.ListingID],
			DetailsJSON:       listing.DetailsJSON,
		})
	}

	writeJSON(w, http.StatusOK, portfolioResponse{
		Holdings:    holdings,
		TotalProfit: totalProfit,
		TaxPaidRSD:  taxPaid,
		TaxUnpaid:   taxUnpaid,
	})
}

// ─── POST /bank/portfolio/publish ─────────────────────────────────────────────

type publishRequest struct {
	ListingID string `json:"listingId"`
	Quantity  int    `json:"quantity"`
}

func (h *PortfolioHandler) publishShares(w http.ResponseWriter, r *http.Request, claims *auth.AccessClaims) {
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "invalid user id")
		return
	}

	var req publishRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Quantity <= 0 {
		writeJSONError(w, http.StatusBadRequest, "quantity must be greater than 0")
		return
	}
	listingID, err := strconv.ParseInt(req.ListingID, 10, 64)
	if err != nil || listingID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid listingId")
		return
	}

	ctx := r.Context()

	// Verify user actually holds at least `quantity` of this listing.
	// Include CANCELED BUY orders with partial fills (executed = quantity - remaining_portions).
	var netShares int64
	h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(
			CASE
				WHEN direction = 'BUY'  AND status = 'DONE'
					THEN quantity * contract_size
				WHEN direction = 'BUY'  AND status = 'CANCELED'
					THEN (quantity - remaining_portions) * contract_size
				WHEN direction = 'SELL' AND status = 'DONE'
					THEN -(quantity * contract_size)
				WHEN direction = 'SELL' AND status = 'CANCELED'
					THEN -((quantity - remaining_portions) * contract_size)
				ELSE 0
			END
		), 0)
		FROM core_banking.orders
		WHERE user_id = ? AND listing_id = ?
		  AND (
		      (status = 'DONE' AND is_done = TRUE)
		      OR (status = 'CANCELED' AND (quantity - remaining_portions) > 0)
		  )
	`, userID, listingID).Scan(&netShares)

	var alreadyPublic int
	h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(quantity),0) FROM core_banking.public_shares
		WHERE user_id = ? AND listing_id = ?
	`, userID, listingID).Scan(&alreadyPublic)

	available := int(netShares) - alreadyPublic
	if req.Quantity > available {
		writeJSONError(w, http.StatusBadRequest, "insufficient shares available for publishing")
		return
	}

	row := publicShareRow{
		ListingID: listingID,
		UserID:    userID,
		Quantity:  req.Quantity,
	}
	if err := h.db.WithContext(ctx).Create(&row).Error; err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to publish shares")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Akcije su uspešno objavljene za OTC trgovanje."})
}

// ─── POST /bank/portfolio/exercise ───────────────────────────────────────────

type exerciseRequest struct {
	ListingID string `json:"listingId"`
}

func (h *PortfolioHandler) exerciseOption(w http.ResponseWriter, r *http.Request, claims *auth.AccessClaims) {
	// Only actuaries (EMPLOYEE type) can exercise options
	if claims.UserType != "EMPLOYEE" {
		writeJSONError(w, http.StatusForbidden, "samo aktuari mogu da iskoriste opcije")
		return
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "invalid user id")
		return
	}

	var req exerciseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	listingID, err := strconv.ParseInt(req.ListingID, 10, 64)
	if err != nil || listingID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid listingId")
		return
	}

	ctx := r.Context()

	// Load listing to verify it's an option and check settlement + in-the-money
	listing, err := h.listingService.GetListingByID(ctx, listingID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "opcija nije pronađena")
		return
	}
	if listing.ListingType != domain.ListingTypeOption {
		writeJSONError(w, http.StatusBadRequest, "hartija nije opcija")
		return
	}

	// Parse settlement_date and strike from details_json
	var details struct {
		SettlementDate string  `json:"settlement_date"`
		StrikePrice    float64 `json:"strike_price"`
		OptionType     string  `json:"option_type"` // "CALL" or "PUT"
	}
	if err := json.Unmarshal([]byte(listing.DetailsJSON), &details); err != nil || details.SettlementDate == "" {
		writeJSONError(w, http.StatusBadRequest, "opcija nema ispravne podatke (settlement_date, strike_price)")
		return
	}

	settlementDate, err := time.Parse("2006-01-02", details.SettlementDate)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "neispravan datum isteka opcije")
		return
	}
	if time.Now().After(settlementDate) {
		writeJSONError(w, http.StatusBadRequest, "rok iskorišćavanja opcije je istekao")
		return
	}

	// Check in-the-money
	inTheMoney := false
	switch strings.ToUpper(details.OptionType) {
	case "CALL":
		inTheMoney = listing.Price > details.StrikePrice
	case "PUT":
		inTheMoney = listing.Price < details.StrikePrice
	default:
		writeJSONError(w, http.StatusBadRequest, "nepoznat tip opcije (CALL/PUT)")
		return
	}
	if !inTheMoney {
		writeJSONError(w, http.StatusBadRequest, "opcija nije in the money")
		return
	}

	// Check user holds the option
	var netOptionQty int64
	h.db.WithContext(ctx).Raw(`
		SELECT COALESCE(SUM(CASE WHEN direction='BUY' THEN quantity ELSE -quantity END), 0)
		FROM core_banking.orders
		WHERE user_id = ? AND listing_id = ? AND status = 'DONE' AND is_done = TRUE
	`, userID, listingID).Scan(&netOptionQty)

	if netOptionQty <= 0 {
		writeJSONError(w, http.StatusBadRequest, "ne posedujete ovu opciju")
		return
	}

	// Profit calculation
	sharesPerOption := int64(100)
	totalShares := netOptionQty * sharesPerOption

	var profit float64
	switch strings.ToUpper(details.OptionType) {
	case "PUT":
		profit = (details.StrikePrice - listing.Price) * float64(totalShares)
	case "CALL":
		profit = (listing.Price - details.StrikePrice) * float64(totalShares)
	}

	// ── Find the account that was used for the original option purchase ──────────
	var accountID int64
	h.db.WithContext(ctx).Raw(`
		SELECT account_id FROM core_banking.orders
		WHERE user_id = ? AND listing_id = ? AND direction = 'BUY' AND status = 'DONE' AND is_done = TRUE
		ORDER BY created_at DESC LIMIT 1
	`, userID, listingID).Scan(&accountID)

	if accountID == 0 {
		writeJSONError(w, http.StatusBadRequest, "nije pronađen originalni račun za kupovinu opcije")
		return
	}

	// ── Credit net profit to the account ─────────────────────────────────────────
	if profit > 0 {
		if err := h.db.WithContext(ctx).Exec(`
			UPDATE core_banking.racun
			SET stanje_racuna = stanje_racuna + ?
			WHERE id = ?
		`, profit, accountID).Error; err != nil {
			writeJSONError(w, http.StatusInternalServerError, "greška pri uplati profita")
			return
		}

		// Record transaction
		h.db.WithContext(ctx).Exec(`
			INSERT INTO core_banking.transakcija (racun_id, tip_transakcije, iznos, opis, vreme_izvrsavanja, status)
			VALUES (?, 'UPLATA', ?, 'Iskorišćavanje opcije', NOW(), 'IZVRSEN')
		`, accountID, profit)
	}

	// ── Close option position: insert a synthetic DONE SELL order ────────────────
	// This removes the options from the portfolio aggregation query.
	now := time.Now().UTC()
	h.db.WithContext(ctx).Exec(`
		INSERT INTO core_banking.orders
		  (user_id, account_id, listing_id, order_type, direction, quantity, contract_size,
		   status, is_done, remaining_portions, after_hours, all_or_none, margin,
		   last_modified, created_at)
		VALUES (?, ?, ?, 'MARKET', 'SELL', ?, 1, 'DONE', TRUE, 0, FALSE, FALSE, FALSE, ?, ?)
	`, userID, accountID, listingID, netOptionQty, now, now)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "Opcija je uspešno iskorišćena.",
		"netProfit":   profit,
		"totalShares": totalShares,
		"strikePrice": details.StrikePrice,
		"marketPrice": listing.Price,
		"optionType":  details.OptionType,
	})
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (h *PortfolioHandler) verifyClaims(w http.ResponseWriter, r *http.Request) (*auth.AccessClaims, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	claims, err := auth.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "), h.jwtSecret)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return nil, false
	}
	return claims, true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
