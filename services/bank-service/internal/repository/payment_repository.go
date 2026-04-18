package repository

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"banka-backend/services/bank-service/internal/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ─── GORM modeli ──────────────────────────────────────────────────────────────

type paymentRecipientModel struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"`
	VlasnikID  int64     `gorm:"column:vlasnik_id"`
	Naziv      string    `gorm:"column:naziv"`
	BrojRacuna string    `gorm:"column:broj_racuna"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (paymentRecipientModel) TableName() string { return "core_banking.payment_recipient" }

type paymentIntentModel struct {
	ID                 int64      `gorm:"column:id;primaryKey;autoIncrement"`
	IdempotencyKey     string     `gorm:"column:idempotency_key"`
	BrojNaloga         string     `gorm:"column:broj_naloga"`
	TipTransakcije     string     `gorm:"column:tip_transakcije"`
	RacunPlatioceID    int64      `gorm:"column:racun_platioca_id"`
	BrojRacunaPlatioca string     `gorm:"column:broj_racuna_platioca"`
	RacunPrimaocaID    *int64     `gorm:"column:racun_primaoca_id"`
	BrojRacunaPrimaoca string     `gorm:"column:broj_racuna_primaoca"`
	NazivPrimaoca      string     `gorm:"column:naziv_primaoca"`
	Iznos              float64    `gorm:"column:iznos"`
	KrajnjiIznos       *float64   `gorm:"column:krajnji_iznos"`
	Provizija          float64    `gorm:"column:provizija"`
	Kurs               float64    `gorm:"column:kurs"`
	ValutaPrimaoca     string     `gorm:"column:valuta_primaoca"`
	Valuta             string     `gorm:"column:valuta"`
	SifraPlacanja      string     `gorm:"column:sifra_placanja"`
	PozivNaBroj        string     `gorm:"column:poziv_na_broj"`
	SvrhaPlacanja      string     `gorm:"column:svrha_placanja"`
	Status             string     `gorm:"column:status"`
	PendingActionID    *int64     `gorm:"column:pending_action_id"`
	InitiatedByUserID  int64      `gorm:"column:initiated_by_user_id"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
	VerifiedAt         *time.Time `gorm:"column:verified_at"`
	ExecutedAt         *time.Time `gorm:"column:executed_at"`
	FailedReason       string     `gorm:"column:failed_reason"`
}

func (paymentIntentModel) TableName() string { return "core_banking.payment_intent" }

// racunForPayment projekcija računa potrebna za izvršenje plaćanja (uključujući limit polja).
type racunForPayment struct {
	ID                  int64   `gorm:"column:id"`
	BrojRacuna          string  `gorm:"column:broj_racuna"`
	IDVlasnika          int64   `gorm:"column:id_vlasnika"`
	IDValute            int64   `gorm:"column:id_valute"`
	ValutaOznaka        string  `gorm:"column:valuta_oznaka"`
	StanjeRacuna        float64 `gorm:"column:stanje_racuna"`
	RezervovanaSredstva float64 `gorm:"column:rezervisana_sredstva"`
	DnevniLimit         float64 `gorm:"column:dnevni_limit"`
	MesecniLimit        float64 `gorm:"column:mesecni_limit"`
	DnevnaPotrosnja     float64 `gorm:"column:dnevna_potrosnja"`
	MesecnaPotrosnja    float64 `gorm:"column:mesecna_potrosnja"`
	Status              string  `gorm:"column:status"`
}

// ─── PaymentRecipientRepository ──────────────────────────────────────────────

type paymentRecipientRepository struct {
	db *gorm.DB
}

func NewPaymentRecipientRepository(db *gorm.DB) domain.PaymentRecipientRepository {
	return &paymentRecipientRepository{db: db}
}

func (r *paymentRecipientRepository) Create(ctx context.Context, recipient *domain.PaymentRecipient) error {
	m := &paymentRecipientModel{
		VlasnikID:  recipient.VlasnikID,
		Naziv:      recipient.Naziv,
		BrojRacuna: recipient.BrojRacuna,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := r.db.WithContext(ctx).Create(m).Error; err != nil {
		return err
	}
	recipient.ID = m.ID
	recipient.CreatedAt = m.CreatedAt
	recipient.UpdatedAt = m.UpdatedAt
	return nil
}

func (r *paymentRecipientRepository) GetByID(ctx context.Context, id, vlasnikID int64) (*domain.PaymentRecipient, error) {
	var m paymentRecipientModel
	if err := r.db.WithContext(ctx).
		Where("id = ? AND vlasnik_id = ?", id, vlasnikID).
		First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrPaymentRecipientNotFound
		}
		return nil, err
	}
	return recipientToDomain(m), nil
}

func (r *paymentRecipientRepository) GetByOwner(ctx context.Context, vlasnikID int64) ([]domain.PaymentRecipient, error) {
	var models []paymentRecipientModel
	if err := r.db.WithContext(ctx).
		Where("vlasnik_id = ?", vlasnikID).
		Order("naziv ASC").
		Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]domain.PaymentRecipient, 0, len(models))
	for _, m := range models {
		result = append(result, *recipientToDomain(m))
	}
	return result, nil
}

func (r *paymentRecipientRepository) Update(ctx context.Context, recipient *domain.PaymentRecipient) error {
	result := r.db.WithContext(ctx).
		Model(&paymentRecipientModel{}).
		Where("id = ? AND vlasnik_id = ?", recipient.ID, recipient.VlasnikID).
		Updates(map[string]interface{}{
			"naziv":       recipient.Naziv,
			"broj_racuna": recipient.BrojRacuna,
			"updated_at":  time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrPaymentRecipientNotFound
	}
	return nil
}

func (r *paymentRecipientRepository) Delete(ctx context.Context, id, vlasnikID int64) error {
	result := r.db.WithContext(ctx).
		Where("id = ? AND vlasnik_id = ?", id, vlasnikID).
		Delete(&paymentRecipientModel{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return domain.ErrPaymentRecipientNotFound
	}
	return nil
}

func (r *paymentRecipientRepository) ExistsByOwnerAndAccount(ctx context.Context, vlasnikID int64, brojRacuna string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&paymentRecipientModel{}).
		Where("vlasnik_id = ? AND broj_racuna = ?", vlasnikID, brojRacuna).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func recipientToDomain(m paymentRecipientModel) *domain.PaymentRecipient {
	return &domain.PaymentRecipient{
		ID:         m.ID,
		VlasnikID:  m.VlasnikID,
		Naziv:      m.Naziv,
		BrojRacuna: m.BrojRacuna,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

// ─── PaymentRepository ────────────────────────────────────────────────────────

type paymentRepository struct {
	db *gorm.DB
}

func NewPaymentRepository(db *gorm.DB) domain.PaymentRepository {
	return &paymentRepository{db: db}
}

// CreateIntent kreira nalog plaćanja i pending_action za mobilnu verifikaciju.
// Idempotentno: isti idempotency_key vraća već kreiran intent.
func (r *paymentRepository) CreateIntent(ctx context.Context, input domain.CreatePaymentIntentInput) (*domain.PaymentIntent, int64, error) {
	// Proveri idempotentnost: ako isti ključ već postoji, vrati ga.
	var existing paymentIntentModel
	err := r.db.WithContext(ctx).
		Where("idempotency_key = ?", input.IdempotencyKey).
		First(&existing).Error
	if err == nil {
		// Već postoji — idempotentni odgovor.
		var actionID int64
		if existing.PendingActionID != nil {
			actionID = *existing.PendingActionID
		}
		return intentToDomain(existing), actionID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, err
	}

	// Dohvati podatke o računu platioca uz validaciju vlasništva.
	var payerAccount racunForPayment
	err = r.db.WithContext(ctx).Raw(`
		SELECT ra.id, ra.broj_racuna, ra.id_vlasnika, ra.id_valute, ra.stanje_racuna,
		       ra.rezervisana_sredstva, ra.dnevni_limit, ra.mesecni_limit,
		       ra.dnevna_potrosnja, ra.mesecna_potrosnja, ra.status,
		       v.oznaka AS valuta_oznaka
		FROM core_banking.racun ra
		JOIN core_banking.valuta v ON v.id = ra.id_valute
		WHERE ra.id = ? AND ra.id_vlasnika = ? AND ra.status = 'AKTIVAN'
		LIMIT 1
	`, input.RacunPlatioceID, input.InitiatedByUserID).Scan(&payerAccount).Error
	if err != nil {
		return nil, 0, err
	}
	if payerAccount.ID == 0 {
		return nil, 0, domain.ErrAccountNotOwned
	}

	// Validacija: primalački račun mora biti različit od platiokovog.
	if payerAccount.BrojRacuna == input.BrojRacunaPrimaoca {
		return nil, 0, domain.ErrSameAccount
	}

	// Pre-flight provera stanja i limita (UX guard — konačna provera je unutar VerifyAndExecute lock-a).
	raspolozivo := payerAccount.StanjeRacuna - payerAccount.RezervovanaSredstva
	if raspolozivo < input.Iznos {
		return nil, 0, domain.ErrInsufficientFunds
	}
	if payerAccount.DnevniLimit > 0 && payerAccount.DnevnaPotrosnja+input.Iznos > payerAccount.DnevniLimit {
		return nil, 0, domain.ErrDailyLimitExceeded
	}
	if payerAccount.MesecniLimit > 0 && payerAccount.MesecnaPotrosnja+input.Iznos > payerAccount.MesecniLimit {
		return nil, 0, domain.ErrMonthlyLimitExceeded
	}

	// Provjeri da li postoji primalački račun (interni), uključujući valutu.
	var recipientAccount racunForPayment
	err = r.db.WithContext(ctx).Raw(`
		SELECT ra.id, ra.broj_racuna, ra.status, v.oznaka AS valuta_oznaka
		FROM core_banking.racun ra
		JOIN core_banking.valuta v ON v.id = ra.id_valute
		WHERE ra.broj_racuna = ? AND ra.status = 'AKTIVAN'
		LIMIT 1
	`, input.BrojRacunaPrimaoca).Scan(&recipientAccount).Error
	if err != nil {
		return nil, 0, err
	}

	// Generiši broj naloga i pripremi intent.
	brojNaloga := generateBrojNaloga()
	now := time.Now().UTC()

	var recipientID *int64
	if recipientAccount.ID != 0 {
		id := recipientAccount.ID
		recipientID = &id
	}

	// Izračunaj krajnji iznos i proviziju za cross-currency interne prenose.
	var krajnjiIznosPreview *float64
	var intentProvizija float64
	var intentKurs float64
	var intentValutaPrimaoca string
	if recipientAccount.ID != 0 &&
		recipientAccount.ValutaOznaka != "" &&
		payerAccount.ValutaOznaka != recipientAccount.ValutaOznaka {
		netAmount, k, prov := convertPaymentAmountWithFee(payerAccount.ValutaOznaka, recipientAccount.ValutaOznaka, input.Iznos)
		krajnjiIznosPreview = &netAmount
		intentProvizija = prov
		intentKurs = k
		intentValutaPrimaoca = recipientAccount.ValutaOznaka
	}

	var actionID int64

	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Kreiraj pending_action za verifikaciju.
		purpose := fmt.Sprintf("Sa: %s | Na: %s | Svrha: %s", payerAccount.BrojRacuna, input.BrojRacunaPrimaoca, input.SvrhaPlacanja)
		action := &pendingActionModel{
			VlasnikID:  input.InitiatedByUserID,
			RacunID:    input.RacunPlatioceID,
			ActionType: "PLACANJE",
			ParamsJSON: fmt.Sprintf(`{"sifra_placanja": "%s"}`, input.SifraPlacanja),
			Opis:       purpose,
			Status:     "PENDING",
		}
		if err := tx.Create(action).Error; err != nil {
			return fmt.Errorf("kreiraj pending_action: %w", err)
		}
		actionID = action.ID

		// Kreiraj payment_intent.
		intent := &paymentIntentModel{
			IdempotencyKey:     input.IdempotencyKey,
			BrojNaloga:         brojNaloga,
			TipTransakcije:     "PLACANJE",
			RacunPlatioceID:    input.RacunPlatioceID,
			BrojRacunaPlatioca: payerAccount.BrojRacuna,
			RacunPrimaocaID:    recipientID,
			BrojRacunaPrimaoca: input.BrojRacunaPrimaoca,
			NazivPrimaoca:      input.NazivPrimaoca,
			Iznos:              input.Iznos,
			KrajnjiIznos:       krajnjiIznosPreview,
			Provizija:          intentProvizija,
			Kurs:               intentKurs,
			ValutaPrimaoca:     intentValutaPrimaoca,
			Valuta:             payerAccount.ValutaOznaka,
			SifraPlacanja:      input.SifraPlacanja,
			PozivNaBroj:        input.PozivNaBroj,
			SvrhaPlacanja:      input.SvrhaPlacanja,
			Status:             "U_OBRADI",
			PendingActionID:    &actionID,
			InitiatedByUserID:  input.InitiatedByUserID,
			CreatedAt:          now,
		}
		if err := tx.Create(intent).Error; err != nil {
			return fmt.Errorf("kreiraj payment_intent: %w", err)
		}

		return nil
	})

	if txErr != nil {
		return nil, 0, txErr
	}

	// Učitaj kreiran intent iz baze.
	var created paymentIntentModel
	if err := r.db.WithContext(ctx).Where("idempotency_key = ?", input.IdempotencyKey).First(&created).Error; err != nil {
		return nil, 0, err
	}
	return intentToDomain(created), actionID, nil
}

// CreateTransferIntent kreira nalog prenosa između računa istog korisnika.
func (r *paymentRepository) CreateTransferIntent(ctx context.Context, input domain.CreateTransferIntentInput) (*domain.PaymentIntent, int64, error) {
	// Idempotentnost.
	var existing paymentIntentModel
	err := r.db.WithContext(ctx).
		Where("idempotency_key = ?", input.IdempotencyKey).
		First(&existing).Error
	if err == nil {
		var actionID int64
		if existing.PendingActionID != nil {
			actionID = *existing.PendingActionID
		}
		return intentToDomain(existing), actionID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, 0, err
	}

	// Validacija: oba računa moraju biti isti vlasnik.
	if input.RacunPlatioceID == input.RacunPrimaocaID {
		return nil, 0, domain.ErrSameAccount
	}

	// Dohvati račun platioca.
	var payerAccount racunForPayment
	err = r.db.WithContext(ctx).Raw(`
		SELECT ra.id, ra.broj_racuna, ra.id_vlasnika, ra.id_valute, ra.stanje_racuna,
		       ra.rezervisana_sredstva, ra.dnevni_limit, ra.mesecni_limit,
		       ra.dnevna_potrosnja, ra.mesecna_potrosnja, ra.status,
		       v.oznaka AS valuta_oznaka
		FROM core_banking.racun ra
		JOIN core_banking.valuta v ON v.id = ra.id_valute
		WHERE ra.id = ? AND ra.id_vlasnika = ? AND ra.status = 'AKTIVAN'
		LIMIT 1
	`, input.RacunPlatioceID, input.InitiatedByUserID).Scan(&payerAccount).Error
	if err != nil {
		return nil, 0, err
	}
	if payerAccount.ID == 0 {
		return nil, 0, domain.ErrAccountNotOwned
	}

	// Dohvati račun primaoca — mora biti isti vlasnik (uključuje i valutu za proviziju).
	var recipientAccount racunForPayment
	err = r.db.WithContext(ctx).Raw(`
		SELECT ra.id, ra.broj_racuna, ra.id_vlasnika, ra.status, v.oznaka AS valuta_oznaka
		FROM core_banking.racun ra
		JOIN core_banking.valuta v ON v.id = ra.id_valute
		WHERE ra.id = ? AND ra.id_vlasnika = ? AND ra.status = 'AKTIVAN'
		LIMIT 1
	`, input.RacunPrimaocaID, input.InitiatedByUserID).Scan(&recipientAccount).Error
	if err != nil {
		return nil, 0, err
	}
	if recipientAccount.ID == 0 {
		return nil, 0, domain.ErrRecipientAccountInvalid
	}

	// Pre-flight provera stanja i limita (UX guard — konačna provera je unutar VerifyAndExecute lock-a).
	raspolozivo := payerAccount.StanjeRacuna - payerAccount.RezervovanaSredstva
	if raspolozivo < input.Iznos {
		return nil, 0, domain.ErrInsufficientFunds
	}
	if payerAccount.DnevniLimit > 0 && payerAccount.DnevnaPotrosnja+input.Iznos > payerAccount.DnevniLimit {
		return nil, 0, domain.ErrDailyLimitExceeded
	}
	if payerAccount.MesecniLimit > 0 && payerAccount.MesecnaPotrosnja+input.Iznos > payerAccount.MesecniLimit {
		return nil, 0, domain.ErrMonthlyLimitExceeded
	}

	brojNaloga := generateBrojNaloga()
	now := time.Now().UTC()
	recipientID := input.RacunPrimaocaID

	var actionID int64

	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		purpose := fmt.Sprintf("Sa: %s | Na: %s | Svrha: %s", payerAccount.BrojRacuna, recipientAccount.BrojRacuna, input.SvrhaPlacanja)
		action := &pendingActionModel{
			VlasnikID:  input.InitiatedByUserID,
			RacunID:    input.RacunPlatioceID,
			ActionType: "PRENOS",
			ParamsJSON: fmt.Sprintf(`{"racun_primaoca_id": %d}`, recipientID),
			Opis:       purpose,
			Status:     "PENDING",
		}
		if err := tx.Create(action).Error; err != nil {
			return fmt.Errorf("kreiraj pending_action: %w", err)
		}
		actionID = action.ID

		// Za konverziju valuta, krajnji_iznos i provizija se računaju automatski ako ConvertedIznos nije prosleđen.
		var krajnjiIznos *float64
		var transferProvizija float64
		var transferKurs float64
		var transferValutaPrimaoca string
		if input.ConvertedIznos > 0 && input.ConvertedIznos != input.Iznos {
			// Menjačnica je već izračunala neto iznos; izvuci proviziju.
			v := input.ConvertedIznos
			krajnjiIznos = &v
			// Izračunaj bruto konverziju da bi dobili proviziju i kurs.
			_, k, prov := convertPaymentAmountWithFee(payerAccount.ValutaOznaka, recipientAccount.ValutaOznaka, input.Iznos)
			transferProvizija = prov
			transferKurs = k
			transferValutaPrimaoca = recipientAccount.ValutaOznaka
		} else if recipientAccount.ValutaOznaka != "" && payerAccount.ValutaOznaka != recipientAccount.ValutaOznaka {
			// Direktan prenos između računa različitih valuta — izračunaj konverziju.
			netAmount, k, prov := convertPaymentAmountWithFee(payerAccount.ValutaOznaka, recipientAccount.ValutaOznaka, input.Iznos)
			krajnjiIznos = &netAmount
			transferProvizija = prov
			transferKurs = k
			transferValutaPrimaoca = recipientAccount.ValutaOznaka
		}

		intent := &paymentIntentModel{
			IdempotencyKey:     input.IdempotencyKey,
			BrojNaloga:         brojNaloga,
			TipTransakcije:     "PRENOS",
			RacunPlatioceID:    input.RacunPlatioceID,
			BrojRacunaPlatioca: payerAccount.BrojRacuna,
			RacunPrimaocaID:    &recipientID,
			BrojRacunaPrimaoca: recipientAccount.BrojRacuna,
			NazivPrimaoca:      "Interni prenos",
			Iznos:              input.Iznos,
			KrajnjiIznos:       krajnjiIznos,
			Provizija:          transferProvizija,
			Kurs:               transferKurs,
			ValutaPrimaoca:     transferValutaPrimaoca,
			Valuta:             payerAccount.ValutaOznaka,
			SvrhaPlacanja:      input.SvrhaPlacanja,
			Status:             "U_OBRADI",
			PendingActionID:    &actionID,
			InitiatedByUserID:  input.InitiatedByUserID,
			CreatedAt:          now,
		}
		if err := tx.Create(intent).Error; err != nil {
			return fmt.Errorf("kreiraj transfer_intent: %w", err)
		}

		return nil
	})

	if txErr != nil {
		return nil, 0, txErr
	}

	var created paymentIntentModel
	if err := r.db.WithContext(ctx).Where("idempotency_key = ?", input.IdempotencyKey).First(&created).Error; err != nil {
		return nil, 0, err
	}
	return intentToDomain(created), actionID, nil
}

// VerifyAndExecute proverava verifikacioni kod i atomski izvršava plaćanje.
// Koristi SELECT FOR UPDATE u determinističkom redosledu da spreči deadlock.
func (r *paymentRepository) VerifyAndExecute(ctx context.Context, input domain.VerifyPaymentInput) (*domain.PaymentIntent, error) {
	var finalIntent *domain.PaymentIntent
	// failureErr čuva grešku koja treba biti vraćena korisniku, ali čiji
	// prateći DB update mora biti COMMIT-ovan (ne rollback-ovan).
	var failureErr error

	txErr := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. Lock intent reda za čitanje (FOR UPDATE).
		var intent paymentIntentModel
		if err := tx.Raw(
			"SELECT * FROM core_banking.payment_intent WHERE id = ? FOR UPDATE",
			input.IntentID,
		).Scan(&intent).Error; err != nil {
			return err
		}
		if intent.ID == 0 {
			return domain.ErrPaymentIntentNotFound
		}

		// Proveri vlasništvo.
		if intent.InitiatedByUserID != input.UserID {
			return domain.ErrPaymentIntentNotFound
		}

		// 2. Idempotentnost: ako je već realizovano, vrati uspeh bez ponovnog izvršenja.
		if intent.Status == "REALIZOVANO" {
			finalIntent = intentToDomain(intent)
			return nil
		}
		if intent.Status == "ODBIJENO" {
			return domain.ErrPaymentAlreadyFailed
		}

		// 3. Nađi i lock pending_action.
		if intent.PendingActionID == nil {
			return domain.ErrPaymentIntentNotFound
		}
		var action pendingActionModel
		if err := tx.Raw(
			"SELECT * FROM core_banking.pending_action WHERE id = ? AND vlasnik_id = ? FOR UPDATE",
			*intent.PendingActionID, input.UserID,
		).Scan(&action).Error; err != nil {
			return err
		}
		if action.ID == 0 {
			return domain.ErrPendingNotFound
		}

		// 4. Proveri status pending_action.
		if action.Status == "CANCELLED" {
			return domain.ErrTooManyAttempts
		}
		if action.Status != "APPROVED" {
			return domain.ErrPendingNotFound
		}

		// 5. Proveri istek koda.
		if action.CodeExpiresAt == nil || time.Now().UTC().After(*action.CodeExpiresAt) {
			_ = tx.Model(&pendingActionModel{}).Where("id = ?", action.ID).
				Update("status", "CANCELLED").Error
			_ = tx.Model(&paymentIntentModel{}).Where("id = ?", intent.ID).
				Updates(map[string]interface{}{
					"status":        "ODBIJENO",
					"failed_reason": "Verifikacioni kod je istekao",
				}).Error
			// Postavljamo failureErr i vraćamo nil da bi GORM commit-ovao update-e.
			failureErr = domain.ErrCodeExpired
			return nil
		}

		// 6. Proveri kod.
		if action.VerificationCode == nil || *action.VerificationCode != input.Code {
			newAttempts := action.Attempts + 1
			update := map[string]interface{}{"attempts": newAttempts}
			if newAttempts >= 3 {
				update["status"] = "CANCELLED"
				_ = tx.Model(&paymentIntentModel{}).Where("id = ?", intent.ID).
					Updates(map[string]interface{}{
						"status":        "ODBIJENO",
						"failed_reason": "Previše neuspešnih pokušaja",
					}).Error
				failureErr = domain.ErrTooManyAttempts
			} else {
				failureErr = domain.ErrWrongCode
			}
			_ = tx.Model(&pendingActionModel{}).Where("id = ?", action.ID).Updates(update).Error
			// Vraćamo nil da bi GORM commit-ovao update-e pre nego što podignemo grešku.
			return nil
		}

		// 7. Odredi da li je cross-currency (različite valute) interni prenos.
		// Za cross-currency: novac prolazi preko trezorskih računa banke.
		isCrossCurrency := intent.RacunPrimaocaID != nil &&
			intent.KrajnjiIznos != nil &&
			*intent.KrajnjiIznos > 0 &&
			*intent.KrajnjiIznos != intent.Iznos

		// Za cross-currency: pronađi trezorske račune banke (pre lockovanja).
		var bankPayerTreasuryID, bankRecipientTreasuryID int64
		if isCrossCurrency {
			type bankTreasuryRow struct {
				ID           int64  `gorm:"column:id"`
				ValutaOznaka string `gorm:"column:valuta_oznaka"`
			}
			// Dohvati valutu računa primaoca.
			var recCurrency struct {
				ValutaOznaka string `gorm:"column:valuta_oznaka"`
			}
			if err := tx.Raw(`
				SELECT v.oznaka AS valuta_oznaka
				FROM core_banking.racun ra
				JOIN core_banking.valuta v ON v.id = ra.id_valute
				WHERE ra.id = ?
			`, *intent.RacunPrimaocaID).Scan(&recCurrency).Error; err != nil {
				return fmt.Errorf("lookup valute primaoca: %w", err)
			}
			// Pronađi trezorske račune banke za obe valute (vlasnik_id = 2 = trezor@exbanka.rs).
			var bankRows []bankTreasuryRow
			if err := tx.Raw(`
				SELECT ra.id, v.oznaka AS valuta_oznaka
				FROM core_banking.racun ra
				JOIN core_banking.valuta v ON v.id = ra.id_valute
				WHERE ra.id_vlasnika = 2 AND ra.status = 'AKTIVAN'
				  AND v.oznaka IN ?
			`, []string{intent.Valuta, recCurrency.ValutaOznaka}).Scan(&bankRows).Error; err != nil {
				return fmt.Errorf("lookup trezorskih računa: %w", err)
			}
			for _, br := range bankRows {
				if br.ValutaOznaka == intent.Valuta {
					bankPayerTreasuryID = br.ID
				}
				if br.ValutaOznaka == recCurrency.ValutaOznaka {
					bankRecipientTreasuryID = br.ID
				}
			}
			if bankPayerTreasuryID == 0 || bankRecipientTreasuryID == 0 {
				return fmt.Errorf("trezorski računi banke za valute %s/%s nisu pronađeni — primenite migraciju 000010", intent.Valuta, recCurrency.ValutaOznaka)
			}
		}

		// 8. Dohvati i zaključaj račune u determinističkom redosledu (sprečava deadlock).
		// Za cross-currency uključujemo i oba trezorska računa banke.
		accountIDs := []int64{intent.RacunPlatioceID}
		if intent.RacunPrimaocaID != nil {
			accountIDs = append(accountIDs, *intent.RacunPrimaocaID)
		}
		if bankPayerTreasuryID != 0 {
			accountIDs = append(accountIDs, bankPayerTreasuryID)
		}
		if bankRecipientTreasuryID != 0 && bankRecipientTreasuryID != bankPayerTreasuryID {
			accountIDs = append(accountIDs, bankRecipientTreasuryID)
		}
		sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })

		var accounts []racunForPayment
		if err := tx.Raw(`
			SELECT ra.id, ra.broj_racuna, ra.id_vlasnika, ra.id_valute,
			       ra.stanje_racuna, ra.rezervisana_sredstva,
			       COALESCE(ra.dnevni_limit, 0) AS dnevni_limit,
			       COALESCE(ra.mesecni_limit, 0) AS mesecni_limit,
			       COALESCE(ra.dnevna_potrosnja, 0) AS dnevna_potrosnja,
			       COALESCE(ra.mesecna_potrosnja, 0) AS mesecna_potrosnja,
			       ra.status, v.oznaka AS valuta_oznaka
			FROM core_banking.racun ra
			JOIN core_banking.valuta v ON v.id = ra.id_valute
			WHERE ra.id IN ? ORDER BY ra.id FOR UPDATE
		`, accountIDs).Scan(&accounts).Error; err != nil {
			return err
		}

		payerIdx := -1
		recipientIdx := -1
		for i, a := range accounts {
			if a.ID == intent.RacunPlatioceID {
				payerIdx = i
			}
			if intent.RacunPrimaocaID != nil && a.ID == *intent.RacunPrimaocaID {
				recipientIdx = i
			}
		}
		if payerIdx == -1 {
			return domain.ErrAccountNotOwned
		}

		payer := accounts[payerIdx]

		// 9. Validacija stanja i limita unutar iste transakcije (nakon lockovanja).
		if payer.Status != "AKTIVAN" {
			return domain.ErrAccountNotOwned
		}
		raspolozivo := payer.StanjeRacuna - payer.RezervovanaSredstva
		if raspolozivo < intent.Iznos {
			return domain.ErrInsufficientFunds
		}
		if payer.DnevniLimit > 0 && payer.DnevnaPotrosnja+intent.Iznos > payer.DnevniLimit {
			return domain.ErrDailyLimitExceeded
		}
		if payer.MesecniLimit > 0 && payer.MesecnaPotrosnja+intent.Iznos > payer.MesecniLimit {
			return domain.ErrMonthlyLimitExceeded
		}

		now := time.Now().UTC()

		// 10. Skini novac sa računa platioca + ažuriraj potrošnju.
		if err := tx.Exec(`
			UPDATE core_banking.racun
			SET stanje_racuna     = stanje_racuna - ?,
			    dnevna_potrosnja  = dnevna_potrosnja + ?,
			    mesecna_potrosnja = mesecna_potrosnja + ?
			WHERE id = ? AND status = 'AKTIVAN'
		`, intent.Iznos, intent.Iznos, intent.Iznos, intent.RacunPlatioceID).Error; err != nil {
			return fmt.Errorf("debit platioca: %w", err)
		}

		// 11. Upiši transakciju ISPLATA za platioca.
		txIzlaz := &transakcijaModel{
			RacunID:          intent.RacunPlatioceID,
			TipTransakcije:   "ISPLATA",
			Iznos:            intent.Iznos,
			Opis:             fmt.Sprintf("Plaćanje %s — %s", intent.BrojNaloga, intent.SvrhaPlacanja),
			VremeIzvrsavanja: now,
			Status:           "IZVRSEN",
		}
		if err := tx.Create(txIzlaz).Error; err != nil {
			return fmt.Errorf("insert transakcija isplata: %w", err)
		}

		// 12. Uplati na račun primaoca (za interna plaćanja).
		// Za cross-currency: novac prolazi preko trezorskih računa banke.
		// Za iste valute: direktan prenos.
		creditAmount := intent.Iznos
		if intent.KrajnjiIznos != nil && *intent.KrajnjiIznos > 0 && *intent.KrajnjiIznos != intent.Iznos {
			creditAmount = *intent.KrajnjiIznos
		}

		if isCrossCurrency {
			// 12a. Banka prima valutu platioca na svoj trezorski račun + transakcija UPLATA.
			if err := tx.Exec(`
				UPDATE core_banking.racun
				SET stanje_racuna = stanje_racuna + ?
				WHERE id = ? AND status = 'AKTIVAN'
			`, intent.Iznos, bankPayerTreasuryID).Error; err != nil {
				return fmt.Errorf("credit banka trezor (platioc valuta): %w", err)
			}
			if err := tx.Create(&transakcijaModel{
				RacunID:          bankPayerTreasuryID,
				TipTransakcije:   "UPLATA",
				Iznos:            intent.Iznos,
				Opis:             fmt.Sprintf("Konverzija %s — uplata od klijenta", intent.BrojNaloga),
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			}).Error; err != nil {
				return fmt.Errorf("insert transakcija trezor uplata: %w", err)
			}

			// 12b. Banka šalje valutu primaoca sa svog trezorskog računa + transakcija ISPLATA.
			if err := tx.Exec(`
				UPDATE core_banking.racun
				SET stanje_racuna = stanje_racuna - ?
				WHERE id = ? AND status = 'AKTIVAN'
			`, creditAmount, bankRecipientTreasuryID).Error; err != nil {
				return fmt.Errorf("debit banka trezor (primalac valuta): %w", err)
			}
			if err := tx.Create(&transakcijaModel{
				RacunID:          bankRecipientTreasuryID,
				TipTransakcije:   "ISPLATA",
				Iznos:            creditAmount,
				Opis:             fmt.Sprintf("Konverzija %s — isplata klijentu", intent.BrojNaloga),
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			}).Error; err != nil {
				return fmt.Errorf("insert transakcija trezor isplata: %w", err)
			}
		}

		if intent.RacunPrimaocaID != nil && recipientIdx >= 0 {
			if err := tx.Exec(`
				UPDATE core_banking.racun
				SET stanje_racuna = stanje_racuna + ?
				WHERE id = ? AND status = 'AKTIVAN'
			`, creditAmount, *intent.RacunPrimaocaID).Error; err != nil {
				return fmt.Errorf("credit primaoca: %w", err)
			}

			txUlaz := &transakcijaModel{
				RacunID:          *intent.RacunPrimaocaID,
				TipTransakcije:   "UPLATA",
				Iznos:            creditAmount,
				Opis:             fmt.Sprintf("Prenos %s — %s", intent.BrojNaloga, intent.SvrhaPlacanja),
				VremeIzvrsavanja: now,
				Status:           "IZVRSEN",
			}
			if err := tx.Create(txUlaz).Error; err != nil {
				return fmt.Errorf("insert transakcija uplata: %w", err)
			}
		}

		// 12. Ažuriraj payment_intent na REALIZOVANO.
		krajnjiIznos := creditAmount
		if err := tx.Model(&paymentIntentModel{}).
			Where("id = ?", intent.ID).
			Updates(map[string]interface{}{
				"status":        "REALIZOVANO",
				"krajnji_iznos": krajnjiIznos,
				"verified_at":   now,
				"executed_at":   now,
			}).Error; err != nil {
			return fmt.Errorf("ažuriraj intent: %w", err)
		}

		// 13. Zatvori pending_action.
		if err := tx.Model(&pendingActionModel{}).
			Where("id = ?", action.ID).
			Update("status", "CANCELLED").Error; err != nil {
			return fmt.Errorf("zatvori pending_action: %w", err)
		}

		// Ažuriraj intent za povrat.
		intent.Status = "REALIZOVANO"
		intent.KrajnjiIznos = &krajnjiIznos
		intent.ExecutedAt = &now
		intent.VerifiedAt = &now
		finalIntent = intentToDomain(intent)
		return nil
	})

	if txErr != nil {
		return nil, txErr
	}
	if failureErr != nil {
		return nil, failureErr
	}
	return finalIntent, nil
}

// GetByID vraća jedan nalog; greška ako ne pripada korisniku.
func (r *paymentRepository) GetByID(ctx context.Context, id, userID int64) (*domain.PaymentIntent, error) {
	var m paymentIntentModel
	if err := r.db.WithContext(ctx).
		Where("id = ? AND initiated_by_user_id = ?", id, userID).
		First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, domain.ErrPaymentIntentNotFound
		}
		return nil, err
	}
	return intentToDomain(m), nil
}

// paymentHistoryRow za JOIN upit istorije.
type paymentHistoryRow struct {
	paymentIntentModel
}

// GetHistory vraća istoriju naloga sa filterima.
func (r *paymentRepository) GetHistory(ctx context.Context, userID int64, filter domain.PaymentHistoryFilter) ([]domain.PaymentIntent, error) {
	query := r.db.WithContext(ctx).
		Model(&paymentIntentModel{}).
		Where("initiated_by_user_id = ?", userID)

	if filter.DateFrom != nil {
		query = query.Where("created_at >= ?", *filter.DateFrom)
	}
	if filter.DateTo != nil {
		query = query.Where("created_at <= ?", *filter.DateTo)
	}
	if filter.MinIznos != nil {
		query = query.Where("iznos >= ?", *filter.MinIznos)
	}
	if filter.MaxIznos != nil {
		query = query.Where("iznos <= ?", *filter.MaxIznos)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}

	var models []paymentIntentModel
	if err := query.
		Order("created_at DESC").
		Omit(clause.Associations).
		Find(&models).Error; err != nil {
		return nil, err
	}

	result := make([]domain.PaymentIntent, 0, len(models))
	for _, m := range models {
		result = append(result, *intentToDomain(m))
	}
	return result, nil
}

// ─── Pomoćne funkcije ─────────────────────────────────────────────────────────

func intentToDomain(m paymentIntentModel) *domain.PaymentIntent {
	return &domain.PaymentIntent{
		ID:                 m.ID,
		IdempotencyKey:     m.IdempotencyKey,
		BrojNaloga:         m.BrojNaloga,
		TipTransakcije:     m.TipTransakcije,
		RacunPlatioceID:    m.RacunPlatioceID,
		BrojRacunaPlatioca: m.BrojRacunaPlatioca,
		RacunPrimaocaID:    m.RacunPrimaocaID,
		BrojRacunaPrimaoca: m.BrojRacunaPrimaoca,
		NazivPrimaoca:      m.NazivPrimaoca,
		Iznos:              m.Iznos,
		KrajnjiIznos:       m.KrajnjiIznos,
		Provizija:          m.Provizija,
		Kurs:               m.Kurs,
		ValutaPrimaoca:     m.ValutaPrimaoca,
		ValutaOznaka:       m.Valuta,
		SifraPlacanja:      m.SifraPlacanja,
		PozivNaBroj:        m.PozivNaBroj,
		SvrhaPlacanja:      m.SvrhaPlacanja,
		Status:             m.Status,
		PendingActionID:    m.PendingActionID,
		InitiatedByUserID:  m.InitiatedByUserID,
		CreatedAt:          m.CreatedAt,
		VerifiedAt:         m.VerifiedAt,
		ExecutedAt:         m.ExecutedAt,
		FailedReason:       m.FailedReason,
	}
}

// generateBrojNaloga generiše jedinstven interni broj naloga.
func generateBrojNaloga() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(999_999_999))
	return fmt.Sprintf("NAL%011d", n.Int64()+1)
}

// paymentFallbackRates su aproximativni srednji kursevi (RSD za 1 jedinicu strane valute).
// Koriste se za konverziju iznosa pri plaćanjima između računa različitih valuta.
var paymentFallbackRates = map[string]float64{
	"EUR": 117.00,
	"CHF": 126.75,
	"USD": 107.75,
	"GBP": 136.75,
	"JPY": 0.69,
	"CAD": 75.50,
	"AUD": 68.50,
}

// paymentProvizijaRate je stopa provizije za cross-currency plaćanja (0.5%).
const paymentProvizijaRate = 0.005

// paymentSpreadRate je polu-raspon za kupovni/prodajni kurs (0.5%).
const paymentSpreadRate = 0.005

// convertPaymentAmountWithFee konvertuje iznos uz spread i proviziju.
// Vraća (netIznos, kurs, provizija) u ciljnoj valuti.
// kurs je efektivni kurs: koliko jedinica toCurrency dobijamo za 1 jedinicu fromCurrency.
// Iste valute → (amount, 1, 0), nema provizije.
func convertPaymentAmountWithFee(fromCurrency, toCurrency string, amount float64) (net float64, kurs float64, provizija float64) {
	if fromCurrency == toCurrency {
		return amount, 1, 0
	}

	kupovni := func(code string) (float64, bool) {
		mid, ok := paymentFallbackRates[code]
		if !ok {
			return 0, false
		}
		return mid * (1 - paymentSpreadRate), true
	}
	prodajni := func(code string) (float64, bool) {
		mid, ok := paymentFallbackRates[code]
		if !ok {
			return 0, false
		}
		return mid * (1 + paymentSpreadRate), true
	}

	var bruto float64
	var effectiveKurs float64
	if fromCurrency == "RSD" {
		// RSD → strani: klijent daje RSD, banka prodaje stranu valutu po prodajnom kursu.
		toRate, ok := prodajni(toCurrency)
		if !ok || toRate == 0 {
			return amount, 1, 0
		}
		bruto = amount / toRate
		effectiveKurs = 1 / toRate
	} else if toCurrency == "RSD" {
		// strani → RSD: klijent prodaje stranu valutu, banka kupuje po kupovnom kursu.
		fromRate, ok := kupovni(fromCurrency)
		if !ok {
			return amount, 1, 0
		}
		bruto = amount * fromRate
		effectiveKurs = fromRate
	} else {
		// Kros-valutna: X → RSD (kupovni) → Y (prodajni)
		fromRate, fromOK := kupovni(fromCurrency)
		toRate, toOK := prodajni(toCurrency)
		if !fromOK || !toOK || toRate == 0 {
			return amount, 1, 0
		}
		bruto = amount * fromRate / toRate
		effectiveKurs = fromRate / toRate
	}

	p := bruto * paymentProvizijaRate
	if p < 0 {
		p = 0
	}
	net = bruto - p
	if net < 0 {
		net = 0
	}
	return net, effectiveKurs, p
}

// convertPaymentAmount konvertuje iznos iz fromCurrency u toCurrency koristeći srednje kurseve (bez provizije).
// Ostavljeno za kompatibilnost — za plaćanja koristiti convertPaymentAmountWithFee.
func convertPaymentAmount(fromCurrency, toCurrency string, amount float64) float64 {
	net, _, _ := convertPaymentAmountWithFee(fromCurrency, toCurrency, amount)
	return net
}
