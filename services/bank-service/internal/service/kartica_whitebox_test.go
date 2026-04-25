package service

// White-box tests for unexported karticaService methods:
// proveraLimitaFlow2 and generateKarticaData.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/mocks"
)

// newConcreteKartica builds a *karticaService (not the interface) so we can
// call unexported methods directly.
func newConcreteKartica(repo domain.KarticaRepository) *karticaService {
	return &karticaService{
		repo:        repo,
		pepper:      "test-pepper",
		redisStore:  nil,
		notifClient: nil,
	}
}

// ─── proveraLimitaFlow2 ───────────────────────────────────────────────────────

func TestProveraLimitaFlow2_LICNI_UnderLimit(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("CountKarticeZaRacun", ctx, int64(1)).Return(int64(1), nil)

	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(ctx, 1, "LICNI", nil)
	assert.NoError(t, err)
}

func TestProveraLimitaFlow2_LICNI_AtLimit(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	// maxKarticeLicni = 2 → count=2 triggers limit exceeded
	repo.On("CountKarticeZaRacun", ctx, int64(1)).Return(int64(2), nil)

	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(ctx, 1, "LICNI", nil)
	assert.ErrorIs(t, err, domain.ErrKarticaLimitPremasen)
}

func TestProveraLimitaFlow2_LICNI_RepoError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("CountKarticeZaRacun", ctx, int64(1)).Return(int64(0), errors.New("db error"))

	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(ctx, 1, "LICNI", nil)
	assert.Error(t, err)
}

func TestProveraLimitaFlow2_POSLOVNI_NilOvlascenoLice_NoExistingCard(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("HasVlasnikovaKarticaPostoji", ctx, int64(5)).Return(false, nil)

	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(ctx, 5, "POSLOVNI", nil)
	assert.NoError(t, err)
}

func TestProveraLimitaFlow2_POSLOVNI_NilOvlascenoLice_AlreadyExists(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("HasVlasnikovaKarticaPostoji", ctx, int64(5)).Return(true, nil)

	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(ctx, 5, "POSLOVNI", nil)
	assert.ErrorIs(t, err, domain.ErrKarticaLimitPremasen)
}

func TestProveraLimitaFlow2_POSLOVNI_NilOvlascenoLice_RepoError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("HasVlasnikovaKarticaPostoji", ctx, int64(5)).Return(false, errors.New("db error"))

	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(ctx, 5, "POSLOVNI", nil)
	assert.Error(t, err)
}

func TestProveraLimitaFlow2_POSLOVNI_WithOvlascenoLice_NoCard(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("HasOvlascenoLiceKarticu", ctx, "radnik@firma.rs").Return(false, nil)

	svc := newConcreteKartica(repo)
	ol := &domain.OvlascenoLiceInput{
		Ime:         "Petar",
		Prezime:     "Petrovic",
		EmailAdresa: "radnik@firma.rs",
	}
	err := svc.proveraLimitaFlow2(ctx, 5, "POSLOVNI", ol)
	assert.NoError(t, err)
}

func TestProveraLimitaFlow2_POSLOVNI_WithOvlascenoLice_AlreadyHasCard(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("HasOvlascenoLiceKarticu", ctx, "radnik@firma.rs").Return(true, nil)

	svc := newConcreteKartica(repo)
	ol := &domain.OvlascenoLiceInput{
		Ime:         "Petar",
		Prezime:     "Petrovic",
		EmailAdresa: "radnik@firma.rs",
	}
	err := svc.proveraLimitaFlow2(ctx, 5, "POSLOVNI", ol)
	assert.ErrorIs(t, err, domain.ErrKarticaVecPostoji)
}

func TestProveraLimitaFlow2_POSLOVNI_WithOvlascenoLice_RepoError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("HasOvlascenoLiceKarticu", ctx, "radnik@firma.rs").Return(false, errors.New("db error"))

	svc := newConcreteKartica(repo)
	ol := &domain.OvlascenoLiceInput{
		Ime:         "Petar",
		Prezime:     "Petrovic",
		EmailAdresa: "radnik@firma.rs",
	}
	err := svc.proveraLimitaFlow2(ctx, 5, "POSLOVNI", ol)
	assert.Error(t, err)
}

func TestProveraLimitaFlow2_UnknownVrstaRacuna_NoOp(t *testing.T) {
	// vrstaRacuna that is not LICNI or POSLOVNI → switch falls through → no error
	repo := &mocks.MockKarticaRepository{}
	svc := newConcreteKartica(repo)
	err := svc.proveraLimitaFlow2(context.Background(), 1, "DEVIZNI", nil)
	assert.NoError(t, err)
}

// ─── generateKarticaData ──────────────────────────────────────────────────────

func TestGenerateKarticaData_Visa_DefaultLimit(t *testing.T) {
	svc := newConcreteKartica(&mocks.MockKarticaRepository{})
	racunInfo := &domain.RacunInfo{
		VrstaRacuna:  "LICNI",
		MesecniLimit: 0, // → fallback to defaultLimitKartice
		ValutaOznaka: "RSD",
	}
	data, err := svc.generateKarticaData(racunInfo, domain.TipKarticaVisa)
	require.NoError(t, err)
	assert.Equal(t, defaultLimitKartice, data.LimitKartice)
	assert.Len(t, data.BrojKartice, 16)
	assert.NotEmpty(t, data.CvvKodHash)
	assert.Nil(t, data.ProvizijaProcenat)
	assert.Nil(t, data.KonverzijaNaknadaProcenat)
}

func TestGenerateKarticaData_Mastercard_RSD_HasFees(t *testing.T) {
	svc := newConcreteKartica(&mocks.MockKarticaRepository{})
	racunInfo := &domain.RacunInfo{
		VrstaRacuna:  "LICNI",
		MesecniLimit: 500_000.0,
		ValutaOznaka: "RSD",
	}
	data, err := svc.generateKarticaData(racunInfo, domain.TipKarticaMastercard)
	require.NoError(t, err)
	assert.Equal(t, 500_000.0, data.LimitKartice)
	require.NotNil(t, data.ProvizijaProcenat)
	assert.InDelta(t, mcProvizijaProcenat, *data.ProvizijaProcenat, 1e-9)
	require.NotNil(t, data.KonverzijaNaknadaProcenat)
	assert.InDelta(t, mcKonverzijaProcenat, *data.KonverzijaNaknadaProcenat, 1e-9)
}

func TestGenerateKarticaData_Mastercard_EUR_NoFees(t *testing.T) {
	svc := newConcreteKartica(&mocks.MockKarticaRepository{})
	racunInfo := &domain.RacunInfo{
		VrstaRacuna:  "LICNI",
		MesecniLimit: 200_000.0,
		ValutaOznaka: "EUR",
	}
	data, err := svc.generateKarticaData(racunInfo, domain.TipKarticaMastercard)
	require.NoError(t, err)
	// MC + non-RSD → no fees
	assert.Nil(t, data.ProvizijaProcenat)
	assert.Nil(t, data.KonverzijaNaknadaProcenat)
}

func TestGenerateKarticaData_Amex_Format(t *testing.T) {
	svc := newConcreteKartica(&mocks.MockKarticaRepository{})
	racunInfo := &domain.RacunInfo{
		VrstaRacuna:  "POSLOVNI",
		MesecniLimit: 0,
		ValutaOznaka: "USD",
	}
	data, err := svc.generateKarticaData(racunInfo, domain.TipKarticaAmex)
	require.NoError(t, err)
	// Amex card numbers are 15 digits
	assert.Len(t, data.BrojKartice, 15)
}
