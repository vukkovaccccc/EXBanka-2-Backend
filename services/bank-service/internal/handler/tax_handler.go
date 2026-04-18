package handler

// tax_handler.go — HTTP handleri „Porez Tracking” portala (supervisor-only).
//
// Sva poslovna logika (obračun, naplata, listing eligible korisnika) živi u
// service.TaxService — handler samo validira zahtev, konvertuje tipove i
// delegira. Nema duplikata SQL-a između handlera i MonthlyTaxWorker-a.
//
// Rute:
//   GET  /bank/tax/users        — lista svih trgovačkih korisnika sa dugovanjem
//                                  (query: role=client|actuary, firstName, lastName)
//   POST /bank/tax/calculate    — ručni obračun + naplata; body opcion { year, month }

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/service"

	"google.golang.org/grpc/metadata"
)

// withBearer pakuje Authorization header u gRPC outgoing metadata tako da
// TaxService → UserServiceClient može forward-ovati token ka user-service-u.
// Bez ovoga HTTP kontekst nema gRPC metadata, pa user-service odbacuje poziv.
func withBearer(ctx context.Context, r *http.Request) context.Context {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", h)
}

// TaxHandler serves all /bank/tax/* endpoints.
type TaxHandler struct {
	taxService *service.TaxService
	jwtSecret  string
}

// NewTaxHandler constructs the handler. Sva stanja (DB, exchange, userClient,
// stateRevenueAccountID) drži TaxService; handler vidi samo njegov interfejs.
func NewTaxHandler(taxService *service.TaxService, jwtSecret string) *TaxHandler {
	return &TaxHandler{
		taxService: taxService,
		jwtSecret:  jwtSecret,
	}
}

// ServeHTTP dispatches to sub-handlers.
func (h *TaxHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	claims, ok := h.verifySupervisor(w, r)
	if !ok {
		return
	}

	switch {
	case r.URL.Path == "/bank/tax/users" && r.Method == http.MethodGet:
		h.listUsers(w, r, claims)
	case r.URL.Path == "/bank/tax/calculate" && r.Method == http.MethodPost:
		h.calculateAndCollect(w, r, claims)
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// ─── GET /bank/tax/users ──────────────────────────────────────────────────────

type taxUserRecord struct {
	UserID    string  `json:"userId"`
	UserType  string  `json:"userType"`
	FirstName string  `json:"firstName"`
	LastName  string  `json:"lastName"`
	Email     string  `json:"email,omitempty"`
	TaxDebt   float64 `json:"taxDebt"`
}

type taxUsersResponse struct {
	Users []taxUserRecord `json:"users"`
}

func (h *TaxHandler) listUsers(w http.ResponseWriter, r *http.Request, _ *auth.AccessClaims) {
	filter := service.TaxUserFilter{
		Role:      strings.TrimSpace(r.URL.Query().Get("role")),
		FirstName: strings.TrimSpace(r.URL.Query().Get("firstName")),
		LastName:  strings.TrimSpace(r.URL.Query().Get("lastName")),
	}

	rows, err := h.taxService.ListTaxEligibleUsers(withBearer(r.Context(), r), filter)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "nije moguće učitati listu korisnika")
		return
	}

	out := make([]taxUserRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, taxUserRecord{
			UserID:    strconv.FormatInt(row.UserID, 10),
			UserType:  row.UserType,
			FirstName: row.FirstName,
			LastName:  row.LastName,
			Email:     row.Email,
			TaxDebt:   row.TaxDebt,
		})
	}

	writeJSON(w, http.StatusOK, taxUsersResponse{Users: out})
}

// ─── POST /bank/tax/calculate ─────────────────────────────────────────────────

type taxCalculateRequest struct {
	Year  int `json:"year"`
	Month int `json:"month"`
}

type taxCalculateResponse struct {
	ProcessedUsers    int     `json:"processedUsers"`
	TotalCollectedRSD float64 `json:"totalCollectedRsd"`
	FullyCollected    int     `json:"fullyCollected"`
	Partial           int     `json:"partial"`
	Unpaid            int     `json:"unpaid"`
	AlreadyCollected  int     `json:"alreadyCollected"`
	Errors            int     `json:"errors"`
	PeriodStart       string  `json:"periodStart"`
	PeriodEnd         string  `json:"periodEnd"`
	Message           string  `json:"message"`
}

func (h *TaxHandler) calculateAndCollect(w http.ResponseWriter, r *http.Request, _ *auth.AccessClaims) {
	ctx := r.Context()
	now := time.Now()

	// Opcioni body: { "year": 2026, "month": 3 } → ceo kalendarski mesec.
	// Ako body nije prosleđen, default je tekući mesec od 1. do NOW() (manual run).
	var start, end time.Time
	if r.ContentLength > 0 {
		var req taxCalculateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "neispravan JSON body")
			return
		}
		if req.Year > 0 && req.Month >= 1 && req.Month <= 12 {
			candidate := time.Date(req.Year, time.Month(req.Month), 1, 0, 0, 0, 0, now.Location())
			if candidate.After(now) {
				writeJSONError(w, http.StatusBadRequest, "izabrani period je u budućnosti")
				return
			}
			start, end = service.MonthWindow(req.Year, time.Month(req.Month), now.Location())
		}
	}
	if start.IsZero() {
		start, end = service.CurrentMonthWindow(now)
	}

	summary, err := h.taxService.CalculateAndCollectForPeriod(ctx, start, end, service.TaxTriggeredByManual)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrPeriodAlreadyRunning):
			writeJSONError(w, http.StatusConflict, "obračun za izabrani period je već u toku")
		case errors.Is(err, service.ErrStateAccountMissing):
			writeJSONError(w, http.StatusInternalServerError, "državni RSD račun nije pronađen — obračun prekinut")
		case errors.Is(err, service.ErrUSDRateUnavailable):
			writeJSONError(w, http.StatusServiceUnavailable, "USD kurs nije dostupan — obračun odložen")
		default:
			writeJSONError(w, http.StatusInternalServerError, "obračun poreza nije uspeo")
		}
		return
	}

	msg := "Obračun poreza je uspešno izvršen."
	if summary.ProcessedUsers == 0 {
		msg = "Nema oporezivih profita za izabrani period."
	}

	writeJSON(w, http.StatusOK, taxCalculateResponse{
		ProcessedUsers:    summary.ProcessedUsers,
		TotalCollectedRSD: summary.TotalCollectedF64,
		FullyCollected:    summary.FullyCollected,
		Partial:           summary.Partial,
		Unpaid:            summary.Unpaid,
		AlreadyCollected:  summary.AlreadyCollected,
		Errors:            summary.Errors,
		PeriodStart:       summary.PeriodStart.UTC().Format(time.RFC3339),
		PeriodEnd:         summary.PeriodEnd.UTC().Format(time.RFC3339),
		Message:           msg,
	})
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// verifySupervisor dopušta ADMIN-ima i EMPLOYEE korisnicima sa SUPERVISOR permisijom.
func (h *TaxHandler) verifySupervisor(w http.ResponseWriter, r *http.Request) (*auth.AccessClaims, bool) {
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
	if claims.UserType != "EMPLOYEE" && claims.UserType != "ADMIN" {
		writeJSONError(w, http.StatusForbidden, "pristup dozvoljen samo supervizorima")
		return nil, false
	}
	if claims.UserType == "EMPLOYEE" {
		isSupervisor := false
		for _, p := range claims.Permissions {
			if p == "SUPERVISOR" {
				isSupervisor = true
				break
			}
		}
		if !isSupervisor {
			writeJSONError(w, http.StatusForbidden, "pristup dozvoljen samo supervizorima")
			return nil, false
		}
	}
	return claims, true
}
