package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	pb "banka-backend/proto/banka"
	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/trading"
	"banka-backend/services/bank-service/internal/transport"
	"banka-backend/services/bank-service/internal/worker"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// clientEmailLookup apstrahuje gRPC pozive ka user-service.
// Omogućava testabilnost BankHandler-a bez pravog gRPC klijenta.
type clientEmailLookup interface {
	// GetClientEmail dohvata email klijenta po ID-u — zahteva EMPLOYEE JWT.
	// Koristiti samo u gRPC handler-ima (BankHandler) koji imaju EMPLOYEE context.
	GetClientEmail(ctx context.Context, clientID int64) (string, error)
	// GetMyEmail dohvata email ulogovanog korisnika — radi sa bilo kojim JWT.
	// Koristiti u direktnim HTTP handler-ima gde je token CLIENT-ov.
	GetMyEmail(ctx context.Context) (string, error)
	// GetClientName dohvata ime i prezime klijenta po ID-u — zahteva EMPLOYEE JWT.
	GetClientName(ctx context.Context, clientID int64) (firstName, lastName string, err error)
	// GetClientInfo dohvata ime, prezime i email klijenta u jednom pozivu — zahteva EMPLOYEE JWT.
	GetClientInfo(ctx context.Context, clientID int64) (*transport.ClientInfo, error)
}

// BankHandler implementira pb.BankaServiceServer.
type BankHandler struct {
	pb.UnimplementedBankaServiceServer
	currencyService  domain.CurrencyService
	delatnostService domain.DelatnostService
	accountService   domain.AccountService
	paymentService   domain.PaymentService
	kreditService    domain.KreditService
	karticaService   domain.KarticaService
	berzaService     domain.BerzaService
	listingService   domain.ListingService
	exchangeService  domain.ExchangeService
	tradingService   trading.TradingService
	userClient       clientEmailLookup
	accountPublisher worker.AccountEmailPublisher
}

func NewBankHandler(
	currencyService domain.CurrencyService,
	delatnostService domain.DelatnostService,
	accountService domain.AccountService,
	paymentService domain.PaymentService,
	kreditService domain.KreditService,
	karticaService domain.KarticaService,
	berzaService domain.BerzaService,
	listingService domain.ListingService,
	exchangeService domain.ExchangeService,
	tradingService trading.TradingService,
	userClient clientEmailLookup,
	accountPublisher worker.AccountEmailPublisher,
) *BankHandler {
	return &BankHandler{
		currencyService:  currencyService,
		delatnostService: delatnostService,
		accountService:   accountService,
		paymentService:   paymentService,
		kreditService:    kreditService,
		karticaService:   karticaService,
		berzaService:     berzaService,
		listingService:   listingService,
		exchangeService:  exchangeService,
		tradingService:   tradingService,
		userClient:       userClient,
		accountPublisher: accountPublisher,
	}
}

// GetCurrencies vraća listu svih podržanih valuta.
// Mapped to: GET /bank/currencies
func (h *BankHandler) GetCurrencies(ctx context.Context, _ *emptypb.Empty) (*pb.GetCurrenciesResponse, error) {
	currencies, err := h.currencyService.GetCurrencies(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch currencies: %v", err)
	}

	pbCurrencies := make([]*pb.Currency, 0, len(currencies))
	for _, c := range currencies {
		pbCurrencies = append(pbCurrencies, &pb.Currency{
			Id:     c.ID,
			Naziv:  c.Naziv,
			Oznaka: c.Oznaka,
		})
	}

	return &pb.GetCurrenciesResponse{Valute: pbCurrencies}, nil
}

// GetDelatnosti vraća listu svih delatnosti.
// Mapped to: GET /bank/delatnosti
func (h *BankHandler) GetDelatnosti(ctx context.Context, _ *emptypb.Empty) (*pb.GetDelatnostiResponse, error) {
	delatnosti, err := h.delatnostService.GetDelatnosti(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to fetch delatnosti: %v", err)
	}

	pbDelatnosti := make([]*pb.Delatnost, 0, len(delatnosti))
	for _, d := range delatnosti {
		pbDelatnosti = append(pbDelatnosti, &pb.Delatnost{
			Id:     d.ID,
			Sifra:  d.Sifra,
			Naziv:  d.Naziv,
			Grana:  d.Grana,
			Sektor: d.Sektor,
		})
	}

	return &pb.GetDelatnostiResponse{Delatnosti: pbDelatnosti}, nil
}

// CreateAccount kreira novi bankovni račun.
// Zahteva ulogu EMPLOYEE u JWT tokenu.
// Mapped to: POST /bank/accounts
//
// Tok izvršavanja:
//  1. Sinhroni gRPC poziv ka user-service: validacija klijenta + dohvat emaila (timeout 3s).
//  2. Kreiranje računa u bazi (postojeća logika).
//  3. Asinhrono slanje ACCOUNT_CREATED notifikacije na RabbitMQ (fire-and-forget).
func (h *BankHandler) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.CreateAccountResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.UserType != "EMPLOYEE" {
		return nil, status.Error(codes.PermissionDenied, "samo zaposleni mogu kreirati račune")
	}

	// ── Korak 1: Validacija klijenta i dohvat emaila via gRPC ────────────────
	// Timeout od 3s: ako user-service ne odgovori, prekidamo kreiranje računa.
	lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	email, err := h.userClient.GetClientEmail(lookupCtx, req.VlasnikId)
	if err != nil {
		st, _ := status.FromError(err)
		switch st.Code() {
		case codes.NotFound:
			return nil, status.Errorf(codes.NotFound, "klijent sa ID %d ne postoji", req.VlasnikId)
		case codes.DeadlineExceeded, codes.Unavailable:
			return nil, status.Error(codes.Unavailable, "user-service nije dostupan, pokušajte ponovo")
		default:
			return nil, status.Errorf(codes.Unavailable, "greška pri komunikaciji sa user-service: %v", err)
		}
	}

	if email == "" {
		log.Printf("[create-account] upozorenje: klijent ID=%d nema email — notifikacija neće biti poslata", req.VlasnikId)
	}

	// ── Korak 2: Kreiranje računa (postojeća logika) ─────────────────────────
	input := domain.CreateAccountInput{
		ZaposleniID:      req.ZaposleniId,
		VlasnikID:        req.VlasnikId,
		ValutaID:         req.ValutaId,
		KategorijaRacuna: req.KategorijaRacuna,
		VrstaRacuna:      req.VrstaRacuna,
		Podvrsta:         req.Podvrsta,
		NazivRacuna:      req.Naziv,
		StanjeRacuna:     req.Stanje,
	}

	if req.Firma != nil {
		input.Firma = &domain.Firma{
			Naziv:       req.Firma.Naziv,
			MaticniBroj: req.Firma.MaticniBroj,
			PIB:         req.Firma.Pib,
			DelatnostID: req.Firma.DelatnostId,
			Adresa:      req.Firma.Adresa,
		}
	}

	id, err := h.accountService.CreateAccount(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCurrency), errors.Is(err, domain.ErrInvalidPodvrsta):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "greška pri kreiranju računa: %v", err)
		}
	}

	// ── Korak 2b: Flow 1 — automatsko kreiranje kartice uz račun ─────────────
	// Ako zaposleni označi "Napravi karticu", kreira se DEBIT kartica odabranog
	// tipa za vlasnika računa (bez ovlasceno_lice).
	// DinaCard+non-RSD: vraća grešku klijentu (frontend treba da onemogući opciju).
	// Ostale greške se loguju — račun je finansijski validan i ostaje kreiran.
	if req.KreirajKarticu {
		tipKartice := req.TipKartice
		if tipKartice == "" {
			tipKartice = domain.TipKarticaVisa // default za stare klijente
		}
		if _, karticaErr := h.karticaService.CreateKarticaZaVlasnika(ctx, id, tipKartice); karticaErr != nil {
			switch {
			case errors.Is(karticaErr, domain.ErrDinaCardSamoRSD),
				errors.Is(karticaErr, domain.ErrNepoznatTipKartice):
				// Ovo ne sme da se desi ako frontend validira — ali backend mora da brani.
				return nil, status.Error(codes.InvalidArgument, karticaErr.Error())
			case errors.Is(karticaErr, domain.ErrKarticaLimitPremasen):
				log.Printf("[create-account] UPOZORENJE: račun kreiran (id=%d) ali kartica nije kreirana — limit premašen: %v", id, karticaErr)
			default:
				log.Printf("[create-account] UPOZORENJE: račun kreiran (id=%d) ali kreiranje kartice nije uspelo: %v", id, karticaErr)
			}
		} else if email != "" {
			// Kartica uspešno kreirana — pošaljemo KREIRANA_KARTICA email.
			if pubErr := h.accountPublisher.Publish(worker.AccountEmailEvent{
				Type:  worker.CardCreatedType,
				Email: email,
				Token: "",
			}); pubErr != nil {
				log.Printf("[create-account] UPOZORENJE: kartica kreirana (racun=%d) ali KREIRANA_KARTICA email nije poslat: %v", id, pubErr)
			}
		}
	}

	// ── Korak 3: Asinhrono slanje notifikacije (RabbitMQ) ────────────────────
	// Fire-and-forget: račun je finansijski validan čak i ako publish ne uspe.
	// Greška se samo loguje — frontend dobija 200/201 bez obzira.
	if email != "" {
		if pubErr := h.accountPublisher.Publish(worker.AccountEmailEvent{
			Type:  "ACCOUNT_CREATED",
			Email: email,
			Token: "",
		}); pubErr != nil {
			log.Printf("[create-account] UPOZORENJE: račun kreiran (id=%d) ali email nije poslat klijentu ID=%d: %v",
				id, req.VlasnikId, pubErr)
		}
	}

	return &pb.CreateAccountResponse{Id: id}, nil
}

// ── Client endpoints ──────────────────────────────────────────────────────────

// extractClientID čita korisnikov ID iz JWT claims-a i proverava da je CLIENT.
func extractClientID(ctx context.Context) (int64, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.UserType != "CLIENT" {
		return 0, status.Error(codes.PermissionDenied, "samo klijenti mogu pristupiti ovom resursu")
	}
	id, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return 0, status.Errorf(codes.Internal, "neispravan korisnički ID u tokenu: %v", err)
	}
	return id, nil
}

// requireClientOrEmployee dozvoljava čitanje tržišnih listinga klijentima i zaposlenima (aktuarima).
func requireClientOrEmployee(ctx context.Context) error {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "niste autentifikovani")
	}
	if claims.UserType != "CLIENT" && claims.UserType != "EMPLOYEE" && claims.UserType != "ADMIN" {
		return status.Error(codes.PermissionDenied, "nemaš pravo pristupa hartijama od vrednosti")
	}
	return nil
}

// GetClientAccounts vraća aktivne račune trenutno prijavljenog korisnika (klijent ili aktuar).
// Mapped to: GET /bank/client/accounts
func (h *BankHandler) GetClientAccounts(ctx context.Context, _ *emptypb.Empty) (*pb.GetClientAccountsResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "niste autentifikovani")
	}
	vlasnikID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "neispravan korisnički ID u tokenu: %v", err)
	}

	accounts, err := h.accountService.GetClientAccounts(ctx, vlasnikID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu računa: %v", err)
	}

	pbAccounts := make([]*pb.AccountListItem, 0, len(accounts))
	for _, a := range accounts {
		pbAccounts = append(pbAccounts, &pb.AccountListItem{
			Id:                 a.ID,
			BrojRacuna:         a.BrojRacuna,
			NazivRacuna:        a.NazivRacuna,
			VrstaRacuna:        a.VrstaRacuna,
			KategorijaRacuna:   a.KategorijaRacuna,
			ValutaOznaka:       a.ValutaOznaka,
			StanjeRacuna:       a.StanjeRacuna,
			RezervisanaSredstva: a.RezervovanaSredstva,
			RaspolozivoStanje:  a.RaspolozivoStanje,
		})
	}

	return &pb.GetClientAccountsResponse{Accounts: pbAccounts}, nil
}

// GetAccountDetail vraća detalje jednog računa.
// Mapped to: GET /bank/client/accounts/{id}
func (h *BankHandler) GetAccountDetail(ctx context.Context, req *pb.GetAccountDetailRequest) (*pb.AccountDetail, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	detail, err := h.accountService.GetAccountDetail(ctx, req.Id, vlasnikID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			return nil, status.Error(codes.NotFound, "račun nije pronađen")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri dohvatu detalja računa: %v", err)
		}
	}

	pbDetail := &pb.AccountDetail{
		Id:                 detail.ID,
		BrojRacuna:         detail.BrojRacuna,
		NazivRacuna:        detail.NazivRacuna,
		VrstaRacuna:        detail.VrstaRacuna,
		KategorijaRacuna:   detail.KategorijaRacuna,
		ValutaOznaka:       detail.ValutaOznaka,
		StanjeRacuna:       detail.StanjeRacuna,
		RezervisanaSredstva: detail.RezervovanaSredstva,
		RaspolozivoStanje:  detail.RaspolozivoStanje,
		DnevniLimit:        detail.DnevniLimit,
		MesecniLimit:       detail.MesecniLimit,
	}
	if detail.NazivFirme != nil {
		pbDetail.NazivFirme = detail.NazivFirme
	}

	return pbDetail, nil
}

// GetAccountTransactions vraća transakcije za dati račun.
// Mapped to: GET /bank/client/accounts/{racun_id}/transactions
func (h *BankHandler) GetAccountTransactions(ctx context.Context, req *pb.GetAccountTransactionsRequest) (*pb.GetAccountTransactionsResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	input := domain.GetAccountTransactionsInput{
		RacunID:   req.RacunId,
		SortBy:    req.SortBy,
		SortOrder: req.SortOrder,
	}

	transactions, err := h.accountService.GetAccountTransactions(ctx, input, vlasnikID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			return nil, status.Error(codes.NotFound, "račun nije pronađen")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri dohvatu transakcija: %v", err)
		}
	}

	pbTx := make([]*pb.Transakcija, 0, len(transactions))
	for _, t := range transactions {
		pbTx = append(pbTx, &pb.Transakcija{
			Id:               t.ID,
			RacunId:          t.RacunID,
			TipTransakcije:   t.TipTransakcije,
			Iznos:            t.Iznos,
			Opis:             t.Opis,
			VremeIzvrsavanja: t.VremeIzvrsavanja.Format("2006-01-02T15:04:05Z"),
			Status:           t.Status,
		})
	}

	return &pb.GetAccountTransactionsResponse{Transactions: pbTx}, nil
}

// RenameAccount menja naziv računa.
// Mapped to: PATCH /bank/client/accounts/{id}/name
func (h *BankHandler) RenameAccount(ctx context.Context, req *pb.RenameAccountRequest) (*emptypb.Empty, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	if req.NoviNaziv == "" {
		return nil, status.Error(codes.InvalidArgument, "novi naziv ne sme biti prazan")
	}

	input := domain.RenameAccountInput{
		VlasnikID: vlasnikID,
		AccountID: req.Id,
		NoviNaziv: req.NoviNaziv,
	}

	if err := h.accountService.RenameAccount(ctx, input); err != nil {
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			return nil, status.Error(codes.NotFound, "račun nije pronađen")
		case errors.Is(err, domain.ErrNazivIsti):
			return nil, status.Error(codes.InvalidArgument, "novi naziv je isti kao trenutni")
		case errors.Is(err, domain.ErrNazivVecPostoji):
			return nil, status.Error(codes.AlreadyExists, "naziv računa već postoji")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri promeni naziva: %v", err)
		}
	}

	return &emptypb.Empty{}, nil
}

// UpdateAccountLimit kreira pending action za promenu limita.
// Mapped to: PATCH /bank/client/accounts/{id}/limit
func (h *BankHandler) UpdateAccountLimit(ctx context.Context, req *pb.UpdateAccountLimitRequest) (*pb.UpdateAccountLimitResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	input := domain.UpdateLimitInput{
		VlasnikID:    vlasnikID,
		AccountID:    req.Id,
		DnevniLimit:  req.DnevniLimit,
		MesecniLimit: req.MesecniLimit,
	}

	actionID, err := h.accountService.UpdateAccountLimit(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAccountNotFound):
			return nil, status.Error(codes.NotFound, "račun nije pronađen")
		case errors.Is(err, domain.ErrPendingAlreadyExists):
			return nil, status.Error(codes.AlreadyExists, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "greška pri kreiranju zahteva za limit: %v", err)
		}
	}

	return &pb.UpdateAccountLimitResponse{
		ActionId: actionID,
		Status:   "AWAITING_VERIFICATION",
	}, nil
}

// GetPendingActions vraća pending akcije za mobilni prikaz.
// Mapped to: GET /bank/transactions/pending
func (h *BankHandler) GetPendingActions(ctx context.Context, _ *emptypb.Empty) (*pb.GetPendingActionsResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	actions, err := h.accountService.GetPendingActions(ctx, vlasnikID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu zahteva: %v", err)
	}

	items := make([]*pb.PendingActionItem, 0, len(actions))
	for _, a := range actions {
		items = append(items, pendingActionToPb(a))
	}
	return &pb.GetPendingActionsResponse{Transactions: items}, nil
}

// GetPendingAction vraća jednu pending akciju.
// Mapped to: GET /bank/transactions/{id}
func (h *BankHandler) GetPendingAction(ctx context.Context, req *pb.GetPendingActionRequest) (*pb.GetPendingActionResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	action, err := h.accountService.GetPendingAction(ctx, req.Id, vlasnikID)
	if err != nil {
		if errors.Is(err, domain.ErrPendingNotFound) {
			return nil, status.Error(codes.NotFound, "zahtev nije pronađen")
		}
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu zahteva: %v", err)
	}
	return &pb.GetPendingActionResponse{Transaction: pendingActionToPb(*action)}, nil
}

// ApprovePendingAction generiše verifikacioni kod.
// Mapped to: POST /bank/transactions/{id}/approve
func (h *BankHandler) ApprovePendingAction(ctx context.Context, req *pb.ApprovePendingActionRequest) (*pb.ApprovePendingActionResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	code, expiresAt, err := h.accountService.ApprovePendingAction(ctx, req.Id, vlasnikID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrPendingNotFound):
			return nil, status.Error(codes.NotFound, "zahtev nije pronađen")
		case errors.Is(err, domain.ErrAlreadyApproved):
			return nil, status.Error(codes.FailedPrecondition, "zahtev je već obrađen")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri odobravanju: %v", err)
		}
	}

	return &pb.ApprovePendingActionResponse{
		VerificationCode: code,
		ExpiresAt:        expiresAt.Format(time.RFC3339),
		ExpiresInSeconds: 300,
	}, nil
}

// VerifyLimitChange proverava kod i primenjuje promenu limita.
// Mapped to: POST /bank/client/pending-actions/{id}/verify
func (h *BankHandler) VerifyLimitChange(ctx context.Context, req *pb.VerifyLimitChangeRequest) (*emptypb.Empty, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	if req.Code == "" {
		return nil, status.Error(codes.InvalidArgument, "kod ne sme biti prazan")
	}

	input := domain.VerifyLimitInput{
		VlasnikID: vlasnikID,
		ActionID:  req.Id,
		Code:      req.Code,
	}

	if err := h.accountService.VerifyAndApplyLimit(ctx, input); err != nil {
		switch {
		case errors.Is(err, domain.ErrPendingNotFound):
			return nil, status.Error(codes.NotFound, "zahtev nije pronađen")
		case errors.Is(err, domain.ErrWrongCode):
			return nil, status.Error(codes.InvalidArgument, "pogrešan verifikacioni kod")
		case errors.Is(err, domain.ErrCodeExpired):
			return nil, status.Error(codes.DeadlineExceeded, "verifikacioni kod je istekao")
		case errors.Is(err, domain.ErrTooManyAttempts):
			return nil, status.Error(codes.PermissionDenied, "zahtev je otkazan — previše neuspešnih pokušaja")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri verifikaciji: %v", err)
		}
	}

	return &emptypb.Empty{}, nil
}

// pendingActionToPb konvertuje domain.PendingAction u pb.PendingActionItem.
// Formatiranje zavisi od tipa akcije: PROMENA_LIMITA, PLACANJE ili PRENOS.
func pendingActionToPb(a domain.PendingAction) *pb.PendingActionItem {
	switch a.ActionType {
	case "PLACANJE", "PRENOS":
		tipLabel := "Plaćanje"
		if a.ActionType == "PRENOS" {
			tipLabel = "Interni prenos"
		}
		purpose := tipLabel
		if a.NazivPrimaoca != "" {
			purpose = fmt.Sprintf("%s — %s", tipLabel, a.NazivPrimaoca)
		}
		return &pb.PendingActionItem{
			Id:               a.ID,
			ActionType:       a.ActionType,
			RecipientName:    a.NazivPrimaoca,
			RecipientAccount: a.BrojRacunaPrimaoca,
			Amount:           a.Iznos,
			Currency:         a.ValutaOznaka,
			Purpose:          purpose,
			CreatedAt:        a.CreatedAt.Format(time.RFC3339),
			Status:           a.Status,
		}
	default:
		// PROMENA_LIMITA — originalna logika.
		var parts []string
		var amount float64
		if a.DnevniLimit >= 0 {
			parts = append(parts, fmt.Sprintf("Dnevni: %.2f %s", a.DnevniLimit, a.ValutaOznaka))
			amount = a.DnevniLimit
		}
		if a.MesecniLimit >= 0 {
			parts = append(parts, fmt.Sprintf("Mesečni: %.2f %s", a.MesecniLimit, a.ValutaOznaka))
			amount = a.MesecniLimit
		}
		if a.DnevniLimit >= 0 && a.MesecniLimit >= 0 && a.DnevniLimit > a.MesecniLimit {
			amount = a.DnevniLimit
		}
		return &pb.PendingActionItem{
			Id:               a.ID,
			ActionType:       a.ActionType,
			RecipientName:    "Promena limita",
			RecipientAccount: a.BrojRacuna,
			Amount:           amount,
			Currency:         a.ValutaOznaka,
			Purpose:          strings.Join(parts, " / "),
			CreatedAt:        a.CreatedAt.Format(time.RFC3339),
			Status:           a.Status,
		}
	}
}

// EmployeeChangeCardStatus — Employee only. Menja status kartice (blokada/deblokada/deaktivacija).
// Nakon uspešne promene statusa u bazi, asinhrono šalje email notifikacije:
//   - uvek vlasniku računa (email iz user-service-a)
//   - za POSLOVNI račun + ovlašćeno lice: i ovlašćenom licu (email iz lokalne baze)
//
// Mapped to: PATCH /bank/employee/cards/{card_number}/status
func (h *BankHandler) EmployeeChangeCardStatus(ctx context.Context, req *pb.EmployeeChangeCardStatusRequest) (*emptypb.Empty, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || (claims.UserType != "EMPLOYEE" && claims.UserType != "ADMIN") {
		return nil, status.Error(codes.PermissionDenied, "samo zaposleni mogu pristupiti ovom endpointu")
	}

	brojKartice := req.GetCardNumber()
	noviStatus := req.GetStatus()

	if brojKartice == "" {
		return nil, status.Error(codes.InvalidArgument, "card_number je obavezan")
	}
	switch noviStatus {
	case "AKTIVNA", "BLOKIRANA", "DEAKTIVIRANA":
		// OK
	default:
		return nil, status.Errorf(codes.InvalidArgument, "status mora biti AKTIVNA, BLOKIRANA ili DEAKTIVIRANA")
	}

	kartica, err := h.karticaService.ChangeEmployeeCardStatus(ctx, brojKartice, noviStatus)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrKarticaNotFound):
			return nil, status.Error(codes.NotFound, "kartica nije pronađena")
		case errors.Is(err, domain.ErrKarticaVecAktivna),
			errors.Is(err, domain.ErrKarticaVecBlokirana):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case errors.Is(err, domain.ErrNedozvoljenaPromenaSatusa):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "greška pri promeni statusa: %v", err)
		}
	}

	// Dohvati email vlasnika — reusing existing GetClientInfo (single gRPC call).
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	ownerInfo, infoErr := h.userClient.GetClientInfo(callCtx, kartica.VlasnikID)
	cancel()
	if infoErr != nil {
		log.Printf("[EmployeeChangeCardStatus] user-service poziv za vlasnik_id=%d nije uspeo: %v", kartica.VlasnikID, infoErr)
	}

	// Asinhrono pošalji notifikaciju vlasniku.
	if infoErr == nil && ownerInfo.Email != "" {
		if pubErr := h.accountPublisher.Publish(worker.AccountEmailEvent{
			Type:  worker.CardStatusChangedType,
			Email: ownerInfo.Email,
			Token: noviStatus,
		}); pubErr != nil {
			log.Printf("[EmployeeChangeCardStatus] RabbitMQ publish za vlasnika nije uspeo: %v", pubErr)
		}
	}

	// Za poslovne račune sa ovlašćenim licem — email je u lokalnoj bazi, bez user-service poziva.
	if kartica.VrstaRacuna == "POSLOVNI" && kartica.OvlascenoLice != nil && kartica.OvlascenoLice.EmailAdresa != "" {
		if pubErr := h.accountPublisher.Publish(worker.AccountEmailEvent{
			Type:  worker.CardStatusChangedType,
			Email: kartica.OvlascenoLice.EmailAdresa,
			Token: noviStatus,
		}); pubErr != nil {
			log.Printf("[EmployeeChangeCardStatus] RabbitMQ publish za ovlašćeno lice nije uspeo: %v", pubErr)
		}
	}

	return &emptypb.Empty{}, nil
}

// GetAllAccounts — Employee only. Vraća agregiranu listu svih računa klijenata,
// sortiranu abecedno po prezimenu vlasnika. Podržava filtere:
//   - account_number: parcijalni match u bazi (ILIKE)
//   - first_name, last_name: in-memory filteri nakon dohvata imena iz user-service-a
//
// Mapped to: GET /bank/employee/accounts
func (h *BankHandler) GetAllAccounts(ctx context.Context, req *pb.GetAllAccountsRequest) (*pb.GetAllAccountsResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || (claims.UserType != "EMPLOYEE" && claims.UserType != "ADMIN") {
		return nil, status.Error(codes.PermissionDenied, "samo zaposleni mogu pristupiti ovom endpointu")
	}

	// Broj računa filter se primenjuje na nivou SQL upita.
	accounts, err := h.accountService.GetAllAccounts(ctx, req.GetAccountNumber())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu računa: %v", err)
	}

	// Dedupliciraj owner ID-jeve da bismo minimizovali broj poziva ka user-service-u.
	ownerIDs := make(map[int64]struct{}, len(accounts))
	for _, a := range accounts {
		ownerIDs[a.VlasnikID] = struct{}{}
	}

	// Sinhroni pozivi ka user-service-u, jedan po vlasniku.
	type ownerName struct {
		ime     string
		prezime string
	}
	names := make(map[int64]ownerName, len(ownerIDs))
	for id := range ownerIDs {
		callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ime, prezime, nameErr := h.userClient.GetClientName(callCtx, id)
		cancel()
		if nameErr != nil {
			log.Printf("[GetAllAccounts] user-service poziv za vlasnik_id=%d nije uspeo: %v", id, nameErr)
			names[id] = ownerName{}
			continue
		}
		names[id] = ownerName{ime: ime, prezime: prezime}
	}

	// Primeni in-memory filtere po imenu i prezimenu (case-insensitive parcijalni match).
	firstNameFilter := strings.ToLower(req.GetFirstName())
	lastNameFilter := strings.ToLower(req.GetLastName())

	pbAccounts := make([]*pb.EmployeeAccountListItem, 0, len(accounts))
	for _, a := range accounts {
		n := names[a.VlasnikID]

		if firstNameFilter != "" && !strings.Contains(strings.ToLower(n.ime), firstNameFilter) {
			continue
		}
		if lastNameFilter != "" && !strings.Contains(strings.ToLower(n.prezime), lastNameFilter) {
			continue
		}

		pbAccounts = append(pbAccounts, &pb.EmployeeAccountListItem{
			Id:               a.ID,
			BrojRacuna:       a.BrojRacuna,
			VrstaRacuna:      a.VrstaRacuna,
			KategorijaRacuna: a.KategorijaRacuna,
			VlasnikId:        a.VlasnikID,
			ImeVlasnika:      n.ime,
			PrezimeVlasnika:  n.prezime,
		})
	}

	// Sortiraj abecedno po prezimenu, pa po imenu kao tiebreaker.
	sort.Slice(pbAccounts, func(i, j int) bool {
		pi, pj := pbAccounts[i].GetPrezimeVlasnika(), pbAccounts[j].GetPrezimeVlasnika()
		if pi != pj {
			return pi < pj
		}
		return pbAccounts[i].GetImeVlasnika() < pbAccounts[j].GetImeVlasnika()
	})

	return &pb.GetAllAccountsResponse{Accounts: pbAccounts}, nil
}

// GetAccountCards — Employee only. Vraća sve kartice vezane za dati broj računa.
// Ime, prezime i email vlasnika dohvataju se sinhronim pozivom ka user-service-u.
// Mapped to: GET /bank/employee/accounts/{broj_racuna}/cards
func (h *BankHandler) GetAccountCards(ctx context.Context, req *pb.GetAccountCardsRequest) (*pb.GetAccountCardsResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || (claims.UserType != "EMPLOYEE" && claims.UserType != "ADMIN") {
		return nil, status.Error(codes.PermissionDenied, "samo zaposleni mogu pristupiti ovom endpointu")
	}

	brojRacuna := req.GetBrojRacuna()
	if brojRacuna == "" {
		return nil, status.Error(codes.InvalidArgument, "broj_racuna je obavezan")
	}

	kartice, err := h.karticaService.GetKarticeZaPortalZaposlenih(ctx, brojRacuna)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu kartica: %v", err)
	}

	if len(kartice) == 0 {
		return &pb.GetAccountCardsResponse{Kartice: []*pb.EmployeeKarticaListItem{}}, nil
	}

	// Sve kartice na istom računu imaju istog vlasnika — jedan poziv ka user-service-u.
	vlasnikID := kartice[0].VlasnikID
	callCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	info, err := h.userClient.GetClientInfo(callCtx, vlasnikID)
	cancel()
	if err != nil {
		log.Printf("[GetAccountCards] user-service poziv za vlasnik_id=%d nije uspeo: %v", vlasnikID, err)
		// Nastavi sa praznim podacima o vlasniku.
		info = &transport.ClientInfo{}
	}

	pbKartice := make([]*pb.EmployeeKarticaListItem, 0, len(kartice))
	for _, k := range kartice {
		pbKartice = append(pbKartice, &pb.EmployeeKarticaListItem{
			Id:              k.ID,
			BrojKartice:     k.BrojKartice,
			Status:          k.Status,
			ImeVlasnika:     info.FirstName,
			PrezimeVlasnika: info.LastName,
			EmailVlasnika:   info.Email,
		})
	}

	return &pb.GetAccountCardsResponse{Kartice: pbKartice}, nil
}
