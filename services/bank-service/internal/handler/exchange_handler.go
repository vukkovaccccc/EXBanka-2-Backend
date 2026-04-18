package handler

// exchange_handler.go — HTTP handleri za menjačnicu.
//
// ExchangeTransferHandler  — POST /bank/client/exchange-transfers
//   Kreira nalog konverzije valuta (intent + mobilna verifikacija).
//   Registrovan direktno na http.ServeMux jer proto schema ne sadrži
//   polje convertedIznos.
//
// ExchangeRateHandler — GET  /bank/exchange-rates[?from=X&to=Y&amount=Z]
//                       POST /bank/exchange-rates/execute
//   Vraća kursnu listu / informativnu konverziju / direktno izvršava transfer.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"banka-backend/services/bank-service/internal/domain"
	auth "banka-backend/shared/auth"
)

// ─── ExchangeTransferHandler ─────────────────────────────────────────────────

// ExchangeTransferHandler kreira nalog konverzije valuta između dva računa istog korisnika.
type ExchangeTransferHandler struct {
	paymentService domain.PaymentService
	jwtSecret      string
}

// NewExchangeTransferHandler kreira novi handler za konverziju valuta.
func NewExchangeTransferHandler(paymentService domain.PaymentService, jwtSecret string) *ExchangeTransferHandler {
	return &ExchangeTransferHandler{
		paymentService: paymentService,
		jwtSecret:      jwtSecret,
	}
}

type exchangeTransferRequest struct {
	IdempotencyKey  string  `json:"idempotencyKey"`
	SourceAccountId int64   `json:"sourceAccountId"`
	TargetAccountId int64   `json:"targetAccountId"`
	Amount          float64 `json:"amount"`          // iznos koji se skida sa izvornog računa
	ConvertedAmount float64 `json:"convertedAmount"` // iznos koji se upisuje na ciljni račun
	SvrhaPlacanja   string  `json:"svrhaPlacanja"`
}

type exchangeTransferResponse struct {
	IntentId   int64  `json:"intentId"`
	ActionId   int64  `json:"actionId"`
	BrojNaloga string `json:"brojNaloga"`
	Status     string `json:"status"`
}

// ServeHTTP obrađuje POST /bank/client/exchange-transfers zahteve.
func (h *ExchangeTransferHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := auth.VerifyToken(token, h.jwtSecret)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if claims.UserType != "CLIENT" {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var req exchangeTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}

	if req.IdempotencyKey == "" {
		writeJSONError(w, http.StatusBadRequest, "idempotencyKey je obavezan")
		return
	}
	if req.Amount <= 0 {
		writeJSONError(w, http.StatusBadRequest, "amount mora biti veći od 0")
		return
	}
	if req.ConvertedAmount <= 0 {
		writeJSONError(w, http.StatusBadRequest, "convertedAmount mora biti veći od 0")
		return
	}
	if req.SourceAccountId == req.TargetAccountId {
		writeJSONError(w, http.StatusBadRequest, "izvorni i ciljni račun moraju biti različiti")
		return
	}

	svrha := req.SvrhaPlacanja
	if svrha == "" {
		svrha = "Konverzija valuta"
	}

	input := domain.CreateTransferIntentInput{
		IdempotencyKey:    req.IdempotencyKey,
		RacunPlatioceID:   req.SourceAccountId,
		RacunPrimaocaID:   req.TargetAccountId,
		Iznos:             req.Amount,
		ConvertedIznos:    req.ConvertedAmount,
		SvrhaPlacanja:     svrha,
		InitiatedByUserID: userID,
	}

	intent, actionID, err := h.paymentService.CreateTransferIntent(r.Context(), input)
	if err != nil {
		status := http.StatusInternalServerError
		msg := err.Error()
		switch {
		case errors.Is(err, domain.ErrSameAccount),
			errors.Is(err, domain.ErrAccountNotOwned),
			errors.Is(err, domain.ErrRecipientAccountInvalid),
			errors.Is(err, domain.ErrInsufficientFunds),
			errors.Is(err, domain.ErrDailyLimitExceeded),
			errors.Is(err, domain.ErrMonthlyLimitExceeded):
			status = http.StatusBadRequest
		}
		writeJSONError(w, status, msg)
		return
	}

	resp := exchangeTransferResponse{
		IntentId:   intent.ID,
		ActionId:   actionID,
		BrojNaloga: intent.BrojNaloga,
		Status:     intent.Status,
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// ─── ExchangeRateHandler ──────────────────────────────────────────────────────

// ExchangeRateHandler serves exchange rate endpoints over plain HTTP.
type ExchangeRateHandler struct {
	exchangeService domain.ExchangeService
	jwtSecret       string
}

// NewExchangeRateHandler creates a new ExchangeRateHandler.
func NewExchangeRateHandler(exchangeService domain.ExchangeService, jwtSecret string) *ExchangeRateHandler {
	return &ExchangeRateHandler{
		exchangeService: exchangeService,
		jwtSecret:       jwtSecret,
	}
}

// ServeHTTP routes requests based on path suffix and HTTP method:
//
//	GET  …/exchange-rates          → rates list or conversion
//	POST …/exchange-rates/execute  → execute transfer
func (h *ExchangeRateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		exchangeWriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	claims, err := auth.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "), h.jwtSecret)
	if err != nil {
		exchangeWriteJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	path := strings.TrimRight(r.URL.Path, "/")
	isExecute := strings.HasSuffix(path, "/execute")

	switch {
	case isExecute && r.Method == http.MethodPost:
		h.handleExecute(w, r, claims)
	case !isExecute && r.Method == http.MethodGet:
		q := r.URL.Query()
		from, to, amountStr := q.Get("from"), q.Get("to"), q.Get("amount")
		if from != "" && to != "" && amountStr != "" {
			h.handleConvert(w, r, from, to, amountStr)
		} else {
			h.handleRatesList(w, r)
		}
	default:
		exchangeWriteJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// ─── Kursna lista ─────────────────────────────────────────────────────────────

type exchangeRateJSON struct {
	Oznaka   string  `json:"oznaka"`
	Naziv    string  `json:"naziv"`
	Kupovni  float64 `json:"kupovni"`
	Srednji  float64 `json:"srednji"`
	Prodajni float64 `json:"prodajni"`
}

type ratesListJSON struct {
	Rates []exchangeRateJSON `json:"rates"`
}

func (h *ExchangeRateHandler) handleRatesList(w http.ResponseWriter, r *http.Request) {
	rates, err := h.exchangeService.GetRates(r.Context())
	if err != nil {
		exchangeWriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "kursna lista nije dostupna"})
		return
	}

	items := make([]exchangeRateJSON, 0, len(rates))
	for _, rate := range rates {
		items = append(items, exchangeRateJSON{
			Oznaka:   rate.Oznaka,
			Naziv:    rate.Naziv,
			Kupovni:  rate.Kupovni,
			Srednji:  rate.Srednji,
			Prodajni: rate.Prodajni,
		})
	}
	exchangeWriteJSON(w, http.StatusOK, ratesListJSON{Rates: items})
}

// ─── Konverzija (informativna) ────────────────────────────────────────────────

type convertJSON struct {
	Result    float64 `json:"result"`
	Bruto     float64 `json:"bruto"`
	Provizija float64 `json:"provizija"`
	ViaRSD    bool    `json:"viaRsd"`
	RateNote  string  `json:"rateNote"`
}

func (h *ExchangeRateHandler) handleConvert(w http.ResponseWriter, r *http.Request, from, to, amountStr string) {
	amount, err := strconv.ParseFloat(amountStr, 64)
	if err != nil || amount <= 0 {
		exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": "neispravan iznos"})
		return
	}

	result, err := h.exchangeService.CalculateExchange(r.Context(), from, to, amount)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrExchangeInvalidAmount):
			exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeRateNotFound):
			exchangeWriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		default:
			exchangeWriteJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "konverzija trenutno nije dostupna"})
		}
		return
	}

	exchangeWriteJSON(w, http.StatusOK, convertJSON{
		Result:    result.Result,
		Bruto:     result.Bruto,
		Provizija: result.Provizija,
		ViaRSD:    result.ViaRSD,
		RateNote:  result.RateNote,
	})
}

// ─── Izvršenje konverzije ─────────────────────────────────────────────────────

type executeTransferRequest struct {
	SourceAccountID int64   `json:"sourceAccountId"`
	TargetAccountID int64   `json:"targetAccountId"`
	FromOznaka      string  `json:"fromOznaka"`
	ToOznaka        string  `json:"toOznaka"`
	Amount          float64 `json:"amount"`
}

type executeTransferResponse struct {
	ReferenceID     string  `json:"referenceId"`
	SourceAccountID int64   `json:"sourceAccountId"`
	TargetAccountID int64   `json:"targetAccountId"`
	FromOznaka      string  `json:"fromOznaka"`
	ToOznaka        string  `json:"toOznaka"`
	OriginalAmount  float64 `json:"originalAmount"`
	GrossAmount     float64 `json:"grossAmount"`
	Provizija       float64 `json:"provizija"`
	NetAmount       float64 `json:"netAmount"`
	ViaRSD          bool    `json:"viaRsd"`
	RateNote        string  `json:"rateNote"`
}

func (h *ExchangeRateHandler) handleExecute(w http.ResponseWriter, r *http.Request, claims *auth.AccessClaims) {
	var req executeTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": "neispravno telo zahteva"})
		return
	}

	vlasnikID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		exchangeWriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "neispravan korisnički ID u tokenu"})
		return
	}

	input := domain.ExchangeTransferInput{
		VlasnikID:       vlasnikID,
		SourceAccountID: req.SourceAccountID,
		TargetAccountID: req.TargetAccountID,
		FromOznaka:      req.FromOznaka,
		ToOznaka:        req.ToOznaka,
		Amount:          req.Amount,
	}

	result, err := h.exchangeService.ExecuteExchangeTransfer(r.Context(), input)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrExchangeInvalidAmount):
			exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeSameCurrency):
			exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeSameAccount):
			exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeWrongCurrency):
			exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeAccountInactive):
			exchangeWriteJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeAccountNotOwned):
			exchangeWriteJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrExchangeInsufficientFunds):
			exchangeWriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrAccountNotFound):
			exchangeWriteJSON(w, http.StatusNotFound, map[string]string{"error": "račun nije pronađen"})
		case errors.Is(err, domain.ErrExchangeRateNotFound):
			exchangeWriteJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		default:
			exchangeWriteJSON(w, http.StatusInternalServerError, map[string]string{"error": "greška pri izvršenju konverzije"})
		}
		return
	}

	exchangeWriteJSON(w, http.StatusOK, executeTransferResponse{
		ReferenceID:     result.ReferenceID,
		SourceAccountID: result.SourceAccountID,
		TargetAccountID: result.TargetAccountID,
		FromOznaka:      result.FromOznaka,
		ToOznaka:        result.ToOznaka,
		OriginalAmount:  result.OriginalAmount,
		GrossAmount:     result.GrossAmount,
		Provizija:       result.Provizija,
		NetAmount:       result.NetAmount,
		ViaRSD:          result.ViaRSD,
		RateNote:        result.RateNote,
	})
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

// writeJSONError writes a {"message": msg} error response (used by ExchangeTransferHandler).
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// exchangeWriteJSON writes any value as JSON (used by ExchangeRateHandler).
func exchangeWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
