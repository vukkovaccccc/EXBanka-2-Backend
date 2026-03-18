package domain

import (
	"context"
	"errors"
	"time"
)

// ─── Greške plaćanja ──────────────────────────────────────────────────────────

var (
	ErrPaymentIntentNotFound    = errors.New("nalog za plaćanje nije pronađen")
	ErrPaymentRecipientNotFound = errors.New("primalac plaćanja nije pronađen")
	ErrDuplicateIdempotencyKey  = errors.New("nalog sa istim idempotency ključem već postoji")
	ErrPaymentAlreadyExecuted   = errors.New("nalog je već izvršen")
	ErrPaymentAlreadyFailed     = errors.New("nalog je odbijen")
	ErrInvalidPaymentCode       = errors.New("šifra plaćanja mora imati 3 cifre i počinjati sa 2")
	ErrSameAccount              = errors.New("račun platioca i primaoca ne smeju biti isti")
	ErrAccountNotOwned          = errors.New("račun ne pripada trenutno prijavljenom korisniku")
	ErrRecipientAccountInvalid  = errors.New("primalački račun nije pronađen ili nije aktivan")
	ErrInsufficientFunds        = errors.New("nedovoljno sredstava na računu")
	ErrDailyLimitExceeded       = errors.New("probijen dnevni limit plaćanja")
	ErrMonthlyLimitExceeded     = errors.New("probijen mesečni limit plaćanja")
)

// ─── PaymentRecipient ─────────────────────────────────────────────────────────

// PaymentRecipient je domenski objekat za sačuvanog primaoca plaćanja.
type PaymentRecipient struct {
	ID         int64
	VlasnikID  int64
	Naziv      string
	BrojRacuna string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ─── PaymentIntent ────────────────────────────────────────────────────────────

// PaymentIntent je domenski objekat za nalog plaćanja/prenosa.
type PaymentIntent struct {
	ID                  int64
	IdempotencyKey      string
	BrojNaloga          string
	TipTransakcije      string // "PLACANJE" | "PRENOS"
	RacunPlatioceID     int64
	BrojRacunaPlatioca  string
	ValutaOznaka        string
	RacunPrimaocaID     *int64
	BrojRacunaPrimaoca  string
	NazivPrimaoca       string
	Iznos               float64
	KrajnjiIznos        *float64
	Provizija           float64
	SifraPlacanja       string
	PozivNaBroj         string
	SvrhaPlacanja       string
	Status              string // "U_OBRADI" | "REALIZOVANO" | "ODBIJENO"
	PendingActionID     *int64
	InitiatedByUserID   int64
	CreatedAt           time.Time
	VerifiedAt          *time.Time
	ExecutedAt          *time.Time
	FailedReason        string
}

// ─── Filter za istoriju plaćanja ──────────────────────────────────────────────

// PaymentHistoryFilter parametri za filtriranje istorije plaćanja.
type PaymentHistoryFilter struct {
	DateFrom *time.Time
	DateTo   *time.Time
	MinIznos *float64
	MaxIznos *float64
	Status   string // "" = sve
}

// ─── Input DTO-ovi ────────────────────────────────────────────────────────────

// CreatePaymentIntentInput ulazni parametri za kreiranje naloga plaćanja.
type CreatePaymentIntentInput struct {
	IdempotencyKey     string
	RacunPlatioceID    int64
	BrojRacunaPrimaoca string
	NazivPrimaoca      string
	Iznos              float64
	SifraPlacanja      string
	PozivNaBroj        string
	SvrhaPlacanja      string
	InitiatedByUserID  int64
}

// CreateTransferIntentInput ulazni parametri za kreiranje naloga prenosa.
type CreateTransferIntentInput struct {
	IdempotencyKey    string
	RacunPlatioceID   int64
	RacunPrimaocaID   int64
	Iznos             float64
	SvrhaPlacanja     string
	InitiatedByUserID int64
}

// VerifyPaymentInput parametri za verifikaciju plaćanja i izvršenje.
type VerifyPaymentInput struct {
	IntentID  int64
	Code      string
	UserID    int64
}

// ─── Repository interfejsi ────────────────────────────────────────────────────

// PaymentRecipientRepository definiše ugovor za operacije sa primaocima.
type PaymentRecipientRepository interface {
	Create(ctx context.Context, recipient *PaymentRecipient) error
	GetByID(ctx context.Context, id, vlasnikID int64) (*PaymentRecipient, error)
	GetByOwner(ctx context.Context, vlasnikID int64) ([]PaymentRecipient, error)
	Update(ctx context.Context, recipient *PaymentRecipient) error
	Delete(ctx context.Context, id, vlasnikID int64) error
	ExistsByOwnerAndAccount(ctx context.Context, vlasnikID int64, brojRacuna string) (bool, error)
}

// PaymentRepository definiše ugovor za operacije sa nalozima plaćanja.
type PaymentRepository interface {
	// CreateIntent kreira nalog i prateći pending_action za mobilnu verifikaciju.
	// Vraća kreiran intent; idempotentno — isti ključ vraća postojeći.
	CreateIntent(ctx context.Context, input CreatePaymentIntentInput) (*PaymentIntent, int64, error)

	// CreateTransferIntent kreira nalog prenosa između računa istog korisnika.
	// Vraća kreiran intent; idempotentno — isti ključ vraća postojeći.
	CreateTransferIntent(ctx context.Context, input CreateTransferIntentInput) (*PaymentIntent, int64, error)

	// VerifyAndExecute proverava verifikacioni kod i atomski izvršava plaćanje/prenos.
	// Zaštićeno row-level lockom oba računa u determinističkom redosledu.
	VerifyAndExecute(ctx context.Context, input VerifyPaymentInput) (*PaymentIntent, error)

	// GetByID vraća jedan nalog; greška ako ne pripada korisniku.
	GetByID(ctx context.Context, id, userID int64) (*PaymentIntent, error)

	// GetHistory vraća istoriju naloga sa filterima za trenutnog korisnika.
	GetHistory(ctx context.Context, userID int64, filter PaymentHistoryFilter) ([]PaymentIntent, error)
}

// ─── Service interfejs ────────────────────────────────────────────────────────

// PaymentService definiše ugovor poslovne logike za modul plaćanja.
type PaymentService interface {
	// Primaoci
	CreateRecipient(ctx context.Context, vlasnikID int64, naziv, brojRacuna string) (*PaymentRecipient, error)
	GetRecipients(ctx context.Context, vlasnikID int64) ([]PaymentRecipient, error)
	UpdateRecipient(ctx context.Context, id, vlasnikID int64, naziv, brojRacuna string) (*PaymentRecipient, error)
	DeleteRecipient(ctx context.Context, id, vlasnikID int64) error

	// Novo plaćanje
	CreatePaymentIntent(ctx context.Context, input CreatePaymentIntentInput) (*PaymentIntent, int64, error)

	// Prenos između računa istog korisnika
	CreateTransferIntent(ctx context.Context, input CreateTransferIntentInput) (*PaymentIntent, int64, error)

	// Verifikacija i izvršenje
	VerifyAndExecute(ctx context.Context, input VerifyPaymentInput) (*PaymentIntent, error)

	// Istorija i detalji
	GetPaymentHistory(ctx context.Context, userID int64, filter PaymentHistoryFilter) ([]PaymentIntent, error)
	GetPaymentDetail(ctx context.Context, id, userID int64) (*PaymentIntent, error)
}
