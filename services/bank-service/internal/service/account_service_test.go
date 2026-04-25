package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/service"
	"banka-backend/services/bank-service/mocks"
)

// ─── CurrencyService ──────────────────────────────────────────────────────────

func TestCurrencyService_GetCurrencies_Success(t *testing.T) {
	repo := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	want := []domain.Currency{{ID: 1, Oznaka: "RSD", Naziv: "Dinar"}}
	repo.On("GetAll", ctx).Return(want, nil)

	svc := service.NewCurrencyService(repo)
	got, err := svc.GetCurrencies(ctx)

	require.NoError(t, err)
	assert.Equal(t, want, got)
	repo.AssertExpectations(t)
}

func TestCurrencyService_GetCurrencies_Error(t *testing.T) {
	repo := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	repo.On("GetAll", ctx).Return(nil, errors.New("db down"))

	svc := service.NewCurrencyService(repo)
	_, err := svc.GetCurrencies(ctx)
	assert.Error(t, err)
}

// ─── accountTypePrefix (white-box via CreateAccount error path) ───────────────
// We test the prefix logic indirectly through CreateAccount: if validateCurrency
// passes (RSD for TEKUCI) but the podvrsta is invalid we get ErrInvalidPodvrsta.
// For valid combos the call reaches generateAccountNumber and then repo.CreateAccount.

// testCurrencyForCode is a helper that stubs GetByID to return a currency with
// the given Oznaka code for currency ID 1.
func testCurrencyForCode(t *testing.T, code string) *mocks.MockCurrencyRepository {
	t.Helper()
	cr := &mocks.MockCurrencyRepository{}
	cr.On("GetByID", mock.Anything, int64(1)).
		Return(&domain.Currency{ID: 1, Oznaka: code}, nil)
	return cr
}

func TestCreateAccount_InvalidPodvrsta(t *testing.T) {
	cr := testCurrencyForCode(t, "RSD")
	ar := &mocks.MockAccountRepository{}
	svc := service.NewAccountService(ar, cr)

	_, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "TEKUCI",
		VrstaRacuna:      "LICNI",
		Podvrsta:         "NEPOSTOJECA",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidPodvrsta)
}

func TestCreateAccount_InvalidCombination(t *testing.T) {
	// DEVIZNI + POSLOVNI is valid, TEKUCI + LICNI without podvrsta is invalid
	cr := testCurrencyForCode(t, "RSD")
	ar := &mocks.MockAccountRepository{}
	svc := service.NewAccountService(ar, cr)

	_, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "TEKUCI",
		VrstaRacuna:      "LICNI",
		Podvrsta:         "",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidPodvrsta)
}

// TestCreateAccount_ValidPodvrstas checks every valid LICNI subtype goes through.
func TestCreateAccount_ValidPodvrstas(t *testing.T) {
	podvrstas := []string{"STANDARDNI", "STEDNI", "PENZIONERSKI", "MLADI", "STUDENT", "NEZAPOSLENI"}
	for _, pv := range podvrstas {
		t.Run(pv, func(t *testing.T) {
			cr := testCurrencyForCode(t, "RSD")
			ar := &mocks.MockAccountRepository{}
			ar.On("CreateAccount", mock.Anything, mock.Anything, mock.MatchedBy(func(s string) bool {
				return len(s) > 0 // just needs a non-empty account number
			})).Return(int64(1), nil)

			svc := service.NewAccountService(ar, cr)
			id, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
				ValutaID:         1,
				KategorijaRacuna: "TEKUCI",
				VrstaRacuna:      "LICNI",
				Podvrsta:         pv,
			})
			require.NoError(t, err, "podvrsta=%s", pv)
			assert.Equal(t, int64(1), id)
		})
	}
}

func TestCreateAccount_TeкuciPoslovni(t *testing.T) {
	cr := testCurrencyForCode(t, "RSD")
	ar := &mocks.MockAccountRepository{}
	ar.On("CreateAccount", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Return(int64(42), nil)

	svc := service.NewAccountService(ar, cr)
	id, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "TEKUCI",
		VrstaRacuna:      "POSLOVNI",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
	ar.AssertExpectations(t)
}

func TestCreateAccount_DevizniLicni(t *testing.T) {
	cr := testCurrencyForCode(t, "EUR")
	ar := &mocks.MockAccountRepository{}
	ar.On("CreateAccount", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Return(int64(7), nil)

	svc := service.NewAccountService(ar, cr)
	id, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "DEVIZNI",
		VrstaRacuna:      "LICNI",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7), id)
}

func TestCreateAccount_DevizniPoslovni(t *testing.T) {
	cr := testCurrencyForCode(t, "USD")
	ar := &mocks.MockAccountRepository{}
	ar.On("CreateAccount", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Return(int64(99), nil)

	svc := service.NewAccountService(ar, cr)
	id, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "DEVIZNI",
		VrstaRacuna:      "POSLOVNI",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(99), id)
}

// ─── validateCurrency ─────────────────────────────────────────────────────────

func TestCreateAccount_CurrencyNotFound(t *testing.T) {
	cr := &mocks.MockCurrencyRepository{}
	cr.On("GetByID", mock.Anything, int64(99)).
		Return((*domain.Currency)(nil), errors.New("not found"))
	ar := &mocks.MockAccountRepository{}

	svc := service.NewAccountService(ar, cr)
	_, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         99,
		KategorijaRacuna: "TEKUCI",
		VrstaRacuna:      "POSLOVNI",
	})
	assert.Error(t, err)
}

func TestCreateAccount_TekuciMustBeRSD(t *testing.T) {
	cr := testCurrencyForCode(t, "EUR") // not RSD
	ar := &mocks.MockAccountRepository{}
	svc := service.NewAccountService(ar, cr)

	_, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "TEKUCI",
		VrstaRacuna:      "POSLOVNI",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidCurrency)
}

func TestCreateAccount_DevizniInvalidCurrency(t *testing.T) {
	cr := testCurrencyForCode(t, "XYZ") // not in valid foreign set
	ar := &mocks.MockAccountRepository{}
	svc := service.NewAccountService(ar, cr)

	_, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
		ValutaID:         1,
		KategorijaRacuna: "DEVIZNI",
		VrstaRacuna:      "LICNI",
	})
	assert.ErrorIs(t, err, domain.ErrInvalidCurrency)
}

// ─── Generated account number properties ──────────────────────────────────────

// TestCreateAccount_AccountNumberProperties calls CreateAccount many times to
// statistically verify the generated number has the expected prefix and checksum.
func TestCreateAccount_AccountNumberProperties(t *testing.T) {
	cr := testCurrencyForCode(t, "RSD")

	var captured []string
	ar := &mocks.MockAccountRepository{}
	ar.On("CreateAccount", mock.Anything, mock.Anything, mock.AnythingOfType("string")).
		Run(func(args mock.Arguments) {
			captured = append(captured, args.String(2))
		}).
		Return(int64(1), nil)

	svc := service.NewAccountService(ar, cr)
	const iterations = 20
	for i := 0; i < iterations; i++ {
		_, err := svc.CreateAccount(context.Background(), domain.CreateAccountInput{
			ValutaID:         1,
			KategorijaRacuna: "TEKUCI",
			VrstaRacuna:      "POSLOVNI",
		})
		require.NoError(t, err)
	}

	// bankCode=666, branchCode=0001, typeCode=12 → prefix = "6660001" + "12"
	const expectedPrefix = "666000112"
	for _, num := range captured {
		assert.True(t, strings.HasPrefix(num, expectedPrefix),
			"account number %q should start with %q", num, expectedPrefix)

		// Total length should be prefix(9) + random(8) + check(1) = 18
		assert.Equal(t, 18, len(num), "account number %q should be 18 chars", num)

		// Verify the mod-11 checksum: sum of all digits except last ≡ 11-checkDigit
		digits := num[:len(num)-1]
		checkDigit := int(num[len(num)-1] - '0')
		sum := 0
		for _, ch := range digits {
			sum += int(ch - '0')
		}
		expected := (11 - sum%11) % 11
		assert.Equal(t, expected, checkDigit,
			"checksum failed for %q", num)
	}
}

// ─── Delegation methods ───────────────────────────────────────────────────────

func TestAccountService_GetClientAccounts(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	want := []domain.AccountListItem{{ID: 1, BrojRacuna: "123"}}
	ar.On("GetClientAccounts", ctx, int64(5)).Return(want, nil)

	svc := service.NewAccountService(ar, cr)
	got, err := svc.GetClientAccounts(ctx, 5)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestAccountService_RenameAccount(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	input := domain.RenameAccountInput{AccountID: 1, VlasnikID: 2, NoviNaziv: "Štednja"}
	ar.On("RenameAccount", ctx, input).Return(nil)

	svc := service.NewAccountService(ar, cr)
	err := svc.RenameAccount(ctx, input)
	assert.NoError(t, err)
}

func TestAccountService_UpdateAccountLimit_Error(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	ar.On("UpdateAccountLimit", ctx, mock.Anything).Return(int64(0), errors.New("limit error"))

	svc := service.NewAccountService(ar, cr)
	_, err := svc.UpdateAccountLimit(ctx, domain.UpdateLimitInput{})
	assert.Error(t, err)
}

func TestAccountService_GetAllAccounts(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	want := []domain.EmployeeAccountListItem{{BrojRacuna: "666000112" + "00000001" + "2"}}
	ar.On("GetAllAccounts", ctx, "").Return(want, nil)

	svc := service.NewAccountService(ar, cr)
	got, err := svc.GetAllAccounts(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestAccountService_GetAccountDetail_Error(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	ar.On("GetAccountDetail", ctx, int64(1), int64(2)).Return((*domain.AccountDetail)(nil), errors.New("not found"))

	svc := service.NewAccountService(ar, cr)
	_, err := svc.GetAccountDetail(ctx, 1, 2)
	assert.Error(t, err)
}

func TestAccountService_VerifyAndApplyLimit(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	input := domain.VerifyLimitInput{ActionID: 1, VlasnikID: 2, Code: "123456"}
	ar.On("VerifyAndApplyLimit", ctx, input).Return(nil)

	svc := service.NewAccountService(ar, cr)
	err := svc.VerifyAndApplyLimit(ctx, input)
	assert.NoError(t, err)
}

// ─── DelatnostService ─────────────────────────────────────────────────────────

type mockDelatnostRepo struct {
	mock.Mock
}

func (m *mockDelatnostRepo) GetAll(ctx context.Context) ([]domain.Delatnost, error) {
	args := m.Called(ctx)
	v, _ := args.Get(0).([]domain.Delatnost)
	return v, args.Error(1)
}

func TestDelatnostService_GetDelatnosti(t *testing.T) {
	repo := &mockDelatnostRepo{}
	ctx := context.Background()
	want := []domain.Delatnost{{ID: 1, Naziv: "Softver"}}
	repo.On("GetAll", ctx).Return(want, nil)

	svc := service.NewDelatnostService(repo)
	got, err := svc.GetDelatnosti(ctx)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestDelatnostService_GetDelatnosti_Error(t *testing.T) {
	repo := &mockDelatnostRepo{}
	ctx := context.Background()
	repo.On("GetAll", ctx).Return(nil, fmt.Errorf("db error"))

	svc := service.NewDelatnostService(repo)
	_, err := svc.GetDelatnosti(ctx)
	assert.Error(t, err)
}

// ─── AccountService delegation methods ───────────────────────────────────────

func TestGetAccountTransactions_Success(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	input := domain.GetAccountTransactionsInput{RacunID: 7, SortBy: "datum", SortOrder: "DESC"}
	want := []domain.Transakcija{{ID: 1}, {ID: 2}}
	ar.On("GetAccountTransactions", ctx, input, int64(5)).Return(want, nil)

	svc := service.NewAccountService(ar, cr)
	got, err := svc.GetAccountTransactions(ctx, input, 5)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestGetAccountTransactions_Error(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	input := domain.GetAccountTransactionsInput{RacunID: 7}
	ar.On("GetAccountTransactions", ctx, input, int64(5)).Return(nil, errors.New("db error"))

	svc := service.NewAccountService(ar, cr)
	_, err := svc.GetAccountTransactions(ctx, input, 5)
	assert.Error(t, err)
}

func TestGetPendingActions_Success(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	want := []domain.PendingAction{{ID: 1, VlasnikID: 3}}
	ar.On("GetPendingActions", ctx, int64(3)).Return(want, nil)

	svc := service.NewAccountService(ar, cr)
	got, err := svc.GetPendingActions(ctx, 3)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestGetPendingAction_Success(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	want := &domain.PendingAction{ID: 10, VlasnikID: 3}
	ar.On("GetPendingAction", ctx, int64(10), int64(3)).Return(want, nil)

	svc := service.NewAccountService(ar, cr)
	got, err := svc.GetPendingAction(ctx, 10, 3)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestApprovePendingAction_Success(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	now := time.Now()
	ar.On("ApprovePendingAction", ctx, int64(10), int64(3)).Return("ODOBREN", now, nil)

	svc := service.NewAccountService(ar, cr)
	status, ts, err := svc.ApprovePendingAction(ctx, 10, 3)
	require.NoError(t, err)
	assert.Equal(t, "ODOBREN", status)
	assert.Equal(t, now, ts)
}

func TestFindAccountIDByNumber_Success(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	ar.On("FindAccountIDByNumber", ctx, "666000112000000001").Return(int64(42), nil)

	svc := service.NewAccountService(ar, cr)
	got, err := svc.FindAccountIDByNumber(ctx, "666000112000000001")
	require.NoError(t, err)
	assert.Equal(t, int64(42), got)
}

func TestFindAccountIDByNumber_Error(t *testing.T) {
	ar := &mocks.MockAccountRepository{}
	cr := &mocks.MockCurrencyRepository{}
	ctx := context.Background()
	ar.On("FindAccountIDByNumber", ctx, "INVALID").Return(int64(0), errors.New("not found"))

	svc := service.NewAccountService(ar, cr)
	_, err := svc.FindAccountIDByNumber(ctx, "INVALID")
	assert.Error(t, err)
}
