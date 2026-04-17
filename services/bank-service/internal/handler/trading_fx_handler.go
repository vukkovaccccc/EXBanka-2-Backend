package handler

// trading_fx_handler.go — plain HTTP handler za FX transparency pri pregledu naloga.
//
// Endpoint:
//   POST /bank/trading/fx-breakdown
//
// Vraća procenjenu cenu i proviziju u valuti odabranog klijentskog računa,
// zajedno sa primenjenim kursem i slobodnim stanjem pre/posle rezervacije.
//
// Namenjen isključivo klijentima (CLIENT). Zaposleni trguju iz USD trezor
// računa — FX konverzija nije relevantna za njih.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
)

// TradingFXHandler serves POST /bank/trading/fx-breakdown.
type TradingFXHandler struct {
	tradingService  trading.TradingService
	accountService  domain.AccountService
	exchangeService domain.ExchangeService
	jwtSecret       string
}

// NewTradingFXHandler constructs the handler.
func NewTradingFXHandler(
	tradingService trading.TradingService,
	accountService domain.AccountService,
	exchangeService domain.ExchangeService,
	jwtSecret string,
) *TradingFXHandler {
	return &TradingFXHandler{
		tradingService:  tradingService,
		accountService:  accountService,
		exchangeService: exchangeService,
		jwtSecret:       jwtSecret,
	}
}

type fxBreakdownRequest struct {
	OrderType    string  `json:"order_type"`
	Direction    string  `json:"direction"`
	ListingID    int64   `json:"listing_id"`
	Quantity     int32   `json:"quantity"`
	ContractSize int32   `json:"contract_size"`
	PricePerUnit *string `json:"price_per_unit,omitempty"`
	StopPrice    *string `json:"stop_price,omitempty"`
	Margin       bool    `json:"margin"`
	AllOrNone    bool    `json:"all_or_none"`
	AccountID    int64   `json:"account_id"`
}

type fxBreakdownResponse struct {
	PricePerUnit          string `json:"price_per_unit"`
	ApproximatePrice      string `json:"approximate_price"`
	Commission            string `json:"commission"`
	ExchangeRate          string `json:"exchange_rate"`           // jedinica valute računa po 1 USD
	AccountCurrency       string `json:"account_currency"`        // "RSD", "EUR", "USD", …
	TotalUSD              string `json:"total_usd"`               // notional + komisija u USD
	ApproximatePriceLocal string `json:"approximate_price_local"` // notional u valuti računa
	CommissionLocal       string `json:"commission_local"`        // komisija u valuti računa
	TotalDebitLocal       string `json:"total_debit_local"`       // ukupno u valuti računa
	AvailableBalance      string `json:"available_balance"`       // slobodno stanje pre
	AvailableBalanceAfter string `json:"available_balance_after"` // slobodno stanje nakon
}

// ServeHTTP handles POST /bank/trading/fx-breakdown.
func (h *TradingFXHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// ── Auth ──────────────────────────────────────────────────────────────────
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	claims, err := auth.VerifyToken(strings.TrimPrefix(authHeader, "Bearer "), h.jwtSecret)
	if err != nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// FX breakdown je primarno za klijente (ne-USD računi).
	// Zaposleni trguju iz USD trezor računa — nema FX konverzije.
	if claims.UserType != "CLIENT" {
		writeJSONError(w, http.StatusForbidden, "FX breakdown je dostupan samo klijentima")
		return
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "neispravan korisnički ID u tokenu")
		return
	}

	// ── Parse request body ────────────────────────────────────────────────────
	var req fxBreakdownRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "neispravan zahtev: "+err.Error())
		return
	}
	if req.AccountID == 0 {
		writeJSONError(w, http.StatusBadRequest, "account_id je obavezan")
		return
	}

	// ── Calculate base price + commission ─────────────────────────────────────
	calcReq := &trading.OrderCalculationRequest{
		OrderType:    trading.OrderType(req.OrderType),
		Direction:    trading.OrderDirection(req.Direction),
		ListingID:    req.ListingID,
		Quantity:     req.Quantity,
		ContractSize: req.ContractSize,
		Margin:       req.Margin,
		AllOrNone:    req.AllOrNone,
	}
	if req.PricePerUnit != nil {
		d, err := decimal.NewFromString(*req.PricePerUnit)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "neispravan price_per_unit")
			return
		}
		calcReq.PricePerUnit = &d
	}
	if req.StopPrice != nil {
		d, err := decimal.NewFromString(*req.StopPrice)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "neispravan stop_price")
			return
		}
		calcReq.StopPrice = &d
	}

	calcResp, err := h.tradingService.CalculateOrderDetails(r.Context(), calcReq)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// ── Get account details (currency + free balance) ─────────────────────────
	accountDetail, err := h.accountService.GetAccountDetail(r.Context(), req.AccountID, userID)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "račun nije pronađen ili ne pripada ovom korisniku")
		return
	}

	// ── Get exchange rates ────────────────────────────────────────────────────
	rates, err := h.exchangeService.GetRates(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "kursna lista nije dostupna")
		return
	}

	// ── FX conversion (isti algoritam kao u funds_manager.go) ─────────────────
	// Klijenti uvek koriste prodajni kurs za USD → RSD (nepovoljniji po klijenta).
	// Za drugu konverziju (RSD → target) koristi se srednji kurs.
	currency := accountDetail.ValutaOznaka
	approxUSD := calcResp.ApproximatePrice
	commUSD := calcResp.Commission
	totalUSD := approxUSD.Add(commUSD)

	var localApprox, localComm, localTotal, effectiveRate decimal.Decimal

	if currency == "USD" {
		effectiveRate = decimal.NewFromInt(1)
		localApprox = approxUSD
		localComm = commUSD
		localTotal = totalUSD
	} else {
		// USD → RSD (prodajni kurs za klijente)
		var usdToRSD float64
		for _, rt := range rates {
			if rt.Oznaka == "USD" && rt.Prodajni > 0 {
				usdToRSD = rt.Prodajni
				break
			}
		}
		if usdToRSD <= 0 {
			writeJSONError(w, http.StatusInternalServerError, "USD kurs nije dostupan")
			return
		}
		usdToRSDDec := decimal.NewFromFloat(usdToRSD)
		rsdApprox := approxUSD.Mul(usdToRSDDec)
		rsdComm := commUSD.Mul(usdToRSDDec)
		rsdTotal := totalUSD.Mul(usdToRSDDec)

		if currency == "RSD" {
			effectiveRate = usdToRSDDec
			localApprox = rsdApprox
			localComm = rsdComm
			localTotal = rsdTotal
		} else {
			// RSD → target (srednji kurs za drugu konverziju)
			var rsdToTarget float64
			for _, rt := range rates {
				if rt.Oznaka == currency && rt.Srednji > 0 {
					rsdToTarget = rt.Srednji
					break
				}
			}
			if rsdToTarget <= 0 {
				writeJSONError(w, http.StatusInternalServerError, "kurs za valutu "+currency+" nije dostupan")
				return
			}
			rsdToTargetDec := decimal.NewFromFloat(rsdToTarget)
			localApprox = rsdApprox.Div(rsdToTargetDec)
			localComm = rsdComm.Div(rsdToTargetDec)
			localTotal = rsdTotal.Div(rsdToTargetDec)
			effectiveRate = usdToRSDDec.Div(rsdToTargetDec)
		}
	}

	freeBal := decimal.NewFromFloat(accountDetail.RaspolozivoStanje)
	freeAfter := freeBal.Sub(localTotal)

	resp := fxBreakdownResponse{
		PricePerUnit:          calcResp.PricePerUnit.StringFixed(6),
		ApproximatePrice:      approxUSD.StringFixed(2),
		Commission:            commUSD.StringFixed(2),
		ExchangeRate:          effectiveRate.StringFixed(6),
		AccountCurrency:       currency,
		TotalUSD:              totalUSD.StringFixed(2),
		ApproximatePriceLocal: localApprox.StringFixed(2),
		CommissionLocal:       localComm.StringFixed(2),
		TotalDebitLocal:       localTotal.StringFixed(2),
		AvailableBalance:      freeBal.StringFixed(2),
		AvailableBalanceAfter: freeAfter.StringFixed(2),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
