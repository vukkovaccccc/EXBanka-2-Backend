package domain

import (
	"context"
	"errors"
	"time"
)

// ─── Greške kredita ───────────────────────────────────────────────────────────

var (
	ErrKreditNotFound         = errors.New("kredit nije pronađen")
	ErrKreditniZahtevNotFound = errors.New("zahtev za kredit nije pronađen")
	ErrZahtevVecObrađen       = errors.New("zahtev je već obrađen (odobren ili odbijen)")
	ErrRataNotFound           = errors.New("rata nije pronađena")
	ErrRataVecPlacena         = errors.New("rata je već plaćena")
	ErrKreditForbidden        = errors.New("kredit ne pripada trenutnom korisniku")
)

// ─── KreditniZahtev ───────────────────────────────────────────────────────────

// KreditniZahtev čuva sve podatke koje klijent upisuje pri podnošenju zahteva.
// Životni ciklus: NA_CEKANJU → ODOBREN | ODBIJEN.
type KreditniZahtev struct {
	ID                int64
	VlasnikID         int64
	VrstaKredita      string // GOTOVINSKI | STAMBENI | AUTO | REFINANSIRAJUCI | STUDENTSKI
	TipKamate         string // FIKSNI | VARIJABILNI
	IznosKredita      float64
	Valuta            string
	SvrhaKredita      string
	IznosMesecnePlate float64
	StatusZaposlenja  string // STALNO | PRIVREMENO | NEZAPOSLEN
	PeriodZaposlenja  int32  // u mesecima
	KontaktTelefon    string
	BrojRacuna        string
	RokOtplate        int32  // u mesecima
	Status            string // NA_CEKANJU | ODOBREN | ODBIJEN
	DatumPodnosenja   time.Time
}

// ─── Kredit ───────────────────────────────────────────────────────────────────

// Kredit čuva kompletno stanje odobrenog kredita.
// Kreira ga servisni sloj pri odobravanju zahteva, sa svim izračunatim
// finansijskim veličinama (anuiteti, kamatne stope).
type Kredit struct {
	ID                    int64
	BrojKredita           string
	KreditniZahtevID      *int64 // nil u edge case scenarijima
	BrojRacuna            string
	VlasnikID             int64
	VrstaKredita          string // GOTOVINSKI | STAMBENI | AUTO | REFINANSIRAJUCI | STUDENTSKI
	TipKamate             string // FIKSNI | VARIJABILNI
	IznosKredita          float64
	PeriodOtplate         int32   // inicijalni broj rata (meseci)
	NominalnaKamatnaStopa float64 // godišnja nominalna stopa, npr. 6.5
	EfektivnaKamatnaStopa float64 // efektivna kamatna stopa (EKS)
	DatumUgovaranja       time.Time
	DatumIsplate          *time.Time // nil dok novac nije uplaćen na račun
	IznosMesecneRate      float64
	DatumSledeceRate      *time.Time // nil za OTPLACEN kredit
	PreostaloDugovanje    float64
	Valuta                string
	Status                string // ODOBREN | OTPLACEN | U_KASNJENJU | ODBIJEN
	CreatedAt             time.Time
}

// ─── Rata ─────────────────────────────────────────────────────────────────────

// Rata je jedan red amortizacione tablice kredita.
// PraviDatumDospeca se popunjava isključivo pri uspešnom skidanju novca (Issue #3).
// BrojPokusaja i SledecaPokusaj podržavaju retry mehanizam (Issue #5).
type Rata struct {
	ID                    int64
	KreditID              int64
	IznosRate             float64
	IznosKamate           float64
	Valuta                string
	OcekivaniDatumDospeca time.Time
	PraviDatumDospeca     *time.Time // nil dok rata nije plaćena
	StatusPlacanja        string     // PLACENO | NEPLACENO | KASNI
	BrojPokusaja          int32
	SledecaPokusaj        *time.Time // nil dok rata nije u KASNI statusu
}

// ─── Projekcija za cron job ───────────────────────────────────────────────────

// DueInstallment je projekcija koja joinuje ratu sa kreditom.
// Koristi je cron job (Issue #5) jer mu trebaju i rata i račun sa kojeg se skida novac.
type DueInstallment struct {
	RataID                int64
	KreditID              int64
	IznosRate             float64
	Valuta                string
	OcekivaniDatumDospeca time.Time
	BrojPokusaja          int32
	SledecaPokusaj        *time.Time
	// Iz kredit tabele:
	BrojRacuna string
	VlasnikID  int64
}

// ─── Input DTO-ovi ────────────────────────────────────────────────────────────

// CreateKreditniZahtevInput ulazni parametri za podnošenje zahteva.
type CreateKreditniZahtevInput struct {
	VlasnikID         int64
	VrstaKredita      string
	TipKamate         string
	IznosKredita      float64
	Valuta            string
	SvrhaKredita      string
	IznosMesecnePlate float64
	StatusZaposlenja  string
	PeriodZaposlenja  int32
	KontaktTelefon    string
	BrojRacuna        string
	RokOtplate        int32
}

// RataInput jedan red amortizacione tablice koji servis prosleđuje repozitorijumu.
type RataInput struct {
	IznosRate             float64
	IznosKamate           float64
	OcekivaniDatumDospeca time.Time
}

// ApproveKreditInput sve podatke potrebne za atomsko odobravanje kredita.
// Servisni sloj popunjava finansijske veličine pre poziva repozitorijuma.
type ApproveKreditInput struct {
	ZahtevID              int64
	BrojKredita           string // pre-generisan od servisa
	BrojRacuna            string
	VlasnikID             int64
	VrstaKredita          string
	TipKamate             string
	IznosKredita          float64
	PeriodOtplate         int32
	NominalnaKamatnaStopa float64
	EfektivnaKamatnaStopa float64
	DatumUgovaranja       time.Time
	IznosMesecneRate      float64
	DatumSledeceRate      time.Time
	PreostaloDugovanje    float64
	Valuta                string
	Rate                  []RataInput
}

// ProcessInstallmentInput parametri za atomsku naplatu rate (cron job).
type ProcessInstallmentInput struct {
	RataID     int64
	KreditID   int64
	BrojRacuna string
	IznosRate  float64
	Valuta     string
}

// ─── Filter DTO-ovi ───────────────────────────────────────────────────────────

// GetPendingRequestsFilter filteri za pregled zahteva (zaposleni portal).
type GetPendingRequestsFilter struct {
	VrstaKredita string // "" = sve vrste
	BrojRacuna   string // "" = svi računi
}

// GetAllCreditsFilter filteri za pregled odobrenih kredita (zaposleni portal).
type GetAllCreditsFilter struct {
	VrstaKredita string // "" = sve vrste
	BrojRacuna   string // "" = svi računi
	Status       string // "" = svi statusi
}

// ─── Repository interfejs ─────────────────────────────────────────────────────

// KreditRepository definiše ugovor prema sloju podataka za Krediti modul.
type KreditRepository interface {
	// ── Zahtevi ──────────────────────────────────────────────────────────────

	// CreateKreditniZahtev kreira novi zahtev sa statusom NA_CEKANJU.
	CreateKreditniZahtev(ctx context.Context, input CreateKreditniZahtevInput) (*KreditniZahtev, error)

	// GetKreditniZahtevByID vraća zahtev po ID-u.
	GetKreditniZahtevByID(ctx context.Context, id int64) (*KreditniZahtev, error)

	// GetPendingRequests vraća sve zahteve u statusu NA_CEKANJU sa opcionim filterima.
	// Sortirano po datum_podnosenja ASC (najstariji zahtevi prvi).
	GetPendingRequests(ctx context.Context, filter GetPendingRequestsFilter) ([]KreditniZahtev, error)

	// RejectKreditRequest postavlja status zahteva na ODBIJEN.
	// Vraća ErrKreditniZahtevNotFound ako ne postoji.
	// Vraća ErrZahtevVecObrađen ako je već obrađen.
	RejectKreditRequest(ctx context.Context, zahtevID int64) error

	// ── Krediti ───────────────────────────────────────────────────────────────

	// ApproveKreditRequest atomski: ažurira zahtev, kreira kredit, kreira sve rate,
	// uplaćuje iznos kredita na račun klijenta i beleži UPLATA transakciju.
	ApproveKreditRequest(ctx context.Context, input ApproveKreditInput) (*Kredit, error)

	// GetKreditByID vraća kredit po surogat PK-u.
	GetKreditByID(ctx context.Context, id int64) (*Kredit, error)

	// GetKreditsByVlasnik vraća sve kredite klijenta sortirano opadajuće po
	// iznosu (Issue #1: "sortirano opadajuće po iznosu").
	GetKreditsByVlasnik(ctx context.Context, vlasnikID int64) ([]Kredit, error)

	// GetAllCredits vraća sve kredite sa opcionim filterima (zaposleni portal).
	// Sortirano po broj_racuna ASC (Issue #2).
	GetAllCredits(ctx context.Context, filter GetAllCreditsFilter) ([]Kredit, error)

	// UpdateKreditStatus menja status kredita (npr. ODOBREN → U_KASNJENJU ili OTPLACEN).
	UpdateKreditStatus(ctx context.Context, kreditID int64, status string) error

	// ── Rate ──────────────────────────────────────────────────────────────────

	// GetInstallmentsByKredit vraća sve rate jednog kredita sortirano po
	// ocekivani_datum_dospeca ASC.
	GetInstallmentsByKredit(ctx context.Context, kreditID int64) ([]Rata, error)

	// GetDueInstallments vraća neplaćene rate čiji je datum dospeća <= asOf
	// i koje još nisu u KASNI statusu (tj. primarni pokušaj naplate).
	// Joinuje sa kredit tabelom da bi vratio i broj_racuna za naplatu.
	GetDueInstallments(ctx context.Context, asOf time.Time) ([]DueInstallment, error)

	// GetRetryInstallments vraća rate u statusu KASNI čiji je sledeci_pokusaj <= asOf.
	// Koristi se za retry logiku u cron job-u (Issue #5).
	GetRetryInstallments(ctx context.Context, asOf time.Time) ([]DueInstallment, error)

	// ProcessInstallmentPayment atomski: proverava idempotentnost (status != PLACENO),
	// skida sredstva sa računa, beleži ISPLATA transakciju, označava ratu PLACENO
	// (sa praviDatumDospeca = NOW()), kreira sledeću ratu unapred i ažurira
	// kredit.datum_sledece_rate i kredit.preostalo_dugovanje.
	// Vraća ErrRataVecPlacena ako je rata već naplaćena (idempotentnost).
	ProcessInstallmentPayment(ctx context.Context, input ProcessInstallmentInput) error

	// MarkInstallmentFailed označava ratu statusom KASNI, uvećava broj_pokusaja
	// i postavlja sledeci_pokusaj na zadati termin (Issue #5).
	MarkInstallmentFailed(ctx context.Context, rataID int64, nextRetry time.Time) error

	// ApplyLatePaymentPenalty uvećava nominalna_kamatna_stopa kredita za zadati
	// broj baznih poena (npr. 0.05 za +0.05%) i ažurira iznos_mesecne_rate po
	// novoj stopi za preostale neizmirene rate (Issue #5).
	ApplyLatePaymentPenalty(ctx context.Context, kreditID int64, penaltyPercent float64) error
}

// ─── Service interfejs ────────────────────────────────────────────────────────

// KreditService definiše ugovor poslovne logike za Krediti modul.
type KreditService interface {
	// Klijentske operacije
	ApplyForCredit(ctx context.Context, input CreateKreditniZahtevInput) (*KreditniZahtev, error)
	GetClientCredits(ctx context.Context, vlasnikID int64) ([]Kredit, error)
	GetCreditDetails(ctx context.Context, kreditID, vlasnikID int64) (*Kredit, []Rata, error)

	// Zaposleni operacije
	GetAllPendingRequests(ctx context.Context, filter GetPendingRequestsFilter) ([]KreditniZahtev, error)
	ApproveCredit(ctx context.Context, zahtevID int64) (*Kredit, error)
	RejectCredit(ctx context.Context, zahtevID int64) error
	GetAllApprovedCredits(ctx context.Context, filter GetAllCreditsFilter) ([]Kredit, error)

	// ProcessFirstInstallment pokušava naplatu prve rate odmah po odobravanju kredita.
	// Vraća insufficientFunds=true ako nema sredstava (rata markirana KASNI, retry za 72h).
	// Vraća insufficientFunds=false i err=nil ako je naplata uspela ili je rata već plaćena.
	ProcessFirstInstallment(ctx context.Context, kreditID int64) (insufficientFunds bool, nextRetry time.Time, err error)
}
