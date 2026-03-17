package handler

import (
	"context"
	"errors"

	pb "banka-backend/proto/banka"
	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// BankHandler implementira pb.BankaServiceServer.
// Zadužen isključivo za primanje zahteva, pozivanje servisa i mapiranje u proto odgovor.
// Ne sme da sadrži biznis logiku niti direktne pozive baze.
type BankHandler struct {
	pb.UnimplementedBankaServiceServer
	currencyService  domain.CurrencyService
	delatnostService domain.DelatnostService
	accountService   domain.AccountService
}

// NewBankHandler konstruiše BankHandler sa injektovanim zavisnostima.
func NewBankHandler(
	currencyService domain.CurrencyService,
	delatnostService domain.DelatnostService,
	accountService domain.AccountService,
) *BankHandler {
	return &BankHandler{
		currencyService:  currencyService,
		delatnostService: delatnostService,
		accountService:   accountService,
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
	// Autorizacija: samo zaposleni može kreirati račun.
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok || claims.UserType != "EMPLOYEE" {
		return nil, status.Error(codes.PermissionDenied, "samo zaposleni mogu kreirati račune")
	}

	// Mapiranje proto → domain (kreiraj_karticu se ignoriše).
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
			// vlasnik_id se preuzima iz root zahteva (req.VlasnikId), ne iz Firma poruke
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
