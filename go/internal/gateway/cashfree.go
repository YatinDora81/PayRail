package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/payrail/go/internal/config"
)

const cashfreeAPIVersion = "2026-01-01"

type Cashfree struct {
	appID         string
	secretKey     string
	webhookSecret string
	baseURL       string
	http          *http.Client
}

func (c *Cashfree) auth(r *http.Request) {
	r.Header.Set("x-api-version", cashfreeAPIVersion)
	r.Header.Set("x-client-id", c.appID)
	r.Header.Set("x-client-secret", c.secretKey)
}

func NewCashfree(appID, secretKey, webhookSecret, baseURL string, cfg config.GatewayConfig) *Cashfree {
	if baseURL == "" {
		baseURL = "https://api.cashfree.com" // live
		if strings.ToLower(cfg.Env) == "development" || strings.ToLower(cfg.Env) == "dev" {
			baseURL = "https://sandbox.cashfree.com" // sandbox
		}
	}
	return &Cashfree{
		appID:         appID,
		secretKey:     secretKey,
		webhookSecret: webhookSecret,
		baseURL:       baseURL,
		http:          &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Cashfree) Name() string {
	return "CASHFREE"
}

func (c *Cashfree) CreateOrder(ctx context.Context, req CreateOrderRequest) (CreateOrderResult, error) {

	body, _ := json.Marshal(map[string]any{
		"order_id":       req.OrderID, // our Order.id — gives Cashfree-side idempotency
		"order_amount":   minorToDecimal(req.AmountMinor, req.Currency),
		"order_currency": req.Currency,
		"customer_details": map[string]any{
			"customer_id": "cust_" + req.OrderID,
		},
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/pg/orders", bytes.NewReader(body))
	if err != nil {
		return CreateOrderResult{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.auth(httpReq)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return CreateOrderResult{}, fmt.Errorf("cashfree create order: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return CreateOrderResult{}, fmt.Errorf("cashfree create order: status %d", resp.StatusCode)
	}

	var out struct {
		CFOrderID        string `json:"cf_order_id"`
		PaymentSessionID string `json:"payment_session_id"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateOrderResult{}, fmt.Errorf("cashfree decode: %w", err)
	}

	return CreateOrderResult{
		GatewayOrderID: out.CFOrderID,
		ClientParams: map[string]any{
			"paymentSessionId": out.PaymentSessionID,
			"appId":            c.appID, // public app id for the checkout widget
		},
	}, nil
}

func (c *Cashfree) VerifyWebhook(ctx context.Context, payload []byte, headers http.Header) error {

	got := headers.Get("x-webhook-signature")
	ts := headers.Get("x-webhook-timestamp")
	if got == "" || ts == "" {
		return ErrInvalidSignature
	}

	mac := hmac.New(sha256.New, []byte(c.webhookSecret))
	mac.Write([]byte(ts))
	mac.Write(payload)
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !secureEqual(got, want) {
		return ErrInvalidSignature
	}

	return nil
}

func (c *Cashfree) FetchPayment(ctx context.Context, gatewayOrderID string) (FetchPaymentResult, error) {

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/pg/orders/"+gatewayOrderID+"/payments", nil)
	if err != nil {
		return FetchPaymentResult{}, err
	}
	c.auth(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return FetchPaymentResult{}, fmt.Errorf("cashfree fetch payments: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return FetchPaymentResult{}, fmt.Errorf("cashfree fetch payments: status %d", resp.StatusCode)
	}
	var out []struct {
		ID       json.Number `json:"cf_payment_id"`
		Status   string      `json:"payment_status"`
		Amount   json.Number `json:"payment_amount"`
		Currency string      `json:"payment_currency"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchPaymentResult{}, fmt.Errorf("cashfree decode: %w", err)
	}

	res := FetchPaymentResult{Status: "PENDING"}

	if len(out) == 0 {
		res.Status = "EXPIRED"
		return res, nil
	}

	for _, p := range out {
		res.GatewayPaymentID = p.ID.String()
		res.AmountMinor = decimalStringToMinor(p.Amount.String())
		res.Currency = p.Currency
		switch p.Status {
		case "SUCCESS":
			res.Status = "CAPTURED"
			return res, nil
		case "FAILED", "USER_DROPPED", "CANCELLED":
			res.Status = "FAILED"
		}
	}
	return res, nil
}

func (c *Cashfree) FetchRefund(ctx context.Context, gatewayRefundID, _ string) (FetchRefundResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/pg/refunds/"+gatewayRefundID, nil)
	if err != nil {
		return FetchRefundResult{}, err
	}
	httpReq.Header.Set("x-api-version", cashfreeAPIVersion)
	httpReq.Header.Set("x-client-id", c.appID)
	httpReq.Header.Set("x-client-secret", c.secretKey)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return FetchRefundResult{}, fmt.Errorf("cashfree fetch refund: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return FetchRefundResult{}, fmt.Errorf("cashfree fetch refund: status %d", resp.StatusCode)
	}
	var out struct {
		ID     json.Number `json:"cf_refund_id"`
		Status string      `json:"refund_status"`
		Amount json.Number `json:"refund_amount"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return FetchRefundResult{}, fmt.Errorf("cashfree decode: %w", err)
	}
	res := FetchRefundResult{
		GatewayRefundID: out.ID.String(),
		AmountMinor:     decimalStringToMinor(out.Amount.String()),
		Status:          "PENDING",
	}

	switch out.Status {
	case "SUCCESS":
		res.Status = "PROCESSED"
	case "CANCELLED":
		res.Status = "FAILED"
	}
	return res, nil
}

// convert 2 decimal to minor (499.19 -> 49919)
func decimalStringToMinor(v string) int64 {
	whole, frac, _ := strings.Cut(v, ".")
	if len(frac) > 2 {
		return 0
	}
	frac = (frac + "00")[:2]
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func cashfreeRefundID(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	id := b.String()
	if len(id) > 40 || len(id) < 3 {
		sum := sha256.Sum256([]byte(key))
		id = "rf-" + hex.EncodeToString(sum[:16])
	}
	return id
}

func (c *Cashfree) CreateRefund(ctx context.Context, req CreateRefundRequest) (CreateRefundResult, error) {
	if req.GatewayOrderID == "" {
		return CreateRefundResult{}, fmt.Errorf("cashfree create refund: gateway order id is required: %w", ErrRefundRejected)
	}

	body, _ := json.Marshal(map[string]any{
		"refund_amount": json.Number(minorToDecimal(req.AmountMinor, req.Currency)),
		"refund_id":     cashfreeRefundID(req.IdempotencyKey),
		"refund_note":   "payrail refund",
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/pg/orders/"+req.GatewayOrderID+"/refunds", bytes.NewReader(body))
	if err != nil {
		return CreateRefundResult{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.auth(httpReq)
	httpReq.Header.Set("x-idempotency-key", req.IdempotencyKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return CreateRefundResult{}, fmt.Errorf("cashfree create refund: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 == 4 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return CreateRefundResult{}, fmt.Errorf("cashfree create refund: status %d: %s: %w", resp.StatusCode, snippet, ErrRefundRejected)
	}

	if resp.StatusCode/100 != 2 {
		return CreateRefundResult{}, fmt.Errorf("cashfree create refund: status %d", resp.StatusCode)
	}

	var out struct {
		CfRefundID json.Number `json:"cf_refund_id"`
		RefundID   string      `json:"refund_id"`
		Status     string      `json:"refund_status"` // SUCCESS | PENDING | CANCELLED | ONHOLD
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CreateRefundResult{}, fmt.Errorf("cashfree decode: %w", err)
	}

	res := CreateRefundResult{GatewayRefundID: out.CfRefundID.String(), Status: "PENDING"}

	if res.GatewayRefundID == "" {
		res.GatewayRefundID = out.RefundID
	}

	switch out.Status {
	case "SUCCESS":
		res.Status = "PROCESSED"
	case "CANCELLED":
		res.Status = "FAILED"
	}

	return res, nil
}

func (c *Cashfree) FindOrderByReference(ctx context.Context, merchantReference string) (OrderLookupResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/pg/orders/"+merchantReference, nil)
	if err != nil {
		return OrderLookupResult{}, err
	}

	c.auth(httpReq)
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return OrderLookupResult{}, fmt.Errorf("cashfree find order: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return OrderLookupResult{}, nil
	}

	if resp.StatusCode/100 != 2 {
		return OrderLookupResult{}, fmt.Errorf("cashfree find order: status %d", resp.StatusCode)
	}

	var out struct {
		OrderID  string      `json:"order_id"`
		Status   string      `json:"order_status"` // ACTIVE | PAID | EXPIRED | TERMINATED
		Amount   json.Number `json:"order_amount"`
		Currency string      `json:"order_currency"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return OrderLookupResult{}, fmt.Errorf("cashfree decode: %w", err)
	}

	res := OrderLookupResult{Found: true, GatewayOrderID: out.OrderID, Status: "PENDING",
		AmountMinor: decimalStringToMinor(out.Amount.String()), Currency: out.Currency}

	switch out.Status {
	case "PAID":
		res.Status = "CAPTURED"
	case "EXPIRED", "TERMINATED":
		res.Status = "EXPIRED"
	}

	return res, nil
}
