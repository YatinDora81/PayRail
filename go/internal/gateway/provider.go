package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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

func NewRegistry(ps ...Provider) *Registry {
	m := make(map[string]Provider, len(ps))
	for _, p := range ps {
		if p != nil {
			m[p.Name()] = p
		}
	}
	return &Registry{
		providers: m,
	}
}

func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.providers))
	for n := range r.providers {
		out = append(out, n)
	}
	return out
}

func hmacSHA256Hex(key, msg []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(msg)
	return hex.EncodeToString(mac.Sum(nil))
}

func secureEqual(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}

// // INR/USD/EUR/GBP/AED/SGD All These Currencies have upto 2 decimals
func minorToDecimal(amountMinor int64, _ string) string {
	whole := amountMinor / 100
	frac := amountMinor % 100
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%02d", whole, frac)
}
