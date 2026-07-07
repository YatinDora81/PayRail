package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
)

type CreateOrderRequest struct {
	OrderID     string
	AmountMinor int64
	Currency    string
}

type CreateOrderResult struct {
	GatewayOrderID string         `json:"gatewayOrderId"`
	ClientParams   map[string]any `json:"clientParams,omitempty"`
}

type FetchPaymentResult struct {
	Status           string
	GatewayPaymentID string
	AmountMinor      int64
	Currency         string
}

type FetchRefundResult struct {
	Status          string
	GatewayRefundID string
	AmountMinor     int64
}

type Provider interface {
	Name() string //razorpay | stripe | .....
	CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResult, error)
	VerifyWebhook(ctx context.Context, payload []byte, headers http.Header) error
	FetchPayment(ctx context.Context, gatewayOrderID string) (FetchPaymentResult, error)
	FetchRefund(ctx context.Context, gatewayRefundID, idempotencyKey string) (FetchRefundResult, error)
}

var ErrInvalidSignature = errors.New("invalid webhook signature")

type Registry struct {
	providers map[string]Provider
}

func hmacSHA256Hex(key, msg []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return hex.EncodeToString(mac.Sum(nil))
}

func secureEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
