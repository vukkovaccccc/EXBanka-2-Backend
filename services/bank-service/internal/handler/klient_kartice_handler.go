package handler

// klient_kartice_handler.go — HTTP handleri za klijentski API kartica.
//
// Endpointi (registrovani u main.go sa Go 1.22+ method+path patternima):
//
//	GET  /api/cards/my          — lista svih kartica ulogovanog klijenta
//	PATCH /api/cards/{id}/block  — blokiranje specifične kartice
//
// Oba endpointa zahtevaju validan CLIENT JWT access token.
// Servis garantuje da klijent može samo blokirati sopstvenu AKTIVNU karticu —
// ne može je odblokirati, deaktivirati, niti promeniti tuđi status.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	auth "banka-backend/shared/auth"
)

// ─── Handler ──────────────────────────────────────────────────────────────────

// KlientKarticeHandler obrađuje zahteve za prikaz i blokiranje kartica klijenta.
type KlientKarticeHandler struct {
	karticaService domain.KarticaService
	jwtSecret      string
}

func NewKlientKarticeHandler(karticaService domain.KarticaService, jwtSecret string) *KlientKarticeHandler {
	return &KlientKarticeHandler{
		karticaService: karticaService,
		jwtSecret:      jwtSecret,
	}
}

// ServeHTTP dispečuje zahteve na osnovu HTTP metode i URL putanje.
// Go 1.22+ ServeMux prosleđuje samo tačno usklađene zahteve, ali zadržavamo
// eksplicitnu proveru radi jasnoće i dubinske odbrane.
func (h *KlientKarticeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	korisnikID, ok := h.requireClientAuth(w, r)
	if !ok {
		return
	}

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/bank/cards/my":
		h.handleGetMojeKartice(w, r, korisnikID)
	case r.Method == http.MethodPatch && r.PathValue("id") != "":
		h.handleBlokirajKarticu(w, r, korisnikID)
	default:
		writeKarticaJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// ─── GET /api/cards/my ────────────────────────────────────────────────────────

// karticaDTO je JSON oblik jedne kartice u odgovoru GET /api/cards/my.
type karticaDTO struct {
	ID           string `json:"id"`
	BrojKartice  string `json:"brojKartice"`
	TipKartice   string `json:"tipKartice"`
	VrstaKartice string `json:"vrstaKartice"`
	DatumIsteka  string `json:"datumIsteka"` // RFC3339
	Status       string `json:"status"`
	RacunID      string `json:"racunId"`
	NazivRacuna  string `json:"nazivRacuna"`
	BrojRacuna   string `json:"brojRacuna"`
}

func (h *KlientKarticeHandler) handleGetMojeKartice(w http.ResponseWriter, r *http.Request, korisnikID int64) {
	kartice, err := h.karticaService.GetMojeKartice(r.Context(), korisnikID)
	if err != nil {
		writeKarticaJSON(w, http.StatusInternalServerError, map[string]string{"error": "interna greška servera"})
		return
	}

	dtos := make([]karticaDTO, 0, len(kartice))
	for _, k := range kartice {
		dtos = append(dtos, karticaDTO{
			ID:           strconv.FormatInt(k.ID, 10),
			BrojKartice:  k.BrojKartice,
			TipKartice:   k.TipKartice,
			VrstaKartice: k.VrstaKartice,
			DatumIsteka:  k.DatumIsteka.UTC().Format(time.RFC3339),
			Status:       k.Status,
			RacunID:      strconv.FormatInt(k.RacunID, 10),
			NazivRacuna:  k.NazivRacuna,
			BrojRacuna:   k.BrojRacuna,
		})
	}

	writeKarticaJSON(w, http.StatusOK, map[string]any{"kartice": dtos})
}

// ─── PATCH /api/cards/{id}/block ─────────────────────────────────────────────

func (h *KlientKarticeHandler) handleBlokirajKarticu(w http.ResponseWriter, r *http.Request, korisnikID int64) {
	karticaIDStr := r.PathValue("id")
	karticaID, err := strconv.ParseInt(karticaIDStr, 10, 64)
	if err != nil || karticaID <= 0 {
		writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": "neispravan ID kartice"})
		return
	}

	if err := h.karticaService.BlokirajKarticu(r.Context(), karticaID, korisnikID); err != nil {
		switch {
		case errors.Is(err, domain.ErrKarticaNijeTvoja):
			writeKarticaJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrKarticaNotFound):
			writeKarticaJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrKarticaVecBlokirana),
			errors.Is(err, domain.ErrKarticaDeaktivirana):
			writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		default:
			writeKarticaJSON(w, http.StatusInternalServerError, map[string]string{"error": "interna greška servera"})
		}
		return
	}

	writeKarticaJSON(w, http.StatusOK, map[string]string{"message": "Kartica je uspešno blokirana"})
}

// ─── Auth helper ──────────────────────────────────────────────────────────────

// requireClientAuth proverava Bearer JWT token i vraća ID korisnika.
// Ako token nedostaje, nije validan ili nije CLIENT tip, upisuje grešku u w i vraća false.
func (h *KlientKarticeHandler) requireClientAuth(w http.ResponseWriter, r *http.Request) (int64, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeKarticaJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return 0, false
	}
	claims, err := auth.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "), h.jwtSecret)
	if err != nil {
		writeKarticaJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return 0, false
	}
	if claims.UserType != "CLIENT" {
		writeKarticaJSON(w, http.StatusForbidden, map[string]string{"error": "samo klijenti mogu koristiti ovaj endpoint"})
		return 0, false
	}
	korisnikID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeKarticaJSON(w, http.StatusUnauthorized, map[string]string{"error": "neispravan token"})
		return 0, false
	}
	return korisnikID, true
}
