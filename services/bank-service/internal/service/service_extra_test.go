package service_test

// Extra tests to improve coverage in payment, kartica, actuary, and kredit services.

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/mocks"
)

// ─── UpdateRecipient — missing error paths ────────────────────────────────────

func TestUpdateRecipient_GetByIDError(t *testing.T) {
	rr := &mocks.MockPaymentRecipientRepository{}
	pr := &mocks.MockPaymentRepository{}
	ctx := context.Background()
	rr.On("GetByID", ctx, int64(1), int64(2)).Return(nil, errors.New("not found"))

	svc := newPaymentService(rr, pr)
	_, err := svc.UpdateRecipient(ctx, 1, 2, "Naziv", "1234567890")
	assert.Error(t, err)
}

func TestUpdateRecipient_UpdateError(t *testing.T) {
	rr := &mocks.MockPaymentRecipientRepository{}
	pr := &mocks.MockPaymentRepository{}
	ctx := context.Background()
	existing := &domain.PaymentRecipient{ID: 1, VlasnikID: 2, Naziv: "Old", BrojRacuna: "0000000000"}
	rr.On("GetByID", ctx, int64(1), int64(2)).Return(existing, nil)
	rr.On("Update", ctx, mock.Anything).Return(errors.New("db error"))

	svc := newPaymentService(rr, pr)
	_, err := svc.UpdateRecipient(ctx, 1, 2, "New Naziv", "1234567890123")
	assert.Error(t, err)
}

// ─── RequestKartica — missing error paths ─────────────────────────────────────

func TestRequestKartica_GetRacunVlasnikInfoError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("GetRacunVlasnikInfo", ctx, int64(20)).Return(nil, errors.New("db error"))

	svc := newKarticaService(repo, &mocks.MockCardRequestStore{}, &mocks.MockNotificationSender{})
	err := svc.RequestKartica(ctx, domain.RequestKarticaInput{
		RacunID: 20, VlasnikID: 1, TipKartice: domain.TipKarticaVisa, VlasnikEmail: "test@test.com",
	})
	assert.Error(t, err)
}

func TestRequestKartica_PoslovniInvalidOvlascenoLice(t *testing.T) {
	// POSLOVNI + OvlascenoLice with missing Ime → validateOvlascenoLice error
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("GetRacunVlasnikInfo", ctx, int64(21)).
		Return(&domain.RacunVlasnikInfo{VlasnikID: 1, Status: "AKTIVAN", VrstaRacuna: "POSLOVNI", MesecniLimit: 100000}, nil)
	repo.On("HasOvlascenoLiceKarticu", ctx, "radnik@test.com").Return(false, nil)

	svc := newKarticaService(repo, &mocks.MockCardRequestStore{}, &mocks.MockNotificationSender{})
	err := svc.RequestKartica(ctx, domain.RequestKarticaInput{
		RacunID:      21,
		VlasnikID:    1,
		TipKartice:   domain.TipKarticaVisa,
		VlasnikEmail: "vlasnik@test.com",
		OvlascenoLice: &domain.OvlascenoLiceInput{
			Ime:         "", // missing — triggers validation error
			Prezime:     "Peric",
			EmailAdresa: "radnik@test.com",
		},
	})
	assert.ErrorIs(t, err, domain.ErrOvlascenoLiceMissingData)
}

func TestRequestKartica_SaveCardRequestError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	store := &mocks.MockCardRequestStore{}
	notif := &mocks.MockNotificationSender{}
	ctx := context.Background()

	repo.On("GetRacunVlasnikInfo", ctx, int64(22)).
		Return(&domain.RacunVlasnikInfo{VlasnikID: 1, Status: "AKTIVAN", VrstaRacuna: "LICNI", MesecniLimit: 100000}, nil)
	repo.On("CountKarticeZaRacun", ctx, int64(22)).Return(int64(0), nil)
	store.On("SaveCardRequest", ctx, int64(1), mock.Anything, mock.Anything).Return(errors.New("redis error"))

	svc := newKarticaService(repo, store, notif)
	err := svc.RequestKartica(ctx, domain.RequestKarticaInput{
		RacunID: 22, VlasnikID: 1, TipKartice: domain.TipKarticaVisa, VlasnikEmail: "test@test.com",
	})
	assert.Error(t, err)
}

// ─── SetAgentLimit — InsertActuaryLimitAudit error ───────────────────────────

func TestSetAgentLimit_InsertAuditError(t *testing.T) {
	// Success path up to Update, then InsertActuaryLimitAudit fails
	// (uses the local mockActuaryRepo from actuary_service_test.go in same package)
	repo := &mockActuaryRepo{}
	ctx := context.Background()

	a := &domain.Actuary{
		ID:          1,
		EmployeeID:  7,
		ActuaryType: "AGENT",
		Limit:       decimal.NewFromFloat(100),
		UsedLimit:   decimal.NewFromFloat(20),
	}

	repo.On("GetByEmployeeID", ctx, int64(7)).Return(a, nil)
	repo.On("Update", ctx, mock.MatchedBy(func(inp domain.UpdateActuaryInput) bool {
		return inp.ID == 1
	})).Return(a, nil)
	repo.On("InsertActuaryLimitAudit", ctx, int64(1), int64(7), a.Limit, decimal.NewFromFloat(200)).
		Return(errors.New("audit DB error"))

	svc := newActuaryService(repo)
	_, err := svc.SetAgentLimit(ctx, 1, 7, decimal.NewFromFloat(200))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit DB error")
}

// ─── ConfirmKartica — missing branches ────────────────────────────────────────

func TestConfirmKartica_CardRequestNotFound(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	store := &mocks.MockCardRequestStore{}
	ctx := context.Background()

	store.On("GetCardRequest", ctx, int64(1)).Return(nil, domain.ErrCardRequestNotFound)

	svc := newKarticaService(repo, store, &mocks.MockNotificationSender{})
	_, err := svc.ConfirmKartica(ctx, domain.ConfirmKarticaInput{VlasnikID: 1, OTPCode: "123456"})
	assert.ErrorIs(t, err, domain.ErrCardRequestNotFound)
}
