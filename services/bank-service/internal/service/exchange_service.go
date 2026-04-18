// Package service — exchange rate business logic.
package service

import (
	"context"
	"errors"
	"fmt"

	"banka-backend/services/bank-service/internal/domain"
)

// fallbackMidRates are approximate mid rates (RSD per 1 unit) used when the
// live provider is unavailable. Values reflect typical EUR/RSD parity.
var fallbackMidRates = map[string]float64{
	"EUR": 117.00,
	"CHF": 126.75,
	"USD": 107.75,
	"GBP": 136.75,
	"JPY": 0.69,
	"CAD": 75.50,
	"AUD": 68.50,
}

type exchangeService struct {
	provider      domain.ExchangeProvider
	transferRepo  domain.ExchangeTransferRepository
	spreadRate    float64 // polu-raspon za kupovni/prodajni kurs (npr. 0.005 = 0.5%)
	provizijaRate float64 // stopa provizije po konverziji (npr. 0.005 = 0.5%)
}

// NewExchangeService creates a new ExchangeService.
// spreadRate i provizijaRate čitaju se iz konfiguracije (env: EXCHANGE_SPREAD_RATE, EXCHANGE_PROVIZIJA_RATE).
func NewExchangeService(
	provider domain.ExchangeProvider,
	transferRepo domain.ExchangeTransferRepository,
	spreadRate float64,
	provizijaRate float64,
) domain.ExchangeService {
	return &exchangeService{
		provider:      provider,
		transferRepo:  transferRepo,
		spreadRate:    spreadRate,
		provizijaRate: provizijaRate,
	}
}

// getMidRates returns live mid rates; silently falls back to local values on error.
func (s *exchangeService) getMidRates(ctx context.Context) map[string]float64 {
	rates, err := s.provider.GetMidRates(ctx)
	if err != nil || len(rates) == 0 {
		return fallbackMidRates
	}
	return rates
}

// buildRate converts a mid rate to a full ExchangeRate with spread.
func buildRate(code string, mid, spreadRate float64) domain.ExchangeRate {
	naziv := domain.ExchangeCurrencyNames[code]
	if naziv == "" {
		naziv = code
	}
	return domain.ExchangeRate{
		Oznaka:   code,
		Naziv:    naziv,
		Kupovni:  mid * (1 - spreadRate),
		Srednji:  mid,
		Prodajni: mid * (1 + spreadRate),
	}
}

// GetRates returns the full kursna lista ordered by SupportedExchangeCodes.
func (s *exchangeService) GetRates(ctx context.Context) ([]domain.ExchangeRate, error) {
	midRates := s.getMidRates(ctx)
	result := make([]domain.ExchangeRate, 0, len(domain.SupportedExchangeCodes))
	for _, code := range domain.SupportedExchangeCodes {
		mid, ok := midRates[code]
		if !ok {
			continue
		}
		result = append(result, buildRate(code, mid, s.spreadRate))
	}
	return result, nil
}

// CalculateExchange converts amount from fromOznaka to toOznaka.
//
// Conversion rules:
//   - Same currency → identity, no provizija
//   - RSD → foreign  : amount / prodajni, then deduct provizija
//   - foreign → RSD  : amount * kupovni, then deduct provizija
//   - foreign → foreign : X → RSD (kupovni) → Y (prodajni), then deduct provizija
func (s *exchangeService) CalculateExchange(
	ctx context.Context,
	fromOznaka, toOznaka string,
	amount float64,
) (*domain.ExchangeConversionResult, error) {
	if amount <= 0 {
		return nil, domain.ErrExchangeInvalidAmount
	}

	if fromOznaka == toOznaka {
		return &domain.ExchangeConversionResult{
			Result:   amount,
			Bruto:    amount,
			ViaRSD:   false,
			RateNote: "Ista valuta – nije potrebna konverzija.",
		}, nil
	}

	midRates := s.getMidRates(ctx)

	isFromRSD := fromOznaka == "RSD"
	isToRSD := toOznaka == "RSD"

	if isFromRSD {
		// RSD → foreign: klijent daje RSD, banka prodaje stranu valutu po prodajnom kursu.
		toMid, ok := midRates[toOznaka]
		if !ok {
			return nil, domain.ErrExchangeRateNotFound
		}
		toRate := buildRate(toOznaka, toMid, s.spreadRate)
		bruto := amount / toRate.Prodajni
		p := bruto * s.provizijaRate
		return &domain.ExchangeConversionResult{
			Result:    max(0, bruto-p),
			Bruto:     bruto,
			Provizija: p,
			ViaRSD:    false,
			RateNote:  fmt.Sprintf("Prodajni kurs: 1 %s = %.4g RSD", toOznaka, toRate.Prodajni),
		}, nil
	}

	if isToRSD {
		// foreign → RSD: klijent prodaje stranu valutu, banka kupuje po kupovnom kursu.
		fromMid, ok := midRates[fromOznaka]
		if !ok {
			return nil, domain.ErrExchangeRateNotFound
		}
		fromRate := buildRate(fromOznaka, fromMid, s.spreadRate)
		bruto := amount * fromRate.Kupovni
		p := bruto * s.provizijaRate
		return &domain.ExchangeConversionResult{
			Result:    max(0, bruto-p),
			Bruto:     bruto,
			Provizija: p,
			ViaRSD:    false,
			RateNote:  fmt.Sprintf("Kupovni kurs: 1 %s = %.4g RSD", fromOznaka, fromRate.Kupovni),
		}, nil
	}

	// Kros-valutna konverzija: X → RSD → Y
	fromMid, fromOK := midRates[fromOznaka]
	toMid, toOK := midRates[toOznaka]
	if !fromOK {
		return nil, errors.New("kurs za valutu " + fromOznaka + " nije dostupan")
	}
	if !toOK {
		return nil, errors.New("kurs za valutu " + toOznaka + " nije dostupan")
	}

	fromRate := buildRate(fromOznaka, fromMid, s.spreadRate)
	toRate := buildRate(toOznaka, toMid, s.spreadRate)

	rsdAmount := amount * fromRate.Kupovni // prodajemo fromOznaka, dobijamo RSD
	bruto := rsdAmount / toRate.Prodajni   // kupujemo toOznaka za RSD
	p := bruto * s.provizijaRate

	return &domain.ExchangeConversionResult{
		Result:    max(0, bruto-p),
		Bruto:     bruto,
		Provizija: p,
		ViaRSD:    true,
		RateNote: fmt.Sprintf(
			"Kupovni %s: %.4g RSD → Prodajni %s: %.4g RSD",
			fromOznaka, fromRate.Kupovni,
			toOznaka, toRate.Prodajni,
		),
	}, nil
}

// ExecuteExchangeTransfer validates input, calculates the exchange, then delegates
// the atomic debit/credit to the transfer repository.
func (s *exchangeService) ExecuteExchangeTransfer(
	ctx context.Context,
	input domain.ExchangeTransferInput,
) (*domain.ExchangeTransferResult, error) {
	if input.Amount <= 0 {
		return nil, domain.ErrExchangeInvalidAmount
	}
	if input.FromOznaka == input.ToOznaka {
		return nil, domain.ErrExchangeSameCurrency
	}
	if input.SourceAccountID == input.TargetAccountID {
		return nil, domain.ErrExchangeSameAccount
	}

	conversion, err := s.CalculateExchange(ctx, input.FromOznaka, input.ToOznaka, input.Amount)
	if err != nil {
		return nil, err
	}

	return s.transferRepo.ExecuteTransfer(ctx, input, *conversion)
}
