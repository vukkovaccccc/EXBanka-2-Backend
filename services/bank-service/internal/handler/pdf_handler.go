package handler

// pdf_handler.go — HTTP handler za generisanje potvrde o plaćanju u HTML formatu.
// Endpoint: GET /bank/payments/{id}/receipt
// Direktno registrovan na http.ServeMux pored gRPC-Gateway mux-a.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"
)

// PaymentReceiptHandler vraća HTML potvrdu o izvršenom plaćanju.
type PaymentReceiptHandler struct {
	paymentService domain.PaymentService
	jwtSecret      string
}

// NewPaymentReceiptHandler kreira novi handler za PDF/HTML potvrde.
func NewPaymentReceiptHandler(paymentService domain.PaymentService, jwtSecret string) *PaymentReceiptHandler {
	return &PaymentReceiptHandler{
		paymentService: paymentService,
		jwtSecret:      jwtSecret,
	}
}

// ServeHTTP obrađuje GET /bank/payments/{id}/receipt zahteve.
func (h *PaymentReceiptHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parsiranje ID-a iz URL-a: /bank/payments/{id}/receipt
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// Očekujemo: bank/payments/{id}/receipt — 4 segmenta
	if len(parts) != 4 || parts[3] != "receipt" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	// Validacija JWT tokena iz Authorization headera.
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")

	claims, err := auth.VerifyToken(token, h.jwtSecret)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if claims.UserType != "CLIENT" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	intent, err := h.paymentService.GetPaymentDetail(r.Context(), id, userID)
	if err != nil {
		if err == domain.ErrPaymentIntentNotFound {
			http.Error(w, "payment not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if intent.Status != "REALIZOVANO" {
		http.Error(w, "receipt available only for executed payments", http.StatusBadRequest)
		return
	}

	html := generateReceiptHTML(intent)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="potvrda-%s.html"`, intent.BrojNaloga))
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, html)
}

// generateReceiptHTML generiše HTML potvrdu o izvršenom plaćanju.
func generateReceiptHTML(i *domain.PaymentIntent) string {
	executedAt := ""
	if i.ExecutedAt != nil {
		executedAt = i.ExecutedAt.Format("02.01.2006. 15:04:05")
	}

	krajnjiIznos := i.Iznos
	if i.KrajnjiIznos != nil {
		krajnjiIznos = *i.KrajnjiIznos
	}

	tipLabel := "Plaćanje"
	if i.TipTransakcije == "PRENOS" {
		tipLabel = "Prenos između računa"
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="sr">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Potvrda o plaćanju — %s</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: 'Segoe UI', Arial, sans-serif; background: #f5f5f5; color: #222; }
  .container { max-width: 680px; margin: 40px auto; background: #fff; border-radius: 8px; box-shadow: 0 2px 12px rgba(0,0,0,0.12); overflow: hidden; }
  .header { background: #1a3c5e; color: #fff; padding: 28px 32px; }
  .header h1 { font-size: 1.4rem; font-weight: 600; margin-bottom: 4px; }
  .header .subtitle { font-size: 0.85rem; opacity: 0.75; }
  .badge { display: inline-block; background: #27ae60; color: #fff; border-radius: 4px; padding: 3px 10px; font-size: 0.78rem; font-weight: 700; letter-spacing: 0.5px; margin-top: 10px; }
  .body { padding: 28px 32px; }
  .section-title { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 1px; color: #888; margin-bottom: 14px; margin-top: 24px; border-bottom: 1px solid #eee; padding-bottom: 6px; }
  .section-title:first-child { margin-top: 0; }
  .row { display: flex; justify-content: space-between; margin-bottom: 10px; }
  .row .label { color: #666; font-size: 0.88rem; }
  .row .value { font-size: 0.88rem; font-weight: 500; text-align: right; max-width: 55%%; }
  .amount-row .value { font-size: 1.15rem; font-weight: 700; color: #1a3c5e; }
  .footer { background: #f9f9f9; border-top: 1px solid #eee; padding: 16px 32px; font-size: 0.78rem; color: #999; text-align: center; }
  @media print {
    body { background: #fff; }
    .container { box-shadow: none; }
  }
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <div class="subtitle">EXBanka — Potvrda o transakciji</div>
    <h1>%s</h1>
    <div class="badge">REALIZOVANO</div>
  </div>
  <div class="body">
    <div class="section-title">Nalog</div>
    <div class="row"><span class="label">Broj naloga</span><span class="value">%s</span></div>
    <div class="row"><span class="label">Datum izvršenja</span><span class="value">%s</span></div>
    <div class="row"><span class="label">Datum kreiranja</span><span class="value">%s</span></div>

    <div class="section-title">Platilac</div>
    <div class="row"><span class="label">Račun platioca</span><span class="value">%s</span></div>

    <div class="section-title">Primalac</div>
    <div class="row"><span class="label">Naziv primaoca</span><span class="value">%s</span></div>
    <div class="row"><span class="label">Račun primaoca</span><span class="value">%s</span></div>
    %s
    %s

    <div class="section-title">Iznos</div>
    <div class="row amount-row"><span class="label">Iznos transakcije</span><span class="value">%.2f %s</span></div>
    <div class="row"><span class="label">Provizija</span><span class="value">%.2f %s</span></div>
    <div class="row amount-row"><span class="label">Ukupno zaduženo</span><span class="value">%.2f %s</span></div>
    %s
  </div>
  <div class="footer">
    Dokument generisan: %s &nbsp;|&nbsp; EXBanka d.o.o. &nbsp;|&nbsp; Sve informacije su tačne u trenutku generisanja.
  </div>
</div>
</body>
</html>`,
		i.BrojNaloga,
		tipLabel,
		i.BrojNaloga,
		executedAt,
		i.CreatedAt.Format("02.01.2006. 15:04:05"),
		i.BrojRacunaPlatioca,
		i.NazivPrimaoca,
		i.BrojRacunaPrimaoca,
		optionalRow("Šifra plaćanja", i.SifraPlacanja),
		optionalRow("Svrha plaćanja", i.SvrhaPlacanja),
		i.Iznos, i.ValutaOznaka,
		i.Provizija, i.ValutaOznaka,
		krajnjiIznos, i.ValutaOznaka,
		optionalRow("Poziv na broj", i.PozivNaBroj),
		time.Now().Format("02.01.2006. 15:04:05"),
	)
}

func optionalRow(label, value string) string {
	if value == "" {
		return ""
	}
	return fmt.Sprintf(`<div class="row"><span class="label">%s</span><span class="value">%s</span></div>`, label, value)
}
