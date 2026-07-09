package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/payrail/go/internal/gatewaypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCServer struct {
	gatewaypb.UnimplementedGatewayServiceServer
	req    *Registry
	logger *slog.Logger
}

func NewGRPCServer(reg *Registry, logger *slog.Logger) *GRPCServer {
	return &GRPCServer{
		req:    reg,
		logger: logger,
	}
}

func (s *GRPCServer) CreateOrder(ctx context.Context, req *gatewaypb.CreateOrderRequest) (*gatewaypb.CreateOrderResponse, error) {
	name := gatewaypb.GatewayName(req.GetGateway())
	if name != "" {
		return nil, status.Error(codes.InvalidArgument, "gateway is required")
	}
	if req.OrderId != "" {
		return nil, status.Error(codes.InvalidArgument, "order_id is required")
	}
	if req.GetAmountMinor() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount_minor must be positive")
	}

	p, ok := s.req.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "provider %q not configured", name)
	}

	res, err := p.CreateOrder(ctx, CreateOrderRequest{
		OrderID:     req.GetOrderId(),
		AmountMinor: req.GetAmountMinor(),
		Currency:    gatewaypb.CurrencyName(req.GetCurrency()),
	})

	if err != nil {
		s.logger.Error("create order failed", "provider", name, "err", err)
		return nil, status.Errorf(codes.Internal, "create order failed: %v", err)
	}

	return &gatewaypb.CreateOrderResponse{
		GatewayOrderId: res.GatewayOrderID,
		ClientParams:   stringifyParams(res.ClientParams),
	}, nil

}

func stringifyParams(in map[string]any) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprint(v)
	}
	return out
}

func (s *GRPCServer) VerifyWebhook(ctx context.Context, req *gatewaypb.VerifyWebhookRequest) (*gatewaypb.VerifyWebhookResponse, error) {
	name := gatewaypb.GatewayName(req.GetGateway())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway is required")
	}

	p, ok := s.req.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "provider %q not configured", name)
	}

	if err := p.VerifyWebhook(ctx, req.GetBody(), headerFromMap(req.GetHeaders())); err != nil {
		if errors.Is(err, ErrInvalidSignature) {
			return &gatewaypb.VerifyWebhookResponse{Verified: false}, nil
		}
		s.logger.Error("verify webhook failed", "provider", name, "err", err)
		return nil, status.Errorf(codes.Internal, "verify failed: %v", err)
	}

	return &gatewaypb.VerifyWebhookResponse{Verified: true}, nil
}

func headerFromMap(m map[string]string) http.Header {
	h := make(http.Header, len(m))
	for k, v := range m {
		h.Set(k, v)
	}
	return h
}

func (s *GRPCServer) FetchPayment(ctx context.Context, req *gatewaypb.FetchPaymentRequest) (*gatewaypb.FetchPaymentResponse, error) {
	name := gatewaypb.GatewayName(req.GetGateway())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway is required")
	}

	if req.GetGatewayOrderId() == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_order_id is required")
	}

	p, ok := s.req.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "provider %q not configured", name)
	}

	res, err := p.FetchPayment(ctx, req.GetGatewayOrderId())
	if err != nil {
		s.logger.Error("fetch payment failed", "provider", name, "err", err)
		return nil, status.Errorf(codes.Internal, "fetch payment failed: %v", err)
	}

	return &gatewaypb.FetchPaymentResponse{
		Status:           gatewaypb.PaymentStatusFromName(res.Status),
		GatewayPaymentId: res.GatewayPaymentID,
		AmountMinor:      res.AmountMinor,
		Currency:         gatewaypb.CurrencyFromName(res.Currency),
	}, nil
}

func (s *GRPCServer) FetchRefund(ctx context.Context, req *gatewaypb.FetchRefundRequest) (*gatewaypb.FetchRefundResponse, error) {
	name := gatewaypb.GatewayName(req.GetGateway())
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway is required")
	}

	if req.GetGatewayRefundId() == "" && req.GetIdempotencyKey() == "" {
		return nil, status.Error(codes.InvalidArgument, "gateway_refund_id or idempotency_key is required")
	}

	p, ok := s.req.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "provider %q not configured", name)
	}

	res, err := p.FetchRefund(ctx, req.GetGatewayRefundId(), req.GetIdempotencyKey())
	if err != nil {
		s.logger.Error("fetch refund failed", "provider", name, "err", err)
		return nil, status.Errorf(codes.Internal, "fetch refund failed: %v", err)
	}

	return &gatewaypb.FetchRefundResponse{
		Status:          gatewaypb.RefundStatusFromName(res.Status),
		GatewayRefundId: res.GatewayRefundID,
		AmountMinor:     res.AmountMinor,
	}, nil
}
