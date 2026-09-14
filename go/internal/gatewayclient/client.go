package gatewayclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/payrail/go/internal/gatewaypb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn   *grpc.ClientConn
	rpc    gatewaypb.GatewayServiceClient
	logger *slog.Logger
}

func NewClient(target string, tlsEnabled bool, logger *slog.Logger) (*Client, error) {
	creds := insecure.NewCredentials()
	if tlsEnabled {
		creds = credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	}

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(creds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		return nil, fmt.Errorf("dial gateway %q: %w", target, err)
	}

	return &Client{conn: conn, rpc: gatewaypb.NewGatewayServiceClient(conn), logger: logger}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type CreateOrderRequest struct {
	Gateway     string // RAZORPAY | STRIPE | ....
	OrderID     string
	AmountMinor int64
	Currency    string
}

type CreateOrderResponse struct {
	GatewayOrderID string
	ClientParams   map[string]any
}

func (c *Client) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResponse, error) {
	resp, err := c.rpc.CreateOrder(ctx, &gatewaypb.CreateOrderRequest{
		Gateway:     gatewaypb.GatewayFromName(req.Gateway),
		OrderId:     req.OrderID,
		AmountMinor: req.AmountMinor, // a real int64 over the wire, no string round-trip
		Currency:    gatewaypb.CurrencyFromName(req.Currency),
	})
	if err != nil {
		return CreateOrderResponse{}, fmt.Errorf("gateway CreateOrder: %w", err)
	}
	return CreateOrderResponse{
		GatewayOrderID: resp.GetGatewayOrderId(),
		ClientParams:   paramsToAny(resp.GetClientParams()),
	}, nil
}

func paramsToAny(in map[string]string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (c *Client) VerifyWebhook(ctx context.Context, provider string, body []byte, headers http.Header) (bool, error) {
	resp, err := c.rpc.VerifyWebhook(ctx, &gatewaypb.VerifyWebhookRequest{
		Gateway: gatewaypb.GatewayFromName(provider),
		Body:    body,
		Headers: flattenHeaders(headers),
	})

	if err != nil {
		return false, fmt.Errorf("gateway VerifyWebhook: %w", err)
	}
	return resp.GetVerified(), nil
}

func flattenHeaders(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k := range h {
		m[k] = h.Get(k) // first value is enough for signature headers
	}
	return m
}

type FetchResult struct {
	Status           string // PENDING | CAPTURED | FAILED | EXPIRED
	GatewayPaymentID string
	AmountMinor      int64
	Currency         string
}

type RefundFetchResult struct {
	Status          string // PENDING | PROCESSED | FAILED
	GatewayRefundID string
	AmountMinor     int64
}

type CreateRefundResult struct {
	GatewayRefundID string
	Status          string
}

type OrderLookupResult struct {
	Found          bool
	GatewayOrderID string
	Status         string
	AmountMinor    int64
	Currency       string
}

func (c *Client) FetchPayment(ctx context.Context, gateway, gatewayOrderID string) (FetchResult, error) {
	resp, err := c.rpc.FetchPayment(ctx, &gatewaypb.FetchPaymentRequest{
		Gateway:        gatewaypb.GatewayFromName(gateway),
		GatewayOrderId: gatewayOrderID,
	})
	if err != nil {
		return FetchResult{}, fmt.Errorf("gateway FetchPayment: %w", err)
	}
	return FetchResult{
		Status:           paymentStatusName(resp.GetStatus()),
		GatewayPaymentID: resp.GetGatewayPaymentId(),
		AmountMinor:      resp.GetAmountMinor(),
		Currency:         gatewaypb.CurrencyName(resp.GetCurrency()),
	}, nil
}

func (c *Client) FetchRefund(ctx context.Context, gateway, gatewayRefundID, idempotencyKey, gatewayOrderID string) (RefundFetchResult, error) {
	resp, err := c.rpc.FetchRefund(ctx, &gatewaypb.FetchRefundRequest{
		Gateway:         gatewaypb.GatewayFromName(gateway),
		GatewayRefundId: gatewayRefundID,
		IdempotencyKey:  idempotencyKey,
		GatewayOrderId:  gatewayOrderID, // just for cashfree
	})
	if err != nil {
		return RefundFetchResult{}, fmt.Errorf("gateway FetchRefund: %w", err)
	}

	return RefundFetchResult{
		Status:          refundStatusName(resp.GetStatus()),
		GatewayRefundID: resp.GetGatewayRefundId(),
		AmountMinor:     resp.GetAmountMinor(),
	}, nil
}

func (c *Client) CreateRefund(ctx context.Context, gateway, gatewayPaymentID, gatewayOrderID string, amountMinor int64, currency, idempotencyKey string) (CreateRefundResult, error) {
	resp, err := c.rpc.CreateRefund(ctx, &gatewaypb.CreateRefundRequest{
		Gateway:          gatewaypb.GatewayFromName(gateway),
		GatewayPaymentId: gatewayPaymentID,
		GatewayOrderId:   gatewayOrderID, // for cashfree only
		AmountMinor:      amountMinor,
		Currency:         gatewaypb.CurrencyFromName(currency),
		IdempotencyKey:   idempotencyKey,
	})
	if err != nil {
		return CreateRefundResult{}, fmt.Errorf("gateway CreateRefund: %w", err)
	}
	return CreateRefundResult{
		GatewayRefundID: resp.GetGatewayRefundId(),
		Status:          refundStatusName(resp.GetStatus()),
	}, nil
}

func (c *Client) CapturePayment(ctx context.Context, gateway, gatewayPaymentID string, amountMinor int64, currency string) (FetchResult, error) {
	resp, err := c.rpc.CapturePayment(ctx, &gatewaypb.CaptureRequest{
		Gateway:          gatewaypb.GatewayFromName(gateway),
		GatewayPaymentId: gatewayPaymentID,
		AmountMinor:      amountMinor,
		Currency:         gatewaypb.CurrencyFromName(currency),
	})

	if err != nil {
		return FetchResult{}, fmt.Errorf("gateway CapturePayment: %w", err)
	}

	return FetchResult{
		Status:           paymentStatusName(resp.GetStatus()),
		GatewayPaymentID: resp.GetGatewayPaymentId(),
		AmountMinor:      resp.GetAmountMinor(),
		Currency:         currency,
	}, nil
}

func (c *Client) FindOrderByReference(ctx context.Context, gateway, merchantReference string) (OrderLookupResult, error) {
	resp, err := c.rpc.FindOrderByReference(ctx, &gatewaypb.FindOrderByReferenceRequest{
		Gateway:           gatewaypb.GatewayFromName(gateway),
		MerchantReference: merchantReference,
	})

	if err != nil {
		return OrderLookupResult{}, fmt.Errorf("gateway FindOrderByReference: %w", err)
	}
	
	return OrderLookupResult{
		Found:          resp.GetFound(),
		GatewayOrderID: resp.GetGatewayOrderId(),
		Status:         paymentStatusName(resp.GetStatus()),
		AmountMinor:    resp.GetAmountMinor(),
		Currency:       gatewaypb.CurrencyName(resp.GetCurrency()),
	}, nil
}

func paymentStatusName(s gatewaypb.PaymentStatus) string {
	return strings.TrimPrefix(s.String(), "PAYMENT_STATUS_")
}

func refundStatusName(s gatewaypb.RefundStatus) string {
	return strings.TrimPrefix(s.String(), "REFUND_STATUS_")
}
