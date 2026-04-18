package handler

// payment_handler.go — gRPC handleri za modul plaćanja.
// Sve metode implementiraju pb.BankaServiceServer interface.

import (
	"context"
	"errors"
	"time"

	pb "banka-backend/proto/banka"
	"banka-backend/services/bank-service/internal/domain"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ─── Primaoci plaćanja ────────────────────────────────────────────────────────

// GetPaymentRecipients vraća listu sačuvanih primalaca plaćanja.
// Mapped to: GET /bank/client/payment-recipients
func (h *BankHandler) GetPaymentRecipients(ctx context.Context, _ *emptypb.Empty) (*pb.GetPaymentRecipientsResponse, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	recipients, err := h.paymentService.GetRecipients(ctx, vlasnikID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu primalaca: %v", err)
	}

	items := make([]*pb.PaymentRecipientItem, 0, len(recipients))
	for _, r := range recipients {
		items = append(items, &pb.PaymentRecipientItem{
			Id:         r.ID,
			Naziv:      r.Naziv,
			BrojRacuna: r.BrojRacuna,
		})
	}
	return &pb.GetPaymentRecipientsResponse{Recipients: items}, nil
}

// CreatePaymentRecipient dodaje novog primaoca plaćanja.
// Mapped to: POST /bank/client/payment-recipients
func (h *BankHandler) CreatePaymentRecipient(ctx context.Context, req *pb.CreatePaymentRecipientRequest) (*pb.PaymentRecipientItem, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	if req.Naziv == "" || req.BrojRacuna == "" {
		return nil, status.Error(codes.InvalidArgument, "naziv i broj računa su obavezni")
	}

	recipient, err := h.paymentService.CreateRecipient(ctx, vlasnikID, req.Naziv, req.BrojRacuna)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri kreiranju primaoca: %v", err)
	}

	return &pb.PaymentRecipientItem{
		Id:         recipient.ID,
		Naziv:      recipient.Naziv,
		BrojRacuna: recipient.BrojRacuna,
	}, nil
}

// UpdatePaymentRecipient menja postojećeg primaoca plaćanja.
// Mapped to: PATCH /bank/client/payment-recipients/{id}
func (h *BankHandler) UpdatePaymentRecipient(ctx context.Context, req *pb.UpdatePaymentRecipientRequest) (*pb.PaymentRecipientItem, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	if req.Naziv == "" || req.BrojRacuna == "" {
		return nil, status.Error(codes.InvalidArgument, "naziv i broj računa su obavezni")
	}

	recipient, err := h.paymentService.UpdateRecipient(ctx, req.Id, vlasnikID, req.Naziv, req.BrojRacuna)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrPaymentRecipientNotFound):
			return nil, status.Error(codes.NotFound, "primalac nije pronađen")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri izmeni primaoca: %v", err)
		}
	}

	return &pb.PaymentRecipientItem{
		Id:         recipient.ID,
		Naziv:      recipient.Naziv,
		BrojRacuna: recipient.BrojRacuna,
	}, nil
}

// DeletePaymentRecipient briše primaoca plaćanja.
// Mapped to: DELETE /bank/client/payment-recipients/{id}
func (h *BankHandler) DeletePaymentRecipient(ctx context.Context, req *pb.DeletePaymentRecipientRequest) (*emptypb.Empty, error) {
	vlasnikID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	if err := h.paymentService.DeleteRecipient(ctx, req.Id, vlasnikID); err != nil {
		switch {
		case errors.Is(err, domain.ErrPaymentRecipientNotFound):
			return nil, status.Error(codes.NotFound, "primalac nije pronađen")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri brisanju primaoca: %v", err)
		}
	}
	return &emptypb.Empty{}, nil
}

// ─── Plaćanja ─────────────────────────────────────────────────────────────────

// CreatePaymentIntent kreira nalog plaćanja i pokreće mobilnu verifikaciju.
// Mapped to: POST /bank/client/payments
func (h *BankHandler) CreatePaymentIntent(ctx context.Context, req *pb.CreatePaymentIntentRequest) (*pb.CreatePaymentIntentResponse, error) {
	userID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	input := domain.CreatePaymentIntentInput{
		IdempotencyKey:     req.IdempotencyKey,
		RacunPlatioceID:    req.RacunPlatiocaId,
		BrojRacunaPrimaoca: req.BrojRacunaPrimaoca,
		NazivPrimaoca:      req.NazivPrimaoca,
		Iznos:              req.Iznos,
		SifraPlacanja:      req.SifraPlacanja,
		PozivNaBroj:        req.PozivNaBroj,
		SvrhaPlacanja:      req.SvrhaPlacanja,
		InitiatedByUserID:  userID,
	}

	intent, actionID, err := h.paymentService.CreatePaymentIntent(ctx, input)
	if err != nil {
		return nil, mapPaymentError(err)
	}

	resp := &pb.CreatePaymentIntentResponse{
		IntentId:       intent.ID,
		ActionId:       actionID,
		BrojNaloga:     intent.BrojNaloga,
		Status:         intent.Status,
		Valuta:         intent.ValutaOznaka,
		Iznos:          intent.Iznos,
		Provizija:      intent.Provizija,
		Kurs:           intent.Kurs,
		ValutaPrimaoca: intent.ValutaPrimaoca,
	}
	if intent.KrajnjiIznos != nil {
		resp.KrajnjiIznos = *intent.KrajnjiIznos
	}
	return resp, nil
}

// CreateTransferIntent kreira nalog prenosa između računa istog klijenta.
// Mapped to: POST /bank/client/transfers
func (h *BankHandler) CreateTransferIntent(ctx context.Context, req *pb.CreateTransferIntentRequest) (*pb.CreatePaymentIntentResponse, error) {
	userID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	input := domain.CreateTransferIntentInput{
		IdempotencyKey:    req.IdempotencyKey,
		RacunPlatioceID:   req.RacunPlatiocaId,
		RacunPrimaocaID:   req.RacunPrimaocaId,
		Iznos:             req.Iznos,
		SvrhaPlacanja:     req.SvrhaPlacanja,
		InitiatedByUserID: userID,
	}

	intent, actionID, err := h.paymentService.CreateTransferIntent(ctx, input)
	if err != nil {
		return nil, mapPaymentError(err)
	}

	resp := &pb.CreatePaymentIntentResponse{
		IntentId:       intent.ID,
		ActionId:       actionID,
		BrojNaloga:     intent.BrojNaloga,
		Status:         intent.Status,
		Valuta:         intent.ValutaOznaka,
		Iznos:          intent.Iznos,
		Provizija:      intent.Provizija,
		Kurs:           intent.Kurs,
		ValutaPrimaoca: intent.ValutaPrimaoca,
	}
	if intent.KrajnjiIznos != nil {
		resp.KrajnjiIznos = *intent.KrajnjiIznos
	}
	return resp, nil
}

// VerifyAndExecutePayment proverava verifikacioni kod i izvršava plaćanje.
// Mapped to: POST /bank/client/payments/{intent_id}/verify
func (h *BankHandler) VerifyAndExecutePayment(ctx context.Context, req *pb.VerifyPaymentRequest) (*pb.PaymentIntentItem, error) {
	userID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	if req.Code == "" {
		return nil, status.Error(codes.InvalidArgument, "kod ne sme biti prazan")
	}

	input := domain.VerifyPaymentInput{
		IntentID: req.IntentId,
		Code:     req.Code,
		UserID:   userID,
	}

	intent, err := h.paymentService.VerifyAndExecute(ctx, input)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrPaymentIntentNotFound):
			return nil, status.Error(codes.NotFound, "nalog nije pronađen")
		case errors.Is(err, domain.ErrPaymentAlreadyExecuted):
			return nil, status.Error(codes.AlreadyExists, "nalog je već izvršen")
		case errors.Is(err, domain.ErrPaymentAlreadyFailed):
			return nil, status.Error(codes.FailedPrecondition, "nalog je odbijen")
		case errors.Is(err, domain.ErrWrongCode):
			return nil, status.Error(codes.InvalidArgument, "pogrešan verifikacioni kod")
		case errors.Is(err, domain.ErrCodeExpired):
			return nil, status.Error(codes.DeadlineExceeded, "verifikacioni kod je istekao")
		case errors.Is(err, domain.ErrTooManyAttempts):
			return nil, status.Error(codes.PermissionDenied, "nalog je otkazan — previše neuspešnih pokušaja")
		case errors.Is(err, domain.ErrInsufficientFunds):
			return nil, status.Error(codes.FailedPrecondition, "nedovoljno sredstava na računu")
		case errors.Is(err, domain.ErrDailyLimitExceeded):
			return nil, status.Error(codes.FailedPrecondition, "probijen dnevni limit plaćanja")
		case errors.Is(err, domain.ErrMonthlyLimitExceeded):
			return nil, status.Error(codes.FailedPrecondition, "probijen mesečni limit plaćanja")
		default:
			return nil, status.Errorf(codes.Internal, "greška pri izvršenju naloga: %v", err)
		}
	}

	return intentToPb(intent), nil
}

// ─── Istorija plaćanja ────────────────────────────────────────────────────────

// GetPaymentHistory vraća istoriju plaćanja sa filterima.
// Mapped to: GET /bank/client/payments
func (h *BankHandler) GetPaymentHistory(ctx context.Context, req *pb.GetPaymentHistoryRequest) (*pb.GetPaymentHistoryResponse, error) {
	userID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	filter := domain.PaymentHistoryFilter{
		Status: req.Status,
	}
	if req.DateFrom != "" {
		t, err := time.Parse(time.RFC3339, req.DateFrom)
		if err == nil {
			filter.DateFrom = &t
		}
	}
	if req.DateTo != "" {
		t, err := time.Parse(time.RFC3339, req.DateTo)
		if err == nil {
			filter.DateTo = &t
		}
	}
	if req.MinIznos > 0 {
		v := req.MinIznos
		filter.MinIznos = &v
	}
	if req.MaxIznos > 0 {
		v := req.MaxIznos
		filter.MaxIznos = &v
	}

	intents, err := h.paymentService.GetPaymentHistory(ctx, userID, filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu istorije: %v", err)
	}

	items := make([]*pb.PaymentIntentItem, 0, len(intents))
	for i := range intents {
		items = append(items, intentToPb(&intents[i]))
	}
	return &pb.GetPaymentHistoryResponse{Payments: items}, nil
}

// GetPaymentDetail vraća detalje jednog naloga plaćanja.
// Mapped to: GET /bank/client/payments/{id}
func (h *BankHandler) GetPaymentDetail(ctx context.Context, req *pb.GetPaymentDetailRequest) (*pb.PaymentIntentItem, error) {
	userID, err := extractClientID(ctx)
	if err != nil {
		return nil, err
	}

	intent, err := h.paymentService.GetPaymentDetail(ctx, req.Id, userID)
	if err != nil {
		if errors.Is(err, domain.ErrPaymentIntentNotFound) {
			return nil, status.Error(codes.NotFound, "nalog nije pronađen")
		}
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu naloga: %v", err)
	}

	return intentToPb(intent), nil
}

// ─── Pomoćne funkcije ─────────────────────────────────────────────────────────

// intentToPb konvertuje domain.PaymentIntent u pb.PaymentIntentItem.
func intentToPb(i *domain.PaymentIntent) *pb.PaymentIntentItem {
	item := &pb.PaymentIntentItem{
		Id:                 i.ID,
		IntentId:           i.ID,
		IdempotencyKey:     i.IdempotencyKey,
		BrojNaloga:         i.BrojNaloga,
		TipTransakcije:     i.TipTransakcije,
		BrojRacunaPlatioca: i.BrojRacunaPlatioca,
		BrojRacunaPrimaoca: i.BrojRacunaPrimaoca,
		NazivPrimaoca:      i.NazivPrimaoca,
		Iznos:              i.Iznos,
		Provizija:          i.Provizija,
		Kurs:               i.Kurs,
		ValutaPrimaoca:     i.ValutaPrimaoca,
		Valuta:             i.ValutaOznaka,
		SifraPlacanja:      i.SifraPlacanja,
		PozivNaBroj:        i.PozivNaBroj,
		SvrhaPlacanja:      i.SvrhaPlacanja,
		Status:             i.Status,
		CreatedAt:          i.CreatedAt.Format(time.RFC3339),
		FailedReason:       i.FailedReason,
	}
	if i.KrajnjiIznos != nil {
		item.KrajnjiIznos = *i.KrajnjiIznos
	}
	if i.ExecutedAt != nil {
		item.ExecutedAt = i.ExecutedAt.Format(time.RFC3339)
	}
	return item
}

// mapPaymentError mapira domensku grešku plaćanja u gRPC status.
func mapPaymentError(err error) error {
	switch {
	case errors.Is(err, domain.ErrAccountNotOwned):
		return status.Error(codes.PermissionDenied, "račun ne pripada vama")
	case errors.Is(err, domain.ErrSameAccount):
		return status.Error(codes.InvalidArgument, "račun platioca i primaoca ne smeju biti isti")
	case errors.Is(err, domain.ErrInvalidPaymentCode):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrRecipientAccountInvalid):
		return status.Error(codes.InvalidArgument, "primalački račun nije validan")
	case errors.Is(err, domain.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrDailyLimitExceeded):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrMonthlyLimitExceeded):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		if err != nil {
			return status.Errorf(codes.Internal, "greška: %v", err)
		}
		return nil
	}
}
