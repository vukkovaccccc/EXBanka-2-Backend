package domain

import (
	"context"
	"errors"
	"time"
)

// ─── Greške ───────────────────────────────────────────────────────────────────

var (
	ErrKarticaNotFound             = errors.New("kartica nije pronađena")
	ErrKarticaLimitPremasen        = errors.New("premašen je dozvoljen broj kartica za ovaj račun")
	ErrOvlascenoLiceNotFound       = errors.New("ovlašćeno lice nije pronađeno")
	ErrKarticaVecPostoji           = errors.New("ovlašćeno lice već ima aktivnu karticu")
	ErrRacunNijeAktivan            = errors.New("račun nije aktivan")
	ErrRacunNijeTvoj               = errors.New("račun ne pripada ovom korisniku")
	ErrInvalidEmailFormat          = errors.New("email adresa ovlašćenog lica nije ispravna")
	ErrOvlascenoLiceMissingData    = errors.New("ime, prezime i email ovlašćenog lica su obavezni")
	ErrOvlascenoLiceNijeDozvoljeno = errors.New("lični račun ne može imati ovlašćeno lice")
	ErrNotificationFailed          = errors.New("greška pri slanju verifikacionog koda — pokušajte ponovo")
	ErrDinaCardSamoRSD             = errors.New("DinaCard se može izdati samo uz dinarski (RSD) račun")
	ErrNepoznatTipKartice          = errors.New("nepoznat tip kartice — dozvoljena: VISA, MASTERCARD, DINACARD, AMEX")

	// Klijentski API — blokiranje kartice
	ErrKarticaNijeTvoja    = errors.New("kartica ne pripada ovom korisniku")
	ErrKarticaVecBlokirana = errors.New("kartica je već blokirana")
	ErrKarticaDeaktivirana = errors.New("deaktivirana kartica ne može biti blokirana")

	// Portal zaposlenih — promena statusa kartice
	ErrNedozvoljenaPromenaSatusa = errors.New("nedozvoljena promena statusa kartice")
	ErrKarticaVecAktivna         = errors.New("kartica je već aktivna")

	// Flow 2 Korak 2 — verifikacija OTP-a
	ErrCardRequestNotFound = errors.New("nema aktivnog zahteva za karticu — pokrenite novi zahtev")
	ErrOTPInvalid          = errors.New("pogrešan verifikacioni kod")
	ErrOTPMaxAttempts      = errors.New("previše neuspelih pokušaja — zahtev je poništen, pokrenite novi")
)

// ─── Tip kartice ──────────────────────────────────────────────────────────────

const (
	TipKarticaVisa       = "VISA"
	TipKarticaMastercard = "MASTERCARD"
	TipKarticaDinaCard   = "DINACARD"
	TipKarticaAmex       = "AMEX"
)

// ─── Domenski objekti ─────────────────────────────────────────────────────────

// Kartica je domenski objekat za platnu karticu vezanu za račun.
type Kartica struct {
	ID             int64
	BrojKartice    string
	TipKartice     string // "VISA" | "MASTERCARD" | "DINACARD" | "AMEX"
	VrstaKartice   string // "DEBIT" | "CREDIT"
	DatumKreiranja time.Time
	DatumIsteka    time.Time
	RacunID        int64
	LimitKartice   float64
	Status         string // "AKTIVNA" | "BLOKIRANA" | "DEAKTIVIRANA"
	OvlascenoLice  *OvlascenoLice
}

// CreateKarticaInput je ulazni objekat za kreiranje kartice u bazi.
// CvvKodHash je HMAC-SHA256(cvv, pepper) — plain CVV se nikad ne čuva.
type CreateKarticaInput struct {
	RacunID        int64
	BrojKartice    string
	TipKartice     string // VISA | MASTERCARD | DINACARD | AMEX
	VrstaKartice   string
	CvvKodHash     string    // HMAC-SHA256(cvv, pepper) — tačno 64 hex karaktera
	DatumKreiranja time.Time // eksplicitno postavljeno u servisu — sprečava zero-value (0001-01-01)
	DatumIsteka    time.Time
	LimitKartice   float64
	Status         string
	// MasterCard + RSD specifičnost: provizija banke i konverziona naknada.
	// NULL za sve ostale kombinacije kartica/valuta.
	ProvizijaProcenat         *float64 // 0.02 = 2%
	KonverzijaNaknadaProcenat *float64 // 0.005 = 0.5%
}

// RacunInfo su podaci o računu koji su potrebni servisu za kreiranje kartice.
type RacunInfo struct {
	VrstaRacuna  string  // "LICNI" | "POSLOVNI"
	MesecniLimit float64 // koristi se kao početni limit kartice
	ValutaOznaka string  // ISO 4217, npr. "RSD", "EUR" — za DinaCard validaciju
}

// OvlascenoLice je domenski objekat za radnika koji koristi karticu
// poslovnog računa. Postoji radi praćenja limita (max 1 kartica po osobi).
type OvlascenoLice struct {
	ID            int64
	KarticaID     int64
	Ime           string
	Prezime       string
	Pol           string
	EmailAdresa   string
	BrojTelefona  string
	Adresa        string
	DatumRodjenja int64 // Unix timestamp (sekunde od 1970-01-01 UTC)
}

// OvlascenoLiceInput je ulazni objekat za ovlašćeno lice u Flow 2.
// Prosleđuje se iz handlera do servisa i kešira se u Redisu dok se ne potvrdi OTP.
type OvlascenoLiceInput struct {
	Ime           string `json:"ime"`
	Prezime       string `json:"prezime"`
	Pol           string `json:"pol"`
	EmailAdresa   string `json:"email_adresa"`
	BrojTelefona  string `json:"broj_telefona"`
	Adresa        string `json:"adresa"`
	DatumRodjenja int64  `json:"datum_rodjenja"` // Unix timestamp
}

// RequestKarticaInput je ulaz za servisnu metodu Flow 2 (klijent inicira zahtev).
type RequestKarticaInput struct {
	RacunID       int64
	VlasnikID     int64               // iz JWT tokena
	VlasnikEmail  string              // dohvaćen iz user-service, koristi se za slanje OTP-a
	TipKartice    string              // VISA | MASTERCARD | DINACARD | AMEX — čuva se u Redis state-u
	OvlascenoLice *OvlascenoLiceInput // nil = kartica za vlasnika; !nil = za ovlašćeno lice
}

// ConfirmKarticaInput je ulaz za Flow 2 Korak 2 — verifikacija OTP-a i kreiranje kartice.
type ConfirmKarticaInput struct {
	VlasnikID int64  // iz JWT tokena
	OTPCode   string // 6-cifreni kod koji je klijent primio emailom
}

// RacunVlasnikInfo sadrži podatke o računu potrebne za Flow 2 validaciju.
type RacunVlasnikInfo struct {
	VlasnikID    int64
	VrstaRacuna  string // "LICNI" | "POSLOVNI"
	Status       string // "AKTIVAN" | ...
	MesecniLimit float64
}

// KarticaEmployeeRow je minimalna projekcija kartice za portal zaposlenih.
// Sadrži ID vlasnika računa kako bi handler mogao da dohvati ime/prezime/email
// sinhronim pozivom ka user-service-u.
type KarticaEmployeeRow struct {
	ID          int64
	BrojKartice string
	Status      string
	VlasnikID   int64
}

// KarticaSaRacunom objedinjuje karticu sa osnovnim podacima o računu za koji je vezana.
// Koristi se u klijentskom API-ju za prikaz liste kartica.
type KarticaSaRacunom struct {
	Kartica
	NazivRacuna string
	BrojRacuna  string
}

// KarticaOwnerInfo sadrži status kartice i ID vlasnika računa.
// Koristi se u servisu za validaciju vlasništva i statusa pre blokiranja.
type KarticaOwnerInfo struct {
	Status    string
	VlasnikID int64
}

// KarticaZaStatusChange sadrži podatke kartice potrebne za promenu statusa
// od strane zaposlenog, uključujući podatke o računu i ovlašćenom licu.
type KarticaZaStatusChange struct {
	ID             int64
	TrenutniStatus string
	VlasnikID      int64
	VrstaRacuna    string         // "LICNI" | "POSLOVNI"
	OvlascenoLice  *OvlascenoLice // nil za lične račune
}

// CardRequestState je JSON payload koji se keširaju u Redis pod ključem card_req_{vlasnikID}.
// Sadrži sve podatke potrebne za Korak 2 (verifikacija OTP-a i kreiranje kartice).
type CardRequestState struct {
	AccountID     int64               `json:"account_id"`
	TipKartice    string              `json:"tip_kartice"` // VISA | MASTERCARD | DINACARD | AMEX
	OvlascenoLice *OvlascenoLiceInput `json:"authorized_person,omitempty"`
	OTPCode       string              `json:"otp_code"`
	Attempts      int                 `json:"attempts"`
}

// ─── Infrastrukturni interfejsi (implementacije u transport paketu) ────────────

// CardRequestStore apstrahuje Redis operacije za čuvanje stanja zahteva za karticu.
type CardRequestStore interface {
	// SaveCardRequest upisuje (ili prepisuje) state u Redis sa datim TTL-om.
	// Koristimo Set (upsert) — Edge Case 5: spam protection, overwrite postojećeg ključa.
	SaveCardRequest(ctx context.Context, ownerID int64, state CardRequestState, ttl time.Duration) error

	// GetCardRequest čita state iz Redisa. Vraća ErrCardRequestNotFound ako ključ ne postoji
	// (zahtev nikad nije pokrenuti ili je istekao TTL-om ili je već uspešno obrađen).
	GetCardRequest(ctx context.Context, ownerID int64) (*CardRequestState, error)

	// DeleteCardRequest briše ključ iz Redisa.
	// Poziva se pri rollback-u (Edge Case 6) ili po uspešnom kreiranju kartice.
	DeleteCardRequest(ctx context.Context, ownerID int64) error
}

// NotificationSender apstrahuje sinhronizovanu komunikaciju sa notification-service.
// Sinhronizovana jer trebamo znati da li je OTP zaista poslat (rollback u slučaju greške).
type NotificationSender interface {
	SendCardOTP(ctx context.Context, toEmail, otpCode string) error
}

// ─── Ugovori (interfejsi) ─────────────────────────────────────────────────────

// KarticaRepository definiše ugovor prema sloju podataka.
type KarticaRepository interface {
	// CreateKartica upisuje novu karticu i vraća njen ID.
	CreateKartica(ctx context.Context, input CreateKarticaInput) (int64, error)

	// CountKarticeZaRacun vraća ukupan broj kartica na računu.
	// Koristi se za proveru limita na LICNI računima (max 2).
	CountKarticeZaRacun(ctx context.Context, racunID int64) (int64, error)

	// HasVlasnikovaKarticaPostoji proverava da li vlasnik već ima karticu
	// na datom računu (kartica bez ovlasceno_lice zapisa = vlasnikova kartica).
	// Koristi se za proveru limita na POSLOVNI računima u Flow 1.
	HasVlasnikovaKarticaPostoji(ctx context.Context, racunID int64) (bool, error)

	// GetKarticaByID vraća karticu sa opcionalno učitanim ovlašćenim licem.
	GetKarticaByID(ctx context.Context, karticaID int64) (*Kartica, error)

	// GetKarticeByRacun vraća sve kartice jednog računa.
	GetKarticeByRacun(ctx context.Context, racunID int64) ([]Kartica, error)

	// HasOvlascenoLiceKarticu proverava limit za Flow 2:
	// da li dato lice (po emailu) već ima karticu na bilo kom računu.
	HasOvlascenoLiceKarticu(ctx context.Context, emailAdresa string) (bool, error)

	// GetRacunInfo dohvata vrsta_racuna i mesecni_limit za dati račun.
	// Koristi se u servisu da bi se odredila vrsta limita i inicijalni limit kartice.
	GetRacunInfo(ctx context.Context, racunID int64) (*RacunInfo, error)

	// GetRacunVlasnikInfo dohvata vlasnik_id, vrsta_racuna, status i mesecni_limit.
	// Koristi se u Flow 2 za Security proveru (da li je klijent vlasnik) i validaciju.
	GetRacunVlasnikInfo(ctx context.Context, racunID int64) (*RacunVlasnikInfo, error)

	// CreateKarticaSaOvlascenoLicem kreira karticu i ovlašćeno lice atomično u jednoj
	// DB transakciji. Koristi se u Flow 2 Korak 2 za poslovne račune.
	// Ako kreiranje ovlašćenog lica ne uspe, cela transakcija se poništava (rollback).
	CreateKarticaSaOvlascenoLicem(ctx context.Context, karticaInput CreateKarticaInput, olInput OvlascenoLiceInput) (int64, error)

	// GetKarticeKorisnika vraća sve kartice na računima čiji je vlasnik korisnikID,
	// zajedno sa nazivom i brojem računa. Koristi se u klijentskom API-ju.
	GetKarticeKorisnika(ctx context.Context, korisnikID int64) ([]KarticaSaRacunom, error)

	// GetKarticaOwnerInfo vraća status kartice i ID vlasnika njenog računa.
	// Koristi se u servisu za validaciju vlasništva i statusa pre blokiranja.
	GetKarticaOwnerInfo(ctx context.Context, karticaID int64) (*KarticaOwnerInfo, error)

	// SetKarticaStatus ažurira status kartice.
	// Poziva se samo nakon što je servis potvrdio vlasništvo i dozvoljenost prelaza.
	SetKarticaStatus(ctx context.Context, karticaID int64, noviStatus string) error

	// GetKarticeZaRacunBroj vraća sve kartice za račun identifikovan brojem računa,
	// zajedno sa ID-em vlasnika računa — za portal zaposlenih.
	GetKarticeZaRacunBroj(ctx context.Context, brojRacuna string) ([]KarticaEmployeeRow, error)

	// GetKarticaZaStatusChange dohvata karticu po broju kartice, zajedno sa
	// podacima o računu (vrsta_racuna, vlasnik_id) i ovlašćenim licem.
	// Koristi se isključivo od strane zaposlenih za promenu statusa.
	GetKarticaZaStatusChange(ctx context.Context, brojKartice string) (*KarticaZaStatusChange, error)
}

// KarticaService definiše ugovor prema sloju poslovne logike.
type KarticaService interface {
	// CreateKarticaZaVlasnika se poziva u Flow 1 — automatski uz kreiranje računa.
	// Kreira karticu za vlasnika računa; ne kreira ovlasceno_lice.
	// tipKartice: VISA | MASTERCARD | DINACARD | AMEX
	// Servis interno dohvata racun iz baze radi provere limita i postavljanja LimitKartice.
	CreateKarticaZaVlasnika(ctx context.Context, racunID int64, tipKartice string) (int64, error)

	// RequestKartica je Korak 1 u Flow 2 — klijent inicira zahtev za karticu.
	// Ne upisuje u PostgreSQL. Proverava limite, generiše OTP, keširaju u Redis (TTL 5min),
	// i šalje OTP na email vlasnika. Ako slanje emaila ne uspe → rollback Redis ključa.
	RequestKartica(ctx context.Context, input RequestKarticaInput) error

	// ConfirmKartica je Korak 2 u Flow 2 — klijent unosi OTP primljen emailom.
	// Proverava OTP (sa brojaračem pokušaja), re-validira limite (TOCTOU zaštita),
	// kreira karticu u PostgreSQL, i briše Redis ključ.
	// Vraća ID novokreirane kartice.
	ConfirmKartica(ctx context.Context, input ConfirmKarticaInput) (int64, error)

	// GetMojeKartice vraća sve kartice na računima ulogovanog klijenta.
	GetMojeKartice(ctx context.Context, korisnikID int64) ([]KarticaSaRacunom, error)

	// BlokirajKarticu blokira karticu sa datim ID-om ako:
	//   1. kartica pripada korisniku (vlasništvo)
	//   2. kartica je u statusu AKTIVNA (jedini dozvoljeni prelaz: AKTIVNA → BLOKIRANA)
	// Klijent ne može sam da odblokira niti deaktivira karticu.
	BlokirajKarticu(ctx context.Context, karticaID, korisnikID int64) error

	// GetKarticeZaPortalZaposlenih vraća kartice za dati broj računa — za portal zaposlenih.
	GetKarticeZaPortalZaposlenih(ctx context.Context, brojRacuna string) ([]KarticaEmployeeRow, error)

	// ChangeEmployeeCardStatus menja status kartice od strane zaposlenog.
	// Dozvoljena tranzicija statusa: AKTIVNA↔BLOKIRANA, */→DEAKTIVIRANA.
	// Vraća podatke o kartici (vlasnik, vrsta računa, ovlašćeno lice) potrebne
	// za slanje email notifikacija.
	ChangeEmployeeCardStatus(ctx context.Context, brojKartice, noviStatus string) (*KarticaZaStatusChange, error)
}
