package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"banka-backend/services/bank-service/internal/domain"
)

// ─── Konstante ────────────────────────────────────────────────────────────────

const (
	// IIN (Issuer Identification Number) po tipu kartice — prvih 6 cifara.
	iinVisa       = "466666" // Visa: počinje sa 4
	iinMastercard = "512345" // Mastercard: počinje sa 51–55
	iinDinaCard   = "989100" // DinaCard: počinje sa 9891
	iinAmex       = "341234" // Amex: počinje sa 34 ili 37

	maxKarticeLicni     = 2           // LICNI račun: max 2 kartice
	defaultLimitKartice = 1_000_000.0 // fallback ako račun nema definisan mesecni_limit

	// MasterCard + RSD: fiksne naknade po specifikaciji.
	mcProvizijaProcenat  = 0.02  // 2% provizija banke
	mcKonverzijaProcenat = 0.005 // 0.5% konverziona naknada

	// Maksimalan broj pogrešnih OTP unosa pre nego što se zahtev poništi.
	maxOTPAttempts = 3
)

// ─── karticaService ───────────────────────────────────────────────────────────

type karticaService struct {
	repo        domain.KarticaRepository
	pepper      string // tajni ključ za HMAC-SHA256 hashiranje CVV-a (iz env CVV_PEPPER)
	redisStore  domain.CardRequestStore
	notifClient domain.NotificationSender
}

func NewKarticaService(
	repo domain.KarticaRepository,
	pepper string,
	redisStore domain.CardRequestStore,
	notifClient domain.NotificationSender,
) domain.KarticaService {
	return &karticaService{
		repo:        repo,
		pepper:      pepper,
		redisStore:  redisStore,
		notifClient: notifClient,
	}
}

// karticaGeneratedData sadrži generisane vrednosti kartice (broj, CVV hash, naknade).
// Interni tip koji deli Flow 1 (CreateKarticaZaVlasnika) i Flow 2 (ConfirmKartica).
type karticaGeneratedData struct {
	BrojKartice               string
	CvvKodHash                string
	LimitKartice              float64
	ProvizijaProcenat         *float64
	KonverzijaNaknadaProcenat *float64
}

// generateKarticaData kreira kriptografski sigurne vrednosti kartice na osnovu
// podataka o računu i tipa kartice. Ne vrši DB operacije ni validaciju limita.
// Zajednički helper za Flow 1 i Flow 2 — logika generisanja je identična.
func (s *karticaService) generateKarticaData(racunInfo *domain.RacunInfo, tipKartice string) (*karticaGeneratedData, error) {
	brojKartice, err := generateBrojKartice(tipKartice)
	if err != nil {
		return nil, fmt.Errorf("generisanje broja kartice: %w", err)
	}
	cvvRaw, err := generateCVV()
	if err != nil {
		return nil, fmt.Errorf("generisanje CVV: %w", err)
	}

	limitKartice := racunInfo.MesecniLimit
	if limitKartice <= 0 {
		limitKartice = defaultLimitKartice
	}

	var provizijaProcenat, konverzijaProcenat *float64
	if tipKartice == domain.TipKarticaMastercard && racunInfo.ValutaOznaka == "RSD" {
		p := mcProvizijaProcenat
		k := mcKonverzijaProcenat
		provizijaProcenat = &p
		konverzijaProcenat = &k
	}

	return &karticaGeneratedData{
		BrojKartice:               brojKartice,
		CvvKodHash:                hashCVV(cvvRaw, s.pepper),
		LimitKartice:              limitKartice,
		ProvizijaProcenat:         provizijaProcenat,
		KonverzijaNaknadaProcenat: konverzijaProcenat,
	}, nil
}

// CreateKarticaZaVlasnika implementira Flow 1: zaposleni kreira karticu za vlasnika računa.
//
// tipKartice: VISA | MASTERCARD | DINACARD | AMEX
//
// Tok izvršavanja:
//  1. Dohvatanje racun podataka iz baze (vrsta_racuna, mesecni_limit, valuta_oznaka).
//  2. Validacija: tip kartice + DinaCard samo za RSD račune.
//  3. Provera limita na osnovu vrsta_racuna.
//  4. Generisanje podataka kartice (broj — IIN+random+Luhn, CVV hash, naknade).
//  5. Upis u bazu.
func (s *karticaService) CreateKarticaZaVlasnika(ctx context.Context, racunID int64, tipKartice string) (int64, error) {
	// ── 1. Dohvatanje podataka o računu ──────────────────────────────────────
	racunInfo, err := s.repo.GetRacunInfo(ctx, racunID)
	if err != nil {
		return 0, fmt.Errorf("dohvatanje podataka o računu: %w", err)
	}

	// ── 2. Biznis validacija tipa kartice ────────────────────────────────────
	switch tipKartice {
	case domain.TipKarticaVisa, domain.TipKarticaMastercard,
		domain.TipKarticaDinaCard, domain.TipKarticaAmex:
		// validan tip
	default:
		return 0, domain.ErrNepoznatTipKartice
	}
	if tipKartice == domain.TipKarticaDinaCard && racunInfo.ValutaOznaka != "RSD" {
		return 0, domain.ErrDinaCardSamoRSD
	}

	// ── 3. Provera limita ────────────────────────────────────────────────────
	if err := s.proveraLimita(ctx, racunID, racunInfo.VrstaRacuna); err != nil {
		return 0, err
	}

	// ── 4. Generisanje podataka kartice ──────────────────────────────────────
	data, err := s.generateKarticaData(racunInfo, tipKartice)
	if err != nil {
		return 0, err
	}

	// ── 5. Upis u bazu ───────────────────────────────────────────────────────
	now := time.Now().UTC()
	return s.repo.CreateKartica(ctx, domain.CreateKarticaInput{
		RacunID:                   racunID,
		BrojKartice:               data.BrojKartice,
		TipKartice:                tipKartice,
		VrstaKartice:              "DEBIT",
		CvvKodHash:                data.CvvKodHash,
		DatumKreiranja:            now,
		DatumIsteka:               now.AddDate(5, 0, 0),
		LimitKartice:              data.LimitKartice,
		Status:                    "AKTIVNA",
		ProvizijaProcenat:         data.ProvizijaProcenat,
		KonverzijaNaknadaProcenat: data.KonverzijaNaknadaProcenat,
	})
}

// proveraLimita proverava da li je dozvoljen broj kartica premašen.
func (s *karticaService) proveraLimita(ctx context.Context, racunID int64, vrstaRacuna string) error {
	switch vrstaRacuna {
	case "LICNI":
		// Max 2 kartice po računu za lične račune.
		count, err := s.repo.CountKarticeZaRacun(ctx, racunID)
		if err != nil {
			return fmt.Errorf("provera limita: %w", err)
		}
		if count >= maxKarticeLicni {
			return domain.ErrKarticaLimitPremasen
		}

	case "POSLOVNI":
		// U Flow 1 vlasnik dobija svoju karticu (bez ovlasceno_lice).
		// Proveravamo da li vlasnik već ima karticu na ovom računu.
		exists, err := s.repo.HasVlasnikovaKarticaPostoji(ctx, racunID)
		if err != nil {
			return fmt.Errorf("provera limita: %w", err)
		}
		if exists {
			return domain.ErrKarticaLimitPremasen
		}
	}
	return nil
}

// ─── Generisanje broja kartice ────────────────────────────────────────────────

// generateBrojKartice generiše broj kartice za dati tip.
//
// Format (Visa / Mastercard / DinaCard — 16 cifara):
//
//	[IIN — 6 cifara][Account Number — 9 cifara][Check Digit — 1 cifra]
//
// Format (American Express — 15 cifara):
//
//	[IIN — 6 cifara][Account Number — 8 cifara][Check Digit — 1 cifra]
//
// Check digit se računa Luhn algoritmom (mod-10).
func generateBrojKartice(tipKartice string) (string, error) {
	var iin string
	var accountLen int64 // broj random cifara pre check digita

	switch tipKartice {
	case domain.TipKarticaMastercard:
		iin, accountLen = iinMastercard, 9
	case domain.TipKarticaDinaCard:
		iin, accountLen = iinDinaCard, 9
	case domain.TipKarticaAmex:
		iin, accountLen = iinAmex, 8 // Amex: 15 cifara ukupno
	default: // VISA i neprepoznati — default na Visa
		iin, accountLen = iinVisa, 9
	}

	maxRand := new(big.Int).Exp(big.NewInt(10), big.NewInt(accountLen), nil)
	n, err := rand.Int(rand.Reader, maxRand)
	if err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}

	format := fmt.Sprintf("%%0%dd", accountLen)
	partial := iin + fmt.Sprintf(format, n.Int64())

	digits := make([]int, len(partial))
	for i, ch := range partial {
		digits[i] = int(ch - '0')
	}
	check := luhnCheckDigit(digits)

	return partial + fmt.Sprintf("%d", check), nil
}

// luhnCheckDigit računa Luhn (modulus 10) kontrolnu cifru za niz cifara.
//
// Algoritam:
//  1. Kreni od poslednje cifre (desno nalevo).
//  2. Dupliraj svaku cifru na neparnoj poziciji od desne strane (1., 3., 5.…).
//  3. Ako je rezultat > 9, oduzmi 9.
//  4. Saberi sve cifre.
//  5. Kontrolna cifra = (10 − suma % 10) % 10.
//
// Napomena: "neparna pozicija od desne strane" u 15-cifrenom parcijalnom broju
// odgovara "parnoj poziciji od desne strane" u konačnom 16-cifrenom broju
// (jer check digit zauzima poziciju 1 s desne strane).
// Oba pristupa daju identičan i validan Luhn broj.
func luhnCheckDigit(digits []int) int {
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		// posFromRight je 1-indeksirana pozicija od desne strane.
		// Rightmost element (index len-1) ima posFromRight = 1.
		posFromRight := len(digits) - i
		d := digits[i]
		if posFromRight%2 == 1 { // dupliraj neparne pozicije (1., 3., 5.…)
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (10 - sum%10) % 10
}

// ─── Generisanje i hashiranje CVV-a ──────────────────────────────────────────

// generateCVV generiše nasumičan 3-cifreni CVV (000–999) kao string.
// Vodeće nule su sačuvane (npr. "007").
func generateCVV() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%03d", n.Int64()), nil
}

// ─── Flow 2 — Klijent inicira zahtev za karticu ───────────────────────────────

// RequestKartica implementira Korak 1 Flow 2: inicijalizacija zahteva za karticu.
//
// Tok izvršavanja:
//  1. Dohvata podatke o računu iz baze i proverava vlasništvo (Security).
//  2. Validacija tipa računa vs. payload (LICNI ne može imati ovlašćeno lice).
//  3. Provera limita u bazi (PostgreSQL).
//  4. Generisanje 6-cifrenog OTP koda.
//  5. Upisivanje state-a u Redis (TTL 5 min) — overwrite ako ključ već postoji.
//  6. Sinhronizovano slanje OTP emaila. Ako slanje ne uspe → delete Redis ključ.
func (s *karticaService) RequestKartica(ctx context.Context, input domain.RequestKarticaInput) error {
	// ── 1. Dohvati podatke o računu ──────────────────────────────────────────
	racunInfo, err := s.repo.GetRacunVlasnikInfo(ctx, input.RacunID)
	if err != nil {
		return fmt.Errorf("dohvatanje podataka o računu: %w", err)
	}

	// Edge Case 1 — Security: klijent mora biti vlasnik računa.
	if racunInfo.VlasnikID != input.VlasnikID {
		return domain.ErrRacunNijeTvoj
	}
	if racunInfo.Status != "AKTIVAN" {
		return domain.ErrRacunNijeAktivan
	}

	// ── 2. Validacija tipa računa vs. payload ────────────────────────────────
	if racunInfo.VrstaRacuna == "LICNI" && input.OvlascenoLice != nil {
		return domain.ErrOvlascenoLiceNijeDozvoljeno
	}
	// Edge Case 2 — validacija polja ovlašćenog lica.
	if input.OvlascenoLice != nil {
		if err := validateOvlascenoLice(input.OvlascenoLice); err != nil {
			return err
		}
	}

	// ── 3. Provera limita ────────────────────────────────────────────────────
	if err := s.proveraLimitaFlow2(ctx, input.RacunID, racunInfo.VrstaRacuna, input.OvlascenoLice); err != nil {
		return err
	}

	// ── 4. Generisanje OTP koda ──────────────────────────────────────────────
	otpCode, err := generateOTP()
	if err != nil {
		return fmt.Errorf("generisanje OTP koda: %w", err)
	}

	// ── 5. Redis — upisivanje state-a (Edge Case 5: overwrite ako postoji) ───
	state := domain.CardRequestState{
		AccountID:     input.RacunID,
		TipKartice:    input.TipKartice,
		OvlascenoLice: input.OvlascenoLice,
		OTPCode:       otpCode,
		Attempts:      0,
	}
	if err := s.redisStore.SaveCardRequest(ctx, input.VlasnikID, state, 5*time.Minute); err != nil {
		return fmt.Errorf("čuvanje zahteva u Redis: %w", err)
	}

	// ── 6. Slanje OTP emaila (Edge Case 6: rollback ako slanje ne uspe) ──────
	if err := s.notifClient.SendCardOTP(ctx, input.VlasnikEmail, otpCode); err != nil {
		_ = s.redisStore.DeleteCardRequest(ctx, input.VlasnikID) // rollback
		return domain.ErrNotificationFailed
	}

	return nil
}

// proveraLimitaFlow2 proverava limite specifične za Flow 2.
func (s *karticaService) proveraLimitaFlow2(
	ctx context.Context,
	racunID int64,
	vrstaRacuna string,
	ovlascenoLice *domain.OvlascenoLiceInput,
) error {
	switch vrstaRacuna {
	case "LICNI":
		count, err := s.repo.CountKarticeZaRacun(ctx, racunID)
		if err != nil {
			return fmt.Errorf("provera limita: %w", err)
		}
		if count >= maxKarticeLicni {
			return domain.ErrKarticaLimitPremasen
		}

	case "POSLOVNI":
		if ovlascenoLice == nil {
			// Vlasnik traži karticu za sebe — max 1 vlasnikova kartica po računu.
			exists, err := s.repo.HasVlasnikovaKarticaPostoji(ctx, racunID)
			if err != nil {
				return fmt.Errorf("provera limita: %w", err)
			}
			if exists {
				return domain.ErrKarticaLimitPremasen
			}
		} else {
			// Vlasnik traži karticu za radnika — max 1 kartica po osobi (email).
			exists, err := s.repo.HasOvlascenoLiceKarticu(ctx, ovlascenoLice.EmailAdresa)
			if err != nil {
				return fmt.Errorf("provera limita: %w", err)
			}
			if exists {
				return domain.ErrKarticaVecPostoji
			}
		}
	}
	return nil
}

// validateOvlascenoLice proverava obavezna polja i format emaila.
func validateOvlascenoLice(ol *domain.OvlascenoLiceInput) error {
	if ol.Ime == "" || ol.Prezime == "" || ol.EmailAdresa == "" {
		return domain.ErrOvlascenoLiceMissingData
	}
	if !strings.Contains(ol.EmailAdresa, "@") || !strings.Contains(ol.EmailAdresa, ".") {
		return domain.ErrInvalidEmailFormat
	}
	return nil
}

// ─── Flow 2 Korak 2 — verifikacija OTP-a i kreiranje kartice ─────────────────

// ConfirmKartica implementira Korak 2 Flow 2: klijent unosi OTP primljen emailom.
//
// Tok izvršavanja:
//  1. Dohvatanje state-a iz Redisa (ErrCardRequestNotFound ako nema aktivnog zahteva).
//  2. Konstantno-vremensko poređenje OTP-a (crypto/subtle — sprečava timing napad).
//     - Pogrešan OTP: incrementuje Attempts i re-upisuje u Redis; posle maxOTPAttempts briše zahtev.
//  3. TOCTOU zaštita: re-provera limita pre kreiranja (između Koraka 1 i 2 neko je možda dodao karticu).
//  4. Generisanje podataka kartice (isti helper kao Flow 1).
//     5a. Ako nema ovlašćenog lica: repo.CreateKartica (jednostavan insert).
//     5b. Ako postoji ovlašćeno lice: repo.CreateKarticaSaOvlascenoLicem (atomična transakcija).
//  6. Brisanje Redis ključa — zahtev je ispunjen.
func (s *karticaService) ConfirmKartica(ctx context.Context, input domain.ConfirmKarticaInput) (int64, error) {
	// ── 1. Dohvati state iz Redisa ───────────────────────────────────────────
	state, err := s.redisStore.GetCardRequest(ctx, input.VlasnikID)
	if err != nil {
		return 0, err
	}

	// ── 2. Provera OTP-a (constant-time — sprečava timing napad) ────────────
	if subtle.ConstantTimeCompare([]byte(state.OTPCode), []byte(input.OTPCode)) != 1 {
		state.Attempts++
		if state.Attempts >= maxOTPAttempts {
			_ = s.redisStore.DeleteCardRequest(ctx, input.VlasnikID)
			return 0, domain.ErrOTPMaxAttempts
		}
		// Re-upiši state sa ažuriranim brojem pokušaja.
		// TTL se osvežava na 5 min od svakog pogrešnog pokušaja — prihvatljivo ponašanje.
		_ = s.redisStore.SaveCardRequest(ctx, input.VlasnikID, *state, 5*time.Minute)
		return 0, domain.ErrOTPInvalid
	}

	// ── 3. TOCTOU: re-provera limita pre kreiranja ───────────────────────────
	racunInfo, err := s.repo.GetRacunInfo(ctx, state.AccountID)
	if err != nil {
		return 0, fmt.Errorf("dohvatanje podataka o računu: %w", err)
	}
	if err := s.proveraLimitaFlow2(ctx, state.AccountID, racunInfo.VrstaRacuna, state.OvlascenoLice); err != nil {
		return 0, err
	}

	// ── 4. Generisanje podataka kartice ──────────────────────────────────────
	data, err := s.generateKarticaData(racunInfo, state.TipKartice)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	karticaInput := domain.CreateKarticaInput{
		RacunID:                   state.AccountID,
		BrojKartice:               data.BrojKartice,
		TipKartice:                state.TipKartice,
		VrstaKartice:              "DEBIT",
		CvvKodHash:                data.CvvKodHash,
		DatumKreiranja:            now,
		DatumIsteka:               now.AddDate(5, 0, 0),
		LimitKartice:              data.LimitKartice,
		Status:                    "AKTIVNA",
		ProvizijaProcenat:         data.ProvizijaProcenat,
		KonverzijaNaknadaProcenat: data.KonverzijaNaknadaProcenat,
	}

	// ── 5. Atomičan upis u bazu ──────────────────────────────────────────────
	var karticaID int64
	if state.OvlascenoLice != nil {
		// Poslovni račun sa radnikom: kartica + ovlašćeno lice u jednoj transakciji.
		karticaID, err = s.repo.CreateKarticaSaOvlascenoLicem(ctx, karticaInput, *state.OvlascenoLice)
	} else {
		// Lični račun ili poslovni vlasnik: samo kartica.
		karticaID, err = s.repo.CreateKartica(ctx, karticaInput)
	}
	if err != nil {
		return 0, fmt.Errorf("kreiranje kartice: %w", err)
	}

	// ── 6. Cleanup Redis ─────────────────────────────────────────────────────
	_ = s.redisStore.DeleteCardRequest(ctx, input.VlasnikID)

	return karticaID, nil
}

// ─── Klijentski API — kartice ─────────────────────────────────────────────────

// GetMojeKartice vraća sve kartice na računima ulogovanog klijenta.
func (s *karticaService) GetMojeKartice(ctx context.Context, korisnikID int64) ([]domain.KarticaSaRacunom, error) {
	return s.repo.GetKarticeKorisnika(ctx, korisnikID)
}

func (s *karticaService) GetKarticeZaPortalZaposlenih(ctx context.Context, brojRacuna string) ([]domain.KarticaEmployeeRow, error) {
	return s.repo.GetKarticeZaRacunBroj(ctx, brojRacuna)
}

// ChangeEmployeeCardStatus menja status kartice od strane zaposlenog.
// Dozvoljene tranzicije:
//   - AKTIVNA   → BLOKIRANA
//   - AKTIVNA   → DEAKTIVIRANA
//   - BLOKIRANA → AKTIVNA     (deblokada — samo zaposleni)
//   - BLOKIRANA → DEAKTIVIRANA
//   - DEAKTIVIRANA → *         (zabranjeno — trajna promena)
//
// Vraća podatke o kartici potrebne za slanje email notifikacija.
func (s *karticaService) ChangeEmployeeCardStatus(ctx context.Context, brojKartice, noviStatus string) (*domain.KarticaZaStatusChange, error) {
	kartica, err := s.repo.GetKarticaZaStatusChange(ctx, brojKartice)
	if err != nil {
		return nil, err
	}

	switch kartica.TrenutniStatus {
	case "DEAKTIVIRANA":
		return nil, domain.ErrNedozvoljenaPromenaSatusa
	case "AKTIVNA":
		if noviStatus == "AKTIVNA" {
			return nil, domain.ErrKarticaVecAktivna
		}
		if noviStatus != "BLOKIRANA" && noviStatus != "DEAKTIVIRANA" {
			return nil, domain.ErrNedozvoljenaPromenaSatusa
		}
	case "BLOKIRANA":
		if noviStatus == "BLOKIRANA" {
			return nil, domain.ErrKarticaVecBlokirana
		}
		if noviStatus != "AKTIVNA" && noviStatus != "DEAKTIVIRANA" {
			return nil, domain.ErrNedozvoljenaPromenaSatusa
		}
	default:
		return nil, domain.ErrNedozvoljenaPromenaSatusa
	}

	if err := s.repo.SetKarticaStatus(ctx, kartica.ID, noviStatus); err != nil {
		return nil, err
	}
	return kartica, nil
}

// BlokirajKarticu blokira karticu ako vlasništvo i status to dozvoljavaju.
//
// Dozvoljeni prelaz: AKTIVNA → BLOKIRANA.
// Zabranjeni prelazi koje klijent ne sme da izvrši:
//   - BLOKIRANA → * (odblokiranje ili dalja promena nije dozvoljena klijentu)
//   - DEAKTIVIRANA → * (deaktivirana kartica je trajno neaktivna)
//
// Ova metoda NAMERNO ne prima `noviStatus` kao parametar — endpoint je
// isključivo za blokiranje. Time se sprečava slučajna promena u drugi status.
func (s *karticaService) BlokirajKarticu(ctx context.Context, karticaID, korisnikID int64) error {
	info, err := s.repo.GetKarticaOwnerInfo(ctx, karticaID)
	if err != nil {
		return err
	}

	// Security: kartica mora biti na računu ulogovanog korisnika.
	if info.VlasnikID != korisnikID {
		return domain.ErrKarticaNijeTvoja
	}

	// Biznis pravila za dozvoljene prelaze statusa.
	switch info.Status {
	case "AKTIVNA":
		// Jedini dozvoljen prelaz — nastavljamo.
	case "BLOKIRANA":
		return domain.ErrKarticaVecBlokirana
	case "DEAKTIVIRANA":
		return domain.ErrKarticaDeaktivirana
	default:
		return fmt.Errorf("neočekivani status kartice: %s", info.Status)
	}

	return s.repo.SetKarticaStatus(ctx, karticaID, "BLOKIRANA")
}

// generateOTP generiše kriptografski siguran 6-cifreni numerički kod (000000–999999).
func generateOTP() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// ─── hashCVV ──────────────────────────────────────────────────────────────────

// hashCVV računa HMAC-SHA256(cvv, pepper) i vraća 64-cifreni hex string.
//
// PCI-DSS zahtev: plain CVV se NIKAD ne čuva u bazi.
// HMAC sa tajnim pepper-om sprečava brute-force (CVV ima samo 1000 vrednosti
// pa je bez ključa trivijalan za proveravati — bcrypt ovde ne bi pomogao).
// Output je uvek tačno 64 hex karaktera — odgovara CHAR(64) koloni u bazi.
func hashCVV(cvv, pepper string) string {
	mac := hmac.New(sha256.New, []byte(pepper))
	mac.Write([]byte(cvv))
	return hex.EncodeToString(mac.Sum(nil))
}
