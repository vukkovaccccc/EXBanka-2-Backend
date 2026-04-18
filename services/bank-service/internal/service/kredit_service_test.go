// White-box tests za kredit_service.go.
// Paket je isti kao produkcijski kod da bi se testirali neeksportovani helperi.
package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/mocks"
)

// ─── izracunajAnuitet ─────────────────────────────────────────────────────────

func TestIzracunajAnuitet_Formula(t *testing.T) {
	tests := []struct {
		name   string
		P      float64
		annual float64
		n      int
	}{
		{"12% godišnje, 12 meseci, 10.000 RSD", 10_000, 0.12, 12},
		{"6% godišnje, 60 meseci, 500.000 RSD", 500_000, 0.06, 60},
		{"8% godišnje, 24 meseci, 100.000 RSD", 100_000, 0.08, 24},
		{"7.5% godišnje, 36 meseci, 250.000 RSD", 250_000, 0.075, 36},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := izracunajAnuitet(tc.P, tc.annual, tc.n)

			// Nezavisna implementacija formule A = P*(r*(1+r)^n)/((1+r)^n-1)
			r := tc.annual / 12
			factor := math.Pow(1+r, float64(tc.n))
			want := math.Round((tc.P*(r*factor)/(factor-1))*100) / 100

			assert.InDelta(t, want, got, 0.01, "anuiteti ne poklapaju")
			assert.Greater(t, got, 0.0, "anuiteti mora biti pozitivan")
		})
	}
}

func TestIzracunajAnuitet_ZeroInterest(t *testing.T) {
	// r = 0 → beskamatni kredit: rata = P / n
	got := izracunajAnuitet(12_000, 0.0, 12)
	assert.InDelta(t, 1_000.0, got, 0.01)
}

func TestIzracunajAnuitet_SingleInstallment(t *testing.T) {
	// n=1: ceo dug se vraća u jednoj rati zajedno sa kamatom
	P := 10_000.0
	annual := 0.12
	got := izracunajAnuitet(P, annual, 1)
	r := annual / 12
	want := math.Round((P*(r*math.Pow(1+r, 1))/(math.Pow(1+r, 1)-1))*100) / 100
	assert.InDelta(t, want, got, 0.01)
}

// ─── baseRateForAmount — razredi kamatne stope ────────────────────────────────

func TestBaseRateForAmount_Tiers(t *testing.T) {
	tests := []struct {
		rsd  float64
		want float64
	}{
		{400_000, 0.0625},    // do 500.000 (inkluzivno)
		{500_000, 0.0625},    // granica 500.000
		{500_001, 0.0600},    // > 500.000 → sledeći razred
		{1_000_000, 0.0600},  // granica 1.000.000
		{1_000_001, 0.0575},  // > 1.000.000
		{5_000_000, 0.0575},  // granica 5.000.000
		{5_000_001, 0.0550},  // > 5.000.000
		{10_000_000, 0.0550}, // granica 10.000.000
		{10_000_001, 0.0525}, // > 10.000.000
		{20_000_000, 0.0525}, // granica 20.000.000
		{20_000_001, 0.0475}, // iznad maksimalne granice
		{50_000_000, 0.0475}, // visok iznos
	}

	for _, tc := range tests {
		got := baseRateForAmount(tc.rsd)
		assert.InDelta(t, tc.want, got, 1e-9,
			"rsdIznos=%.0f: want=%.4f got=%.4f", tc.rsd, tc.want, got)
	}
}

// ─── marginByType — marže po vrsti kredita ────────────────────────────────────

func TestMarginByType_AllValues(t *testing.T) {
	expected := map[string]float64{
		"GOTOVINSKI":      0.0175,
		"STAMBENI":        0.0150,
		"AUTO":            0.0125,
		"REFINANSIRAJUCI": 0.0100,
		"STUDENTSKI":      0.0075,
	}

	assert.Len(t, marginByType, len(expected), "broj vrsta kredita se ne poklapa")

	for vrsta, want := range expected {
		got, ok := marginByType[vrsta]
		assert.True(t, ok, "vrsta %q nije u marginByType", vrsta)
		assert.InDelta(t, want, got, 1e-9, "marža za vrstu %q", vrsta)
	}
}

// ─── generisuAmortizacionuTablicu ─────────────────────────────────────────────

func TestGenerisuAmortizacionuTablicu_Count(t *testing.T) {
	prvaRata := time.Now().UTC().AddDate(0, 1, 0)
	n := 24
	rate := generisuAmortizacionuTablicu(100_000, 0.07, n, izracunajAnuitet(100_000, 0.07, n), prvaRata)
	assert.Len(t, rate, n)
}

func TestGenerisuAmortizacionuTablicu_RoundingDrift(t *testing.T) {
	// Suma svih otplata (IznosRate - IznosKamate) treba da bude tačno P.
	// Ovo verifikuje korekciju zaokruživačkog ostatka na poslednjoj rati.
	tests := []struct {
		P      float64
		annual float64
		n      int
	}{
		{100_000, 0.07, 24},
		{500_000, 0.065, 60},
		{1_000_000, 0.08, 120},
		{50_000, 0.0625, 12},
		{10_000, 0.0, 12}, // beskamatni kredit
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			mesecnaRata := izracunajAnuitet(tc.P, tc.annual, tc.n)
			prvaRata := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			rate := generisuAmortizacionuTablicu(tc.P, tc.annual, tc.n, mesecnaRata, prvaRata)

			require.Len(t, rate, tc.n)

			var sumaOtplate float64
			for _, r := range rate {
				otplata := r.IznosRate - r.IznosKamate
				assert.GreaterOrEqual(t, otplata, 0.0, "otplata ne sme biti negativna")
				sumaOtplate += otplata
			}

			assert.InDelta(t, tc.P, sumaOtplate, 0.01,
				"suma otplata mora biti jednaka iznosu kredita P=%.0f", tc.P)
		})
	}
}

func TestGenerisuAmortizacionuTablicu_Dates(t *testing.T) {
	prvaRata := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	n := 3
	mesecnaRata := izracunajAnuitet(30_000, 0.06, n)
	rate := generisuAmortizacionuTablicu(30_000, 0.06, n, mesecnaRata, prvaRata)

	require.Len(t, rate, n)
	assert.Equal(t, prvaRata, rate[0].OcekivaniDatumDospeca, "prva rata")
	assert.Equal(t, prvaRata.AddDate(0, 1, 0), rate[1].OcekivaniDatumDospeca, "druga rata")
	assert.Equal(t, prvaRata.AddDate(0, 2, 0), rate[2].OcekivaniDatumDospeca, "treća rata")
}

func TestGenerisuAmortizacionuTablicu_LastInstallmentCorrected(t *testing.T) {
	// Poslednja rata treba da pokrije tačno preostalo dugovanje (bez negativnog ostatka).
	n := 12
	P := 99_999.99 // neokrugli iznos koji povećava grešku zaokruživanja
	annual := 0.0793
	mesecnaRata := izracunajAnuitet(P, annual, n)
	prvaRata := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	rate := generisuAmortizacionuTablicu(P, annual, n, mesecnaRata, prvaRata)

	// Suma svih otplata ≈ P (max 1 cent razlike)
	var sumaOtplate float64
	for _, r := range rate {
		sumaOtplate += r.IznosRate - r.IznosKamate
	}
	assert.InDelta(t, P, sumaOtplate, 0.01)
}

// ─── ApproveCredit — mock-based testovi ──────────────────────────────────────

func newKreditService(repo domain.KreditRepository) domain.KreditService {
	return NewKreditService(repo)
}

func TestApproveCredit_CorrectInstallmentCount(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	const zahtevID = int64(1)
	const n = int32(24)

	zahtev := &domain.KreditniZahtev{
		ID:           zahtevID,
		VlasnikID:    42,
		VrstaKredita: "GOTOVINSKI",
		TipKamate:    "FIKSNI",
		IznosKredita: 100_000,
		Valuta:       "RSD",
		BrojRacuna:   "123-456-789",
		RokOtplate:   n,
		Status:       "NA_CEKANJU",
	}

	kredit := &domain.Kredit{
		ID:          1,
		BrojKredita: "KRD-20240101-12345678",
		VlasnikID:   42,
		Status:      "ODOBREN",
	}

	repo.On("GetKreditniZahtevByID", mock.Anything, zahtevID).Return(zahtev, nil)
	repo.On("ApproveKreditRequest", mock.Anything, mock.MatchedBy(func(input domain.ApproveKreditInput) bool {
		// Ključna provera: servis mora da preda tačno n rata repozitorijumu.
		return len(input.Rate) == int(n) && input.ZahtevID == zahtevID
	})).Return(kredit, nil)

	result, err := svc.ApproveCredit(context.Background(), zahtevID)
	require.NoError(t, err)
	assert.Equal(t, kredit.ID, result.ID)
}

func TestApproveCredit_NominalRateCalculated(t *testing.T) {
	// Provera da se nominalna stopa ispravno izračunava: osnova + marža.
	// GOTOVINSKI kredit, 100.000 RSD → osnova 6.25%, marža 1.75% → ukupno 8.00%.
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	const zahtevID = int64(2)

	zahtev := &domain.KreditniZahtev{
		ID:           zahtevID,
		VlasnikID:    99,
		VrstaKredita: "GOTOVINSKI",
		TipKamate:    "FIKSNI",
		IznosKredita: 400_000, // ≤ 500.000 RSD → osnova 6.25%
		Valuta:       "RSD",
		BrojRacuna:   "444-555-666",
		RokOtplate:   12,
		Status:       "NA_CEKANJU",
	}

	kredit := &domain.Kredit{ID: 2, VlasnikID: 99, Status: "ODOBREN"}

	repo.On("GetKreditniZahtevByID", mock.Anything, zahtevID).Return(zahtev, nil)
	repo.On("ApproveKreditRequest", mock.Anything, mock.MatchedBy(func(input domain.ApproveKreditInput) bool {
		// nominalna = (osnova + marža) * 100 = (0.0625 + 0.0175) * 100 = 8.00
		return math.Abs(input.NominalnaKamatnaStopa-8.00) < 0.001
	})).Return(kredit, nil)

	_, err := svc.ApproveCredit(context.Background(), zahtevID)
	require.NoError(t, err)
}

func TestApproveCredit_AlreadyProcessed(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	zahtev := &domain.KreditniZahtev{
		ID:     1,
		Status: "ODOBREN", // već obrađen — ne sme biti ponovo odobren
	}

	repo.On("GetKreditniZahtevByID", mock.Anything, int64(1)).Return(zahtev, nil)

	_, err := svc.ApproveCredit(context.Background(), 1)
	assert.ErrorIs(t, err, domain.ErrZahtevVecObrađen)
}

func TestApproveCredit_RepoError_OnFetch(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	dbErr := errors.New("connection refused")
	repo.On("GetKreditniZahtevByID", mock.Anything, int64(99)).Return(nil, dbErr)

	_, err := svc.ApproveCredit(context.Background(), 99)
	assert.ErrorIs(t, err, dbErr)
}

func TestApproveCredit_RepoError_OnApprove(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	zahtev := &domain.KreditniZahtev{
		ID:           5,
		VlasnikID:    10,
		VrstaKredita: "AUTO",
		TipKamate:    "FIKSNI",
		IznosKredita: 2_000_000,
		Valuta:       "RSD",
		BrojRacuna:   "777-888-999",
		RokOtplate:   36,
		Status:       "NA_CEKANJU",
	}

	dbErr := errors.New("insufficient balance on credit account")
	repo.On("GetKreditniZahtevByID", mock.Anything, int64(5)).Return(zahtev, nil)
	repo.On("ApproveKreditRequest", mock.Anything, mock.Anything).Return(nil, dbErr)

	_, err := svc.ApproveCredit(context.Background(), 5)
	assert.ErrorIs(t, err, dbErr)
}

// ─── ApplyForCredit — validacija ──────────────────────────────────────────────

func TestApplyForCredit_InvalidAmount(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	_, err := svc.ApplyForCredit(context.Background(), domain.CreateKreditniZahtevInput{
		IznosKredita: 0,
		RokOtplate:   12,
		BrojRacuna:   "123",
		VrstaKredita: "AUTO",
		TipKamate:    "FIKSNI",
	})
	assert.Error(t, err)
}

func TestApplyForCredit_UnknownType(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	_, err := svc.ApplyForCredit(context.Background(), domain.CreateKreditniZahtevInput{
		IznosKredita: 50_000,
		RokOtplate:   12,
		BrojRacuna:   "123",
		VrstaKredita: "NEPOZNATA_VRSTA",
		TipKamate:    "FIKSNI",
	})
	assert.Error(t, err)
}

func TestApplyForCredit_InvalidRok(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	_, err := svc.ApplyForCredit(context.Background(), domain.CreateKreditniZahtevInput{
		IznosKredita: 50_000,
		RokOtplate:   0,
		BrojRacuna:   "123",
		VrstaKredita: "AUTO",
		TipKamate:    "FIKSNI",
	})
	assert.Error(t, err)
}

func TestApplyForCredit_EmptyBrojRacuna(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	_, err := svc.ApplyForCredit(context.Background(), domain.CreateKreditniZahtevInput{
		IznosKredita: 50_000,
		RokOtplate:   12,
		BrojRacuna:   "",
		VrstaKredita: "AUTO",
		TipKamate:    "FIKSNI",
	})
	assert.Error(t, err)
}

func TestApplyForCredit_InvalidTipKamate(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	_, err := svc.ApplyForCredit(context.Background(), domain.CreateKreditniZahtevInput{
		IznosKredita: 50_000,
		RokOtplate:   12,
		BrojRacuna:   "123",
		VrstaKredita: "AUTO",
		TipKamate:    "NEPOZNATI",
	})
	assert.Error(t, err)
}

func TestApplyForCredit_Success(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	ctx := context.Background()
	input := domain.CreateKreditniZahtevInput{
		IznosKredita: 100_000,
		RokOtplate:   24,
		BrojRacuna:   "666000112000000001",
		VrstaKredita: "GOTOVINSKI",
		TipKamate:    "VARIJABILNI",
		VlasnikID:    7,
		Valuta:       "RSD",
	}
	want := &domain.KreditniZahtev{ID: 42}
	repo.On("CreateKreditniZahtev", ctx, input).Return(want, nil)

	svc := newKreditService(repo)
	got, err := svc.ApplyForCredit(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// ─── toRSD ────────────────────────────────────────────────────────────────────

func TestToRSD_KnownCurrency(t *testing.T) {
	// EUR should convert using the rsdEquivalentRates map.
	result := toRSD(100, "EUR")
	assert.Greater(t, result, 0.0)
	assert.NotEqual(t, 100.0, result) // should be != 100 since EUR != RSD
}

func TestToRSD_UnknownCurrency(t *testing.T) {
	// Unknown currency should return the amount unchanged (treated as RSD).
	result := toRSD(500, "UNKNOWN_CCY")
	assert.Equal(t, 500.0, result)
}

// ─── ProcessFirstInstallment ──────────────────────────────────────────────────

func TestProcessFirstInstallment_NoInstallments(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	repo.On("GetInstallmentsByKredit", mock.Anything, int64(1)).Return([]domain.Rata{}, nil)

	insuf, retry, err := svc.ProcessFirstInstallment(context.Background(), 1)
	require.NoError(t, err)
	assert.False(t, insuf)
	assert.True(t, retry.IsZero())
}

func TestProcessFirstInstallment_RepoError(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	repo.On("GetInstallmentsByKredit", mock.Anything, int64(2)).Return(nil, errors.New("db error"))

	_, _, err := svc.ProcessFirstInstallment(context.Background(), 2)
	assert.Error(t, err)
}

func TestProcessFirstInstallment_PaymentSuccess(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	rata := domain.Rata{ID: 10, IznosRate: 5000, Valuta: "RSD"}
	kredit := &domain.Kredit{ID: 3, BrojRacuna: "111222333444555666"}

	repo.On("GetInstallmentsByKredit", mock.Anything, int64(3)).Return([]domain.Rata{rata}, nil)
	repo.On("GetKreditByID", mock.Anything, int64(3)).Return(kredit, nil)
	repo.On("ProcessInstallmentPayment", mock.Anything, mock.MatchedBy(func(inp domain.ProcessInstallmentInput) bool {
		return inp.RataID == 10 && inp.KreditID == 3
	})).Return(nil)

	insuf, retry, err := svc.ProcessFirstInstallment(context.Background(), 3)
	require.NoError(t, err)
	assert.False(t, insuf)
	assert.True(t, retry.IsZero())
}

func TestProcessFirstInstallment_InsufficientFunds(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	rata := domain.Rata{ID: 11, IznosRate: 9000, Valuta: "RSD"}
	kredit := &domain.Kredit{ID: 4, BrojRacuna: "111222333444555777"}

	repo.On("GetInstallmentsByKredit", mock.Anything, int64(4)).Return([]domain.Rata{rata}, nil)
	repo.On("GetKreditByID", mock.Anything, int64(4)).Return(kredit, nil)
	repo.On("ProcessInstallmentPayment", mock.Anything, mock.Anything).Return(domain.ErrInsufficientFunds)
	repo.On("MarkInstallmentFailed", mock.Anything, int64(11), mock.Anything).Return(nil)

	insuf, retry, err := svc.ProcessFirstInstallment(context.Background(), 4)
	require.NoError(t, err)
	assert.True(t, insuf)
	assert.False(t, retry.IsZero())
}

func TestProcessFirstInstallment_AlreadyPaid(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	rata := domain.Rata{ID: 12, IznosRate: 3000, Valuta: "RSD"}
	kredit := &domain.Kredit{ID: 5, BrojRacuna: "111222333444555888"}

	repo.On("GetInstallmentsByKredit", mock.Anything, int64(5)).Return([]domain.Rata{rata}, nil)
	repo.On("GetKreditByID", mock.Anything, int64(5)).Return(kredit, nil)
	repo.On("ProcessInstallmentPayment", mock.Anything, mock.Anything).Return(domain.ErrRataVecPlacena)

	insuf, retry, err := svc.ProcessFirstInstallment(context.Background(), 5)
	require.NoError(t, err)
	assert.False(t, insuf)
	assert.True(t, retry.IsZero())
}

func TestProcessFirstInstallment_UnexpectedError(t *testing.T) {
	repo := mocks.NewMockKreditRepository(t)
	svc := newKreditService(repo)

	rata := domain.Rata{ID: 13, IznosRate: 1000, Valuta: "RSD"}
	kredit := &domain.Kredit{ID: 6, BrojRacuna: "111222333444555999"}

	repo.On("GetInstallmentsByKredit", mock.Anything, int64(6)).Return([]domain.Rata{rata}, nil)
	repo.On("GetKreditByID", mock.Anything, int64(6)).Return(kredit, nil)
	repo.On("ProcessInstallmentPayment", mock.Anything, mock.Anything).Return(errors.New("unexpected db error"))

	_, _, err := svc.ProcessFirstInstallment(context.Background(), 6)
	assert.Error(t, err)
}
