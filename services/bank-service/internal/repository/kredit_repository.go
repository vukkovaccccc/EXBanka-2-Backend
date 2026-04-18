package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"gorm.io/gorm"
)

// =============================================================================
// GORM modeli (interni za repozitorijum — ne izlaze iz ovog paketa)
// =============================================================================

// kreditniZahtevModel je GORM projekcija tabele kreditni_zahtev.
type kreditniZahtevModel struct {
	ID                int64     `gorm:"column:id;primaryKey"`
	VlasnikID         int64     `gorm:"column:vlasnik_id;not null"`
	VrstaKredita      string    `gorm:"column:vrsta_kredita;not null"`
	TipKamate         string    `gorm:"column:tip_kamate;not null"`
	IznosKredita      float64   `gorm:"column:iznos_kredita;not null"`
	Valuta            string    `gorm:"column:valuta;not null"`
	SvrhaKredita      string    `gorm:"column:svrha_kredita"`
	IznosMesecnePlate float64   `gorm:"column:iznos_mesecne_plate;not null"`
	StatusZaposlenja  string    `gorm:"column:status_zaposlenja;not null"`
	PeriodZaposlenja  int32     `gorm:"column:period_zaposlenja;not null"`
	KontaktTelefon    string    `gorm:"column:kontakt_telefon;not null"`
	BrojRacuna        string    `gorm:"column:broj_racuna;not null"`
	RokOtplate        int32     `gorm:"column:rok_otplate;not null"`
	Status            string    `gorm:"column:status;not null;default:NA_CEKANJU"`
	DatumPodnosenja   time.Time `gorm:"column:datum_podnosenja;not null"`
}

func (kreditniZahtevModel) TableName() string {
	return "core_banking.kreditni_zahtev"
}

func (m kreditniZahtevModel) toDomain() domain.KreditniZahtev {
	return domain.KreditniZahtev{
		ID:                m.ID,
		VlasnikID:         m.VlasnikID,
		VrstaKredita:      m.VrstaKredita,
		TipKamate:         m.TipKamate,
		IznosKredita:      m.IznosKredita,
		Valuta:            m.Valuta,
		SvrhaKredita:      m.SvrhaKredita,
		IznosMesecnePlate: m.IznosMesecnePlate,
		StatusZaposlenja:  m.StatusZaposlenja,
		PeriodZaposlenja:  m.PeriodZaposlenja,
		KontaktTelefon:    m.KontaktTelefon,
		BrojRacuna:        m.BrojRacuna,
		RokOtplate:        m.RokOtplate,
		Status:            m.Status,
		DatumPodnosenja:   m.DatumPodnosenja,
	}
}

// kreditModel je GORM projekcija tabele kredit.
type kreditModel struct {
	ID                    int64      `gorm:"column:id;primaryKey"`
	BrojKredita           string     `gorm:"column:broj_kredita;not null;uniqueIndex"`
	KreditniZahtevID      *int64     `gorm:"column:kreditni_zahtev_id"`
	BrojRacuna            string     `gorm:"column:broj_racuna;not null"`
	VlasnikID             int64      `gorm:"column:vlasnik_id;not null"`
	VrstaKredita          string     `gorm:"column:vrsta_kredita;not null"`
	TipKamate             string     `gorm:"column:tip_kamate;not null"`
	IznosKredita          float64    `gorm:"column:iznos_kredita;not null"`
	PeriodOtplate         int32      `gorm:"column:period_otplate;not null"`
	NominalnaKamatnaStopa float64    `gorm:"column:nominalna_kamatna_stopa;not null"`
	EfektivnaKamatnaStopa float64    `gorm:"column:efektivna_kamatna_stopa;not null"`
	DatumUgovaranja       time.Time  `gorm:"column:datum_ugovaranja;not null"`
	DatumIsplate          *time.Time `gorm:"column:datum_isplate"`
	IznosMesecneRate      float64    `gorm:"column:iznos_mesecne_rate;not null"`
	DatumSledeceRate      *time.Time `gorm:"column:datum_sledece_rate"`
	PreostaloDugovanje    float64    `gorm:"column:preostalo_dugovanje;not null"`
	Valuta                string     `gorm:"column:valuta;not null"`
	Status                string     `gorm:"column:status;not null;default:ODOBREN"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null;autoCreateTime"`
}

func (kreditModel) TableName() string {
	return "core_banking.kredit"
}

func (m kreditModel) toDomain() domain.Kredit {
	return domain.Kredit{
		ID:                    m.ID,
		BrojKredita:           m.BrojKredita,
		KreditniZahtevID:      m.KreditniZahtevID,
		BrojRacuna:            m.BrojRacuna,
		VlasnikID:             m.VlasnikID,
		VrstaKredita:          m.VrstaKredita,
		TipKamate:             m.TipKamate,
		IznosKredita:          m.IznosKredita,
		PeriodOtplate:         m.PeriodOtplate,
		NominalnaKamatnaStopa: m.NominalnaKamatnaStopa,
		EfektivnaKamatnaStopa: m.EfektivnaKamatnaStopa,
		DatumUgovaranja:       m.DatumUgovaranja,
		DatumIsplate:          m.DatumIsplate,
		IznosMesecneRate:      m.IznosMesecneRate,
		DatumSledeceRate:      m.DatumSledeceRate,
		PreostaloDugovanje:    m.PreostaloDugovanje,
		Valuta:                m.Valuta,
		Status:                m.Status,
		CreatedAt:             m.CreatedAt,
	}
}

// rataModel je GORM projekcija tabele rata.
type rataModel struct {
	ID                    int64      `gorm:"column:id;primaryKey"`
	KreditID              int64      `gorm:"column:kredit_id;not null"`
	IznosRate             float64    `gorm:"column:iznos_rate;not null"`
	IznosKamate           float64    `gorm:"column:iznos_kamate;not null"`
	Valuta                string     `gorm:"column:valuta;not null"`
	OcekivaniDatumDospeca time.Time  `gorm:"column:ocekivani_datum_dospeca;not null"`
	PraviDatumDospeca     *time.Time `gorm:"column:pravi_datum_dospeca"`
	StatusPlacanja        string     `gorm:"column:status_placanja;not null;default:NEPLACENO"`
	BrojPokusaja          int32      `gorm:"column:broj_pokusaja;not null;default:0"`
	SledecaPokusaj        *time.Time `gorm:"column:sledeci_pokusaj"`
}

func (rataModel) TableName() string {
	return "core_banking.rata"
}

func (m rataModel) toDomain() domain.Rata {
	return domain.Rata{
		ID:                    m.ID,
		KreditID:              m.KreditID,
		IznosRate:             m.IznosRate,
		IznosKamate:           m.IznosKamate,
		Valuta:                m.Valuta,
		OcekivaniDatumDospeca: m.OcekivaniDatumDospeca,
		PraviDatumDospeca:     m.PraviDatumDospeca,
		StatusPlacanja:        m.StatusPlacanja,
		BrojPokusaja:          m.BrojPokusaja,
		SledecaPokusaj:        m.SledecaPokusaj,
	}
}

// transakcijaModel je minimalna GORM projekcija za kreiranje transakcija u
// okviru kreditnih operacija (depo pri odobravanju; isplata pri naplati rate).
type transakcijaInsertModel struct {
	RacunID          int64     `gorm:"column:racun_id"`
	TipTransakcije   string    `gorm:"column:tip_transakcije"`
	Iznos            float64   `gorm:"column:iznos"`
	Opis             string    `gorm:"column:opis"`
	VremeIzvrsavanja time.Time `gorm:"column:vreme_izvrsavanja"`
	Status           string    `gorm:"column:status;default:IZVRSEN"`
}

func (transakcijaInsertModel) TableName() string {
	return "core_banking.transakcija"
}

// dueInstallmentRow je projekcija JOIN-a rata × kredit za cron job.
type dueInstallmentRow struct {
	RataID                int64      `gorm:"column:rata_id"`
	KreditID              int64      `gorm:"column:kredit_id"`
	IznosRate             float64    `gorm:"column:iznos_rate"`
	Valuta                string     `gorm:"column:valuta"`
	OcekivaniDatumDospeca time.Time  `gorm:"column:ocekivani_datum_dospeca"`
	BrojPokusaja          int32      `gorm:"column:broj_pokusaja"`
	SledecaPokusaj        *time.Time `gorm:"column:sledeci_pokusaj"`
	BrojRacuna            string     `gorm:"column:broj_racuna"`
	VlasnikID             int64      `gorm:"column:vlasnik_id"`
}

func (r dueInstallmentRow) toDomain() domain.DueInstallment {
	return domain.DueInstallment{
		RataID:                r.RataID,
		KreditID:              r.KreditID,
		IznosRate:             r.IznosRate,
		Valuta:                r.Valuta,
		OcekivaniDatumDospeca: r.OcekivaniDatumDospeca,
		BrojPokusaja:          r.BrojPokusaja,
		SledecaPokusaj:        r.SledecaPokusaj,
		BrojRacuna:            r.BrojRacuna,
		VlasnikID:             r.VlasnikID,
	}
}

// =============================================================================
// Repozitorijum
// =============================================================================

type kreditRepository struct {
	db *gorm.DB
}

// NewKreditRepository konstruktor — prihvata isti *gorm.DB koji koriste ostali repozitorijumi.
func NewKreditRepository(db *gorm.DB) domain.KreditRepository {
	return &kreditRepository{db: db}
}

// =============================================================================
// Zahtevi
// =============================================================================

func (r *kreditRepository) CreateKreditniZahtev(
	ctx context.Context,
	input domain.CreateKreditniZahtevInput,
) (*domain.KreditniZahtev, error) {
	m := kreditniZahtevModel{
		VlasnikID:         input.VlasnikID,
		VrstaKredita:      input.VrstaKredita,
		TipKamate:         input.TipKamate,
		IznosKredita:      input.IznosKredita,
		Valuta:            input.Valuta,
		SvrhaKredita:      input.SvrhaKredita,
		IznosMesecnePlate: input.IznosMesecnePlate,
		StatusZaposlenja:  input.StatusZaposlenja,
		PeriodZaposlenja:  input.PeriodZaposlenja,
		KontaktTelefon:    input.KontaktTelefon,
		BrojRacuna:        input.BrojRacuna,
		RokOtplate:        input.RokOtplate,
		Status:            "NA_CEKANJU",
		DatumPodnosenja:   time.Now().UTC(),
	}

	if err := r.db.WithContext(ctx).Create(&m).Error; err != nil {
		return nil, fmt.Errorf("kreiranje zahteva za kredit: %w", err)
	}

	result := m.toDomain()
	return &result, nil
}

func (r *kreditRepository) GetKreditniZahtevByID(
	ctx context.Context,
	id int64,
) (*domain.KreditniZahtev, error) {
	var m kreditniZahtevModel
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrKreditniZahtevNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dohvat zahteva za kredit: %w", err)
	}
	result := m.toDomain()
	return &result, nil
}

func (r *kreditRepository) GetPendingRequests(
	ctx context.Context,
	filter domain.GetPendingRequestsFilter,
) ([]domain.KreditniZahtev, error) {
	query := `
		SELECT *
		FROM core_banking.kreditni_zahtev
		WHERE status = 'NA_CEKANJU'
		  AND ($1 = '' OR vrsta_kredita = $1)
		  AND ($2 = '' OR broj_racuna   = $2)
		ORDER BY datum_podnosenja ASC
	`
	var rows []kreditniZahtevModel
	err := r.db.WithContext(ctx).
		Raw(query, filter.VrstaKredita, filter.BrojRacuna).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dohvat zahteva na čekanju: %w", err)
	}

	result := make([]domain.KreditniZahtev, len(rows))
	for i, row := range rows {
		result[i] = row.toDomain()
	}
	return result, nil
}

// RejectKreditRequest atomski:
//  1. Dohvata i zaključava zahtev (SELECT FOR UPDATE).
//  2. Proverava da je status NA_CEKANJU.
//  3. Menja status zahteva u ODBIJEN.
//  4. Upisuje ledger zapis u kredit tabelu sa status = 'ODBIJEN' i
//     nultim finansijskim veličinama, kako bi odbijeni zahtevi bili
//     vidljivi u zaposlenom portalu ("Svi krediti").
func (r *kreditRepository) RejectKreditRequest(ctx context.Context, zahtevID int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// ── 1: dohvati i zaključaj zahtev ───────────────────────────────────
		var zahtev kreditniZahtevModel
		err := tx.WithContext(ctx).
			Raw(`SELECT * FROM core_banking.kreditni_zahtev WHERE id = ? FOR UPDATE`, zahtevID).
			Scan(&zahtev).Error
		if err != nil {
			return fmt.Errorf("dohvat zahteva za odbijanje: %w", err)
		}
		if zahtev.ID == 0 {
			return domain.ErrKreditniZahtevNotFound
		}
		if zahtev.Status != "NA_CEKANJU" {
			return domain.ErrZahtevVecObrađen
		}

		// ── 2: odbij zahtev ───────────────────────────────────────────────────
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.kreditni_zahtev SET status = 'ODBIJEN' WHERE id = ?`, zahtevID).
			Error; err != nil {
			return fmt.Errorf("odbijanje zahteva: %w", err)
		}

		// ── 3: ledger zapis u kredit sa status = 'ODBIJEN' ───────────────────
		// Finansijske veličine su 0 jer kredit nije odobren.
		// iznos_kredita i period_otplate se kopiraju iz zahteva (moraju biti > 0
		// zbog CHECK ograničenja u bazi).
		today := time.Now().UTC().Truncate(24 * time.Hour)
		zahtevRef := zahtev.ID
		rejected := kreditModel{
			BrojKredita:           fmt.Sprintf("REJ-%d", zahtevID),
			KreditniZahtevID:      &zahtevRef,
			BrojRacuna:            zahtev.BrojRacuna,
			VlasnikID:             zahtev.VlasnikID,
			VrstaKredita:          zahtev.VrstaKredita,
			TipKamate:             zahtev.TipKamate,
			IznosKredita:          zahtev.IznosKredita,
			PeriodOtplate:         zahtev.RokOtplate,
			NominalnaKamatnaStopa: 0,
			EfektivnaKamatnaStopa: 0,
			DatumUgovaranja:       today,
			IznosMesecneRate:      0,
			PreostaloDugovanje:    0,
			Valuta:                zahtev.Valuta,
			Status:                "ODBIJEN",
		}
		if err := tx.WithContext(ctx).Create(&rejected).Error; err != nil {
			return fmt.Errorf("kreiranje ODBIJEN kredit zapisa: %w", err)
		}

		return nil
	})
}

// =============================================================================
// Krediti
// =============================================================================

// ApproveKreditRequest atomski:
//  1. Proverava da li zahtev postoji i ima status NA_CEKANJU.
//  2. Menja status zahteva u ODOBREN.
//  3. Kreira kredit zapis.
//  4. Bulk-inserts sve rate (amortizaciona tablica).
//  5. Kredituje račun klijenta (stanje_racuna += iznos_kredita).
//  6. Beleži UPLATA transakciju.
//  7. Postavlja datum_isplate na kredit.
func (r *kreditRepository) ApproveKreditRequest(
	ctx context.Context,
	input domain.ApproveKreditInput,
) (*domain.Kredit, error) {
	var created kreditModel

	err := r.db.Transaction(func(tx *gorm.DB) error {
		// ── 1 + 2: zahtev mora biti NA_CEKANJU ──────────────────────────────
		res := tx.WithContext(ctx).
			Model(&kreditniZahtevModel{}).
			Where("id = ? AND status = 'NA_CEKANJU'", input.ZahtevID).
			Update("status", "ODOBREN")
		if res.Error != nil {
			return fmt.Errorf("ažuriranje statusa zahteva: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			var count int64
			tx.WithContext(ctx).
				Model(&kreditniZahtevModel{}).
				Where("id = ?", input.ZahtevID).
				Count(&count)
			if count == 0 {
				return domain.ErrKreditniZahtevNotFound
			}
			return domain.ErrZahtevVecObrađen
		}

		// ── 3: kredit zapis ──────────────────────────────────────────────────
		today := time.Now().UTC().Truncate(24 * time.Hour)
		zahtevID := input.ZahtevID
		nextRate := input.DatumSledeceRate

		created = kreditModel{
			BrojKredita:           input.BrojKredita,
			KreditniZahtevID:      &zahtevID,
			BrojRacuna:            input.BrojRacuna,
			VlasnikID:             input.VlasnikID,
			VrstaKredita:          input.VrstaKredita,
			TipKamate:             input.TipKamate,
			IznosKredita:          input.IznosKredita,
			PeriodOtplate:         input.PeriodOtplate,
			NominalnaKamatnaStopa: input.NominalnaKamatnaStopa,
			EfektivnaKamatnaStopa: input.EfektivnaKamatnaStopa,
			DatumUgovaranja:       input.DatumUgovaranja,
			DatumIsplate:          &today,
			IznosMesecneRate:      input.IznosMesecneRate,
			DatumSledeceRate:      &nextRate,
			PreostaloDugovanje:    input.PreostaloDugovanje,
			Valuta:                input.Valuta,
			Status:                "ODOBREN",
		}
		if err := tx.WithContext(ctx).Create(&created).Error; err != nil {
			return fmt.Errorf("kreiranje kredita: %w", err)
		}

		// ── 4: amortizaciona tablica (bulk insert) ───────────────────────────
		rate := make([]rataModel, len(input.Rate))
		for i, ri := range input.Rate {
			rate[i] = rataModel{
				KreditID:              created.ID,
				IznosRate:             ri.IznosRate,
				IznosKamate:           ri.IznosKamate,
				Valuta:                input.Valuta,
				OcekivaniDatumDospeca: ri.OcekivaniDatumDospeca,
				StatusPlacanja:        "NEPLACENO",
				BrojPokusaja:          0,
			}
		}
		if err := tx.WithContext(ctx).Create(&rate).Error; err != nil {
			return fmt.Errorf("kreiranje rate: %w", err)
		}

		// ── 5: pronađi i zaključaj trezorski račun za datu valutu ───────────
		// trezorVlasnikID = 2 odgovara trezor@exbanka.rs (garantovano migracijama).
		// FOR UPDATE sprečava souběžne izmene istog trezorskog računa.
		var trezorRacunID int64
		err := tx.WithContext(ctx).
			Raw(`SELECT r.id
			     FROM core_banking.racun r
			     JOIN core_banking.valuta v ON v.id = r.id_valute
			     WHERE r.id_vlasnika = ?
			       AND v.oznaka      = ?
			       AND r.status      = 'AKTIVAN'
			     LIMIT 1
			     FOR UPDATE`,
				trezorVlasnikID, input.Valuta).
			Scan(&trezorRacunID).Error
		if err != nil || trezorRacunID == 0 {
			return fmt.Errorf("pronalaženje trezorskog računa za valutu %s: %w", input.Valuta, err)
		}

		// ── 6: pronađi i zaključaj račun klijenta ────────────────────────────
		var klijentRacunID int64
		err = tx.WithContext(ctx).
			Raw(`SELECT id FROM core_banking.racun WHERE broj_racuna = ? FOR UPDATE`,
				input.BrojRacuna).
			Scan(&klijentRacunID).Error
		if err != nil || klijentRacunID == 0 {
			return fmt.Errorf("pronalaženje računa klijenta %s: %w", input.BrojRacuna, err)
		}

		now := time.Now().UTC()

		// ── 7: teret trezorskog računa — isplata kredita ─────────────────────
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.racun
			         SET stanje_racuna = stanje_racuna - ?
			       WHERE id = ?`,
				input.IznosKredita, trezorRacunID).Error; err != nil {
			return fmt.Errorf("terećenje trezorskog računa pri isplati kredita: %w", err)
		}

		// ── 8: ISPLATA transakcija za trezorski račun ────────────────────────
		trezorTxIsplata := transakcijaInsertModel{
			RacunID:          trezorRacunID,
			TipTransakcije:   "ISPLATA",
			Iznos:            input.IznosKredita,
			Opis:             fmt.Sprintf("Isplata kredita %s klijentu", input.BrojKredita),
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}
		if err := tx.WithContext(ctx).Create(&trezorTxIsplata).Error; err != nil {
			return fmt.Errorf("kreiranje trezorske transakcije za isplatu kredita: %w", err)
		}

		// ── 9: kredituj račun klijenta ───────────────────────────────────────
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.racun
			         SET stanje_racuna = stanje_racuna + ?
			       WHERE id = ?`,
				input.IznosKredita, klijentRacunID).Error; err != nil {
			return fmt.Errorf("kreditovanje računa klijenta: %w", err)
		}

		// ── 10: UPLATA transakcija za račun klijenta ─────────────────────────
		klijentTxUplata := transakcijaInsertModel{
			RacunID:          klijentRacunID,
			TipTransakcije:   "UPLATA",
			Iznos:            input.IznosKredita,
			Opis:             fmt.Sprintf("Uplata po osnovu kredita %s", input.BrojKredita),
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}
		if err := tx.WithContext(ctx).Create(&klijentTxUplata).Error; err != nil {
			return fmt.Errorf("kreiranje transakcije klijenta za uplatu kredita: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	result := created.toDomain()
	return &result, nil
}

func (r *kreditRepository) GetKreditByID(
	ctx context.Context,
	id int64,
) (*domain.Kredit, error) {
	var m kreditModel
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrKreditNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("dohvat kredita: %w", err)
	}
	result := m.toDomain()
	return &result, nil
}

func (r *kreditRepository) GetKreditsByVlasnik(
	ctx context.Context,
	vlasnikID int64,
) ([]domain.Kredit, error) {
	var rows []kreditModel
	err := r.db.WithContext(ctx).
		Where("vlasnik_id = ?", vlasnikID).
		Order("iznos_kredita DESC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dohvat kredita klijenta: %w", err)
	}

	result := make([]domain.Kredit, len(rows))
	for i, row := range rows {
		result[i] = row.toDomain()
	}
	return result, nil
}

func (r *kreditRepository) GetAllCredits(
	ctx context.Context,
	filter domain.GetAllCreditsFilter,
) ([]domain.Kredit, error) {
	query := `
		SELECT *
		FROM core_banking.kredit
		WHERE ($1 = '' OR vrsta_kredita = $1)
		  AND ($2 = '' OR broj_racuna   = $2)
		  AND ($3 = '' OR status        = $3)
		ORDER BY broj_racuna ASC
	`
	var rows []kreditModel
	err := r.db.WithContext(ctx).
		Raw(query, filter.VrstaKredita, filter.BrojRacuna, filter.Status).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dohvat svih kredita: %w", err)
	}

	result := make([]domain.Kredit, len(rows))
	for i, row := range rows {
		result[i] = row.toDomain()
	}
	return result, nil
}

func (r *kreditRepository) UpdateKreditStatus(
	ctx context.Context,
	kreditID int64,
	status string,
) error {
	res := r.db.WithContext(ctx).
		Model(&kreditModel{}).
		Where("id = ?", kreditID).
		Update("status", status)
	if res.Error != nil {
		return fmt.Errorf("ažuriranje statusa kredita: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return domain.ErrKreditNotFound
	}
	return nil
}

// =============================================================================
// Rate
// =============================================================================

func (r *kreditRepository) GetInstallmentsByKredit(
	ctx context.Context,
	kreditID int64,
) ([]domain.Rata, error) {
	var rows []rataModel
	err := r.db.WithContext(ctx).
		Where("kredit_id = ?", kreditID).
		Order("ocekivani_datum_dospeca ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dohvat rata kredita: %w", err)
	}

	result := make([]domain.Rata, len(rows))
	for i, row := range rows {
		result[i] = row.toDomain()
	}
	return result, nil
}

// GetDueInstallments vraća neplaćene rate čiji je datum dospeća <= asOf
// i kojima još nije pokrenut retry (status = NEPLACENO, ne KASNI).
// JOIN-uje sa kredit da bi cron job dobio broj_racuna za naplatu.
func (r *kreditRepository) GetDueInstallments(
	ctx context.Context,
	asOf time.Time,
) ([]domain.DueInstallment, error) {
	query := `
		SELECT
		    r.id                       AS rata_id,
		    r.kredit_id,
		    r.iznos_rate,
		    r.valuta,
		    r.ocekivani_datum_dospeca,
		    r.broj_pokusaja,
		    r.sledeci_pokusaj,
		    k.broj_racuna,
		    k.vlasnik_id
		FROM core_banking.rata    r
		JOIN core_banking.kredit  k ON k.id = r.kredit_id
		WHERE r.status_placanja         = 'NEPLACENO'
		  AND r.ocekivani_datum_dospeca <= ?
		  AND k.status                  = 'ODOBREN'
		ORDER BY r.ocekivani_datum_dospeca ASC
	`
	var rows []dueInstallmentRow
	err := r.db.WithContext(ctx).Raw(query, asOf).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dohvat dospelih rata: %w", err)
	}

	result := make([]domain.DueInstallment, len(rows))
	for i, row := range rows {
		result[i] = row.toDomain()
	}
	return result, nil
}

// GetRetryInstallments vraća rate u statusu KASNI čiji je sledeci_pokusaj <= asOf.
func (r *kreditRepository) GetRetryInstallments(
	ctx context.Context,
	asOf time.Time,
) ([]domain.DueInstallment, error) {
	query := `
		SELECT
		    r.id                       AS rata_id,
		    r.kredit_id,
		    r.iznos_rate,
		    r.valuta,
		    r.ocekivani_datum_dospeca,
		    r.broj_pokusaja,
		    r.sledeci_pokusaj,
		    k.broj_racuna,
		    k.vlasnik_id
		FROM core_banking.rata    r
		JOIN core_banking.kredit  k ON k.id = r.kredit_id
		WHERE r.status_placanja = 'KASNI'
		  AND r.sledeci_pokusaj <= ?
		  AND k.status           = 'ODOBREN'
		ORDER BY r.sledeci_pokusaj ASC
	`
	var rows []dueInstallmentRow
	err := r.db.WithContext(ctx).Raw(query, asOf).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("dohvat rata za retry: %w", err)
	}

	result := make([]domain.DueInstallment, len(rows))
	for i, row := range rows {
		result[i] = row.toDomain()
	}
	return result, nil
}

// ProcessInstallmentPayment atomski naplaćuje ratu (Issue #5):
//  1. SELECT FOR UPDATE na ratu — sprečava dvostruku naplatu (idempotentnost).
//  2. Ako je rata već PLACENO, vraća ErrRataVecPlacena bez izmene.
//  3. Skida iznos_rate sa računa klijenta.
//  4. Beleži ISPLATA transakciju.
//  5. Označava ratu PLACENO sa praviDatumDospeca = NOW().
//  6. Traži sledeću neplaćenu ratu i ažurira kredit.datum_sledece_rate.
//  7. Smanjuje kredit.preostalo_dugovanje za iznos_rate.
//  8. Ako nema više neplaćenih rata, menja status kredita u OTPLACEN.
func (r *kreditRepository) ProcessInstallmentPayment(
	ctx context.Context,
	input domain.ProcessInstallmentInput,
) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// ── 1: lock rate reda ────────────────────────────────────────────────
		var rata rataModel
		err := tx.WithContext(ctx).
			Raw(`SELECT * FROM core_banking.rata WHERE id = ? FOR UPDATE`,
				input.RataID).
			Scan(&rata).Error
		if err != nil {
			return fmt.Errorf("lock rate: %w", err)
		}
		if rata.ID == 0 {
			return domain.ErrRataNotFound
		}

		// ── 2: idempotentnost ────────────────────────────────────────────────
		if rata.StatusPlacanja == "PLACENO" {
			return domain.ErrRataVecPlacena
		}

		// ── 3: pronađi i lock-uj račun ───────────────────────────────────────
		var racunID int64
		var stanje float64
		err = tx.WithContext(ctx).
			Raw(`SELECT id, stanje_racuna
			     FROM core_banking.racun
			     WHERE broj_racuna = ?
			     FOR UPDATE`,
				input.BrojRacuna).
			Row().Scan(&racunID, &stanje)
		if err != nil {
			return fmt.Errorf("dohvat računa za naplatu: %w", err)
		}

		if stanje < input.IznosRate {
			return domain.ErrInsufficientFunds
		}

		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.racun
			         SET stanje_racuna = stanje_racuna - ?
			       WHERE id = ?`,
				input.IznosRate, racunID).Error; err != nil {
			return fmt.Errorf("skidanje sredstava: %w", err)
		}

		// ── 4: ISPLATA transakcija ───────────────────────────────────────────
		now := time.Now().UTC()
		t := transakcijaInsertModel{
			RacunID:          racunID,
			TipTransakcije:   "ISPLATA",
			Iznos:            input.IznosRate,
			Opis:             fmt.Sprintf("Rata kredita (rata ID: %d)", input.RataID),
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}
		if err := tx.WithContext(ctx).Create(&t).Error; err != nil {
			return fmt.Errorf("kreiranje transakcije za ratu: %w", err)
		}

		// ── 4b: pronađi i zaključaj trezorski račun za datu valutu ──────────
		// trezorVlasnikID = 2 odgovara trezor@exbanka.rs (garantovano migracijama).
		var trezorRacunID int64
		err = tx.WithContext(ctx).
			Raw(`SELECT r.id
			     FROM core_banking.racun r
			     JOIN core_banking.valuta v ON v.id = r.id_valute
			     WHERE r.id_vlasnika = ?
			       AND v.oznaka      = ?
			       AND r.status      = 'AKTIVAN'
			     LIMIT 1
			     FOR UPDATE`,
				trezorVlasnikID, input.Valuta).
			Scan(&trezorRacunID).Error
		if err != nil || trezorRacunID == 0 {
			return fmt.Errorf("pronalaženje trezorskog računa za valutu %s: %w", input.Valuta, err)
		}

		// ── 4c: kredituj trezorski račun za iznos naplaćene rate ─────────────
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.racun
			         SET stanje_racuna = stanje_racuna + ?
			       WHERE id = ?`,
				input.IznosRate, trezorRacunID).Error; err != nil {
			return fmt.Errorf("kreditovanje trezorskog računa pri naplati rate: %w", err)
		}

		// ── 4d: UPLATA transakcija za trezorski račun ────────────────────────
		trezorTxUplata := transakcijaInsertModel{
			RacunID:          trezorRacunID,
			TipTransakcije:   "UPLATA",
			Iznos:            input.IznosRate,
			Opis:             fmt.Sprintf("Naplata rate kredita (rata ID: %d)", input.RataID),
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}
		if err := tx.WithContext(ctx).Create(&trezorTxUplata).Error; err != nil {
			return fmt.Errorf("kreiranje trezorske transakcije za naplatu rate: %w", err)
		}

		// ── 5: označi ratu PLACENO ───────────────────────────────────────────
		today := now.Truncate(24 * time.Hour)
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.rata
			         SET status_placanja      = 'PLACENO',
			             pravi_datum_dospeca  = ?,
			             broj_pokusaja        = broj_pokusaja + 1
			       WHERE id = ?`,
				today, input.RataID).Error; err != nil {
			return fmt.Errorf("ažuriranje statusa rate: %w", err)
		}

		// ── 6 + 7 + 8: ažuriranje kredita ───────────────────────────────────
		// Nađi sledeću neplaćenu ratu za ovaj kredit.
		var nextDue *time.Time
		var nextDueVal time.Time
		err = tx.WithContext(ctx).
			Raw(`SELECT ocekivani_datum_dospeca
			     FROM core_banking.rata
			     WHERE kredit_id      = ?
			       AND status_placanja = 'NEPLACENO'
			     ORDER BY ocekivani_datum_dospeca ASC
			     LIMIT 1`,
				input.KreditID).
			Scan(&nextDueVal).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("traženje sledeće rate: %w", err)
		}
		if !nextDueVal.IsZero() {
			nextDue = &nextDueVal
		}

		if nextDue != nil {
			// Još ima rata — pomeri datum i smanji preostalo dugovanje.
			if err := tx.WithContext(ctx).
				Exec(`UPDATE core_banking.kredit
				         SET datum_sledece_rate   = ?,
				             preostalo_dugovanje  = preostalo_dugovanje - ?
				       WHERE id = ?`,
					nextDue, input.IznosRate, input.KreditID).Error; err != nil {
				return fmt.Errorf("ažuriranje kredita posle naplate: %w", err)
			}
		} else {
			// Sve rate su plaćene — kredit je otplaćen.
			if err := tx.WithContext(ctx).
				Exec(`UPDATE core_banking.kredit
				         SET datum_sledece_rate   = NULL,
				             preostalo_dugovanje  = 0,
				             status               = 'OTPLACEN'
				       WHERE id = ?`,
					input.KreditID).Error; err != nil {
				return fmt.Errorf("zatvaranje kredita: %w", err)
			}
		}

		return nil
	})
}

func (r *kreditRepository) MarkInstallmentFailed(
	ctx context.Context,
	rataID int64,
	nextRetry time.Time,
) error {
	err := r.db.WithContext(ctx).
		Exec(`UPDATE core_banking.rata
		         SET status_placanja = 'KASNI',
		             broj_pokusaja   = broj_pokusaja + 1,
		             sledeci_pokusaj = ?
		       WHERE id = ?`,
			nextRetry, rataID).Error
	if err != nil {
		return fmt.Errorf("označavanje rate kao KASNI: %w", err)
	}
	return nil
}

// ApplyLatePaymentPenalty uvećava nominalnu kamatnu stopu kredita i ažurira
// iznos_mesecne_rate za sve preostale neplaćene rate na osnovu nove stope.
// Servisni sloj izračunava novu mesečnu ratu i prosleđuje je kao parametar
// da bi repozitorijum ostao bez poslovne logike izračunavanja.
func (r *kreditRepository) ApplyLatePaymentPenalty(
	ctx context.Context,
	kreditID int64,
	penaltyPercent float64,
) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Uvećaj nominalnu stopu.
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.kredit
			         SET nominalna_kamatna_stopa = nominalna_kamatna_stopa + ?
			       WHERE id = ?`,
				penaltyPercent, kreditID).Error; err != nil {
			return fmt.Errorf("primena kazne na kredit: %w", err)
		}

		// Postavi status na U_KASNJENJU ako već nije.
		if err := tx.WithContext(ctx).
			Exec(`UPDATE core_banking.kredit
			         SET status = 'U_KASNJENJU'
			       WHERE id = ? AND status = 'ODOBREN'`,
				kreditID).Error; err != nil {
			return fmt.Errorf("postavljanje statusa U_KASNJENJU: %w", err)
		}

		return nil
	})
}
