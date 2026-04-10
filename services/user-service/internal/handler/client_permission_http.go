package handler

// client_permission_http.go — HTTP handler za upravljanje TRADE_STOCKS permisijom klijenta.
//
// Izlaže dva endpointa koja se dodaju ispred grpc-gateway mux-a:
//
//	GET  /client/{id}/trade-permission  — vraća {"has_trade_permission": bool}
//	PATCH /client/{id}/trade-permission — telo: {"grant": bool}; dodaje ili uklanja permisiju
//
// Zahtevaju JWT zaposlenog ili administratora.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	auth "banka-backend/shared/auth"
)

// ClientPermissionHandler drži zavisnosti potrebne za upravljanje permisijama.
type ClientPermissionHandler struct {
	db        *sql.DB
	jwtSecret string
}

// NewClientPermissionHandler kreira novi handler za trade permisije.
func NewClientPermissionHandler(db *sql.DB, jwtSecret string) *ClientPermissionHandler {
	return &ClientPermissionHandler{db: db, jwtSecret: jwtSecret}
}

// WrapMux obmotava grpc-gateway mux sa custom rutama za trade-permission.
// Intercepts: GET/PATCH /client/{id}/trade-permission
// Sve ostalo prosleđuje originalnom mux-u.
func (h *ClientPermissionHandler) WrapMux(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Provjeri da li je ruta /client/{id}/trade-permission
		path := r.URL.Path
		if strings.HasSuffix(path, "/trade-permission") {
			h.handleTradePermission(w, r, path)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *ClientPermissionHandler) handleTradePermission(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "application/json")

	// ── 1. Autorizacija — zaposleni ili admin ────────────────────────────────
	tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tokenStr == "" {
		http.Error(w, `{"message":"niste autentifikovani"}`, http.StatusUnauthorized)
		return
	}
	claims, err := auth.VerifyToken(tokenStr, h.jwtSecret)
	if err != nil || (claims.UserType != "EMPLOYEE" && claims.UserType != "ADMIN") {
		http.Error(w, `{"message":"samo zaposleni ili administrator mogu upravljati permisijama klijenata"}`, http.StatusForbidden)
		return
	}

	// ── 2. Parsiranje client ID iz URL-a ─────────────────────────────────────
	// path: /client/{id}/trade-permission
	segments := strings.Split(strings.Trim(path, "/"), "/")
	// segments: ["client", "{id}", "trade-permission"]
	if len(segments) < 3 {
		http.Error(w, `{"message":"neispravan URL"}`, http.StatusBadRequest)
		return
	}
	clientID, parseErr := strconv.ParseInt(segments[1], 10, 64)
	if parseErr != nil || clientID == 0 {
		http.Error(w, `{"message":"neispravan client ID"}`, http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getTradePermission(w, r, clientID)
	case http.MethodPatch:
		h.setTradePermission(w, r, clientID)
	default:
		http.Error(w, `{"message":"metod nije podržan"}`, http.StatusMethodNotAllowed)
	}
}

func (h *ClientPermissionHandler) getTradePermission(w http.ResponseWriter, _ *http.Request, clientID int64) {
	var count int
	err := h.db.QueryRow(`
		SELECT COUNT(*) FROM user_permissions up
		JOIN permissions p ON p.id = up.permission_id
		WHERE up.user_id = $1 AND p.permission_code = 'TRADE_STOCKS'
	`, clientID).Scan(&count)
	if err != nil {
		http.Error(w, `{"message":"greška pri čitanju permisije"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"has_trade_permission": count > 0})
}

func (h *ClientPermissionHandler) setTradePermission(w http.ResponseWriter, r *http.Request, clientID int64) {
	var body struct {
		Grant bool `json:"grant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, `{"message":"neispravan JSON body"}`, http.StatusBadRequest)
		return
	}

	if body.Grant {
		// Dodaj TRADE_STOCKS permisiju ako već ne postoji
		_, err := h.db.Exec(`
			INSERT INTO user_permissions (user_id, permission_id)
			SELECT $1, p.id FROM permissions p
			WHERE p.permission_code = 'TRADE_STOCKS'
			ON CONFLICT DO NOTHING
		`, clientID)
		if err != nil {
			http.Error(w, `{"message":"greška pri dodavanju permisije"}`, http.StatusInternalServerError)
			return
		}
	} else {
		// Ukloni TRADE_STOCKS permisiju
		_, err := h.db.Exec(`
			DELETE FROM user_permissions
			WHERE user_id = $1
			AND permission_id = (SELECT id FROM permissions WHERE permission_code = 'TRADE_STOCKS')
		`, clientID)
		if err != nil {
			http.Error(w, `{"message":"greška pri uklanjanju permisije"}`, http.StatusInternalServerError)
			return
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]bool{"has_trade_permission": body.Grant})
}
