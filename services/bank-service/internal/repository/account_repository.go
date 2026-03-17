package repository

import (
	"context"
	"fmt"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"gorm.io/gorm"
)

// currencyModel je GORM projekcija tabele currencies.
// Neeksportovan — detalj implementacije, ne izlazi iz ovog paketa.
type currencyModel struct {
	ID     int64  `gorm:"column:id;primaryKey"`
	Naziv  string `gorm:"column:naziv"`
	Oznaka string `gorm:"column:oznaka"`
}

func (currencyModel) TableName() string {
    return "core_banking.valuta"
}

// toDomain mapira GORM model u domenski objekat.
func (m currencyModel) toDomain() domain.Currency {
	return domain.Currency{
		ID:     m.ID,
		Naziv:  m.Naziv,
		Oznaka: m.Oznaka,
	}
}

// currencyRepository implementira domain.CurrencyRepository.
type currencyRepository struct {
	db *gorm.DB
}

// NewCurrencyRepository vraća implementaciju CurrencyRepository.
func NewCurrencyRepository(db *gorm.DB) domain.CurrencyRepository {
	return &currencyRepository{db: db}
}

// GetByID vraća valutu po ID-u.
func (r *currencyRepository) GetByID(ctx context.Context, id int64) (*domain.Currency, error) {
	var m currencyModel
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	c := m.toDomain()
	return &c, nil
}

// GetAll vraća sve valute iz tabele currencies.
func (r *currencyRepository) GetAll(ctx context.Context) ([]domain.Currency, error) {
	var models []currencyModel
	if err := r.db.WithContext(ctx).Find(&models).Error; err != nil {
		return nil, err
	}

	currencies := make([]domain.Currency, 0, len(models))
	for _, m := range models {
		currencies = append(currencies, m.toDomain())
	}
	return currencies, nil
}

// ── Delatnost ─────────────────────────────────────────────────────────────────

// delatnostModel je GORM projekcija tabele core_banking.delatnost.
type delatnostModel struct {
	ID     int64  `gorm:"column:id;primaryKey"`
	Sifra  string `gorm:"column:sifra"`
	Naziv  string `gorm:"column:naziv"`
	Grana  string `gorm:"column:grana"`
	Sektor string `gorm:"column:sektor"`
}
// Napomena: kolone odgovaraju tabeli core_banking.delatnost
// (sifra, naziv, grana, sektor — definisano u 000001_init_schema.up.sql)

func (delatnostModel) TableName() string {
	return "core_banking.delatnost"
}

func (m delatnostModel) toDomain() domain.Delatnost {
	return domain.Delatnost{
		ID:     m.ID,
		Sifra:  m.Sifra,
		Naziv:  m.Naziv,
		Grana:  m.Grana,
		Sektor: m.Sektor,
	}
}

// delatnostRepository implementira domain.DelatnostRepository.
type delatnostRepository struct {
	db *gorm.DB
}

// NewDelatnostRepository vraća implementaciju DelatnostRepository.
func NewDelatnostRepository(db *gorm.DB) domain.DelatnostRepository {
	return &delatnostRepository{db: db}
}

// GetAll vraća sve delatnosti iz tabele core_banking.delatnost.
func (r *delatnostRepository) GetAll(ctx context.Context) ([]domain.Delatnost, error) {
	var models []delatnostModel
	if err := r.db.WithContext(ctx).Find(&models).Error; err != nil {
		return nil, err
	}

	delatnosti := make([]domain.Delatnost, 0, len(models))
	for _, m := range models {
		delatnosti = append(delatnosti, m.toDomain())
	}
	return delatnosti, nil
}

// ── Account ───────────────────────────────────────────────────────────────────

// firmaModel je GORM projekcija tabele core_banking.firma.
type firmaModel struct {
	ID           int64  `gorm:"column:id;primaryKey"`
	NazivFirme   string `gorm:"column:naziv_firme"`
	MaticniBroj  string `gorm:"column:maticni_broj"`
	PoresikBroj  string `gorm:"column:poreski_broj"`
	IDDelatnosti int64  `gorm:"column:id_delatnosti"`
	Adresa       string `gorm:"column:adresa"`
	VlasnikID    int64  `gorm:"column:vlasnik_id"`
}

func (firmaModel) TableName() string { return "core_banking.firma" }

// racunModel je GORM projekcija tabele core_banking.racun.
type racunModel struct {
	ID               int64      `gorm:"column:id;primaryKey;autoIncrement"`
	BrojRacuna       string     `gorm:"column:broj_racuna;uniqueIndex"`
	IDZaposlenog     int64      `gorm:"column:id_zaposlenog"`
	IDVlasnika       int64      `gorm:"column:id_vlasnika"`
	IDFirme          *int64     `gorm:"column:id_firme"`
	IDValute         int64      `gorm:"column:id_valute"`
	KategorijaRacuna string     `gorm:"column:kategorija_racuna"`
	VrstaRacuna      string     `gorm:"column:vrsta_racuna"`
	Podvrsta         *string    `gorm:"column:podvrsta"`
	NazivRacuna      string     `gorm:"column:naziv_racuna"`
	StanjeRacuna     float64    `gorm:"column:stanje_racuna"`
	DatumKreiranja   time.Time  `gorm:"column:datum_kreiranja"`
	DatumIsteka      time.Time  `gorm:"column:datum_isteka"`
}

func (racunModel) TableName() string { return "core_banking.racun" }

// accountRepository implementira domain.AccountRepository.
type accountRepository struct {
	db *gorm.DB
}

// NewAccountRepository vraća implementaciju AccountRepository.
func NewAccountRepository(db *gorm.DB) domain.AccountRepository {
	return &accountRepository{db: db}
}

// CreateAccount izvršava SQL transakciju: INSERT firma (opcionalno) → INSERT racun.
// Vraća racun.id (surogat PK) novokreiranog računa.
func (r *accountRepository) CreateAccount(ctx context.Context, input domain.CreateAccountInput, brojRacuna string) (int64, error) {
	var idFirme int64
	var racunID int64

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Korak 1: INSERT firma — samo za POSLOVNI račun sa prosleđenim podacima firme.
		if input.VrstaRacuna == "POSLOVNI" && input.Firma != nil {
			f := &firmaModel{
				NazivFirme:   input.Firma.Naziv,
				MaticniBroj:  input.Firma.MaticniBroj,
				PoresikBroj:  input.Firma.PIB,
				IDDelatnosti: input.Firma.DelatnostID,
				Adresa:       input.Firma.Adresa,
				VlasnikID:    input.VlasnikID, // vlasnik_id dolazi iz root zahteva
			}
			if err := tx.Create(f).Error; err != nil {
				return fmt.Errorf("insert firma: %w", err)
			}
			idFirme = f.ID
		}

		// Korak 2: INSERT racun.
		now := time.Now().UTC()

		var idFirmePtr *int64
		if idFirme != 0 {
			idFirmePtr = &idFirme
		}

		var podvrstaPtr *string
		if input.Podvrsta != "" {
			p := input.Podvrsta
			podvrstaPtr = &p
		}

		racun := &racunModel{
			BrojRacuna:       brojRacuna,
			IDZaposlenog:     input.ZaposleniID,
			IDVlasnika:       input.VlasnikID,
			IDFirme:          idFirmePtr,
			IDValute:         input.ValutaID,
			KategorijaRacuna: input.KategorijaRacuna,
			VrstaRacuna:      input.VrstaRacuna,
			Podvrsta:         podvrstaPtr,
			NazivRacuna:      input.NazivRacuna,
			StanjeRacuna:     input.StanjeRacuna,
			DatumKreiranja:   now,
			DatumIsteka:      now.AddDate(5, 0, 0),
		}
		if err := tx.Create(racun).Error; err != nil {
			return fmt.Errorf("insert racun: %w", err)
		}
		racunID = racun.ID

		return nil
	})

	if err != nil {
		return 0, err
	}
	return racunID, nil
}
