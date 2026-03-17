package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"banka-backend/services/bank-service/internal/domain"
)

// currencyService implementira domain.CurrencyService.
type currencyService struct {
	repo domain.CurrencyRepository
}

// NewCurrencyService vraća implementaciju CurrencyService.
func NewCurrencyService(repo domain.CurrencyRepository) domain.CurrencyService {
	return &currencyService{repo: repo}
}

// GetCurrencies vraća listu svih valuta iz repozitorijuma.
func (s *currencyService) GetCurrencies(ctx context.Context) ([]domain.Currency, error) {
	return s.repo.GetAll(ctx)
}

// ── Delatnost ─────────────────────────────────────────────────────────────────

// delatnostService implementira domain.DelatnostService.
type delatnostService struct {
	repo domain.DelatnostRepository
}

// NewDelatnostService vraća implementaciju DelatnostService.
func NewDelatnostService(repo domain.DelatnostRepository) domain.DelatnostService {
	return &delatnostService{repo: repo}
}

// GetDelatnosti vraća listu svih delatnosti iz repozitorijuma.
func (s *delatnostService) GetDelatnosti(ctx context.Context) ([]domain.Delatnost, error) {
	return s.repo.GetAll(ctx)
}

// ── AccountService ────────────────────────────────────────────────────────────

// validDevizneValute su ISO 4217 oznake dozvoljene za devizne račune.
var validDevizneValute = map[string]bool{
	"EUR": true, "CHF": true, "USD": true,
	"GBP": true, "JPY": true, "CAD": true, "AUD": true,
}

// accountService implementira domain.AccountService.
type accountService struct {
	repo         domain.AccountRepository
	currencyRepo domain.CurrencyRepository
}

// NewAccountService vraća implementaciju AccountService.
func NewAccountService(repo domain.AccountRepository, currencyRepo domain.CurrencyRepository) domain.AccountService {
	return &accountService{repo: repo, currencyRepo: currencyRepo}
}

// CreateAccount validira valutu, generiše broj računa i kreira račun u bazi.
func (s *accountService) CreateAccount(ctx context.Context, input domain.CreateAccountInput) (int64, error) {
	// 1. Validacija valute prema kategoriji računa.
	if err := s.validateCurrency(ctx, input.ValutaID, input.KategorijaRacuna); err != nil {
		return 0, err
	}

	// 2. Određivanje prefiksa broja računa na osnovu kategorije, vrste i podvrste.
	prefix, err := accountTypePrefix(input.KategorijaRacuna, input.VrstaRacuna, input.Podvrsta)
	if err != nil {
		return 0, domain.ErrInvalidPodvrsta
	}

	// 3. Generisanje 18-cifrenog broja računa sa mod-11 checksumom.
	brojRacuna, err := generateAccountNumber(prefix)
	if err != nil {
		return 0, fmt.Errorf("greška pri generisanju broja računa: %w", err)
	}

	// 4. Persitovanje u bazi (transakcija).
	return s.repo.CreateAccount(ctx, input, brojRacuna)
}

// validateCurrency proverava da li je data valuta ispravna za zadatu kategoriju računa.
func (s *accountService) validateCurrency(ctx context.Context, valutaID int64, kategorija string) error {
	currency, err := s.currencyRepo.GetByID(ctx, valutaID)
	if err != nil {
		return fmt.Errorf("valuta sa ID %d nije pronađena: %w", valutaID, err)
	}

	switch kategorija {
	case "TEKUCI":
		if currency.Oznaka != "RSD" {
			return domain.ErrInvalidCurrency
		}
	case "DEVIZNI":
		if !validDevizneValute[currency.Oznaka] {
			return domain.ErrInvalidCurrency
		}
	}
	return nil
}

// accountTypePrefix vraća 9-cifreni prefiks (banka+filijala+tip) za datu kombinaciju.
func accountTypePrefix(kategorija, vrsta, podvrsta string) (string, error) {
	const bankCode = "666"
	const branchCode = "0001"

	var typeCode string
	switch {
	case kategorija == "TEKUCI" && vrsta == "POSLOVNI":
		typeCode = "12"
	case kategorija == "DEVIZNI" && vrsta == "LICNI":
		typeCode = "21"
	case kategorija == "DEVIZNI" && vrsta == "POSLOVNI":
		typeCode = "22"
	case kategorija == "TEKUCI" && vrsta == "LICNI":
		switch strings.ToUpper(podvrsta) {
		case "STANDARDNI":
			typeCode = "11"
		case "STEDNI":
			typeCode = "13"
		case "PENZIONERSKI":
			typeCode = "14"
		case "MLADI":
			typeCode = "15"
		case "STUDENT":
			typeCode = "16"
		case "NEZAPOSLENI":
			typeCode = "17"
		default:
			return "", fmt.Errorf("nepoznata podvrsta: %s", podvrsta)
		}
	default:
		return "", fmt.Errorf("nevalidna kombinacija kategorija/vrsta: %s/%s", kategorija, vrsta)
	}

	return bankCode + branchCode + typeCode, nil // 3+4+2 = 9 cifara
}

// generateAccountNumber gradi 18-cifreni broj računa koji zadovoljava:
//
//	(suma svih 18 cifara) % 11 == 0
//
// Algoritam: generiši 8 nasumičnih cifara, izračunaj check-cifru (0–9) koja
// ispunjava uslov. Ako check-cifra izlazi van opsega (10), ponovi.
func generateAccountNumber(prefix string) (string, error) {
	// Izračunaj sumu cifara fiksnog prefiksa (9 cifara).
	prefixSum := 0
	for _, ch := range prefix {
		prefixSum += int(ch - '0')
	}

	maxRand := big.NewInt(100_000_000) // interval [0, 99_999_999] → tačno 8 cifara

	for {
		n, err := rand.Int(rand.Reader, maxRand)
		if err != nil {
			return "", fmt.Errorf("crypto/rand: %w", err)
		}

		random8 := fmt.Sprintf("%08d", n.Int64())

		s := prefixSum
		for _, ch := range random8 {
			s += int(ch - '0')
		}

		// check = cifra koja dopunjava sumu do sledećeg višekratnika od 11.
		check := (11 - s%11) % 11
		if check > 9 {
			// check == 10 ne može stati u jednu cifru — ponovi.
			continue
		}

		return prefix + random8 + fmt.Sprintf("%d", check), nil
	}
}
