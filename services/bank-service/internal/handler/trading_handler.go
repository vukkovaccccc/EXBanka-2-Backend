package handler

import (
	"context"
	"errors"
	"strconv"

	pb "banka-backend/proto/banka"
	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/trading"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// orderToPb converts a trading.Order domain entity to the proto TradingOrder message.
func orderToPb(o trading.Order) *pb.TradingOrder {
	msg := &pb.TradingOrder{
		Id:                o.ID,
		UserId:            o.UserID,
		AccountId:         o.AccountID,
		ListingId:         o.ListingID,
		OrderType:         string(o.OrderType),
		Direction:         string(o.Direction),
		Quantity:          o.Quantity,
		ContractSize:      o.ContractSize,
		Status:            string(o.Status),
		IsDone:            o.IsDone,
		RemainingPortions: o.RemainingPortions,
		AfterHours:        o.AfterHours,
		AllOrNone:         o.AllOrNone,
		Margin:            o.Margin,
		LastModified:      o.LastModified.UTC().Format("2006-01-02T15:04:05Z"),
		CreatedAt:         o.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if o.PricePerUnit != nil {
		v := o.PricePerUnit.String()
		msg.PricePerUnit = &v
	}
	if o.StopPrice != nil {
		v := o.StopPrice.String()
		msg.StopPrice = &v
	}
	if o.ApprovedBy != nil {
		msg.ApprovedBy = o.ApprovedBy
	}
	return msg
}

// tradingError maps domain sentinel errors to appropriate gRPC status codes.
func tradingError(err error) error {
	switch {
	case errors.Is(err, trading.ErrOrderNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, trading.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, trading.ErrInvalidOrderState):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, trading.ErrInsufficientMargin):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, trading.ErrLimitPriceRequired),
		errors.Is(err, trading.ErrStopPriceRequired),
		errors.Is(err, trading.ErrInvalidOrderType),
		errors.Is(err, trading.ErrInvalidDirection):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Errorf(codes.Internal, "interna greška: %v", err)
	}
}

// parseOptionalDecimal parses a *string proto field into a *decimal.Decimal.
// Returns an InvalidArgument status error if the string is not a valid decimal.
func parseOptionalDecimal(field *string, name string) (*decimal.Decimal, error) {
	if field == nil {
		return nil, nil
	}
	d, err := decimal.NewFromString(*field)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "neispravan %s: %v", name, err)
	}
	return &d, nil
}

// =============================================================================
// TradingCalculate
// =============================================================================

// TradingCalculate computes an approximate price and commission without persisting anything.
// Auth: any authenticated user.
// Mapped to: POST /bank/trading/calculate
func (h *BankHandler) TradingCalculate(ctx context.Context, req *pb.TradingCalculateRequest) (*pb.TradingCalculateResponse, error) {
	if _, ok := auth.ClaimsFromContext(ctx); !ok {
		return nil, status.Error(codes.Unauthenticated, "niste autentifikovani")
	}

	calcReq := &trading.OrderCalculationRequest{
		OrderType:    trading.OrderType(req.GetOrderType()),
		Direction:    trading.OrderDirection(req.GetDirection()),
		ListingID:    req.GetListingId(),
		Quantity:     req.GetQuantity(),
		ContractSize: req.GetContractSize(),
		Margin:       req.GetMargin(),
		AllOrNone:    req.GetAllOrNone(),
	}

	ppu, err := parseOptionalDecimal(req.PricePerUnit, "price_per_unit")
	if err != nil {
		return nil, err
	}
	calcReq.PricePerUnit = ppu

	sp, err := parseOptionalDecimal(req.StopPrice, "stop_price")
	if err != nil {
		return nil, err
	}
	calcReq.StopPrice = sp

	resp, err := h.tradingService.CalculateOrderDetails(ctx, calcReq)
	if err != nil {
		return nil, tradingError(err)
	}

	out := &pb.TradingCalculateResponse{
		PricePerUnit:    resp.PricePerUnit.String(),
		ApproximatePrice: resp.ApproximatePrice.String(),
		Commission:      resp.Commission.String(),
	}
	if resp.InitialMarginCost != nil {
		v := resp.InitialMarginCost.String()
		out.InitialMarginCost = &v
	}
	return out, nil
}

// =============================================================================
// TradingCreateOrder
// =============================================================================

// TradingCreateOrder places a new order and applies the approval workflow.
// Auth: any authenticated user.
// Mapped to: POST /bank/trading/orders
func (h *BankHandler) TradingCreateOrder(ctx context.Context, req *pb.TradingCreateOrderRequest) (*pb.TradingCreateOrderResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "niste autentifikovani")
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "neispravan korisnički ID u tokenu: %v", err)
	}

	domainReq := &trading.CreateOrderRequest{
		UserID:       userID,
		AccountID:    req.GetAccountId(),
		ListingID:    req.GetListingId(),
		OrderType:    trading.OrderType(req.GetOrderType()),
		Direction:    trading.OrderDirection(req.GetDirection()),
		Quantity:     req.GetQuantity(),
		ContractSize: req.GetContractSize(),
		AfterHours:   req.GetAfterHours(),
		AllOrNone:    req.GetAllOrNone(),
		Margin:       req.GetMargin(),
	}

	ppu, err := parseOptionalDecimal(req.PricePerUnit, "price_per_unit")
	if err != nil {
		return nil, err
	}
	domainReq.PricePerUnit = ppu

	sp, err := parseOptionalDecimal(req.StopPrice, "stop_price")
	if err != nil {
		return nil, err
	}
	domainReq.StopPrice = sp

	order, err := h.tradingService.CreateOrder(ctx, domainReq)
	if err != nil {
		return nil, tradingError(err)
	}
	return &pb.TradingCreateOrderResponse{Order: orderToPb(*order)}, nil
}

// =============================================================================
// TradingListOrders
// =============================================================================

// TradingListOrders returns all orders with an optional status filter.
// Auth: EMPLOYEE only (supervisor dashboard).
// Mapped to: GET /bank/trading/orders
func (h *BankHandler) TradingListOrders(ctx context.Context, req *pb.TradingListOrdersRequest) (*pb.TradingListOrdersResponse, error) {
	if _, err := extractEmployeeID(ctx); err != nil {
		return nil, err
	}

	var statusFilter *trading.OrderStatus
	if s := req.GetStatus(); s != "" {
		v := trading.OrderStatus(s)
		statusFilter = &v
	}

	orders, err := h.tradingService.ListOrders(ctx, statusFilter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "greška pri dohvatu naloga: %v", err)
	}

	pbOrders := make([]*pb.TradingOrder, 0, len(orders))
	for _, o := range orders {
		pbOrders = append(pbOrders, orderToPb(o))
	}
	return &pb.TradingListOrdersResponse{Orders: pbOrders}, nil
}

// =============================================================================
// TradingApproveOrder
// =============================================================================

// TradingApproveOrder transitions a PENDING order to APPROVED.
// Auth: EMPLOYEE only.
// Mapped to: POST /bank/trading/orders/{order_id}/approve
func (h *BankHandler) TradingApproveOrder(ctx context.Context, req *pb.TradingApproveOrderRequest) (*pb.TradingApproveOrderResponse, error) {
	supervisorID, err := extractEmployeeID(ctx)
	if err != nil {
		return nil, err
	}

	order, err := h.tradingService.ApproveOrder(ctx, req.GetOrderId(), supervisorID)
	if err != nil {
		return nil, tradingError(err)
	}
	return &pb.TradingApproveOrderResponse{Order: orderToPb(*order)}, nil
}

// =============================================================================
// TradingDeclineOrder
// =============================================================================

// TradingDeclineOrder transitions a PENDING order to DECLINED.
// Auth: EMPLOYEE only.
// Mapped to: POST /bank/trading/orders/{order_id}/decline
func (h *BankHandler) TradingDeclineOrder(ctx context.Context, req *pb.TradingDeclineOrderRequest) (*pb.TradingDeclineOrderResponse, error) {
	supervisorID, err := extractEmployeeID(ctx)
	if err != nil {
		return nil, err
	}

	order, err := h.tradingService.DeclineOrder(ctx, req.GetOrderId(), supervisorID)
	if err != nil {
		return nil, tradingError(err)
	}
	return &pb.TradingDeclineOrderResponse{Order: orderToPb(*order)}, nil
}

// =============================================================================
// TradingCancelOrder
// =============================================================================

// TradingCancelOrder stops an active (PENDING or APPROVED) order.
// Auth: the order's owner OR any EMPLOYEE.
// Mapped to: POST /bank/trading/orders/{order_id}/cancel
func (h *BankHandler) TradingCancelOrder(ctx context.Context, req *pb.TradingCancelOrderRequest) (*pb.TradingCancelOrderResponse, error) {
	claims, ok := auth.ClaimsFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "niste autentifikovani")
	}
	callerID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "neispravan korisnički ID u tokenu: %v", err)
	}

	order, err := h.tradingService.CancelOrder(ctx, req.GetOrderId(), callerID)
	if err != nil {
		return nil, tradingError(err)
	}
	return &pb.TradingCancelOrderResponse{Order: orderToPb(*order)}, nil
}
