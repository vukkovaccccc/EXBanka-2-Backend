package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	pb "banka-backend/proto/banka"
	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// BankHandler implementira pb.BankaServiceServer.
type BankHandler struct {
	pb.UnimplementedBankaServiceServer
	currencyService  domain.CurrencyService
	delatnostService domain.DelatnostService
	accountService   domain.AccountService
	paymentService   domain.PaymentService
}

func NewBankHandler(
	currencyService domain.CurrencyService,
	delatnostService domain.DelatnostService,
	accountService domain.AccountService,
	paymentService domain.PaymentService,
) *BankHandler {
	return &BankHandler{
		currencyService:  currencyService,
		delatnostService: delatnostService,
		accountService:   accountService,
		paymentService:   paymentService,
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
func (h *BankHandler) CreateAccount(ctx context.Context, req *pb.CreateAccountRequest) (*pb.CreateAccountResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.UserType != "EMPLOYEE" {
		return nil, status.Error(codes.PermissionDenied, "samo zaposleni mogu kreirati račune")
	}

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

// GetClientAccounts vraća aktivne račune trenutno prijavljenog klijenta.
// Mapped to: GET /bank/client/accounts
func (h *BankHandler) GetClientAccounts(ctx context.Context, _ *emptypb.Empty) (*pb.GetClientAccountsResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
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
