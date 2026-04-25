package service

// White-box tests for miscellaneous private/public service methods.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/mocks"
)

// ─── buildRate ────────────────────────────────────────────────────────────────

func TestBuildRate_KnownCurrency_UsesName(t *testing.T) {
	rate := buildRate("USD", 117.5, 0.01)
	assert.Equal(t, "USD", rate.Oznaka)
	assert.NotEmpty(t, rate.Naziv, "known currency should have a name")
	assert.InDelta(t, 117.5, rate.Srednji, 1e-9)
	assert.Less(t, rate.Kupovni, rate.Srednji)
	assert.Greater(t, rate.Prodajni, rate.Srednji)
}

func TestBuildRate_UnknownCurrency_FallsBackToCode(t *testing.T) {
	// "XZY" is not in ExchangeCurrencyNames → naziv falls back to the code itself
	rate := buildRate("XZY", 1.0, 0.01)
	assert.Equal(t, "XZY", rate.Naziv, "unknown currency should use code as naziv")
}

// ─── GetListingHistory — GetByID error path ───────────────────────────────────

func TestGetListingHistory_GetByIDError_ReturnsError(t *testing.T) {
	repo := &mockListingRepo{
		getByIDFn: func(ctx context.Context, id int64) (*domain.Listing, error) {
			return nil, errors.New("listing not found")
		},
	}
	svc := NewListingService(repo, nil, "")
	now := time.Now()
	_, err := svc.GetListingHistory(context.Background(), 99, now.AddDate(0, -1, 0), now)
	assert.Error(t, err)
}

// ─── BlokirajKarticu — additional status branches ────────────────────────────

func TestBlokirajKarticu_StatusDeaktivirana_ReturnsError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("GetKarticaOwnerInfo", ctx, int64(1)).
		Return(&domain.KarticaOwnerInfo{VlasnikID: 5, Status: "DEAKTIVIRANA"}, nil)

	svc := newConcreteKartica(repo)
	err := svc.BlokirajKarticu(ctx, 1, 5)
	assert.ErrorIs(t, err, domain.ErrKarticaDeaktivirana)
}

func TestBlokirajKarticu_UnknownStatus_ReturnsError(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("GetKarticaOwnerInfo", ctx, int64(1)).
		Return(&domain.KarticaOwnerInfo{VlasnikID: 5, Status: "NEPOZNATO"}, nil)

	svc := newConcreteKartica(repo)
	err := svc.BlokirajKarticu(ctx, 1, 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "neočekivani status")
}

func TestBlokirajKarticu_Success_ReturnsNil(t *testing.T) {
	repo := &mocks.MockKarticaRepository{}
	ctx := context.Background()
	repo.On("GetKarticaOwnerInfo", ctx, int64(2)).
		Return(&domain.KarticaOwnerInfo{VlasnikID: 7, Status: "AKTIVNA"}, nil)
	repo.On("SetKarticaStatus", ctx, int64(2), "BLOKIRANA").Return(nil)

	svc := newConcreteKartica(repo)
	err := svc.BlokirajKarticu(ctx, 2, 7)
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

// ─── generateOTP — format test ────────────────────────────────────────────────

func TestGenerateOTP_Format(t *testing.T) {
	for i := 0; i < 20; i++ {
		otp, err := generateOTP()
		require.NoError(t, err)
		assert.Len(t, otp, 6, "OTP should always be 6 chars, got %q", otp)
		for _, ch := range otp {
			assert.True(t, ch >= '0' && ch <= '9', "OTP char %q should be a digit", ch)
		}
	}
}
