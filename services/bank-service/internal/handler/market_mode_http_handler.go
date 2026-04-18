package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"banka-backend/services/bank-service/internal/domain"
	auth "banka-backend/shared/auth"
)

// MarketModeHTTPHandler obrađuje GET /bank/admin/exchanges/test-mode.
// Vraća trenutno stanje test moda iz Redisa.
// Pristup: bilo koji validan JWT (dugme je ionako vidljivo samo supervisoru na frontendu).
type MarketModeHTTPHandler struct {
	store     domain.MarketModeStore
	jwtSecret string
}

func NewMarketModeHTTPHandler(store domain.MarketModeStore, jwtSecret string) *MarketModeHTTPHandler {
	return &MarketModeHTTPHandler{store: store, jwtSecret: jwtSecret}
}

func (h *MarketModeHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if _, err := auth.VerifyToken(token, h.jwtSecret); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	enabled, err := h.store.IsTestMode(r.Context())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"enabled": enabled}) //nolint:errcheck
}
