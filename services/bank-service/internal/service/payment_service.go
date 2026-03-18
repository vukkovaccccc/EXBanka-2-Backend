package service

import (
	"context"
	"fmt"
	"unicode"

	"banka-backend/services/bank-service/internal/domain"
)

// paymentService implementira domain.PaymentService.
type paymentService struct {
	recipientRepo domain.PaymentRecipientRepository
	paymentRepo   domain.PaymentRepository
}

// NewPaymentService kreira novu instancu PaymentService.
func NewPaymentService(
	recipientRepo domain.PaymentRecipientRepository,
	paymentRepo domain.PaymentRepository,
) domain.PaymentService {
	return &paymentService{
		recipientRepo: recipientRepo,
		paymentRepo:   paymentRepo,
	}
}

// ─── Primaoci plaćanja ────────────────────────────────────────────────────────

func (s *paymentService) CreateRecipient(ctx context.Context, vlasnikID int64, naziv, brojRacuna string) (*domain.PaymentRecipient, error) {
	if naziv == "" {
		return nil, fmt.Errorf("naziv primaoca ne sme biti prazan")
	}
	if len(brojRacuna) < 10 || len(brojRacuna) > 18 {
		return nil, fmt.Errorf("broj računa mora imati između 10 i 18 cifara")
	}

	recipient := &domain.PaymentRecipient{
		VlasnikID:  vlasnikID,
		Naziv:      naziv,
		BrojRacuna: brojRacuna,
	}
	if err := s.recipientRepo.Create(ctx, recipient); err != nil {
		return nil, err
	}
	return recipient, nil
}

func (s *paymentService) GetRecipients(ctx context.Context, vlasnikID int64) ([]domain.PaymentRecipient, error) {
	return s.recipientRepo.GetByOwner(ctx, vlasnikID)
}

func (s *paymentService) UpdateRecipient(ctx context.Context, id, vlasnikID int64, naziv, brojRacuna string) (*domain.PaymentRecipient, error) {
	if naziv == "" {
		return nil, fmt.Errorf("naziv primaoca ne sme biti prazan")
	}
	if len(brojRacuna) < 10 || len(brojRacuna) > 18 {
		return nil, fmt.Errorf("broj računa mora imati između 10 i 18 cifara")
	}

	recipient, err := s.recipientRepo.GetByID(ctx, id, vlasnikID)
	if err != nil {
		return nil, err
	}

	recipient.Naziv = naziv
	recipient.BrojRacuna = brojRacuna

	if err := s.recipientRepo.Update(ctx, recipient); err != nil {
		return nil, err
	}
	return recipient, nil
}

func (s *paymentService) DeleteRecipient(ctx context.Context, id, vlasnikID int64) error {
	return s.recipientRepo.Delete(ctx, id, vlasnikID)
}

// ─── Novo plaćanje ────────────────────────────────────────────────────────────

func (s *paymentService) CreatePaymentIntent(ctx context.Context, input domain.CreatePaymentIntentInput) (*domain.PaymentIntent, int64, error) {
	// Validacija šifre plaćanja.
	if err := validateSifraPlacanja(input.SifraPlacanja); err != nil {
		return nil, 0, err
	}
	if input.NazivPrimaoca == "" {
		return nil, 0, fmt.Errorf("naziv primaoca ne sme biti prazan")
	}
	if input.BrojRacunaPrimaoca == "" {
		return nil, 0, fmt.Errorf("broj računa primaoca ne sme biti prazan")
	}
	if input.Iznos <= 0 {
		return nil, 0, fmt.Errorf("iznos mora biti veći od 0")
	}
	if input.IdempotencyKey == "" {
		return nil, 0, fmt.Errorf("idempotency key ne sme biti prazan")
	}

	return s.paymentRepo.CreateIntent(ctx, input)
}

// ─── Prenos ───────────────────────────────────────────────────────────────────

func (s *paymentService) CreateTransferIntent(ctx context.Context, input domain.CreateTransferIntentInput) (*domain.PaymentIntent, int64, error) {
	if input.Iznos <= 0 {
		return nil, 0, fmt.Errorf("iznos mora biti veći od 0")
	}
	if input.RacunPlatioceID == input.RacunPrimaocaID {
		return nil, 0, domain.ErrSameAccount
	}
	if input.IdempotencyKey == "" {
		return nil, 0, fmt.Errorf("idempotency key ne sme biti prazan")
	}

	return s.paymentRepo.CreateTransferIntent(ctx, input)
}

// ─── Verifikacija i izvršenje ─────────────────────────────────────────────────

func (s *paymentService) VerifyAndExecute(ctx context.Context, input domain.VerifyPaymentInput) (*domain.PaymentIntent, error) {
	if input.Code == "" {
		return nil, fmt.Errorf("verifikacioni kod ne sme biti prazan")
	}
	return s.paymentRepo.VerifyAndExecute(ctx, input)
}

// ─── Istorija i detalji ───────────────────────────────────────────────────────

func (s *paymentService) GetPaymentHistory(ctx context.Context, userID int64, filter domain.PaymentHistoryFilter) ([]domain.PaymentIntent, error) {
	return s.paymentRepo.GetHistory(ctx, userID, filter)
}

func (s *paymentService) GetPaymentDetail(ctx context.Context, id, userID int64) (*domain.PaymentIntent, error) {
	return s.paymentRepo.GetByID(ctx, id, userID)
}

// ─── Validacije ───────────────────────────────────────────────────────────────

// validateSifraPlacanja proverava format šifre plaćanja: 3 cifre, počinje sa 2.
// Online plaćanja moraju imati šifru oblika 2xx.
func validateSifraPlacanja(sifra string) error {
	if len(sifra) != 3 {
		return domain.ErrInvalidPaymentCode
	}
	for _, ch := range sifra {
		if !unicode.IsDigit(ch) {
			return domain.ErrInvalidPaymentCode
		}
	}
	if sifra[0] != '2' {
		return domain.ErrInvalidPaymentCode
	}
	return nil
}
