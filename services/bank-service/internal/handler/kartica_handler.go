package handler

// kartica_handler.go — HTTP handleri za Flow 2: klijent inicira i potvrđuje zahtev za karticu.
//
// Endpointi (registrovani u main.go):
//
//	POST /bank/cards/request — Korak 1: inicijalizacija zahteva (OTP se šalje emailom)
//	POST /bank/cards/confirm — Korak 2: verifikacija OTP-a i kreiranje kartice
//
// Oba endpointa zahtevaju validan CLIENT JWT access token.
// Registrovani direktno na http.ServeMux (van gRPC-Gateway) jer zahtevaju
// sinhronizovanu komunikaciju sa Redis i notification-service.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/worker"
	auth "banka-backend/shared/auth"

	"google.golang.org/grpc/metadata"
)

// KarticaRequestHandler obrađuje POST /api/cards/request.
type KarticaRequestHandler struct {
	karticaService   domain.KarticaService
	userClient       clientEmailLookup // isti interfejs koji koristi BankHandler
	jwtSecret        string
	accountPublisher worker.AccountEmailPublisher
}

// NewKarticaRequestHandler kreira novi handler.
func NewKarticaRequestHandler(
	karticaService domain.KarticaService,
	userClient clientEmailLookup,
	jwtSecret string,
	accountPublisher worker.AccountEmailPublisher,
) *KarticaRequestHandler {
	return &KarticaRequestHandler{
		karticaService:   karticaService,
		userClient:       userClient,
		jwtSecret:        jwtSecret,
		accountPublisher: accountPublisher,
	}
}

// karticaRequestBody je ulazni JSON payload za POST /bank/cards/request.
type karticaRequestBody struct {
	AccountID        int64                    `json:"account_id"`
	TipKartice       string                   `json:"tip_kartice"`       // VISA | MASTERCARD | DINACARD | AMEX
	AuthorizedPerson *authorizedPersonPayload `json:"authorized_person"` // nil = kartica za vlasnika
}

// karticaConfirmBody je ulazni JSON payload za POST /bank/cards/confirm.
type karticaConfirmBody struct {
	OTPCode string `json:"otp_code"` // 6-cifreni kod primljen emailom
}

type authorizedPersonPayload struct {
	Ime           string `json:"ime"`
	Prezime       string `json:"prezime"`
	DatumRodjenja int64  `json:"datum_rodjenja"` // Unix timestamp
	Pol           string `json:"pol"`
	Email         string `json:"email"`
	BrojTelefona  string `json:"broj_telefona"`
	Adresa        string `json:"adresa"`
}

// ServeHTTP dispatčuje POST zahteve na handleRequest (Korak 1) ili handleConfirm (Korak 2).
func (h *KarticaRequestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeKarticaJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	switch r.URL.Path {
	case "/bank/cards/request":
		h.handleRequest(w, r)
	case "/bank/cards/confirm":
		h.handleConfirm(w, r)
	default:
		writeKarticaJSON(w, http.StatusNotFound, map[string]string{"error": "endpoint nije pronađen"})
	}
}

// requireClientAuth proverava Bearer JWT i vraća ownerID iz tokena.
// Ako validacija ne prođe, upisuje HTTP grešku u w i vraća (0, false).
func (h *KarticaRequestHandler) requireClientAuth(w http.ResponseWriter, r *http.Request) (int64, bool) {
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
	ownerID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeKarticaJSON(w, http.StatusUnauthorized, map[string]string{"error": "neispravan token"})
		return 0, false
	}
	return ownerID, true
}

// handleRequest obrađuje POST /bank/cards/request — Korak 1 Flow 2.
func (h *KarticaRequestHandler) handleRequest(w http.ResponseWriter, r *http.Request) {
	// ── 1. JWT validacija ────────────────────────────────────────────────────
	ownerID, ok := h.requireClientAuth(w, r)
	if !ok {
		return
	}

	// ── 2. Parsiranje JSON body-a ────────────────────────────────────────────
	var body karticaRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": "neispravan JSON payload"})
		return
	}
	if body.AccountID == 0 {
		writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id je obavezan"})
		return
	}
	if body.TipKartice == "" {
		writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": "tip_kartice je obavezan (VISA, MASTERCARD, DINACARD, AMEX)"})
		return
	}

	// ── 3. Dohvat emaila vlasnika iz user-service ────────────────────────────
	// Koristimo GetMyEmail (→ GetMyProfile) umesto GetClientEmail (→ GetClientByID)
	// jer GetClientByID zahteva EMPLOYEE JWT, a ovde imamo CLIENT JWT.
	// GetMyProfile radi sa bilo kojim autentifikovanim korisnikom i vraća email
	// iz samog tokena (user-service čita sub claim).
	lookupCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// Direktan HTTP handler nema gRPC incoming metadata — ručno injektujemo JWT
	// kao outgoing metadata da bi gRPC interceptor user-servicea prihvatio poziv.
	lookupCtx = metadata.NewOutgoingContext(
		lookupCtx,
		metadata.Pairs("authorization", r.Header.Get("Authorization")),
	)

	ownerEmail, err := h.userClient.GetMyEmail(lookupCtx)
	if err != nil {
		writeKarticaJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "user-service nije dostupan — pokušajte ponovo",
		})
		return
	}

	// ── 4. Kreiranje domenskog inputa i poziv servisa ────────────────────────
	input := domain.RequestKarticaInput{
		RacunID:      body.AccountID,
		VlasnikID:    ownerID,
		VlasnikEmail: ownerEmail,
		TipKartice:   body.TipKartice,
	}
	if body.AuthorizedPerson != nil {
		input.OvlascenoLice = &domain.OvlascenoLiceInput{
			Ime:           body.AuthorizedPerson.Ime,
			Prezime:       body.AuthorizedPerson.Prezime,
			Pol:           body.AuthorizedPerson.Pol,
			EmailAdresa:   body.AuthorizedPerson.Email,
			BrojTelefona:  body.AuthorizedPerson.BrojTelefona,
			Adresa:        body.AuthorizedPerson.Adresa,
			DatumRodjenja: body.AuthorizedPerson.DatumRodjenja,
		}
	}

	if err := h.karticaService.RequestKartica(r.Context(), input); err != nil {
		switch {
		case errors.Is(err, domain.ErrRacunNijeTvoj):
			writeKarticaJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		case errors.Is(err, domain.ErrAccountNotFound),
			errors.Is(err, domain.ErrRacunNijeAktivan),
			errors.Is(err, domain.ErrOvlascenoLiceNijeDozvoljeno),
			errors.Is(err, domain.ErrOvlascenoLiceMissingData),
			errors.Is(err, domain.ErrInvalidEmailFormat),
			errors.Is(err, domain.ErrKarticaLimitPremasen),
			errors.Is(err, domain.ErrKarticaVecPostoji),
			errors.Is(err, domain.ErrNepoznatTipKartice),
			errors.Is(err, domain.ErrDinaCardSamoRSD):
			// "message" ključ jer grpcErrorMapper prosleđuje rawMessage samo za INVALID_ARGUMENT (400).
			writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		case errors.Is(err, domain.ErrNotificationFailed):
			writeKarticaJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		default:
			writeKarticaJSON(w, http.StatusInternalServerError, map[string]string{"error": "interna greška servera"})
		}
		return
	}

	writeKarticaJSON(w, http.StatusOK, map[string]string{
		"message": "Verifikacioni kod je poslat na vaš email",
	})
}

// handleConfirm obrađuje POST /bank/cards/confirm — Korak 2 Flow 2.
func (h *KarticaRequestHandler) handleConfirm(w http.ResponseWriter, r *http.Request) {
	// ── 1. JWT validacija ────────────────────────────────────────────────────
	ownerID, ok := h.requireClientAuth(w, r)
	if !ok {
		return
	}

	// ── 2. Parsiranje JSON body-a ────────────────────────────────────────────
	var body karticaConfirmBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": "neispravan JSON payload"})
		return
	}
	if body.OTPCode == "" {
		writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"error": "otp_code je obavezan"})
		return
	}

	// ── 3. Poziv servisa ─────────────────────────────────────────────────────
	karticaID, err := h.karticaService.ConfirmKartica(r.Context(), domain.ConfirmKarticaInput{
		VlasnikID: ownerID,
		OTPCode:   body.OTPCode,
	})
	if err != nil {
		switch {
		// Sve user-facing greške vraćamo kao 400 sa "message" ključem kako bi
		// grpcErrorMapper prosledilo poruku korisniku (INVALID_ARGUMENT passthrough).
		// VAŽNO: nikad 401 za OTP greške — grpcClient.ts bi to tumačio kao isteklu
		// sesiju i odjavio korisnika!
		case errors.Is(err, domain.ErrCardRequestNotFound),
			errors.Is(err, domain.ErrOTPInvalid),
			errors.Is(err, domain.ErrOTPMaxAttempts),
			errors.Is(err, domain.ErrKarticaLimitPremasen),
			errors.Is(err, domain.ErrKarticaVecPostoji):
			writeKarticaJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		default:
			writeKarticaJSON(w, http.StatusInternalServerError, map[string]string{"error": "interna greška servera"})
		}
		return
	}

	// Pošalji email notifikaciju o kreiranoj kartici (fire-and-forget).
	emailCtx, emailCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer emailCancel()
	emailCtx = metadata.NewOutgoingContext(
		emailCtx,
		metadata.Pairs("authorization", r.Header.Get("Authorization")),
	)
	if email, mailErr := h.userClient.GetMyEmail(emailCtx); mailErr == nil && email != "" {
		if pubErr := h.accountPublisher.Publish(worker.AccountEmailEvent{
			Type:  worker.CardCreatedType,
			Email: email,
			Token: "",
		}); pubErr != nil {
			log.Printf("[confirm-kartica] UPOZORENJE: kartica kreirana (id=%d) ali KREIRANA_KARTICA email nije poslat: %v", karticaID, pubErr)
		}
	}

	writeKarticaJSON(w, http.StatusCreated, map[string]any{
		"message":    "Kartica je uspešno kreirana",
		"kartica_id": karticaID,
	})
}

// writeKarticaJSON piše JSON odgovor sa datim statusom.
func writeKarticaJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
