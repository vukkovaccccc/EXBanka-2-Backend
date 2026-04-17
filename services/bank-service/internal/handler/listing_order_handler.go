package handler

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	pb "banka-backend/proto/banka"
	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/domain"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const listingOrderFundsMsg = "Nemate dovoljno sredstava na izabranom računu."

// estimateListingChargeInAccountCurrency procenjuje koliko treba skinuti sa računa u valuti računa
// za kupovinu u USD (cene hartija su u USD).
func estimateListingChargeInAccountCurrency(
	ctx context.Context,
	ex domain.ExchangeService,
	usdAmount float64,
	accVal string,
) (float64, error) {
	if usdAmount <= 0 {
		return 0, errors.New("iznos mora biti veći od nule")
	}
	if accVal == "USD" {
		return usdAmount, nil
	}

	rates, err := ex.GetRates(ctx)
	if err != nil {
		return 0, err
	}

	var usdRate *domain.ExchangeRate
	for i := range rates {
		if rates[i].Oznaka == "USD" {
			usdRate = &rates[i]
			break
		}
	}
	if usdRate == nil {
		return 0, domain.ErrExchangeRateNotFound
	}

	rsdCost := usdAmount * usdRate.Prodajni
	if accVal == "RSD" {
		return rsdCost, nil
	}

	var tgtRate *domain.ExchangeRate
	for i := range rates {
		if rates[i].Oznaka == accVal {
			tgtRate = &rates[i]
			break
		}
	}
	if tgtRate == nil || tgtRate.Prodajni == 0 {
		return 0, domain.ErrExchangeRateNotFound
	}

	return rsdCost / tgtRate.Prodajni, nil
}

// CreateListingOrder validira kupovinu.
//   - CLIENT: račun mora biti u vlasništvu klijenta, imati dovoljno sredstava;
//     dozvoljeni tipovi: STOCK i FUTURE.
//   - EMPLOYEE/ADMIN (aktuari): mogu kupovati sve tipove; provera limita aktuara
//     je u Celini 4 — ovde se samo proverava da hartija postoji.
//
// Ne knjiži stvarnu trgovinu (puna logika u Celini 4).
func (h *BankHandler) CreateListingOrder(ctx context.Context, req *pb.CreateListingOrderRequest) (*pb.CreateListingOrderResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "niste autentifikovani")
	}

	if req.GetListingId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "nije navedena hartija")
	}
	if req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "količina mora biti veća od nule")
	}
	if req.GetSide() != "BUY" && req.GetSide() != "SELL" {
		return nil, status.Error(codes.InvalidArgument, "smer naloga mora biti BUY ili SELL")
	}

	calc, err := h.listingService.GetListingByID(ctx, req.GetListingId())
	if err != nil {
		if errors.Is(err, domain.ErrListingNotFound) {
			return nil, status.Error(codes.NotFound, "hartija od vrednosti nije pronađena")
		}
		return nil, status.Errorf(codes.Internal, "listing: %v", err)
	}

	var execPrice float64
	switch req.GetOrderType() {
	case "MARKET":
		execPrice = calc.Ask
		if execPrice <= 0 {
			execPrice = calc.Price
		}
	case "LIMIT":
		if req.GetLimitPrice() <= 0 {
			return nil, status.Error(codes.InvalidArgument, "unesite limit cenu")
		}
		execPrice = req.GetLimitPrice()
	case "STOP":
		if req.GetStopPrice() <= 0 {
			return nil, status.Error(codes.InvalidArgument, "unesite stop cenu")
		}
		execPrice = req.GetStopPrice()
	default:
		return nil, status.Error(codes.InvalidArgument, "nepoznat tip naloga")
	}

	estimatedUSD := req.GetQuantity() * execPrice * calc.ContractSize
	if estimatedUSD <= 0 {
		return nil, status.Error(codes.InvalidArgument, "procena iznosa mora biti veća od nule")
	}

	// ── EMPLOYEE / ADMIN (aktuari) ─────────────────────────────────────────────
	if claims.UserType == "EMPLOYEE" || claims.UserType == "ADMIN" {
		// Aktuari mogu da trguju svim tipovima hartija.
		// Provera limita aktuara je odložena za Celinu 4.
		return &pb.CreateListingOrderResponse{
			Message: fmt.Sprintf("Nalog prihvaćen (procena ~ %.2f USD). Puna obrada u modulu naloga.", estimatedUSD),
		}, nil
	}

	// ── CLIENT ────────────────────────────────────────────────────────────────
	clientID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "neispravan korisnički ID: %v", err)
	}

	lt := calc.ListingType
	if lt == domain.ListingTypeForex {
		return nil, status.Error(codes.InvalidArgument, "klijent ne može da trguje FOREX instrumentima")
	}

	if req.GetAccountId() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "izaberite račun sa kog se plaća")
	}

	acc, err := h.accountService.GetAccountDetail(ctx, req.GetAccountId(), clientID)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			return nil, status.Error(codes.NotFound, "račun nije pronađen")
		}
		return nil, status.Errorf(codes.Internal, "račun: %v", err)
	}

	required, err := estimateListingChargeInAccountCurrency(ctx, h.exchangeService, estimatedUSD, acc.ValutaOznaka)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "nije moguće proceniti iznos u valuti računa: %v", err)
	}

	if acc.RaspolozivoStanje+1e-4 < required {
		return nil, status.Error(codes.FailedPrecondition, listingOrderFundsMsg)
	}

	return &pb.CreateListingOrderResponse{
		Message: fmt.Sprintf("Nalog prihvaćen (procena ~ %.2f %s). Puna obrada u modulu naloga.", required, acc.ValutaOznaka),
	}, nil
}
