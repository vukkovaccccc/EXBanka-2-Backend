package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"banka-backend/services/bank-service/internal/domain"
	authpkg "banka-backend/shared/auth"
)

// InternalActuaryHandler vrši HTTP operacije kreiranja i brisanja aktuar zapisa.
// Poziva ga isključivo user-service kada zaposleni dobije ili izgubi SUPERVISOR/AGENT permisiju.
// Autentifikacija: JWT access token sa user_type == "ADMIN".
type InternalActuaryHandler struct {
	service   domain.ActuaryService
	jwtSecret string
}

func NewInternalActuaryHandler(service domain.ActuaryService, jwtSecret string) *InternalActuaryHandler {
	return &InternalActuaryHandler{service: service, jwtSecret: jwtSecret}
}

// requireAdmin validates the Bearer token and checks user_type == "ADMIN".
func (h *InternalActuaryHandler) requireAdmin(r *http.Request) (*authpkg.AccessClaims, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, false
	}
	claims, err := authpkg.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "), h.jwtSecret)
	if err != nil || claims.UserType != "ADMIN" {
		return nil, false
	}
	return claims, true
}

// ServeHTTP routes POST and DELETE on /bank/internal/actuary and
// DELETE on /bank/internal/actuary/{employee_id}.
func (h *InternalActuaryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAdmin(r); !ok {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		h.handleCreate(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

type createActuaryInternalRequest struct {
	EmployeeID  int64  `json:"employee_id"`
	ActuaryType string `json:"actuary_type"` // "SUPERVISOR" or "AGENT"
}

func (h *InternalActuaryHandler) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createActuaryInternalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.EmployeeID == 0 {
		http.Error(w, `{"error":"employee_id is required"}`, http.StatusBadRequest)
		return
	}
	actuaryType := domain.ActuaryType(req.ActuaryType)
	if actuaryType != domain.ActuaryTypeSupervisor && actuaryType != domain.ActuaryTypeAgent {
		http.Error(w, `{"error":"actuary_type must be SUPERVISOR or AGENT"}`, http.StatusBadRequest)
		return
	}

	a, err := h.service.CreateActuaryForEmployee(r.Context(), req.EmployeeID, actuaryType)
	if err != nil {
		log.Printf("[internal-actuary] create employee_id=%d: %v", req.EmployeeID, err)
		http.Error(w, `{"error":"failed to create actuary"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":           a.ID,
		"employee_id":  a.EmployeeID,
		"actuary_type": string(a.ActuaryType),
	})
}

func (h *InternalActuaryHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	// Path: /bank/internal/actuary/{employee_id}
	parts := strings.Split(strings.TrimSuffix(r.URL.Path, "/"), "/")
	if len(parts) == 0 {
		http.Error(w, `{"error":"employee_id is required in path"}`, http.StatusBadRequest)
		return
	}
	employeeID, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil || employeeID == 0 {
		http.Error(w, `{"error":"invalid employee_id"}`, http.StatusBadRequest)
		return
	}

	if err := h.service.DeleteActuaryForEmployee(r.Context(), employeeID); err != nil {
		log.Printf("[internal-actuary] delete employee_id=%d: %v", employeeID, err)
		http.Error(w, `{"error":"failed to delete actuary"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
