package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

// =============================================================================
// Poslovne konstante — Issue #4
// =============================================================================

// baseRateByAmount definiše osnovu kamatne stope u zavisnosti od iznosa kredita
// iskazanog u RSD ekvivalentu. Ključevi su gornje granice (inkluzivne).
// Iznosi iznad 20_000_001 RSD nose najniži rizik → najnižu stopu.
var baseRateByAmount = []struct {
	maxRSD float64
	rate   float64
}{
	{500_000, 0.0625},         // do   500.000 RSD  → 6,25 %
	{1_000_000, 0.0600},       // do 1.000.000 RSD  → 6,00 %
	{5_000_000, 0.0575},       // do 5.000.000 RSD  → 5,75 %
	{10_000_000, 0.0550},      // do 10.000.000 RSD → 5,50 %
	{20_000_000, 0.0525},      // do 20.000.000 RSD → 5,25 %
	{math.MaxFloat64, 0.0475}, // iznad 20.000.001 RSD → 4,75 %
}

// marginByType definiše maržu banke po vrsti kredita (Issue #4).
var marginByType = map[string]float64{
	"GOTOVINSKI":      0.0175, // 1,75 %
	"STAMBENI":        0.0150, // 1,50 %
	"AUTO":            0.0125, // 1,25 %
	"REFINANSIRAJUCI": 0.0100, // 1,00 %
	"STUDENTSKI":      0.0075, // 0,75 %
}

// rsdEquivalentRates aproksimativni kursevi za konverziju stranih valuta u RSD.
// Koriste se isključivo za određivanje razreda osnove kamatne stope (Issue #4).
var rsdEquivalentRates = map[string]float64{
	"RSD": 1.0,
	"EUR": 117.0,
	"USD": 108.0,
	"CHF": 122.0,
	"GBP": 137.0,
}

// =============================================================================
// kreditService implementira domain.KreditService
// =============================================================================

type kreditService struct {
	repo domain.KreditRepository
}

func NewKreditService(repo domain.KreditRepository) domain.KreditService {
	return &kreditService{repo: repo}
}

// =============================================================================
// Klijentske operacije
// =============================================================================

// ApplyForCredit validira zahtev i persistuje ga sa statusom NA_CEKANJU.
func (s *kreditService) ApplyForCredit(
	ctx context.Context,
	input domain.CreateKreditniZahtevInput,
) (*domain.KreditniZahtev, error) {
	if input.IznosKredita <= 0 {
		return nil, fmt.Errorf("iznos kredita mora biti veći od 0")
	}
	if input.RokOtplate <= 0 {
		return nil, fmt.Errorf("rok otplate mora biti veći od 0")
	}
	if input.BrojRacuna == "" {
		return nil, fmt.Errorf("broj računa ne sme biti prazan")
	}
	if _, ok := marginByType[input.VrstaKredita]; !ok {
		return nil, fmt.Errorf("nepoznata vrsta kredita: %s", input.VrstaKredita)
	}
	if input.TipKamate != "FIKSNI" && input.TipKamate != "VARIJABILNI" {
		return nil, fmt.Errorf("tip kamate mora biti FIKSNI ili VARIJABILNI")
	}

	log.Printf("[kredit] novi zahtev: vlasnik=%d vrsta=%s iznos=%.2f %s rok=%d mes",
		input.VlasnikID, input.VrstaKredita, input.IznosKredita, input.Valuta, input.RokOtplate)

	return s.repo.CreateKreditniZahtev(ctx, input)
}

// GetClientCredits vraća sve kredite klijenta sortirane opadajuće po iznosu.
func (s *kreditService) GetClientCredits(
	ctx context.Context,
	vlasnikID int64,
) ([]domain.Kredit, error) {
	return s.repo.GetKreditsByVlasnik(ctx, vlasnikID)
}

// GetCreditDetails vraća detalje kredita sa amortizacionom tablicom.
// Proverava da kredit pripada traženom klijentu.
func (s *kreditService) GetCreditDetails(
	ctx context.Context,
	kreditID, vlasnikID int64,
) (*domain.Kredit, []domain.Rata, error) {
	kredit, err := s.repo.GetKreditByID(ctx, kreditID)
	if err != nil {
		return nil, nil, err
	}
	if kredit.VlasnikID != vlasnikID {
		return nil, nil, domain.ErrKreditForbidden
	}

	rate, err := s.repo.GetInstallmentsByKredit(ctx, kreditID)
	if err != nil {
		return nil, nil, err
	}

	return kredit, rate, nil
}

// =============================================================================
// Zaposleni operacije
// =============================================================================

// GetAllPendingRequests vraća zahteve koji čekaju obradu sa opcionim filterima.
func (s *kreditService) GetAllPendingRequests(
	ctx context.Context,
	filter domain.GetPendingRequestsFilter,
) ([]domain.KreditniZahtev, error) {
	return s.repo.GetPendingRequests(ctx, filter)
}

// ApproveCredit odobrava zahtev za kredit:
//  1. Dohvata zahtev i proverava status.
//  2. Izračunava nominalnu i efektivnu kamatnu stopu (Issue #4).
//  3. Izračunava mesečnu ratu po anuitetu (Issue #4).
//  4. Generiše punu amortizacionu tablicu (Issue #3).
//  5. Poziva repo za atomski DB upis (zahtev + kredit + rate + kreditovanje računa).
func (s *kreditService) ApproveCredit(
	ctx context.Context,
	zahtevID int64,
) (*domain.Kredit, error) {
	zahtev, err := s.repo.GetKreditniZahtevByID(ctx, zahtevID)
	if err != nil {
		return nil, err
	}
	if zahtev.Status != "NA_CEKANJU" {
		return nil, domain.ErrZahtevVecObrađen
	}

	// ── 1. Osnovna kamatna stopa po iznosu u RSD ekvivalentu ─────────────────
	rsdIznos := toRSD(zahtev.IznosKredita, zahtev.Valuta)
	baseRate := baseRateForAmount(rsdIznos)
	margin := marginByType[zahtev.VrstaKredita]
	nominalnaGodisnja := baseRate + margin // godišnja nominalna stopa

	log.Printf("[kredit] odobravanje zahtev=%d: iznos=%.2f %s (RSD equiv=%.0f)",
		zahtevID, zahtev.IznosKredita, zahtev.Valuta, rsdIznos)
	log.Printf("[kredit] kamatne stope: osnova=%.4f%% marža=%.4f%% nominalna=%.4f%%",
		baseRate*100, margin*100, nominalnaGodisnja*100)

	// ── 2. Mesečna rata po anuitetu ───────────────────────────────────────────
	n := int(zahtev.RokOtplate)
	mesecnaRata := izracunajAnuitet(zahtev.IznosKredita, nominalnaGodisnja, n)

	log.Printf("[kredit] anuiteti: n=%d mesecna_rata=%.2f", n, mesecnaRata)

	// ── 3. Efektivna kamatna stopa (EKS) ──────────────────────────────────────
	// EKS ≈ (1 + r_mesecna)^12 - 1  (bez naknada za ovu implementaciju)
	mesecnaStopa := nominalnaGodisnja / 12
	efektivna := math.Pow(1+mesecnaStopa, 12) - 1

	log.Printf("[kredit] EKS=%.4f%%", efektivna*100)

	// ── 4. Amortizaciona tablica ──────────────────────────────────────────────
	today := time.Now().UTC().Truncate(24 * time.Hour)
	prvaRata := today.AddDate(0, 1, 0)
	rate := generisuAmortizacionuTablicu(
		zahtev.IznosKredita,
		nominalnaGodisnja,
		n,
		mesecnaRata,
		prvaRata,
	)

	log.Printf("[kredit] amortizaciona tablica: %d rata, prva=%s, poslednja=%s",
		len(rate), rate[0].OcekivaniDatumDospeca.Format("2006-01-02"),
		rate[len(rate)-1].OcekivaniDatumDospeca.Format("2006-01-02"))

	// ── 5. Generiši broj kredita ───────────────────────────────────────────────
	brojKredita, err := generateBrojKredita()
	if err != nil {
		return nil, fmt.Errorf("generisanje broja kredita: %w", err)
	}

	input := domain.ApproveKreditInput{
		ZahtevID:              zahtevID,
		BrojKredita:           brojKredita,
		BrojRacuna:            zahtev.BrojRacuna,
		VlasnikID:             zahtev.VlasnikID,
		VrstaKredita:          zahtev.VrstaKredita,
		TipKamate:             zahtev.TipKamate,
		IznosKredita:          zahtev.IznosKredita,
		PeriodOtplate:         zahtev.RokOtplate,
		NominalnaKamatnaStopa: nominalnaGodisnja * 100, // čuvamo kao procenat, npr. 6.5
		EfektivnaKamatnaStopa: efektivna * 100,
		DatumUgovaranja:       today,
		IznosMesecneRate:      mesecnaRata,
		DatumSledeceRate:      prvaRata,
		PreostaloDugovanje:    zahtev.IznosKredita,
		Valuta:                zahtev.Valuta,
		Rate:                  rate,
	}

	kredit, err := s.repo.ApproveKreditRequest(ctx, input)
	if err != nil {
		return nil, err
	}

	log.Printf("[kredit] kredit odobren: id=%d broj=%s vlasnik=%d",
		kredit.ID, kredit.BrojKredita, kredit.VlasnikID)

	return kredit, nil
}

// ProcessFirstInstallment pokušava da naplati prvu ratu odmah po odobravanju kredita.
// Ako nema dovoljno sredstava, rata se markira KASNI sa retry za 72h.
func (s *kreditService) ProcessFirstInstallment(
	ctx context.Context,
	kreditID int64,
) (insufficientFunds bool, nextRetry time.Time, err error) {
	rate, err := s.repo.GetInstallmentsByKredit(ctx, kreditID)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("dohvat rata kredita %d: %w", kreditID, err)
	}
	if len(rate) == 0 {
		return false, time.Time{}, nil
	}

	prva := rate[0] // sortirano ASC po datumu — prva rata je najranija
	kredit, err := s.repo.GetKreditByID(ctx, kreditID)
	if err != nil {
		return false, time.Time{}, fmt.Errorf("dohvat kredita %d: %w", kreditID, err)
	}

	input := domain.ProcessInstallmentInput{
		RataID:     prva.ID,
		KreditID:   kreditID,
		BrojRacuna: kredit.BrojRacuna,
		IznosRate:  prva.IznosRate,
		Valuta:     prva.Valuta,
	}

	payErr := s.repo.ProcessInstallmentPayment(ctx, input)
	switch {
	case payErr == nil:
		log.Printf("[kredit] prva rata ID=%d kredita %d naplaćena odmah pri odobravanju", prva.ID, kreditID)
		return false, time.Time{}, nil
	case errors.Is(payErr, domain.ErrRataVecPlacena):
		return false, time.Time{}, nil
	case errors.Is(payErr, domain.ErrInsufficientFunds):
		retry := time.Now().UTC().Add(72 * time.Hour)
		if markErr := s.repo.MarkInstallmentFailed(ctx, prva.ID, retry); markErr != nil {
			log.Printf("[kredit] GREŠKA: markiranje rate %d kao KASNI: %v", prva.ID, markErr)
		}
		log.Printf("[kredit] prva rata ID=%d kredita %d nije naplaćena — nema sredstava, retry=%s",
			prva.ID, kreditID, retry.Format("2006-01-02 15:04"))
		return true, retry, nil
	default:
		return false, time.Time{}, payErr
	}
}

// RejectCredit odbija zahtev za kredit.
func (s *kreditService) RejectCredit(ctx context.Context, zahtevID int64) error {
	log.Printf("[kredit] odbijanje zahteva id=%d", zahtevID)
	return s.repo.RejectKreditRequest(ctx, zahtevID)
}

// GetAllApprovedCredits vraća sve odobrene kredite sa filterima (zaposleni portal).
func (s *kreditService) GetAllApprovedCredits(
	ctx context.Context,
	filter domain.GetAllCreditsFilter,
) ([]domain.Kredit, error) {
	return s.repo.GetAllCredits(ctx, filter)
}

// =============================================================================
// Matematičke pomoćne funkcije — Issue #4
// =============================================================================

// izracunajAnuitet implementira formulu A = P * (r*(1+r)^n) / ((1+r)^n - 1).
//
//	P = iznos kredita
//	r = mesečna kamatna stopa (godišnja / 12)
//	n = broj rata
func izracunajAnuitet(P, godisnjaNominalna float64, n int) float64 {
	r := godisnjaNominalna / 12
	if r == 0 {
		// Edge case: beskamatni kredit.
		return roundCent(P / float64(n))
	}
	faktor := math.Pow(1+r, float64(n))
	A := P * (r * faktor) / (faktor - 1)

	log.Printf("[kredit/math] anuiteti: P=%.2f r=%.6f n=%d faktor=%.6f A=%.2f",
		P, r, n, faktor, A)

	return roundCent(A)
}

// generisuAmortizacionuTablicu kreira slice RataInput za sve rate kredita.
// Tablica se bazira na amortizacionom obračunu: svaka rata = kamata + otplata.
// Poslednja rata se koriguje za eventualni zaokruživački ostatak.
func generisuAmortizacionuTablicu(
	P, godisnjaNominalna float64,
	n int,
	mesecnaRata float64,
	prvaRata time.Time,
) []domain.RataInput {
	r := godisnjaNominalna / 12
	rate := make([]domain.RataInput, n)
	preostalo := P

	for i := range n {
		kamata := roundCent(preostalo * r)
		otplata := mesecnaRata - kamata

		// Poslednja rata — koriguj za akumulirane greške zaokruživanja.
		if i == n-1 {
			otplata = preostalo
			mesecnaRata = roundCent(kamata + otplata)
		}

		preostalo = roundCent(preostalo - otplata)
		if preostalo < 0 {
			preostalo = 0
		}

		dospece := prvaRata.AddDate(0, i, 0)

		log.Printf("[kredit/amort] rata %d/%d: kamata=%.2f otplata=%.2f preostalo=%.2f dospece=%s",
			i+1, n, kamata, otplata, preostalo, dospece.Format("2006-01-02"))

		rate[i] = domain.RataInput{
			IznosRate:             mesecnaRata,
			IznosKamate:           kamata,
			OcekivaniDatumDospeca: dospece,
		}
	}

	return rate
}

// baseRateForAmount vraća godišnju osnovu kamatne stope na osnovu iznosa u RSD.
func baseRateForAmount(rsdIznos float64) float64 {
	for _, tier := range baseRateByAmount {
		if rsdIznos <= tier.maxRSD {
			return tier.rate
		}
	}
	return baseRateByAmount[len(baseRateByAmount)-1].rate
}

// toRSD konvertuje iznos u zadanoj valuti u RSD ekvivalent za razredovanje stope.
// Ako valuta nije poznata, iznos se tretira kao RSD (konzervativna pretpostavka).
func toRSD(iznos float64, valuta string) float64 {
	kurs, ok := rsdEquivalentRates[valuta]
	if !ok {
		log.Printf("[kredit/fx] nepoznata valuta %s, tretiramo kao RSD", valuta)
		return iznos
	}
	return iznos * kurs
}

// roundCent zaokružuje na 2 decimale (centi).
func roundCent(v float64) float64 {
	return math.Round(v*100) / 100
}

// generateBrojKredita generiše čitljivi jedinstveni identifikator kredita.
// Format: KRD-YYYYMMDD-XXXXXXXX (8 nasumičnih cifara).
func generateBrojKredita() (string, error) {
	maxRand := big.NewInt(100_000_000)
	n, err := rand.Int(rand.Reader, maxRand)
	if err != nil {
		return "", err
	}
	datum := time.Now().UTC().Format("20060102")
	return fmt.Sprintf("KRD-%s-%08d", datum, n.Int64()), nil
}
